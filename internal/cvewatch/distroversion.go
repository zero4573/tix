package cvewatch

import (
	"strconv"
	"strings"
)

type versionOrder int

const (
	orderGeneric versionOrder = iota
	orderDpkg
	orderRPM
	orderAPK
)

func orderOf(eco string) versionOrder {
	switch {
	case strings.HasPrefix(eco, "Debian:"), strings.HasPrefix(eco, "Ubuntu:"):
		return orderDpkg
	case strings.HasPrefix(eco, "Red Hat"), strings.HasPrefix(eco, "Rocky Linux"), strings.HasPrefix(eco, "AlmaLinux"):
		return orderRPM
	case strings.HasPrefix(eco, "Alpine:"), eco == "Wolfi", eco == "Chainguard":
		return orderAPK
	}
	return orderGeneric
}

func compareIn(eco, a, b string) int {
	switch orderOf(eco) {
	case orderDpkg:
		return compareDpkg(a, b)
	case orderRPM:
		return compareRPM(a, b)
	case orderAPK:
		return compareAPK(a, b)
	}
	return compareVersions(a, b)
}

func splitEpoch(v string) (int, string) {
	if i := strings.IndexByte(v, ':'); i > 0 {
		if n, err := strconv.Atoi(v[:i]); err == nil {
			return n, v[i+1:]
		}
	}
	return 0, v
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareDpkg(a, b string) int {
	ea, ra := splitEpoch(a)
	eb, rb := splitEpoch(b)
	if c := cmpInt(ea, eb); c != 0 {
		return c
	}
	ua, va := splitRevision(ra)
	ub, vb := splitRevision(rb)
	if c := dpkgVerrev(ua, ub); c != 0 {
		return c
	}
	return dpkgVerrev(va, vb)
}

func splitRevision(v string) (string, string) {
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func dpkgOrder(s string, i int) int {
	if i >= len(s) {
		return 0
	}
	switch c := s[i]; {
	case isDigit(c):
		return 0
	case isAlpha(c):
		return int(c)
	case c == '~':
		return -1
	default:
		return int(c) + 256
	}
}

func dpkgVerrev(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		for i < len(a) && !isDigit(a[i]) || j < len(b) && !isDigit(b[j]) {
			if c := cmpInt(dpkgOrder(a, i), dpkgOrder(b, j)); c != 0 {
				return c
			}
			i, j = i+1, j+1
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		diff := 0
		for i < len(a) && isDigit(a[i]) && j < len(b) && isDigit(b[j]) {
			if diff == 0 {
				diff = cmpInt(int(a[i]), int(b[j]))
			}
			i, j = i+1, j+1
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if diff != 0 {
			return diff
		}
	}
	return 0
}

func compareRPM(a, b string) int {
	ea, ra := splitEpoch(a)
	eb, rb := splitEpoch(b)
	if c := cmpInt(ea, eb); c != 0 {
		return c
	}
	va, rela := splitRevision(ra)
	vb, relb := splitRevision(rb)
	if c := rpmvercmp(va, vb); c != 0 {
		return c
	}
	if rela == "" || relb == "" {
		return 0
	}
	return rpmvercmp(rela, relb)
}

func rpmSeparator(c byte) bool { return !isDigit(c) && !isAlpha(c) && c != '~' && c != '^' }

func rpmvercmp(a, b string) int {
	if a == b {
		return 0
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		for i < len(a) && rpmSeparator(a[i]) {
			i++
		}
		for j < len(b) && rpmSeparator(b[j]) {
			j++
		}
		at, bt := i < len(a), j < len(b)
		if at && a[i] == '~' || bt && b[j] == '~' {
			if !at || a[i] != '~' {
				return 1
			}
			if !bt || b[j] != '~' {
				return -1
			}
			i, j = i+1, j+1
			continue
		}
		if at && a[i] == '^' || bt && b[j] == '^' {
			switch {
			case !at:
				return -1
			case !bt:
				return 1
			case a[i] != '^':
				return 1
			case b[j] != '^':
				return -1
			}
			i, j = i+1, j+1
			continue
		}
		if !at || !bt {
			break
		}
		numeric := isDigit(a[i])
		class := isAlpha
		if numeric {
			class = isDigit
		}
		si, sj := i, j
		for i < len(a) && class(a[i]) {
			i++
		}
		for j < len(b) && class(b[j]) {
			j++
		}
		sa, sb := a[si:i], b[sj:j]
		if sb == "" {
			if numeric {
				return 1
			}
			return -1
		}
		if numeric {
			sa, sb = strings.TrimLeft(sa, "0"), strings.TrimLeft(sb, "0")
			if c := cmpInt(len(sa), len(sb)); c != 0 {
				return c
			}
		}
		if c := strings.Compare(sa, sb); c != 0 {
			return c
		}
	}
	at, bt := i < len(a), j < len(b)
	switch {
	case !at && !bt:
		return 0
	case !at:
		return -1
	}
	return 1
}

var apkSuffixRank = map[string]int{"alpha": -4, "beta": -3, "pre": -2, "rc": -1,
	"cvs": 1, "svn": 2, "git": 3, "hg": 4, "p": 5}

type apkVersion struct {
	nums     []string
	letter   byte
	suffixes [][2]int
	rev      int
}

func parseAPK(v string) apkVersion {
	var p apkVersion
	if i := strings.LastIndex(v, "-r"); i >= 0 {
		if n, err := strconv.Atoi(v[i+2:]); err == nil {
			p.rev, v = n, v[:i]
		}
	}
	main, rest, _ := strings.Cut(v, "_")
	if n := len(main); n > 0 && isAlpha(main[n-1]) {
		p.letter, main = main[n-1], main[:n-1]
	}
	p.nums = strings.Split(main, ".")
	for rest != "" {
		var part string
		part, rest, _ = strings.Cut(rest, "_")
		k := 0
		for k < len(part) && isAlpha(part[k]) {
			k++
		}
		n, _ := strconv.Atoi(part[k:])
		p.suffixes = append(p.suffixes, [2]int{apkSuffixRank[part[:k]], n})
	}
	return p
}

func compareNumString(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if c := cmpInt(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func compareAPK(a, b string) int {
	pa, pb := parseAPK(a), parseAPK(b)
	for k := 0; k < len(pa.nums) || k < len(pb.nums); k++ {
		switch {
		case k >= len(pa.nums):
			return -1
		case k >= len(pb.nums):
			return 1
		}
		if c := compareNumString(pa.nums[k], pb.nums[k]); c != 0 {
			return c
		}
	}
	if c := cmpInt(int(pa.letter), int(pb.letter)); c != 0 {
		return c
	}
	for k := 0; k < len(pa.suffixes) || k < len(pb.suffixes); k++ {
		var sa, sb [2]int
		if k < len(pa.suffixes) {
			sa = pa.suffixes[k]
		}
		if k < len(pb.suffixes) {
			sb = pb.suffixes[k]
		}
		if c := cmpInt(sa[0], sb[0]); c != 0 {
			return c
		}
		if c := cmpInt(sa[1], sb[1]); c != 0 {
			return c
		}
	}
	return cmpInt(pa.rev, pb.rev)
}
