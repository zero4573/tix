package ticketconv

import (
	"regexp"
	"strings"
)

func wordChar(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

func RewriteID(text, old, new string) string {
	if old == "" || !strings.Contains(text, old) {
		return text
	}
	lines := strings.SplitAfter(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "branch:") || strings.HasPrefix(l, "converted-from:") {
			continue
		}
		lines[i] = rewriteLine(l, old, new)
	}
	return strings.Join(lines, "")
}

func rewriteLine(l, old, new string) string {
	var out strings.Builder
	i := 0
	for {
		j := strings.Index(l[i:], old)
		if j < 0 {
			out.WriteString(l[i:])
			return out.String()
		}
		j += i
		end := j + len(old)
		whole := (j == 0 || !(wordChar(l[j-1]) || l[j-1] == '-')) && (end == len(l) || !wordChar(l[end]))
		if whole && strings.HasSuffix(l[:j], "task/") && end < len(l) && l[end] == '-' {
			whole = false
		}
		out.WriteString(l[i:j])
		if whole {
			out.WriteString(new)
		} else {
			out.WriteString(old)
		}
		i = end
	}
}

func RenameFile(old, new string) func(string) string {
	return func(base string) string {
		if rest, ok := strings.CutPrefix(base, old); ok && rest != "" && (rest[0] == '.' || rest[0] == '-') {
			return new + rest
		}
		return base
	}
}

func Section(text, heading string) string {
	var out []string
	in := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(l[3:]) == heading
			continue
		}
		if in {
			out = append(out, strings.TrimRight(l, "\r"))
		}
	}
	return strings.TrimSpace(stripComments(strings.Join(out, "\n")))
}

var commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

func stripComments(s string) string { return commentRe.ReplaceAllString(s, "") }

var taskRe = regexp.MustCompile(`(?m)^(\s*(?:[-*+]|\d+[.)])\s+)\[([ xX]?)\]\s`)

func HasTaskList(text string) bool { return taskRe.MatchString(text) }

func plainTasks(md string) string {
	return taskRe.ReplaceAllStringFunc(md, func(m string) string {
		sub := taskRe.FindStringSubmatch(m)
		box := `\[ \] `
		if sub[2] == "x" || sub[2] == "X" {
			box = `\[x\] `
		}
		return sub[1] + box
	})
}

func Description(text string) string {
	var parts []string
	if d := Section(text, "Description"); d != "" {
		parts = append(parts, d)
	}
	if a := Section(text, "Acceptance criteria"); a != "" {
		parts = append(parts, "### Acceptance criteria\n"+a)
	}
	return plainTasks(strings.Join(parts, "\n\n"))
}
