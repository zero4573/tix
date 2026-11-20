package spec

import "strings"

func changeBounds(lines []string) (from, to int) {
	from, to = -1, len(lines)
	fence := false
	for i, ln := range lines {
		ln = strings.TrimRight(ln, "\r\n")
		if isFence(ln) {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		lvl, h := heading(ln)
		if from < 0 {
			if lvl == 2 && h == SectionHeading {
				from = i
			}
		} else if lvl == 1 || lvl == 2 {
			return from, i
		}
	}
	return from, to
}

func keepLines(s string) []string {
	return strings.SplitAfter(s, "\n")
}

func CurrentChange(desc string) (string, bool) {
	lines := keepLines(desc)
	from, to := changeBounds(lines)
	if from < 0 {
		return "", false
	}
	return strings.Join(lines[from:to], ""), true
}

func HasChange(desc string) bool {
	lines := keepLines(desc)
	from, to := changeBounds(lines)
	if from < 0 {
		return false
	}
	body := strings.TrimSpace(strings.Join(lines[from+1:to], ""))
	return body != "" && body != "No spec change."
}

func ReplaceChange(desc, body string) string {
	eol := "\n"
	if strings.Contains(desc, "\r\n") {
		eol = "\r\n"
	}
	body = strings.TrimRight(body, "\n")
	if body == "" {
		body = "No spec change."
	}
	section := "## " + SectionHeading + "\n\n" + body + "\n"
	if eol != "\n" {
		section = strings.ReplaceAll(section, "\n", eol)
	}
	lines := keepLines(desc)
	from, to := changeBounds(lines)
	if from < 0 {
		if desc == "" {
			return section
		}
		sep := ""
		switch {
		case !strings.HasSuffix(desc, "\n"):
			sep = eol + eol
		case !strings.HasSuffix(desc, eol+eol) && !strings.HasSuffix(desc, "\n\n"):
			sep = eol
		}
		return desc + sep + section
	}
	if to < len(lines) {
		section += eol
	}
	return strings.Join(lines[:from], "") + section + strings.Join(lines[to:], "")
}
