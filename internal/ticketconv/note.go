package ticketconv

import (
	"strings"
)

func frontmatter(text string) (fm []string, rest string, ok bool) {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return nil, text, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			return lines[1:i], strings.Join(lines[i:], ""), true
		}
	}
	return nil, text, false
}

func setKey(fm []string, key, value string, drop bool) []string {
	var out []string
	done, skipping := false, false
	for _, l := range fm {
		if skipping && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(strings.TrimSpace(l), "- ")) {
			continue
		}
		skipping = false
		if strings.HasPrefix(l, key+":") {
			skipping = true
			if drop || done {
				continue
			}
			out = append(out, key+": "+value+"\n")
			done = true
			continue
		}
		out = append(out, l)
	}
	if !done && !drop {
		if n := len(out); n > 0 && !strings.HasSuffix(out[n-1], "\n") {
			out[n-1] += "\n"
		}
		out = append(out, key+": "+value+"\n")
	}
	return out
}

func getKey(fm []string, key string) string {
	for _, l := range fm {
		if v, ok := strings.CutPrefix(l, key+":"); ok {
			v = strings.TrimSpace(v)
			if n := len(v); n >= 2 && (v[0] == '"' && v[n-1] == '"' || v[0] == '\'' && v[n-1] == '\'') {
				v = v[1 : n-1]
			}
			return v
		}
	}
	return ""
}

func Field(text, key string) string {
	fm, _, _ := frontmatter(text)
	return getKey(fm, key)
}

func SetFields(text string, kv ...string) string {
	fm, rest, ok := frontmatter(text)
	if !ok {
		return text
	}
	for i := 0; i+1 < len(kv); i += 2 {
		fm = setKey(fm, kv[i], kv[i+1], kv[i+1] == "")
	}
	return "---\n" + strings.Join(fm, "") + rest
}

func editTags(fm []string, drop string, add ...string) []string {
	for i, l := range fm {
		v, ok := strings.CutPrefix(l, "tags:")
		v = strings.TrimSpace(v)
		if !ok || !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
			continue
		}
		var tags []string
		for _, t := range strings.Split(v[1:len(v)-1], ",") {
			if t = strings.TrimSpace(t); t != "" && t != drop {
				tags = append(tags, t)
			}
		}
	next:
		for _, a := range add {
			for _, t := range tags {
				if t == a {
					continue next
				}
			}
			tags = append(tags, a)
		}
		fm[i] = "tags: [" + strings.Join(tags, ", ") + "]\n"
	}
	return fm
}

const (
	blockStart = "<!-- source:start -->"
	blockEnd   = "<!-- source:end -->"
)

func Flip(text, source, id, url, oldID, label string) string {
	fm, rest, ok := frontmatter(text)
	if !ok {
		return text
	}
	for _, kv := range [][2]string{{"source", source}, {"source-id", id}, {"source-url", url}, {"converted-from", oldID}} {
		fm = setKey(fm, kv[0], kv[1], false)
	}
	for _, k := range []string{"convert-source", "convert-key", "convert-url"} {
		fm = setKey(fm, k, "", true)
	}
	project := strings.ToLower(strings.SplitN(id, "-", 2)[0])
	fm = editTags(fm, "manual", source, project)
	if !strings.Contains(rest, blockStart) {
		block := blockStart + "\n## Source (" + label + ")\n" + blockEnd + "\n"
		lines := strings.SplitAfter(rest, "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "# ") {
				lines = append(lines[:i+1], append([]string{"\n" + block}, lines[i+1:]...)...)
				rest = strings.Join(lines, "")
				break
			}
		}
	}
	return "---\n" + strings.Join(fm, "") + rest
}

func Label(source string) string {
	if source == "" {
		return source
	}
	return strings.ToUpper(source[:1]) + source[1:]
}
