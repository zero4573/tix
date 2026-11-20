package diffcheck

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zero4573/tix/internal/gitx"
)

var (
	boardWaveRe  = regexp.MustCompile(`(?m)^- \[.\] .* · w([0-9]+) · `)
	reviewWaveRe = regexp.MustCompile(`^wave-([0-9]+)-`)
	waveTestRe   = regexp.MustCompile(`\b\w*?[Tt]est_?[Ww]([0-9]+)\w*`)
)

func TicketWaves(folder string) []int {
	set := map[int]bool{}
	add := func(s string) {
		if n, err := strconv.Atoi(s); err == nil {
			set[n] = true
		}
	}
	for _, f := range []string{"board.md", "board-archive.md"} {
		b, err := os.ReadFile(filepath.Join(folder, f))
		if err != nil {
			continue
		}
		for _, m := range boardWaveRe.FindAllStringSubmatch(string(b), -1) {
			add(m[1])
		}
	}
	reviews, _ := filepath.Glob(filepath.Join(folder, "reviews", "wave-*.md"))
	for _, f := range reviews {
		if m := reviewWaveRe.FindStringSubmatch(filepath.Base(f)); m != nil {
			add(m[1])
		}
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

type waveTestMatcher struct{ waves map[int]bool }

func (m waveTestMatcher) count(s string, into map[string]int) {
	for _, sm := range waveTestRe.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(sm[1]); err == nil && m.waves[n] {
			into[sm[0]]++
		}
	}
}

func isTestPath(p string) bool {
	base := path.Base(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch {
	case strings.HasSuffix(stem, "_test"), strings.HasSuffix(stem, "_spec"),
		strings.HasPrefix(base, "test_") && ext == ".py",
		strings.HasSuffix(stem, ".test"), strings.HasSuffix(stem, ".spec"):
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "test", "tests", "__tests__", "spec":
			return true
		}
	}
	return false
}

func CheckWaveTests(dir, revRange string, waves []int) ([]IDHit, error) {
	if len(waves) == 0 {
		return nil, nil
	}
	revs, err := Revs(dir, revRange)
	if err != nil {
		return nil, err
	}
	m := waveTestMatcher{waves: map[int]bool{}}
	for _, n := range waves {
		m.waves[n] = true
	}
	var hits []IDHit
	for _, sha := range revs {
		h, err := commitIDs(dir, sha, m, false, isTestPath)
		if err != nil {
			return nil, err
		}
		hits = append(hits, h...)
	}
	return hits, nil
}

var waveWordRe = regexp.MustCompile(`(?i)\bwaves?[ -]?([0-9]+)\b`)

func CheckWaveWords(dir, revRange string, waves []int) ([]IDHit, error) {
	if len(waves) == 0 {
		return nil, nil
	}
	if strings.HasPrefix(revRange, "-") {
		return nil, fmt.Errorf("bad range '%s' (it must not start with '-')", revRange)
	}
	out, err := gitx.OutIn(dir, nil, "rev-list", revRange, "--")
	if err != nil {
		return nil, err
	}
	known := map[int]bool{}
	for _, w := range waves {
		known[w] = true
	}
	var hits []IDHit
	for _, sha := range strings.Fields(out) {
		msg, err := gitx.OutIn(dir, nil, "log", "-1", "--format=%B", sha)
		if err != nil {
			return nil, err
		}
		seen := map[int]bool{}
		for _, m := range waveWordRe.FindAllStringSubmatch(msg, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || !known[n] || seen[n] {
				continue
			}
			seen[n] = true
			hits = append(hits, IDHit{ID: fmt.Sprintf("wave %d", n), Commit: ShortSHA(sha), Path: MessagePath})
		}
	}
	return hits, nil
}
