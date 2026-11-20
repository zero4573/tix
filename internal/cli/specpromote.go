package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/links"
	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/spec"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

var promoteNow = time.Now

var (
	writeJournal  = fsx.WriteFileAtomic
	renameTree    = os.Rename
	writeStaged   = fsx.WriteFileAtomic
	writeSpecFile = fsx.WriteFileAtomic
	trashFile     = os.Rename
	redirectLinks = links.RedirectLinks
	removeFile    = os.Remove
	appendDone    = appendFile
	restoreFile   = restoreWrite
)

const (
	promoteDone    = "promoted"
	promoteNothing = "nothing"
	promoteRefused = "refused"
)

const (
	keepTheirs = "theirs"
	keepOurs   = "ours"
	keepMerged = "merged"
)

type promoteOther struct {
	Ticket   string `json:"ticket"`
	Note     string `json:"note"`
	Promoted string `json:"promoted"`
}

type promoteConflict struct {
	Files   []string       `json:"files"`
	Others  []promoteOther `json:"others"`
	Theirs  spec.Diff      `json:"theirs"`
	Ours    spec.Diff      `json:"ours"`
	Clashes []spec.Clash   `json:"clashes"`
	Copy    string         `json:"copy"`
}

type promoteResolved struct {
	Keep  string   `json:"keep"`
	Files []string `json:"files"`
	Trash string   `json:"trash,omitempty"`
}

type promoteHeld struct {
	File   string `json:"file"`
	Ticket string `json:"ticket,omitempty"`
	Note   string `json:"note,omitempty"`
	Reason string `json:"reason"`
}

type promoteResult struct {
	Slug         string           `json:"slug"`
	Status       string           `json:"status"`
	Files        []string         `json:"files"`
	Capabilities []string         `json:"capabilities"`
	Version      string           `json:"version,omitempty"`
	Resolved     *promoteResolved `json:"resolved,omitempty"`
	Conflict     *promoteConflict `json:"conflict,omitempty"`
	Held         []promoteHeld    `json:"held,omitempty"`
	Warnings     []string         `json:"warnings,omitempty"`
	Replayed     *promoteResult   `json:"replayed,omitempty"`
}

