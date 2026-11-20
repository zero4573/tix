package cite

import (
	"regexp"
	"strconv"
	"strings"
)

type Citation struct {
	Line    int
	Text    string
	Repo    string
	Path    string
	Start   int
	End     int
	Caption bool
	Symbols []string
	Chunk   []string
}

type Note struct {
	Citations []Citation
	Pins      map[string]string
}

var (
	captionRe = regexp.MustCompile("^\\*\\*([^*]+)\\*\\*\\s*·\\s*`([^`]+)`(.*?)·\\s*lines?\\s+([0-9]+)(?:\\s*[-–]\\s*([0-9]+))?\\s*$")
	spanRe    = regexp.MustCompile("`([^`\n]+)`")
	inlineRe  = regexp.MustCompile(`^(?:([A-Za-z0-9][A-Za-z0-9_.-]*):)?([^\s:]+):([0-9]+)(?:[-–]([0-9]+))?$`)
	pinRe     = regexp.MustCompile("@\\s*`([0-9a-f]{7,64})`")
	extRe     = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,4}$`)
)

func Key(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
}

func fence(l string) (marker string, indent int, ok bool) {
	t := strings.TrimLeft(l, " ")
	indent = len(l) - len(t)
	if indent > 3 {
		return "", 0, false
	}
	for _, c := range []string{"`", "~"} {
		n := len(t) - len(strings.TrimLeft(t, c))
		if n >= 3 {
			return strings.Repeat(c, n), indent, true
		}
	}
	return "", 0, false
}

func closes(l, marker string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, marker) && strings.Trim(t, marker[:1]) == ""
}

func Parse(src string) Note {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	n := Note{Pins: map[string]string{}}
	i := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				i = j + 1
				break
			}
		}
	}
	for ; i < len(lines); i++ {
		l := lines[i]
		if marker, _, ok := fence(l); ok {
			for i++; i < len(lines) && !closes(lines[i], marker); i++ {
			}
			continue
		}
		if m := captionRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			c := Citation{Line: i + 1, Text: strings.TrimSpace(l), Repo: strings.TrimSpace(m[1]),
				Path: m[2], Caption: true}
			c.Start, _ = strconv.Atoi(m[4])
			c.End = c.Start
			if m[5] != "" {
				c.End, _ = strconv.Atoi(m[5])
			}
			for _, s := range spanRe.FindAllStringSubmatch(m[3], -1) {
				c.Symbols = append(c.Symbols, s[1])
			}
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) {
				if marker, indent, ok := fence(lines[j]); ok {
					c.Chunk = []string{}
					for j++; j < len(lines) && !closes(lines[j], marker); j++ {
						c.Chunk = append(c.Chunk, dedent(lines[j], indent))
					}
					i = j
				}
			}
			n.Citations = append(n.Citations, c)
			continue
		}
		pins(l, n.Pins)
		for _, s := range spanRe.FindAllStringSubmatch(l, -1) {
			if c, ok := inline(s[1]); ok {
				c.Line = i + 1
				n.Citations = append(n.Citations, c)
			}
		}
	}
	return n
}

func dedent(l string, indent int) string {
	for k := 0; k < indent && strings.HasPrefix(l, " "); k++ {
		l = l[1:]
	}
	return l
}

func inline(s string) (Citation, bool) {
	m := inlineRe.FindStringSubmatch(s)
	if m == nil || strings.Contains(s, "://") {
		return Citation{}, false
	}
	p := m[2]
	if !strings.Contains(p, "/") && !extRe.MatchString(p) {
		return Citation{}, false
	}
	c := Citation{Text: "`" + s + "`", Repo: m[1], Path: p}
	c.Start, _ = strconv.Atoi(m[3])
	c.End = c.Start
	if m[4] != "" {
		c.End, _ = strconv.Atoi(m[4])
	}
	return c, true
}

func pins(l string, into map[string]string) {
	for _, m := range pinRe.FindAllStringSubmatchIndex(l, -1) {
		before := strings.TrimRight(l[:m[0]], " ")
		if strings.HasSuffix(before, "`") {
			if k := strings.LastIndex(before[:len(before)-1], "`"); k >= 0 {
				before = strings.TrimRight(before[:k], " ")
			}
		}
		name := before
		if k := strings.LastIndexAny(name, " \t"); k >= 0 {
			name = name[k+1:]
		}
		name = strings.Trim(name, "*`[]:,;(")
		if k := strings.LastIndex(name, "|"); k >= 0 {
			name = name[k+1:]
		}
		if name = Key(name); name != "" {
			into[name] = l[m[2]:m[3]]
		}
	}
}
