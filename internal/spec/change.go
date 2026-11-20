package spec

import "strings"

const SectionHeading = "Spec change"

type Change struct {
	Slug         string        `json:"slug"`
	Capability   string        `json:"capability"`
	Line         int           `json:"line"`
	Requirements []Requirement `json:"requirements"`
	Interfaces   string        `json:"interfaces,omitempty"`
}

type ChangeSection struct {
	Found    bool      `json:"found"`
	Line     int       `json:"line"`
	Changes  []Change  `json:"changes"`
	Problems []Problem `json:"problems,omitempty"`
}

func (c *ChangeSection) Requirements() []Requirement {
	var out []Requirement
	for _, ch := range c.Changes {
		out = append(out, ch.Requirements...)
	}
	return out
}

func ParseChange(name string, src []byte) *ChangeSection {
	lines := splitLines(src)
	cs := &ChangeSection{}
	from, to := -1, len(lines)
	fence := false
	for i, ln := range lines {
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
			to = i
			break
		}
	}
	if from < 0 {
		return cs
	}
	cs.Found, cs.Line = true, from+1
	bad := func(line int, msg string) {
		cs.Problems = append(cs.Problems, Problem{File: name, Line: line + 1, Msg: msg})
	}

	type block struct {
		kind       string
		start, end int
	}
	var blocks []block
	fence = false
	for i := from + 1; i < to; i++ {
		if isFence(lines[i]) {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		lvl, h := heading(lines[i])
		kind := ""
		switch {
		case lvl == 3 && reqHeadRe.MatchString(h):
			kind = "req"
		case lvl == 3:
			kind = "cap"
		case lvl == 4 && h == "Interfaces":
			kind = "iface"
		}
		if kind != "" {
			blocks = append(blocks, block{kind: kind, start: i})
		}
	}
	for k := range blocks {
		blocks[k].end = to
		if k+1 < len(blocks) {
			blocks[k].end = blocks[k+1].start
		}
	}
	var cur *Change
	flush := func() {
		if cur != nil {
			cs.Changes = append(cs.Changes, *cur)
			cur = nil
		}
	}
	for _, b := range blocks {
		switch b.kind {
		case "cap":
			flush()
			_, text := heading(lines[b.start])
			slug, capab, ok := strings.Cut(text, "·")
			slug, capab = strings.TrimSpace(slug), strings.TrimSpace(capab)
			if !ok || slug == "" || capab == "" {
				bad(b.start, "capability heading must be \"### <slug> · <capability>\"")
				continue
			}
			cur = &Change{Slug: slug, Capability: capab, Line: b.start + 1}
		case "req":
			if cur == nil {
				bad(b.start, "requirement outside a \"### <slug> · <capability>\" part")
				continue
			}
			r, probs := parseReq(name, lines, b.start, b.end)
			cs.Problems = append(cs.Problems, probs...)
			if r != nil {
				cur.Requirements = append(cur.Requirements, *r)
			}
		case "iface":
			if cur == nil {
				bad(b.start, "#### Interfaces outside a capability part")
				continue
			}
			cur.Interfaces = strings.TrimSpace(strings.Join(lines[b.start+1:b.end], "\n"))
		}
	}
	flush()
	return cs
}
