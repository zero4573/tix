package note

import (
	"os"
	"regexp"
	"strings"
)

func Normalize(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func fileLines(file string) ([]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	s := Normalize(string(data))
	if s == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), nil
}

func writeLines(file string, lines []string) error {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return os.WriteFile(file, []byte(b.String()), 0o644)
}

func continuation(l string) bool {
	return strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "- ") || l == "-"
}

func Set(file, key, value string) error {
	lines, err := fileLines(file)
	if err != nil {
		return err
	}
	if len(lines) == 0 || lines[0] != "---" {
		return nil
	}
	field := key + ":"
	if value != "" {
		field += " " + value
	}
	out := []string{lines[0]}
	done, in, dropping := false, true, false
	for _, l := range lines[1:] {
		if dropping && in && continuation(l) {
			continue
		}
		dropping = false
		switch {
		case in && l == "---":
			if !done {
				out = append(out, field)
			}
			in, done = false, true
			out = append(out, l)
		case in && strings.HasPrefix(l, key+":"):
			out = append(out, field)
			done, dropping = true, true
		default:
			out = append(out, l)
		}
	}
	return writeLines(file, out)
}

var (
	flowTags  = regexp.MustCompile(`^tags: *\[(.*)\] *$`)
	blockTags = regexp.MustCompile(`^tags: *$`)
)

func listItem(l string) string {
	v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "-"))
	if n := len(v); n >= 2 && (v[0] == '"' && v[n-1] == '"' || v[0] == '\'' && v[n-1] == '\'') {
		v = v[1 : n-1]
	}
	return v
}

func EditTags(file string, add bool, tag string) error {
	lines, err := fileLines(file)
	if err != nil {
		return err
	}
	if len(lines) == 0 || lines[0] != "---" {
		return nil
	}
	for i := 1; i < len(lines) && lines[i] != "---"; i++ {
		var items []string
		end := i + 1
		if m := flowTags.FindStringSubmatch(lines[i]); m != nil {
			items = strings.Split(m[1], ",")
		} else if blockTags.MatchString(lines[i]) {
			for end < len(lines) && continuation(lines[end]) {
				items = append(items, listItem(lines[end]))
				end++
			}
		} else {
			continue
		}
		var out []string
		found := false
		for _, x := range items {
			x = strings.Trim(x, " ")
			if x == "" {
				continue
			}
			if x == tag {
				found = true
				if !add {
					continue
				}
			}
			out = append(out, x)
		}
		if add && !found {
			out = append(out, tag)
		}
		lines = append(append(lines[:i:i], "tags: ["+strings.Join(out, ", ")+"]"), lines[end:]...)
	}
	return writeLines(file, lines)
}

func Fields(file string) map[string]string {
	lines, _ := fileLines(file)
	f := map[string]string{}
	if len(lines) == 0 || lines[0] != "---" {
		return f
	}
	for i := 1; i < len(lines) && lines[i] != "---"; i++ {
		m := fieldRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		v := strings.Trim(m[2], " \t")
		if v == "" && i+1 < len(lines) && continuation(lines[i+1]) {
			var items []string
			for i+1 < len(lines) && continuation(lines[i+1]) {
				i++
				items = append(items, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "-")))
			}
			v = "[" + strings.Join(items, ", ") + "]"
		}
		v = strings.TrimPrefix(v, `"`)
		v = strings.TrimSuffix(v, `"`)
		f[m[1]] = v
	}
	return f
}

var fieldRe = regexp.MustCompile(`^([a-z][a-z-]*):(.*)$`)

func ListField(file, key string) []string {
	lines, _ := fileLines(file)
	return ListFieldLines(lines, key)
}

func Lines(src []byte) []string {
	s := Normalize(string(src))
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func ListFieldLines(lines []string, key string) []string {
	if len(lines) == 0 || lines[0] != "---" {
		return nil
	}
	for i := 1; i < len(lines) && lines[i] != "---"; i++ {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok || k != key {
			continue
		}
		v = strings.TrimSpace(stripComment(v))
		switch v {
		case "null", "Null", "NULL", "~":
			return nil
		}
		var items []string
		switch {
		case v == "":
			for j := i + 1; j < len(lines) && continuation(lines[j]); j++ {
				if t := strings.TrimSpace(lines[j]); strings.HasPrefix(t, "-") {
					items = append(items, listItem(strings.TrimSpace(stripComment(t))))
				}
			}
		case bareLinksRe.MatchString(v):
			items = bareLinkRe.FindAllString(v, -1)
		case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
			for _, x := range splitFlow(v[1 : len(v)-1]) {
				items = append(items, listItem(x))
			}
		default:
			items = []string{unquote(v)}
		}
		var out []string
		for _, x := range items {
			if x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	return nil
}

var (
	bareLinkRe  = regexp.MustCompile(`\[\[[^\[\]]*\]\]`)
	bareLinksRe = regexp.MustCompile(`^\[\[[^\[\]]*\]\](\s*,\s*\[\[[^\[\]]*\]\])*$`)
)

func stripComment(v string) string {
	var quote byte
	depth := 0
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == '#' && depth <= 0 && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return v[:i]
		}
	}
	return v
}

func splitFlow(s string) []string {
	var out []string
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ',':
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
