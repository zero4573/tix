package jira

import (
	"regexp"
	"strings"
)

// Jira's markdown writes an empty task box as "[]" with no list marker
var taskItem = regexp.MustCompile(`^(\s*(?:>\s*)*)([-*+]|\d+[.)])?\s*\[([ xX]?)\]\s+(.*)$`)

var fenceOpen = regexp.MustCompile("^\\s*(`{3,}|~{3,})")

func plainTaskItems(md string) string {
	lines := strings.Split(md, "\n")
	fence := ""
	for i, l := range lines {
		if fence != "" {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if m := fenceOpen.FindStringSubmatch(l); m != nil {
			fence = m[1]
			continue
		}
		m := taskItem.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		prefix, marker, box, text := m[1], m[2], m[3], m[4]
		if marker == "" && box != "" {
			continue
		}
		if marker == "" || strings.ContainsAny(marker, "*+") {
			marker = "-"
		}
		state := " "
		if box != " " && box != "" {
			state = "x"
		}
		lines[i] = prefix + marker + ` \[` + state + `\] ` + text
	}
	return strings.Join(lines, "\n")
}
