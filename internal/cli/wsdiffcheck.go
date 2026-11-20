package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/diffcheck"
	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/vault"
)

func wsDiffCheckCmd() *cobra.Command {
	var dir, maxSize, ticket string
	cmd := &cobra.Command{
		Use:   "diff-check [-C <dir>] [--max-size <size>] [--ticket <ID>] <rev-range>",
		Short: "Flag binary or oversized files added by the commits of a range",
		Long: `Checks every non-merge commit of <rev-range> (anything git rev-list
accepts, typically HEAD..task/<branch>) and flags each file it adds or
modifies that git treats as binary or whose blob is larger than --max-size
(a byte count, or a number with K, M or G; default 1M). Deletions are
ignored. One line per flagged file on stdout:

  <binary|large|binary,large>  <size in bytes>  <short sha>  <path>

With --ticket <ID> it also flags the ticket's own vault-only IDs (the ID,
the R-nn of its relay.md, the B-nn of its backlog.md, the finding IDs of
its reviews/: entry lines only) that a commit introduces: in a commit
message any whole-word occurrence, in a file an ID that occurs more often
in the commit's added lines than in its removed ones (an ID kept on a
reworded line passes). A ticket whose commit style is ticket (a synced
ticket, or commit-style: ticket in its board.md) has its own ID in every
subject, so there the ticket ID is not flagged in a subject line:

  vault-id  <the ID>  <short sha>  <path | :commit-message>

and a test name built from one of the ticket's waves (the w<N> of board.md's
task lines and of reviews/wave-<N>-*.md: TestW116Foo, test_w3_bar) that a
commit adds to a test file (*_test.go, test_*.py, *.spec.ts, a file under
test/ or spec/, ...) more often than it removes it:

  wave-test  <the name>  <short sha>  <path>

and a commit message, of a merge commit too, that names one of those waves
("wave 7 review fixes"), after the per-commit lines:

  vault-id  wave <N>  <short sha>  :commit-message

Lines are per commit in rev-list order, its size lines first, then its
vault-id and wave-test lines, then three kinds of informational note lines (they never change the
exit code):

  note  test-changed  <name>  <short sha>  <path>
  note  comment-added  <lines>  <short sha>  <path>
  note  commit-style  <ticket|conventional>  <short sha>  :commit-message

test-changed: the commit changes or removes a test that its first parent
had: a top-level Test*, Benchmark*, Fuzz* or Example* func of a *_test.go
file whose source text (comments included) differs or is gone, or (name -)
an existing testdata/**/*.txtar file it modifies or deletes. Name - also
stands for a test file whose type changed, that no longer parses, or that
is above --max-size. Names follow go test (Testdata is no test). Added
tests and added files are never noted, and a failure to compute notes
yields none.
comment-added: the commit adds whole-line comments to a source in a //
language (Go, JS/TS, Dart, Rust, Java, Kotlin, Swift, C, C#) or a #
language (shell, Python, Ruby, Nix, YAML, TOML, Terraform, Makefile,
Dockerfile), not under testdata/, vendor/ or node_modules/; <lines> counts the distinct comment lines it adds beyond
those it removes, so a moved comment is not new. Comments the no-comments
rule keeps are not counted: tool directives (//go:, //line, //export,
+build, #!, # -*-, # syntax=, # shellcheck shell=|source=), legal
headers, generated-code markers, prettier-ignore, /// <reference> and lines
with a link. Lint suppressions (//nolint, eslint-disable, @ts-ignore,
@ts-expect-error, # noqa, # shellcheck disable=) are counted.
commit-style (with --ticket): the subject misses the ticket's commit style,
"<ID>: summary" for ticket, "type(scope): summary" for conventional
(fixup!, squash! and Revert subjects pass; style repo is not checked). A
repo whose own history writes "<KEY-n> summary" without the colon more often
than with it (the last 200 subjects of the range's left side) is held to
that form instead.

For a range A..B or A...B it also checks the repo's specs/<capability>/spec.md:
a requirement ID that B adds (not defined at the merge base) but A already
defines, or that B defines twice, is flagged, one line per definition, after
the commit lines (a repo without specs/ is skipped):

  spec-id  <REQ-ID>  <short sha of B>  <spec file>

Exits 0 when clean (notes don't count), 3 when something is flagged, 1 on a usage error, a
directory that is not a git checkout, or a bad range.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = vault.Optional()
			limit, err := diffcheck.ParseSize(maxSize)
			if err != nil {
				return fmt.Errorf("diff-check: %w", err)
			}
			if dir == "" {
				dir = "."
			}
			if _, err := exec.LookPath("git"); err != nil {
				return fmt.Errorf("diff-check: git not found")
			}
			var ids []string
			var waves []int
			style := ""
			if ticket != "" {
				ctx, err := vault.Require()
				if err != nil {
					return fmt.Errorf("diff-check: %w", err)
				}
				folder := note.TicketDir(ctx.Vault, ticket)
				if st, err := os.Stat(folder); !note.ValidKey(ticket) || err != nil || !st.IsDir() {
					return fmt.Errorf("diff-check: ticket %s not found in the vault", ticket)
				}
				ids = diffcheck.TicketIDs(folder, ticket)
				waves = diffcheck.TicketWaves(folder)
				style = diffCheckStyle(ctx.Vault, ticket)
			}
			found, err := diffcheck.Check(dir, args[0], limit)
			if err != nil {
				return fmt.Errorf("diff-check: %w", err)
			}
			var hits, waveHits, wordHits []diffcheck.IDHit
			if ticket != "" {
				subjectID := ""
				if style == "ticket" {
					subjectID = ticket
				}
				if hits, err = diffcheck.CheckIDsStyle(dir, args[0], ids, subjectID); err != nil {
					return fmt.Errorf("diff-check: %w", err)
				}
				if waveHits, err = diffcheck.CheckWaveTests(dir, args[0], waves); err != nil {
					return fmt.Errorf("diff-check: %w", err)
				}
				if wordHits, err = diffcheck.CheckWaveWords(dir, args[0], waves); err != nil {
					return fmt.Errorf("diff-check: %w", err)
				}
			}
			specHits, err := diffcheck.CheckSpecIDs(dir, args[0])
			if err != nil {
				return fmt.Errorf("diff-check: %w", err)
			}
			revs, err := diffcheck.Revs(dir, args[0])
			if err != nil {
				return fmt.Errorf("diff-check: %w", err)
			}
			sep := ""
			if style == "ticket" {
				sep = ticketSubjectSep(dir, args[0])
			}
			var b strings.Builder
			fi, hi, wi := 0, 0, 0
			for _, sha := range revs {
				short := diffcheck.ShortSHA(sha)
				for ; fi < len(found) && found[fi].Commit == short; fi++ {
					f := found[fi]
					fmt.Fprintf(&b, "%s\t%d\t%s\t%s\n", f.Kind, f.Size, f.Commit, f.Path)
				}
				for ; hi < len(hits) && hits[hi].Commit == short; hi++ {
					h := hits[hi]
					fmt.Fprintf(&b, "vault-id\t%s\t%s\t%s\n", h.ID, h.Commit, h.Path)
				}
				for ; wi < len(waveHits) && waveHits[wi].Commit == short; wi++ {
					h := waveHits[wi]
					fmt.Fprintf(&b, "wave-test\t%s\t%s\t%s\n", h.ID, h.Commit, h.Path)
				}
				for _, n := range diffNotes(dir, sha, short, limit) {
					b.WriteString(n + "\n")
				}
				for _, n := range diffNoteComments(dir, sha, short) {
					b.WriteString(n + "\n")
				}
				if n := diffNoteStyle(dir, sha, short, style, ticket, sep); n != "" {
					b.WriteString(n + "\n")
				}
			}
			if fi != len(found) || hi != len(hits) || wi != len(waveHits) {
				return fmt.Errorf("diff-check: the range changed while it was checked")
			}
			for _, h := range wordHits {
				fmt.Fprintf(&b, "vault-id\t%s\t%s\t%s\n", h.ID, h.Commit, h.Path)
			}
			for _, h := range specHits {
				fmt.Fprintf(&b, "spec-id\t%s\n", h)
			}
			fmt.Fprint(cmd.OutOrStdout(), b.String())
			if len(found) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "tix ws diff-check: %d file(s) flagged (binary or > %s)\n", len(found), maxSize)
			}
			if len(hits) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "tix ws diff-check: %d vault-only ID(s) of %s in the range\n", len(hits), ticket)
			}
			if len(wordHits) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "tix ws diff-check: %d commit message(s) name a wave of %s (say what changed instead)\n", len(wordHits), ticket)
			}
			if len(waveHits) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "tix ws diff-check: %d test name(s) built from a wave of %s (name the test after the behaviour)\n", len(waveHits), ticket)
			}
			if len(specHits) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "tix ws diff-check: %d requirement ID(s) taken twice in specs/ (renumber the later one)\n", len(specHits))
			}
			if len(found) > 0 || len(hits) > 0 || len(waveHits) > 0 || len(wordHits) > 0 || len(specHits) > 0 {
				return exitError(3)
			}
			return nil
		},
	}
	cmd.SilenceUsage = true
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "the git checkout to inspect (default: the current directory)")
	cmd.Flags().StringVar(&ticket, "ticket", "", "also flag this ticket's vault-only IDs (the ticket must be in the vault)")
	cmd.Flags().StringVar(&maxSize, "max-size", "1M", "size limit: bytes, or a number with K, M or G")
	return cmd
}

func diffCheckStyle(v, ticket string) string {
	st := note.Frontmatter(filepath.Join(note.TicketDir(v, ticket), "board.md"))["commit-style"]
	switch {
	case st == "ticket", st == "conventional", st == "repo":
		return st
	case note.Synced(note.Frontmatter(note.TicketPath(v, ticket))):
		return "ticket"
	}
	return ""
}

var conventionalSubjectRe = regexp.MustCompile(`^[a-z]+(\([^()]+\))?!?: \S`)

var (
	keyColonRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+: \S`)
	keySpaceRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+ \S`)
)

func ticketSubjectSep(dir, revRange string) string {
	base, _, ok := strings.Cut(revRange, "..")
	if !ok || base == "" || strings.HasPrefix(base, "-") {
		return ": "
	}
	out, err := gitx.OutIn(dir, nil, "log", "-n", "200", "--no-merges", "--format=%s", base, "--")
	if err != nil {
		return ": "
	}
	colon, space := 0, 0
	for _, l := range strings.Split(out, "\n") {
		switch {
		case keyColonRe.MatchString(l):
			colon++
		case keySpaceRe.MatchString(l):
			space++
		}
	}
	if space > colon {
		return " "
	}
	return ": "
}

func diffNoteStyle(dir, sha, short, style, ticket, sep string) string {
	if style != "ticket" && style != "conventional" {
		return ""
	}
	subj, err := diffcheck.Subject(dir, sha)
	if err != nil {
		return ""
	}
	for _, p := range []string{"fixup! ", "squash! ", "amend! ", "Revert "} {
		if strings.HasPrefix(subj, p) {
			return ""
		}
	}
	ok := conventionalSubjectRe.MatchString(subj)
	if style == "ticket" {
		rest, has := strings.CutPrefix(subj, ticket+sep)
		ok = has && strings.TrimSpace(rest) != ""
	}
	if ok {
		return ""
	}
	return fmt.Sprintf("note\tcommit-style\t%s\t%s\t%s", style, short, diffcheck.MessagePath)
}

func diffNoteComments(dir, sha, short string) []string {
	found, err := diffcheck.CheckComments(dir, sha)
	if err != nil {
		return nil
	}
	notes := make([]string, 0, len(found))
	for _, c := range found {
		notes = append(notes, fmt.Sprintf("note\tcomment-added\t%d\t%s\t%s", c.Lines, short, c.Path))
	}
	return notes
}

func diffNoteRegular(mode string) bool { return mode == "100644" || mode == "100755" }

func diffNotes(dir, sha, short string, limit int64) []string {
	var notes []string
	if parent, err := gitx.Out(dir, "rev-parse", "--verify", "-q", sha+"^1"); err == nil && parent != "" {
		out, err := gitx.OutIn(dir, nil, "diff-tree", "-r", "--no-renames", "--no-commit-id", "-z", "--raw", parent, sha)
		if err != nil {
			out = ""
		}
		toks := strings.Split(out, "\x00")
		for i := 0; i+1 < len(toks); i += 2 {
			meta := strings.Fields(strings.TrimPrefix(toks[i], ":"))
			p := toks[i+1]
			if len(meta) < 5 || !diffNoteRegular(meta[0]) {
				continue
			}
			oldSHA, newSHA, status := meta[2], meta[3], meta[4]
			if status != "M" && status != "D" && status != "T" {
				continue
			}
			line := func(name string) string {
				return fmt.Sprintf("note\ttest-changed\t%s\t%s\t%s", name, short, p)
			}
			switch {
			case strings.HasSuffix(p, ".txtar") && diffNoteInTestdata(p):
				notes = append(notes, line("-"))
			case strings.HasSuffix(p, "_test.go"):
				if status == "T" || (status == "M" && !diffNoteRegular(meta[1])) {
					notes = append(notes, line("-"))
					continue
				}
				for _, n := range diffNoteChangedTests(dir, oldSHA, newSHA, p, status == "D", limit) {
					notes = append(notes, line(n))
				}
			}
		}
	}
	return notes
}

func diffNoteInTestdata(p string) bool {
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if seg == "testdata" {
			return true
		}
	}
	return false
}

func diffNoteChangedTests(dir, oldSHA, newSHA, p string, deleted bool, limit int64) []string {
	big := func(blob string) bool {
		out, err := gitx.Out(dir, "cat-file", "-s", blob)
		if err != nil {
			return false
		}
		n, err := strconv.ParseInt(out, 10, 64)
		return err == nil && n > limit
	}
	if big(oldSHA) || (!deleted && big(newSHA)) {
		return []string{"-"}
	}
	old, err := gitx.OutIn(dir, nil, "cat-file", "blob", oldSHA)
	if err != nil {
		return nil
	}
	before, ok := diffNoteTestFuncs(p, old)
	if !ok {
		return nil
	}
	var after diffNoteFuncs
	if !deleted {
		cur, err := gitx.OutIn(dir, nil, "cat-file", "blob", newSHA)
		if err != nil {
			return nil
		}
		if after, ok = diffNoteTestFuncs(p, cur); !ok {
			return []string{"-"}
		}
	}
	var names []string
	for _, n := range before.order {
		if txt, there := after.text[n]; !there || txt != before.text[n] {
			names = append(names, n)
		}
	}
	return names
}

func diffNoteIsTest(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			r, _ := utf8.DecodeRuneInString(rest)
			return rest == "" || !unicode.IsLower(r)
		}
	}
	return false
}

type diffNoteFuncs struct {
	order []string
	text  map[string]string
}

func diffNoteTestFuncs(name, src string) (diffNoteFuncs, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return diffNoteFuncs{}, false
	}
	res := diffNoteFuncs{text: map[string]string{}}
	for _, d := range f.Decls {
		fn, isFn := d.(*ast.FuncDecl)
		if !isFn || fn.Recv != nil {
			continue
		}
		n := fn.Name.Name
		if !diffNoteIsTest(n) {
			continue
		}
		start := fset.Position(fn.Pos()).Offset
		if fn.Doc != nil {
			start = fset.Position(fn.Doc.Pos()).Offset
		}
		res.order = append(res.order, n)
		res.text[n] = src[start:fset.Position(fn.End()).Offset]
	}
	return res, true
}
