package cvewatch

import (
	"strconv"
	"strings"
)

func normVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "go")
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	return v
}

type token struct {
	num   uint64
	isNum bool
	word  string
}

const releaseRank = 10

var wordRanks = map[string]int{
	"dev": 0, "alpha": 1, "a": 1, "beta": 2, "b": 2, "milestone": 3, "m": 3,
	"rc": 4, "cr": 4, "c": 4, "pre": 4, "preview": 4, "snapshot": 5,
	"final": releaseRank, "ga": releaseRank, "release": releaseRank, "jre": releaseRank, "android": releaseRank,
	"sp": 11, "post": 11, "p": 11, "patch": 11,
}

const unknownWordRank = 4

func rankOf(w string) int {
	if r, ok := wordRanks[w]; ok {
		return r
	}
	return unknownWordRank
}

func tokenize(s string) []token {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n, err := strconv.ParseUint(s[i:j], 10, 64)
			if err != nil {
				out = append(out, token{word: s[i:j]})
			} else {
				out = append(out, token{num: n, isNum: true})
			}
			i = j
		case c >= 'a' && c <= 'z':
			j := i
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			out = append(out, token{word: s[i:j]})
			i = j
		default:
			i++
		}
	}
	return out
}

type parsedVersion struct {
	main   []token
	pre    []token
	hasPre bool
}

func parseVersion(v string) parsedVersion {
	v = strings.ToLower(normVersion(v))
	v, _, _ = strings.Cut(v, "+")
	mainStr, preStr := v, ""
	if i := strings.IndexAny(v, "-~"); i >= 0 {
		mainStr, preStr = v[:i], v[i+1:]
	}
	p := parsedVersion{main: tokenize(mainStr)}
	pre := tokenize(preStr)
	if preStr != "" && onlyQualifiers(pre) {
		p.main = append(p.main, pre...)
		return p
	}
	p.pre, p.hasPre = pre, preStr != ""
	return p
}

func onlyQualifiers(ts []token) bool {
	if len(ts) == 0 {
		return false
	}
	for _, t := range ts {
		if !t.isNum && rankOf(t.word) < releaseRank {
			return false
		}
	}
	return !ts[0].isNum
}

func compareToken(x, y token) int {
	switch {
	case x.isNum && y.isNum:
		switch {
		case x.num < y.num:
			return -1
		case x.num > y.num:
			return 1
		}
		return 0
	case x.isNum:
		return 1
	case y.isNum:
		return -1
	}
	rx, ry := rankOf(x.word), rankOf(y.word)
	switch {
	case rx < ry:
		return -1
	case rx > ry:
		return 1
	}
	return strings.Compare(x.word, y.word)
}

func compareMissing(t token) int {
	switch {
	case t.isNum && t.num == 0:
		return 0
	case t.isNum:
		return 1
	}
	switch r := rankOf(t.word); {
	case r < releaseRank:
		return -1
	case r > releaseRank:
		return 1
	}
	return 0
}

func compareTokens(a, b []token) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var c int
		switch {
		case i >= len(a):
			c = -compareMissing(b[i])
		case i >= len(b):
			c = compareMissing(a[i])
		default:
			c = compareToken(a[i], b[i])
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	if c := compareTokens(pa.main, pb.main); c != 0 {
		return c
	}
	switch {
	case pa.hasPre == pb.hasPre && !pa.hasPre:
		return 0
	case !pa.hasPre:
		return 1
	case !pb.hasPre:
		return -1
	}
	return compareTokens(pa.pre, pb.pre)
}