func specPromoteCmd() *cobra.Command {
	var asJSON bool
	var resolve []string
	cmd := &cobra.Command{
		Use:   "promote [--json] [--resolve <slug>=theirs|ours|merged]... <ID>",
		Short: "Move a ticket's vault spec copy into projects/ and record it as a version",
		Long: `For every repo of the ticket's workspace whose specs live in the vault and
whose copy tickets/<source>/<ID>/specs/<slug>/ holds files, one transaction:
  conflict check: a copy file whose projects/<slug>/specs/ version differs
     from this ticket's snapshots (specs-base/<slug>/, taken by tix spec start,
     and specs-after/<slug>/ when there is one) was changed by another ticket
     since. The repo is refused and nothing is written for it; the output
     names the files, the other tickets' version notes that hold them, both
     requirement changes (theirs and ours, from the snapshot) and the
     capabilities and requirements both changed
  plan and stage: from the committed state only, every file the promotion
     writes goes into tickets/<source>/<ID>/specs-stage/<slug>/: the base and
     after snapshots, the kept list, the version note
     projects/<slug>/spec-versions/<slug>-spec-<ID>.md (one per ticket and
     repo; its pin is cleared when the promoted files' content changed) and
     the moved files, their links pointed at projects/
  commit point: the journal specs-stage/<slug>.journal, written last
  apply: base, kept list, the copy's files kept as theirs to .trash/, note,
     after, then each copy file into projects/<slug>/specs/, then (links
     across the vault follow) out of the copy; journal and stage are then
     removed
A run that stops after the commit point is finished by the next tix spec
promote (before any other check of any repo) or tix spec start of the
ticket (which takes the vault lock for it, failing at once when another
session holds it); until then tix spec diff and show refuse the ticket, log
notes it and pin skips it. A stage without a journal is deleted by promote.
The finishing run never overwrites a projects/ file changed (by another
ticket or by hand) after this promote planned: it holds that file back (its snapshot entries
and the version note return to their plan-time state, the copy file
stays), reports it, and the plan that follows refuses it as a conflict,
resolved as below. A copy file edited after the stop stays in the copy,
with a warning.
A refused repo is resolved with --resolve <slug>=theirs (drop the copy's
conflicting files to the vault's .trash/ and keep projects/ as it is; the
files are listed in specs-after/.<slug>.kept, which every later promotion
carries into the after snapshot, and their base entries move onto theirs;
a kept file this ticket never promoted always has theirs as its base),
--resolve <slug>=ours (replace them with the copy), or by merging the
other change into the copy by hand and re-running with --resolve <slug>=merged (the copy is accepted
as the merge: the ticket's before side becomes the current projects/
content of those files). A copy file that already equals projects/ byte for
byte counts as merged without the flag. --json prints the results as data.
Exit 1 when any repo was refused. A copy without the snapshot tix spec start
takes is an error, before anything is written: run tix spec start <ID> <slug>
(it snapshots projects/ as it is then) and promote again. The transaction
and the reasons for it: docs/spec-versions.md, "Promote".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			out := cmd.OutOrStdout()
			failFast := func(err error) error {
				if asJSON {
					b, merr := json.MarshalIndent(map[string]any{"ticket": id, "repos": []promoteResult{}, "error": err.Error()}, "", "  ")
					if merr == nil {
						fmt.Fprintln(out, string(b))
					}
				}
				return err
			}
			ctx, err := vault.Require()
			if err != nil {
				return failFast(err)
			}
			if !note.ValidKey(id) {
				return failFast(fmt.Errorf("invalid ticket ID %q", id))
			}
			info, err := workspace.Read(filepath.Join(ctx.WorkRoot, id))
			if err != nil {
				return failFast(fmt.Errorf("no workspace for %s: %w", id, err))
			}
			res, err := parseResolve(resolve)
			if err != nil {
				return failFast(err)
			}
			var repos []workspace.Repo
			for _, r := range info.Repos {
				loc, err := spec.Resolve(ctx.Vault, r.Slug, r.Path)
				if err != nil {
					return failFast(fmt.Errorf("%s: %w", r.Slug, err))
				}
				if loc.Kind == spec.InVault {
					repos = append(repos, r)
				}
			}
			for slug := range res {
				if !hasSlug(repos, slug) {
					return failFast(fmt.Errorf("--resolve %s: not a repo of %s with its specs in the vault", slug, id))
				}
			}
			results, runErr := runPromote(ctx, id, repos, res)
			if asJSON {
				if results == nil {
					results = []promoteResult{}
				}
				doc := map[string]any{"ticket": id, "repos": results}
				if runErr != nil {
					doc["error"] = runErr.Error()
				}
				b, err := json.MarshalIndent(doc, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(b))
			} else {
				for _, pr := range results {
					printPromote(out, id, pr)
				}
			}
			if runErr != nil {
				return runErr
			}
			for _, pr := range results {
				if pr.Status == promoteRefused {
					return exitError(1)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the results as JSON")
	cmd.Flags().StringArrayVar(&resolve, "resolve", nil, "resolve a refused repo: <slug>=theirs (keep projects/), <slug>=ours (keep the copy) or <slug>=merged (the copy is your hand merge); repeatable")
	return cmd
}

func parseResolve(vals []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vals {
		slug, keep, ok := strings.Cut(v, "=")
		if !ok || slug == "" || (keep != keepTheirs && keep != keepOurs && keep != keepMerged) {
			return nil, fmt.Errorf("--resolve %q: want <slug>=theirs, <slug>=ours or <slug>=merged", v)
		}
		out[slug] = keep
	}
	return out, nil
}

func hasSlug(repos []workspace.Repo, slug string) bool {
	for _, r := range repos {
		if r.Slug == slug {
			return true
		}
	}
	return false
}

func vaultRel(vaultDir, p string) string {
	r, err := filepath.Rel(vaultDir, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

func runPromote(ctx vault.Context, id string, repos []workspace.Repo, res map[string]string) ([]promoteResult, error) {
	replayed := make([]*promoteResult, len(repos))
	for i, r := range repos {
		rp, err := finishPromote(ctx.Vault, id, r.Slug)
		if err != nil {
			return finishedReplays(repos, replayed), fmt.Errorf("%s: %w", r.Slug, err)
		}
		replayed[i] = rp
	}
	for _, r := range repos {
		if err := needsStart(ctx, id, r.Slug); err != nil {
			return finishedReplays(repos, replayed), err
		}
	}
	var results []promoteResult
	for i, r := range repos {
		pr, err := promotePlanned(ctx, id, r, res[r.Slug], replayed[i])
		if err != nil {
			return append(results, finishedReplays(repos[i:], replayed[i:])...), fmt.Errorf("%s: %w", r.Slug, err)
		}
		results = append(results, pr)
	}
	return results, nil
}

func finishedReplays(repos []workspace.Repo, replayed []*promoteResult) []promoteResult {
	var out []promoteResult
	for i, rp := range replayed {
		if rp != nil {
			out = append(out, promoteResult{Slug: repos[i].Slug, Status: promoteNothing, Files: []string{}, Capabilities: []string{}, Replayed: rp})
		}
	}
	return out
}

func promoteRepo(ctx vault.Context, id string, r workspace.Repo, keep string) (promoteResult, error) {
	replayed, err := finishPromote(ctx.Vault, id, r.Slug)
	if err != nil {
		return promoteResult{Slug: r.Slug, Files: []string{}, Capabilities: []string{}}, err
	}
	return promotePlanned(ctx, id, r, keep, replayed)
}

func promotePlanned(ctx vault.Context, id string, r workspace.Repo, keep string, replayed *promoteResult) (promoteResult, error) {
	pr := promoteResult{Slug: r.Slug, Files: []string{}, Capabilities: []string{}}
	copyDir := spec.CopyDir(ctx.Vault, id, r.Slug)
	cp, err := spec.ReadTree(copyDir)
	if err != nil {
		return pr, err
	}
	if len(cp) == 0 {
		if replayed != nil {
			return *replayed, nil
		}
		pr.Status = promoteNothing
		return pr, nil
	}
	pr.Replayed = replayed
	if err := needsStart(ctx, id, r.Slug); err != nil {
		return pr, err
	}
	p, err := planPromote(ctx, id, r, keep, cp, &pr)
	if err != nil || p == nil {
		return pr, err
	}
	ops, err := stagePromote(ctx.Vault, id, r.Slug, p)
	if err != nil {
		return pr, err
	}
	j, err := applyJournal(ctx.Vault, id, r.Slug, ops)
	if err != nil {
		return pr, err
	}
	if len(j.held) > 0 || len(j.warnings) > 0 {
		jr, err := j.result()
		if err != nil {
			return pr, err
		}
		pr.Held, pr.Warnings = jr.Held, jr.Warnings
	}
	return pr, cleanPromote(ctx.Vault, id, r.Slug)
}

type promotePlan struct {
	base, after         spec.Tree
	kept                []string
	note                []byte
	moves               spec.Tree
	trash               []string
	prevBase, prevAfter spec.Tree
	prevNote            []byte
	pre, copyHash       map[string]string
}

func planPromote(ctx vault.Context, id string, r workspace.Repo, keep string, cp spec.Tree, pr *promoteResult) (*promotePlan, error) {
	dir := spec.VaultDir(ctx.Vault, r.Slug)
	baseDir, afterDir := spec.BaseDir(ctx.Vault, id, r.Slug), spec.AfterDir(ctx.Vault, id, r.Slug)
	notePath := spec.VersionPath(ctx.Vault, r.Slug, id)
	projects, err := spec.ReadTree(dir)
	if err != nil {
		return nil, err
	}
	base, err := spec.ReadSnapshot(baseDir)
	if err != nil {
		return nil, err
	}
	oldAfter, err := spec.ReadSnapshot(afterDir)
	if err != nil {
		return nil, err
	}
	v, err := spec.ReadVersion(notePath)
	hasNote := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	oldKept, err := spec.ReadKept(spec.KeptList(ctx.Vault, id, r.Slug))
	if err != nil {
		return nil, err
	}
	for _, f := range cp.Keys() {
		if !spec.PlainName(f) {
			return nil, fmt.Errorf("%s: copy file %q: a name with a control character can't be promoted; rename it", r.Slug, f)
		}
	}
	if err := refuseUnfilled(ctx, id, r.Slug, oldAfter, projects, v, cp); err != nil {
		return nil, err
	}

	newBase := spec.Overlay(spec.Tree{}, base)
	for _, f := range oldKept {
		if v.HasFile(f) {
			continue
		}
		t, ok := oldAfter[f]
		if oldAfter == nil || !ok {
			t, ok = projects[f]
		}
		setEntry(newBase, f, t, ok)
	}

	conf := spec.Conflicts(projects, newBase, oldAfter, cp)
	var merged, open []string
	for _, f := range conf {
		if t, ok := projects[f]; ok && bytes.Equal(t, cp[f]) {
			merged = append(merged, f)
		} else {
			open = append(open, f)
		}
	}
	p := &promotePlan{prevBase: base, prevAfter: oldAfter, pre: map[string]string{}, copyHash: map[string]string{}}
	for f, b := range cp {
		p.copyHash[f] = spec.Hash(b)
	}
	if hasNote {
		if p.prevNote, err = os.ReadFile(notePath); err != nil {
			return nil, err
		}
	}
	kept := oldKept
	if len(open) > 0 {
		switch keep {
		case "":
			c, err := explainConflict(ctx, id, r.Slug, open, projects, newBase, oldAfter, cp)
			if err != nil {
				return nil, err
			}
			pr.Status, pr.Conflict = promoteRefused, c
			return nil, nil
		case keepTheirs:
			kept = spec.MergeFiles(kept, open)
			p.kept = kept
			p.trash = open
			cp = spec.Overlay(spec.Tree{}, cp)
			for _, f := range open {
				delete(cp, f)
			}
			pr.Resolved = &promoteResolved{Keep: keep, Files: open, Trash: vaultRel(ctx.Vault, promoteTrash(ctx.Vault, id, r.Slug)) + "/"}
		case keepMerged:
			merged = append(merged, open...)
			pr.Resolved = &promoteResolved{Keep: keep, Files: open}
		default:
			pr.Resolved = &promoteResolved{Keep: keep, Files: open}
		}
	}
	rebase := merged
	if keep == keepTheirs {
		rebase = append(append([]string(nil), merged...), open...)
	}
	for _, f := range rebase {
		t, ok := projects[f]
		setEntry(newBase, f, t, ok)
	}
	if !sameTree(newBase, base) {
		p.base = newBase
	}
	earlier := spec.MergeFiles(v.Files, kept)
	if len(cp) == 0 {
		p.after = spec.TicketAfter(oldAfter, projects, earlier, spec.Pick(projects, open))
		pr.Status = promoteNothing
		return p, nil
	}

	moved := cp.Keys()
	rs := make([]links.Redirect, len(moved))
	for i, f := range moved {
		rs[i] = links.Redirect{Src: vaultRel(ctx.Vault, filepath.Join(spec.CopyDir(ctx.Vault, id, r.Slug), filepath.FromSlash(f))), Dst: vaultRel(ctx.Vault, filepath.Join(dir, filepath.FromSlash(f)))}
	}
	p.moves = spec.Tree{}
	for _, f := range moved {
		b := cp[f]
		if strings.HasSuffix(f, ".md") {
			s, _ := links.RewriteText(filepath.Join(spec.CopyDir(ctx.Vault, id, r.Slug), filepath.FromSlash(f)), string(b), rs)
			b = []byte(s)
		}
		p.moves[f] = b
		p.pre[f] = spec.PreAbsent
		if t, ok := projects[f]; ok {
			p.pre[f] = spec.Hash(t)
		}
	}
	now := spec.Overlay(projects, p.moves)
	p.after = spec.TicketAfter(oldAfter, now, earlier, spec.Pick(now, append(append([]string{}, open...), moved...)))
	if !hasNote {
		v = spec.Version{Slug: r.Slug, Ticket: "[[" + id + "]]", Project: "[[" + r.Slug + "]]"}
	}
	v.Files = spec.MergeFiles(v.Files, moved)
	if oldAfter != nil && spec.PinStale(oldAfter, p.after, v.Files) {
		v.ClearPin()
	}
	v.Branch = r.Branch
	v.Promoted = promoteNow().Format(spec.TimeLayout)
	before, aft := spec.Sides(p.after, newBase, p.after, spec.Tree{}, v.Files)
	p.note, err = spec.RenderVersion(v, note.RelDir(ctx.Vault, id), before, spec.DiffTrees(r.Slug, before, aft))
	if err != nil {
		return nil, err
	}
	pr.Status, pr.Files, pr.Version = promoteDone, moved, vaultRel(ctx.Vault, notePath)
	pr.Capabilities = capsOf(moved)
	return p, nil
}

func setEntry(t spec.Tree, f string, b []byte, ok bool) {
	if ok {
		t[f] = b
	} else {
		delete(t, f)
	}
}

func sameTree(a, b spec.Tree) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for f, x := range a {
		if y, ok := b[f]; !ok || !bytes.Equal(x, y) {
			return false
		}
	}
	return true
}

func promoteTrash(vaultDir, id, slug string) string {
	return filepath.Join(vaultDir, ".trash", id+"-spec-theirs", slug)
}

func stagePromote(vaultDir, id, slug string, p *promotePlan) ([]spec.Op, error) {
	stage := spec.StageDir(vaultDir, id, slug)
	if err := removeStage(vaultDir, id, slug); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return nil, err
	}
	var ops []spec.Op
	if p.base != nil {
		if err := spec.WriteTree(filepath.Join(stage, spec.TreeBase), p.base); err != nil {
			return nil, err
		}
		ops = append(ops, spec.Op{Kind: spec.OpReplaceTree, Arg: spec.TreeBase})
	}
	if p.kept != nil {
		if err := spec.WriteKept(filepath.Join(stage, spec.WriteOfKept), p.kept); err != nil {
			return nil, err
		}
		ops = append(ops, spec.Op{Kind: spec.OpWrite, Arg: spec.WriteOfKept, Dest: vaultRel(vaultDir, spec.KeptList(vaultDir, id, slug))})
	}
	for _, f := range p.trash {
		ops = append(ops, spec.Op{Kind: spec.OpTrash, Arg: f, Hash: p.copyHash[f]})
	}
	if p.note != nil {
		if err := fsx.WriteFileAtomic(filepath.Join(stage, "note.md"), p.note, 0o644); err != nil {
			return nil, err
		}
		ops = append(ops, spec.Op{Kind: spec.OpWrite, Arg: spec.WriteOfNote, Dest: vaultRel(vaultDir, spec.VersionPath(vaultDir, slug, id))})
	}
	if p.after != nil {
		if err := spec.WriteTree(filepath.Join(stage, spec.TreeAfter), p.after); err != nil {
			return nil, err
		}
		ops = append(ops, spec.Op{Kind: spec.OpReplaceTree, Arg: spec.TreeAfter})
	}
	if len(p.moves) > 0 {
		if err := spec.WriteTree(filepath.Join(stage, "moves"), p.moves); err != nil {
			return nil, err
		}
		files := p.moves.Keys()
		prevBase, prevAfter := spec.Tree{}, spec.Tree{}
		var absBase, absAfter []string
		for _, f := range files {
			if b, ok := p.prevBase[f]; ok {
				prevBase[f] = b
			} else {
				absBase = append(absBase, f)
			}
			if b, ok := p.prevAfter[f]; ok {
				prevAfter[f] = b
			} else {
				absAfter = append(absAfter, f)
			}
		}
		prev := filepath.Join(stage, "prev")
		if err := spec.WriteTree(filepath.Join(prev, spec.TreeBase), prevBase); err != nil {
			return nil, err
		}
		if err := spec.WriteTree(filepath.Join(prev, spec.TreeAfter), prevAfter); err != nil {
			return nil, err
		}
		absNote := ""
		if p.prevNote != nil {
			if err := fsx.WriteFileAtomic(filepath.Join(prev, "note.md"), p.prevNote, 0o644); err != nil {
				return nil, err
			}
		} else {
			absNote = vaultRel(vaultDir, spec.VersionPath(vaultDir, slug, id))
		}
		if err := fsx.WriteFileAtomic(filepath.Join(prev, spec.PrevAbsent), spec.RenderAbsent(absBase, absAfter, absNote), 0o644); err != nil {
			return nil, err
		}
		for _, f := range files {
			ops = append(ops, spec.Op{Kind: spec.OpPut, Arg: f, Hash: p.pre[f]})
		}
		for _, f := range files {
			ops = append(ops, spec.Op{Kind: spec.OpDrop, Arg: f, Hash: p.copyHash[f]})
		}
	}
	if err := writeJournal(spec.JournalPath(vaultDir, id, slug), spec.RenderJournal(ops), 0o644); err != nil {
		return nil, err
	}
	return ops, nil
}

func removeStage(vaultDir, id, slug string) error {
	for _, p := range []string{spec.DonePath(vaultDir, id, slug), spec.HeldPath(vaultDir, id, slug)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.RemoveAll(spec.StageDir(vaultDir, id, slug))
}

func finishPromote(vaultDir, id, slug string) (*promoteResult, error) {
	ops, ok, err := spec.ReadJournal(vaultDir, id, slug)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := removeStage(vaultDir, id, slug); err != nil {
			return nil, err
		}
		_ = os.Remove(spec.StageRoot(vaultDir, id))
		return nil, nil
	}
	j, err := applyJournal(vaultDir, id, slug, ops)
	if err != nil {
		return nil, err
	}
	pr, err := j.result()
	if err != nil {
		return nil, err
	}
	if err := cleanPromote(vaultDir, id, slug); err != nil {
		return nil, err
	}
	return &pr, nil
}

type journalRun struct {
	vault, id, slug     string
	stage, copyDir, dir string
	ops                 []spec.Op
	done                map[int]bool
	complete            int
	torn                bool
	held                map[string]bool
	absent              map[string]map[string]bool
	warnings            []string
}

func (j *journalRun) copyFile(f string) string {
	return filepath.Join(j.copyDir, filepath.FromSlash(f))
}

func (j *journalRun) dest(f string) string { return filepath.Join(j.dir, filepath.FromSlash(f)) }

func (j *journalRun) staged(parts ...string) string {
	return filepath.Join(append([]string{j.stage}, parts...)...)
}

func (j *journalRun) treeDir(tree string) string {
	if st, err := os.Lstat(j.staged(tree)); err == nil && st.IsDir() {
		return j.staged(tree)
	}
	if tree == spec.TreeBase {
		return spec.BaseDir(j.vault, j.id, j.slug)
	}
	return spec.AfterDir(j.vault, j.id, j.slug)
}

func exists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func applyJournal(vaultDir, id, slug string, ops []spec.Op) (*journalRun, error) {
	j := &journalRun{
		vault: vaultDir, id: id, slug: slug, ops: ops,
		stage:   spec.StageDir(vaultDir, id, slug),
		copyDir: spec.CopyDir(vaultDir, id, slug),
		dir:     spec.VaultDir(vaultDir, slug),
		held:    map[string]bool{},
	}
	damaged := func(err error) error { return spec.DamagedJournal(vaultDir, id, slug, err) }
	read := func(p string) ([]byte, error) {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	b, err := read(spec.DonePath(vaultDir, id, slug))
	if err != nil {
		return nil, err
	}
	if j.done, j.complete, err = spec.ParseDone(b, len(ops)); err != nil {
		return nil, damaged(err)
	}
	j.torn = j.complete < len(b)
	if b, err = read(spec.HeldPath(vaultDir, id, slug)); err != nil {
		return nil, err
	}
	held, err := spec.ParseHeld(b)
	if err != nil {
		return nil, damaged(err)
	}
	for _, f := range held {
		j.held[f] = true
	}
	if b, err = read(j.staged("prev", spec.PrevAbsent)); err != nil {
		return nil, err
	}
	if j.absent, err = spec.ParseAbsent(b); err != nil {
		return nil, damaged(err)
	}

	var hold []string
	for i, o := range ops {
		if o.Kind != spec.OpPut || j.done[i+1] || j.held[o.Arg] {
			continue
		}
		staged, err := os.ReadFile(j.staged("moves", filepath.FromSlash(o.Arg)))
		if errors.Is(err, os.ErrNotExist) {
			return nil, damaged(fmt.Errorf("the stage lacks moves/%s", o.Arg))
		} else if err != nil {
			return nil, err
		}
		cur, err := os.ReadFile(j.dest(o.Arg))
		switch {
		case err == nil && (bytes.Equal(cur, staged) || spec.Hash(cur) == o.Hash):
		case errors.Is(err, os.ErrNotExist) && o.Hash == spec.PreAbsent:
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return nil, err
		default:
			hold = append(hold, o.Arg)
		}
	}
	if err := j.checkStage(hold); err != nil {
		return nil, damaged(err)
	}
	if len(hold) > 0 {
		if err := j.holdBack(hold); err != nil {
			return nil, err
		}
	}

	redirected := false
	for i, o := range ops {
		if j.done[i+1] || ((o.Kind == spec.OpPut || o.Kind == spec.OpDrop) && j.held[o.Arg]) {
			continue
		}
		switch o.Kind {
		case spec.OpReplaceTree:
			dst := spec.BaseDir(vaultDir, id, slug)
			if o.Arg == spec.TreeAfter {
				dst = spec.AfterDir(vaultDir, id, slug)
			}
			if err := replaceTree(j.staged(o.Arg), dst); err != nil {
				return nil, err
			}
		case spec.OpWrite:
			dst, src := spec.KeptList(vaultDir, id, slug), j.staged(spec.WriteOfKept)
			if o.Arg == spec.WriteOfNote {
				dst, src = spec.VersionPath(vaultDir, slug, id), j.staged("note.md")
			}
			if want := vaultRel(vaultDir, dst); o.Dest != want {
				return nil, damaged(fmt.Errorf("%s, want %s", o, want))
			}
			b, err := os.ReadFile(src)
			if errors.Is(err, os.ErrNotExist) && o.Arg == spec.WriteOfNote {
				if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
				break
			} else if err != nil {
				return nil, err
			}
			if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, b) {
				break
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return nil, err
			}
			if err := writeStaged(dst, b, 0o644); err != nil {
				return nil, err
			}
		case spec.OpPut:
			b, err := os.ReadFile(j.staged("moves", filepath.FromSlash(o.Arg)))
			if err != nil {
				return nil, err
			}
			dst := j.dest(o.Arg)
			if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, b) {
				break
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return nil, err
			}
			if err := writeSpecFile(dst, b, 0o644); err != nil {
				return nil, err
			}
		case spec.OpTrash:
			src := j.copyFile(o.Arg)
			if ok, err := j.unchanged(o); err != nil || !ok {
				if err != nil {
					return nil, err
				}
				break
			}
			dst := uniqueDest(filepath.Join(promoteTrash(vaultDir, id, slug), filepath.FromSlash(o.Arg)))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return nil, err
			}
			if err := trashFile(src, dst); err != nil {
				return nil, err
			}
		case spec.OpDrop:
			if !redirected {
				if err := j.redirect(i); err != nil {
					return nil, err
				}
				redirected = true
			}
			if ok, err := j.unchanged(o); err != nil || !ok {
				if err != nil {
					return nil, err
				}
				break
			}
			if err := removeFile(j.copyFile(o.Arg)); err != nil {
				return nil, err
			}
		}
		if err := j.mark(i + 1); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func (j *journalRun) sameCopy(o spec.Op, b []byte) bool {
	if spec.Hash(b) == o.Hash {
		return true
	}
	if o.Kind != spec.OpDrop {
		return false
	}
	m, err := os.ReadFile(j.staged("moves", filepath.FromSlash(o.Arg)))
	if err != nil {
		return false
	}
	if bytes.Equal(m, b) {
		return true
	}
	if !strings.HasSuffix(o.Arg, ".md") {
		return false
	}
	var fwd []links.Redirect
	for _, h := range j.ops {
		if h.Kind == spec.OpDrop && j.held[h.Arg] {
			fwd = append(fwd, links.Redirect{Src: vaultRel(j.vault, j.copyFile(h.Arg)), Dst: vaultRel(j.vault, j.dest(h.Arg))})
		}
	}
	if len(fwd) == 0 {
		return false
	}
	s, _ := links.RewriteText(j.copyFile(o.Arg), string(b), fwd)
	return s == string(m)
}

func (j *journalRun) unchanged(o spec.Op) (bool, error) {
	b, err := os.ReadFile(j.copyFile(o.Arg))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if !j.sameCopy(o, b) {
		j.warnings = append(j.warnings, fmt.Sprintf("%s changed in the copy after this promote planned: left there, it promotes later as an edit", o.Arg))
		return false, nil
	}
	return true, nil
}

func (j *journalRun) redirect(from int) error {
	var rs []links.Redirect
	for k := from; k < len(j.ops); k++ {
		o := j.ops[k]
		if o.Kind != spec.OpDrop || j.done[k+1] || j.held[o.Arg] {
			continue
		}
		b, err := os.ReadFile(j.copyFile(o.Arg))
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if !j.sameCopy(o, b) {
			continue
		}
		rs = append(rs, links.Redirect{Src: vaultRel(j.vault, j.copyFile(o.Arg)), Dst: vaultRel(j.vault, j.dest(o.Arg))})
	}
	if len(rs) == 0 {
		return nil
	}
	_, err := redirectLinks(j.vault, rs)
	return err
}

func (j *journalRun) mark(ns ...int) error {
	p := spec.DonePath(j.vault, j.id, j.slug)
	if j.torn {
		if err := os.Truncate(p, int64(j.complete)); err != nil {
			return err
		}
		j.torn = false
	}
	var b strings.Builder
	for _, n := range ns {
		fmt.Fprintf(&b, "%d\n", n)
	}
	if err := appendDone(p, []byte(b.String())); err != nil {
		return err
	}
	for _, n := range ns {
		j.done[n] = true
	}
	return nil
}

func appendFile(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func restoreWrite(p string, b []byte, remove bool) error {
	if remove {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(p, b, 0o644)
}

func (j *journalRun) checkStage(hold []string) error {
	anyHeld := len(j.held)+len(hold) > 0
	noteRel := vaultRel(j.vault, spec.VersionPath(j.vault, j.slug, j.id))
	puts := false
	for i, o := range j.ops {
		switch o.Kind {
		case spec.OpWrite:
			if j.done[i+1] {
				continue
			}
			src := j.staged(spec.WriteOfKept)
			if o.Arg == spec.WriteOfNote {
				src = j.staged("note.md")
			}
			ok, err := exists(src)
			if err != nil {
				return err
			}
			if !ok && !(o.Arg == spec.WriteOfNote && anyHeld && j.absent[spec.WriteOfNote][o.Dest]) {
				return fmt.Errorf("the stage lacks %s", vaultRel(j.vault, src))
			}
		case spec.OpPut:
			puts = true
			for _, tree := range []string{spec.TreeBase, spec.TreeAfter} {
				ok, err := exists(j.staged("prev", tree, filepath.FromSlash(o.Arg)))
				if err != nil {
					return err
				}
				if ok == j.absent[tree][o.Arg] {
					return fmt.Errorf("prev/%s/%s: in both prev/ and prev/absent, or in neither", tree, o.Arg)
				}
			}
		}
	}
	if puts {
		ok, err := exists(j.staged("prev", "note.md"))
		if err != nil {
			return err
		}
		if ok == j.absent[spec.WriteOfNote][noteRel] {
			return errors.New("prev/note.md: in both prev/ and prev/absent, or in neither")
		}
	}
	return nil
}

func (j *journalRun) holdBack(hold []string) error {
	for _, f := range hold {
		for _, tree := range []string{spec.TreeBase, spec.TreeAfter} {
			dst := filepath.Join(j.treeDir(tree), filepath.FromSlash(f))
			if j.absent[tree][f] {
				if err := restoreFile(dst, nil, true); err != nil {
					return err
				}
				continue
			}
			b, err := os.ReadFile(j.staged("prev", tree, filepath.FromSlash(f)))
			if err != nil {
				return err
			}
			if err := restoreFile(dst, b, false); err != nil {
				return err
			}
		}
	}
	for _, f := range hold {
		j.held[f] = true
	}
	if err := j.renderNote(); err != nil {
		return err
	}
	files := make([]string, 0, len(j.held))
	for f := range j.held {
		files = append(files, f)
	}
	sort.Strings(files)
	if err := restoreFile(spec.HeldPath(j.vault, j.id, j.slug), []byte(strings.Join(files, "\n")+"\n"), false); err != nil {
		return err
	}
	var ns []int
	for i, o := range j.ops {
		if (o.Kind == spec.OpPut || o.Kind == spec.OpDrop) && slices.Contains(hold, o.Arg) && !j.done[i+1] {
			ns = append(ns, i+1)
		}
	}
	return j.mark(ns...)
}

func (j *journalRun) renderNote() error {
	noteN := 0
	var noteOp spec.Op
	for i, o := range j.ops {
		if o.Kind == spec.OpWrite && o.Arg == spec.WriteOfNote {
			noteN, noteOp = i+1, o
		}
	}
	if noteN == 0 {
		return nil
	}
	staged := j.staged("note.md")
	v, err := spec.ReadVersion(staged)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	var files []string
	noPrev := j.absent[spec.WriteOfNote][noteOp.Dest]
	if !noPrev {
		pv, err := spec.ReadVersion(j.staged("prev", "note.md"))
		if err != nil {
			return err
		}
		files = pv.Files
	}
	var moved []string
	for _, o := range j.ops {
		if o.Kind == spec.OpPut && !j.held[o.Arg] {
			moved = append(moved, o.Arg)
		}
	}
	v.Files = spec.MergeFiles(files, moved)
	live := spec.VersionPath(j.vault, j.slug, j.id)
	if len(v.Files) == 0 && noPrev {
		if err := restoreFile(staged, nil, true); err != nil {
			return err
		}
		if j.done[noteN] {
			return restoreFile(live, nil, true)
		}
		return nil
	}
	base, err := spec.ReadSnapshot(j.treeDir(spec.TreeBase))
	if err != nil {
		return err
	}
	after, err := spec.ReadSnapshot(j.treeDir(spec.TreeAfter))
	if err != nil {
		return err
	}
	if after == nil {
		after = spec.Tree{}
	}
	before, aft := spec.Sides(after, base, after, spec.Tree{}, v.Files)
	body, err := spec.RenderVersion(v, note.RelDir(j.vault, j.id), before, spec.DiffTrees(j.slug, before, aft))
	if err != nil {
		return err
	}
	if err := restoreFile(staged, body, false); err != nil {
		return err
	}
	if j.done[noteN] {
		return restoreFile(live, body, false)
	}
	return nil
}

func (j *journalRun) result() (promoteResult, error) {
	pr := promoteResult{Slug: j.slug, Status: promoteNothing, Files: []string{}, Capabilities: []string{}, Warnings: j.warnings}
	var trashed []string
	for _, o := range j.ops {
		switch o.Kind {
		case spec.OpPut:
			if !j.held[o.Arg] {
				pr.Files = append(pr.Files, o.Arg)
			}
		case spec.OpTrash:
			trashed = append(trashed, o.Arg)
		case spec.OpWrite:
			if o.Arg == spec.WriteOfNote {
				if ok, err := exists(spec.VersionPath(j.vault, j.slug, j.id)); err != nil {
					return pr, err
				} else if ok {
					pr.Version = o.Dest
				}
			}
		}
	}
	if len(pr.Files) > 0 {
		pr.Status, pr.Capabilities = promoteDone, capsOf(pr.Files)
	}
	if len(trashed) > 0 {
		pr.Resolved = &promoteResolved{Keep: keepTheirs, Files: trashed, Trash: vaultRel(j.vault, promoteTrash(j.vault, j.id, j.slug)) + "/"}
	}
	if len(j.held) == 0 {
		return pr, nil
	}
	versions, err := spec.ListVersions(j.vault, j.slug)
	if err != nil {
		return pr, err
	}
	files := make([]string, 0, len(j.held))
	for f := range j.held {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		h := promoteHeld{File: f, Reason: "projects/ changed after this promote planned; no version note holds it"}
		cur, ok, err := readOptional(j.dest(f))
		if err != nil {
			return pr, err
		}
		by, err := spec.ChangedBy(j.vault, j.slug, versions, j.id, f, cur, ok)
		if err != nil {
			return pr, err
		}
		if len(by) > 0 {
			h.Ticket, h.Note = by[0].Version.ID(), vaultRel(j.vault, by[0].Path)
			h.Reason = fmt.Sprintf("projects/ holds %s's recorded after copy (%s)", h.Ticket, h.Note)
		}
		pr.Held = append(pr.Held, h)
	}
	return pr, nil
}

func readOptional(p string) (b []byte, ok bool, err error) {
	b, err = os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func replaceTree(staged, dst string) error {
	parent, name := filepath.Dir(dst), filepath.Base(dst)
	olds, _ := filepath.Glob(filepath.Join(parent, "."+name+".old-*"))
	if _, err := os.Lstat(staged); errors.Is(err, os.ErrNotExist) {
		for _, o := range olds {
			if err := os.RemoveAll(o); err != nil {
				return err
			}
		}
		return nil
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		old := filepath.Join(parent, fmt.Sprintf(".%s.old-%d", name, time.Now().UnixNano()))
		if err := renameTree(dst, old); err != nil {
			return err
		}
		olds = append(olds, old)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := renameTree(staged, dst); err != nil {
		return err
	}
	for _, o := range olds {
		if err := os.RemoveAll(o); err != nil {
			return err
		}
	}
	return nil
}

func cleanPromote(vaultDir, id, slug string) error {
	if err := os.Remove(spec.JournalPath(vaultDir, id, slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := removeStage(vaultDir, id, slug); err != nil {
		return err
	}
	_ = os.Remove(spec.StageRoot(vaultDir, id))
	fsx.RemoveEmptyDirs(spec.CopyDir(vaultDir, id, slug))
	return nil
}

func capsOf(files []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, f := range files {
		c := strings.SplitN(f, "/", 2)[0]
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func explainConflict(ctx vault.Context, id, slug string, conf []string, projects, base, after, cp spec.Tree) (*promoteConflict, error) {
	anc := spec.Ancestor(base, after, conf)
	c := &promoteConflict{
		Files:  conf,
		Others: []promoteOther{},
		Theirs: spec.DiffTrees(slug, anc, spec.Pick(projects, conf)),
		Ours:   spec.DiffTrees(slug, anc, spec.Pick(cp, conf)),
		Copy:   vaultRel(ctx.Vault, spec.CopyDir(ctx.Vault, id, slug)) + "/",
	}
	c.Clashes = spec.Clashes(c.Theirs, c.Ours)
	if c.Clashes == nil {
		c.Clashes = []spec.Clash{}
	}
	versions, err := spec.ListVersions(ctx.Vault, slug)
	if err != nil {
		return nil, err
	}
	named := map[string]bool{}
	for _, f := range conf {
		cur, ok := projects[f]
		by, err := spec.ChangedBy(ctx.Vault, slug, versions, id, f, cur, ok)
		if err != nil {
			return nil, err
		}
		for _, vf := range by {
			if !named[vf.Path] {
				named[vf.Path] = true
				c.Others = append(c.Others, promoteOther{Ticket: vf.Version.ID(), Note: vaultRel(ctx.Vault, vf.Path), Promoted: vf.Version.Promoted})
			}
		}
	}
	sort.SliceStable(c.Others, func(a, b int) bool { return c.Others[a].Promoted > c.Others[b].Promoted })
	return c, nil
}

func printPromote(out io.Writer, id string, pr promoteResult) {
	if pr.Replayed != nil {
		fmt.Fprintf(out, "%s: finished the pending promote first:\n", pr.Slug)
		printPromote(out, id, *pr.Replayed)
	}
	for _, w := range pr.Warnings {
		fmt.Fprintf(out, "%s: warning: %s\n", pr.Slug, w)
	}
	for _, h := range pr.Held {
		fmt.Fprintf(out, "%s: held back %s: %s\n", pr.Slug, h.File, h.Reason)
	}
	if pr.Resolved != nil {
		fmt.Fprintf(out, "%s: kept %s for %s\n", pr.Slug, pr.Resolved.Keep, strings.Join(pr.Resolved.Files, ", "))
		if pr.Resolved.Trash != "" {
			fmt.Fprintf(out, "%s: the copy's files moved to %s\n", pr.Slug, pr.Resolved.Trash)
		}
	}
	switch pr.Status {
	case promoteNothing:
		fmt.Fprintf(out, "%s: nothing to promote\n", pr.Slug)
	case promoteDone:
		fmt.Fprintf(out, "%s: promoted %d files (%s); version %s\n", pr.Slug, len(pr.Files), strings.Join(pr.Capabilities, ", "), pr.Version)
	case promoteRefused:
		c := pr.Conflict
		fmt.Fprintf(out, "%s: refused: another ticket changed %s in projects/%s/specs/ since this ticket's snapshot\n", pr.Slug, strings.Join(c.Files, ", "), pr.Slug)
		if len(c.Others) == 0 {
			fmt.Fprintln(out, "  changed by: no version note names these files")
		}
		for _, o := range c.Others {
			fmt.Fprintf(out, "  changed by: %s (%s, promoted %s)\n", o.Ticket, o.Note, o.Promoted)
		}
		for _, side := range []struct {
			name string
			d    spec.Diff
		}{{"theirs (projects/)", c.Theirs}, {"ours (the ticket's copy)", c.Ours}} {
			fmt.Fprintf(out, "  %s:\n", side.name)
			lines := side.d.Summary()
			if len(lines) == 0 {
				lines = []string{"no requirement change"}
			}
			for _, l := range lines {
				fmt.Fprintf(out, "    %s\n", l)
			}
		}
		for _, cl := range c.Clashes {
			var what bytes.Buffer
			if len(cl.Requirements) > 0 {
				what.WriteString("both changed " + strings.Join(cl.Requirements, ", "))
			} else {
				what.WriteString("both changed the file, no requirement in common")
			}
			if cl.Interfaces {
				what.WriteString("; both changed its interfaces")
			}
			fmt.Fprintf(out, "  clash: %s (%s): %s\n", cl.Capability, cl.File, what.String())
		}
		fmt.Fprintf(out, "  resolve: after merging into %s, re-run with --resolve %s=merged,\n", c.Copy, pr.Slug)
		fmt.Fprintf(out, "    or --resolve %s=theirs (drop the copy's conflicting files) or --resolve %s=ours (replace them with the copy)\n", pr.Slug, pr.Slug)
	}
}

func needsStart(ctx vault.Context, id, slug string) error {
	cp, err := spec.ReadTree(spec.CopyDir(ctx.Vault, id, slug))
	if err != nil || len(cp) == 0 {
		return err
	}
	base, err := spec.ReadSnapshot(spec.BaseDir(ctx.Vault, id, slug))
	if err != nil {
		return err
	}
	if base != nil {
		return pendingRebase(ctx, id, slug)
	}
	return fmt.Errorf("%s: the copy %s/ has no snapshot %s/: run tix spec start %s %s first (it snapshots projects/%s/specs/ as it is now; another ticket's change made before that can't be told from this ticket's)",
		slug, vaultRel(ctx.Vault, spec.CopyDir(ctx.Vault, id, slug)), vaultRel(ctx.Vault, spec.BaseDir(ctx.Vault, id, slug)), id, slug, slug)
}

func pendingRebase(ctx vault.Context, id, slug string) error {
	if err := spec.PendingRebase(ctx.Vault, id, slug); err != nil {
		if errors.Is(err, spec.ErrRebasePending) {
			return fmt.Errorf("%w, then promote", err)
		}
		return err
	}
	return nil
}

func refuseUnfilled(ctx vault.Context, id, slug string, after, projects spec.Tree, v spec.Version, cp spec.Tree) error {
	files := spec.Unfilled(after, projects, v.Files, cp)
	if len(files) == 0 {
		return nil
	}
	versions, err := spec.ListVersions(ctx.Vault, slug)
	if err != nil {
		return err
	}
	for _, f := range files {
		o, ok := spec.PromotedSince(versions, id, f, v.Promoted)
		if !ok {
			continue
		}
		return fmt.Errorf("%s: %s/ lacks %s, which this ticket promoted (a promotion stopped part way), and %s promoted it since (%s, promoted %s): projects/%s/specs/ no longer holds this ticket's content. Run tix spec start %s %s %s to copy it again, redo the change, then promote",
			slug, vaultRel(ctx.Vault, spec.AfterDir(ctx.Vault, id, slug)), f, o.Version.ID(), vaultRel(ctx.Vault, o.Path), o.Version.Promoted, slug, id, slug, strings.SplitN(f, "/", 2)[0])
	}
	return nil
}
