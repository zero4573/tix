package workspace

import (
	"encoding/json"
	"fmt"
)

type Mark struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
	At     string `json:"at"`
}

const (
	MarkTidy = "tidy"
)

var MarkKinds = []string{MarkTidy}

func ValidMark(kind string) bool { return kind == MarkTidy }

const (
	MarkOK    = "ok"
	MarkStale = "stale"
	MarkNone  = "none"
)

func MarkStatus(r Repo, kind, tree string) string {
	m, ok := r.Marks[kind]
	switch {
	case !ok || m.Tree == "":
		return MarkNone
	case m.Tree == tree:
		return MarkOK
	}
	return MarkStale
}

func SetMark(file, slug, kind string, m Mark) error {
	if !ValidMark(kind) {
		return fmt.Errorf("unknown mark kind %q", kind)
	}
	return Update(file, func(doc map[string]any) error {
		repos, _ := doc["repos"].([]any)
		for _, e := range repos {
			entry, ok := e.(map[string]any)
			if !ok || entry["slug"] != slug {
				continue
			}
			marks, _ := entry["marks"].(map[string]any)
			if marks == nil {
				marks = map[string]any{}
			}
			data, _ := json.Marshal(m)
			var v any
			_ = json.Unmarshal(data, &v)
			marks[kind] = v
			entry["marks"] = marks
			return nil
		}
		return fmt.Errorf("%s is not in the workspace", slug)
	})
}
