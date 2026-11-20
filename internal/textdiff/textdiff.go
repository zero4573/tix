package textdiff

import (
	"fmt"
	"strings"
)

type opKind byte

const (
	equal opKind = ' '
	del   opKind = '-'
	ins   opKind = '+'
)

type op struct {
	kind opKind
	line string
}

func lines(text []byte) []string {
	if len(text) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(text), "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func edits(a, b []string) []op {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{equal, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{del, a[i]})
			i++
		default:
			ops = append(ops, op{ins, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{del, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{ins, b[j]})
	}
	return ops
}

func span(before, count int) string {
	start := before + 1
	if count == 0 {
		start = before
	}
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func Unified(aName, bName string, a, b []byte, context int) string {
	ops := edits(lines(a), lines(b))
	var changes []int
	for k, o := range ops {
		if o.kind != equal {
			changes = append(changes, k)
		}
	}
	if len(changes) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", aName, bName)
	for g := 0; g < len(changes); {
		last := g
		for last+1 < len(changes) && changes[last+1]-changes[last] <= 2*context+1 {
			last++
		}
		from := max(0, changes[g]-context)
		to := min(len(ops), changes[last]+context+1)
		aBefore, bBefore := 0, 0
		for _, o := range ops[:from] {
			if o.kind != ins {
				aBefore++
			}
			if o.kind != del {
				bBefore++
			}
		}
		aCount, bCount := 0, 0
		for _, o := range ops[from:to] {
			if o.kind != ins {
				aCount++
			}
			if o.kind != del {
				bCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", span(aBefore, aCount), span(bBefore, bCount))
		for _, o := range ops[from:to] {
			out.WriteByte(byte(o.kind))
			out.WriteString(o.line)
			if !strings.HasSuffix(o.line, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		g = last + 1
	}
	return out.String()
}
