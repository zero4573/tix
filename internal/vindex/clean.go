package vindex

import (
	"encoding/json"
	"regexp"
	"strings"
)

var wikiRe = regexp.MustCompile(`!?\[\[([^\[\]|]*)(?:\|([^\[\]]*))?\]\]`)

func cleanLinks(s string) string {
	if !strings.Contains(s, "[[") {
		return s
	}
	return wikiRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := wikiRe.FindStringSubmatch(m)
		if strings.Contains(m, "|") {
			return strings.TrimSpace(sub[2])
		}
		return strings.TrimSpace(sub[1])
	})
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
