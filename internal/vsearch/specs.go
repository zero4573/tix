package vsearch

import (
	"sort"
)

type SpecIndexResponse struct {
	Slug string
	Resp *Response
}

func SpecPath(slug, rel string) string { return slug + ":" + rel }

func MergeSpecs(vault *Response, specs []SpecIndexResponse, k, budget int) *Response {
	if len(specs) == 0 {
		return vault
	}
	if k <= 0 {
		k = DefaultK
	}
	all := append([]Result(nil), vault.Results...)
	for _, s := range specs {
		for _, r := range s.Resp.Results {
			r.Path = SpecPath(s.Slug, r.Path)
			r.Type, r.Project = "spec", s.Slug
			all = append(all, r)
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].Score > all[b].Score })
	out := *vault
	out.Results = []Result{}
	used := 0
	out.Budget = nil
	if budget > 0 {
		out.Budget = &BudgetInfo{Limit: budget}
	}
	for _, r := range all {
		if len(out.Results) >= k {
			break
		}
		if budget > 0 && len(out.Results) > 0 && used+r.Tokens > budget {
			out.Budget.Truncated = true
			break
		}
		used += r.Tokens
		r.Rank = len(out.Results) + 1
		out.Results = append(out.Results, r)
	}
	if out.Budget != nil {
		out.Budget.Used = used
	}
	return &out
}
