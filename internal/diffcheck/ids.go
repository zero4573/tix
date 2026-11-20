package diffcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/zero4573/tix/internal/gitx"
)

const MessagePath = ":commit-message"

type IDHit struct {
	ID     string
	Commit string
	Path   string
}

var (
	relayIDRe   = regexp.MustCompile(`(?m)^(R-[0-9]+) · `)
	backlogIDRe = regexp.MustCompile(`(?m)^- \[.\] (B-[0-9]+) · `)
	findingIDRe = regexp.MustCompile(`(?m)^- (?:\[.\] )?(W[0-9]+-[A-Za-z0-9-]+-[MSC][0-9]+) · `)
)

func TicketIDs(folder, id string) []string {
	set := map[string]bool{id: true}
	add := func(file string, re *regexp.Regexp) {
		b, err := os.ReadFile(file)
		if err != nil {
			return
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			set[m[1]] = true
		}
	}
	add(filepath.Join(folder, "relay.md"), relayIDRe)
	add(filepath.Join(folder, "backlog.md"), backlogIDRe)
	reviews, _ := filepath.Glob(filepath.Join(folder, "reviews", "*.md"))
	for _, f := range reviews {
		add(f, findingIDRe)
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type matcher interface {
	count(s string, into map[string]int)
}

type pathMatcher interface {
	forPath(p string) matcher
}

type idMatcher struct{ re *regexp.Regexp }

func newIDMatcher(ids []string) idMatcher {
	if len(ids) == 0 {
		return idMatcher{}
	}
	alts := make([]string, len(ids))
	for i, id := range ids {
		alts[i] = regexp.QuoteMeta(id)
	}
	sort.Slice(alts, func(i, j int) bool { return len(alts[i]) > len(alts[j]) })
	return idMatcher{re: regexp.MustCompile(`\b(?:` + strings.Join(alts, "|") + `)\b`)}
}

func (m idMatcher) count(s string, into map[string]int) {
	if m.re == nil {
		return
	}
	for _, id := range m.re.FindAllString(s, -1) {
		into[id]++
	}
}

func Revs(dir, revRange string) ([]string, error) {
	if strings.HasPrefix(revRange, "-") {
		return nil, fmt.Errorf("bad range '%s' (it must not start with '-')", revRange)
	}
	out, err := gitx.OutIn(dir, nil, "rev-list", "--no-merges", revRange, "--")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func CheckIDs(dir, revRange string, ids []string) ([]IDHit, error) {
	return CheckIDsStyle(dir, revRange, ids, "")
}

func CheckIDsStyle(dir, revRange string, ids []string, subjectID string) ([]IDHit, error) {
	revs, err := Revs(dir, revRange)
	if err != nil {
		return nil, err
	}
	m := newIDMatcher(ids)
	var hits []IDHit
	for _, sha := range revs {
		h, err := commitIDs(dir, sha, m, true, nil)
		if err != nil {
			return nil, err
		}
		if subjectID != "" {
			if h, err = dropSubjectID(dir, sha, m, subjectID, h); err != nil {
				return nil, err
			}
		}
		hits = append(hits, h...)
	}
	return hits, nil
}

func Subject(dir, sha string) (string, error) {
	return gitx.OutIn(dir, nil, "log", "-1", "--format=%s", sha)
}

func dropSubjectID(dir, sha string, m matcher, id string, hits []IDHit) ([]IDHit, error) {
	at := -1
	for i, h := range hits {
		if h.Path == MessagePath && h.ID == id {
			at = i
		}
	}
	if at < 0 {
		return hits, nil
	}
	msg, err := gitx.OutIn(dir, nil, "log", "-1", "--format=%B", sha)
	if err != nil {
		return nil, err
	}
	_, body, _ := strings.Cut(msg, "\n")
	inBody := map[string]int{}
	m.count(body, inBody)
	if inBody[id] > 0 {
		return hits, nil
	}
	return append(hits[:at:at], hits[at+1:]...), nil
}

func commitIDs(dir, sha string, m matcher, message bool, keep func(path string) bool) ([]IDHit, error) {
	var hits []IDHit
	if message {
		msg, err := gitx.OutIn(dir, nil, "log", "-1", "--format=%B", sha)
		if err != nil {
			return nil, err
		}
		inMsg := map[string]int{}
		m.count(msg, inMsg)
		for _, id := range sortedKeys(inMsg) {
			hits = append(hits, IDHit{ID: id, Commit: ShortSHA(sha), Path: MessagePath})
		}
	}
	diffArgs := []string{"diff-tree", "-r", "--root", "--no-renames", "--no-commit-id", "--no-ext-diff", "--no-textconv"}
	names, err := gitx.OutIn(dir, nil, append(diffArgs, "-z", "--name-status", sha)...)
	if err != nil {
		return nil, err
	}
	var paths []string
	if names != "" {
		f := strings.Split(strings.TrimRight(names, "\x00"), "\x00")
		for i := 0; i+1 < len(f); i += 2 {
			paths = append(paths, f[i+1])
			if f[i] == "T" {
				paths = append(paths, f[i+1])
			}
		}
	}
	type counts struct{ plus, minus map[string]int }
	per := map[string]*counts{}
	block, inHunk := -1, false
	var cur *counts
	lm := m
	err = gitx.Lines(dir, func(l string) {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			block++
			inHunk, cur = false, nil
			if block < len(paths) && (keep == nil || keep(paths[block])) {
				p := paths[block]
				if per[p] == nil {
					per[p] = &counts{map[string]int{}, map[string]int{}}
				}
				cur = per[p]
				lm = m
				if pm, ok := m.(pathMatcher); ok {
					lm = pm.forPath(p)
				}
			}
		case strings.HasPrefix(l, "@@"):
			inHunk = true
		case !inHunk || cur == nil:
		case strings.HasPrefix(l, "+"):
			lm.count(l[1:], cur.plus)
		case strings.HasPrefix(l, "-"):
			lm.count(l[1:], cur.minus)
		}
	}, append(diffArgs, "-p", "--no-color", sha)...)
	if err != nil {
		return nil, err
	}
	if block+1 != len(paths) {
		return nil, fmt.Errorf("diff-tree listed %d paths for %d diffs of %s", len(paths), block+1, ShortSHA(sha))
	}
	plus, minus := map[string]int{}, map[string]int{}
	var order []string
	for p, c := range per {
		order = append(order, p)
		for id, n := range c.plus {
			plus[id] += n
		}
		for id, n := range c.minus {
			minus[id] += n
		}
	}
	sort.Strings(order)
	for _, p := range order {
		c := per[p]
		for _, id := range sortedKeys(c.plus) {
			if plus[id] > minus[id] && c.plus[id] > c.minus[id] {
				hits = append(hits, IDHit{ID: id, Commit: ShortSHA(sha), Path: p})
			}
		}
	}
	return hits, nil
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
