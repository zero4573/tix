package links

import "strings"

func Targets(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range linksIn("", text) {
		t := BareName(target(l.parts))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func BareName(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.LastIndexAny(t, `/\`); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSuffix(t, ".md")
}
