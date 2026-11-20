package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zero4573/tix/internal/mcp"
)

type PRRef struct {
	Workspace, Repo string
	ID              int
}

func (r PRRef) String() string { return fmt.Sprintf("%s/%s#%d", r.Workspace, r.Repo, r.ID) }

var (
	prURL   = regexp.MustCompile(`^https://bitbucket\.org/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull-requests/([0-9]+)(?:[/?#].*)?$`)
	prShort = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([0-9]+)$`)
)

func ParsePR(s string) (PRRef, error) {
	m := prURL.FindStringSubmatch(s)
	if m == nil {
		m = prShort.FindStringSubmatch(s)
	}
	if m == nil {
		return PRRef{}, fmt.Errorf("%q: not a Bitbucket pull request (its link, or workspace/repo#id)", s)
	}
	id, err := strconv.Atoi(m[3])
	if err != nil || id <= 0 {
		return PRRef{}, fmt.Errorf("%q: bad pull request number", s)
	}
	return PRRef{m[1], m[2], id}, nil
}

type PRState struct {
	State   string `json:"state"`
	Updated string `json:"updated"`
	CI      string `json:"ci"`
	Checks  string `json:"checks"`
}

type PRSource func(ctx context.Context, r PRRef) (PRState, error)

const MaxWatched = 10

func changes(old, cur PRState) string {
	var c []string
	if old.State != cur.State {
		c = append(c, "state "+cur.State)
		if cur.State != "OPEN" {
			return strings.Join(c, ", ")
		}
	}
	if old.CI != cur.CI {
		c = append(c, "CI "+orNone(cur.CI))
	}
	if old.Checks != cur.Checks {
		c = append(c, "merge checks "+orNone(cur.Checks))
	}
	if len(c) == 0 && old.Updated != cur.Updated {
		c = append(c, "updated (comments, commits or approvals)")
	}
	return strings.Join(c, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func (s *Server) watched() map[string]map[string]PRState {
	m := map[string]map[string]PRState{}
	if b, err := os.ReadFile(s.watchFile()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *Server) saveWatched(m map[string]map[string]PRState) {
	b, _ := json.MarshalIndent(m, "", "  ")
	f := s.watchFile()
	_ = os.MkdirAll(filepath.Dir(f), 0o700)
	if os.WriteFile(f+".tmp", append(b, '\n'), 0o600) == nil {
		_ = os.Rename(f+".tmp", f)
	}
}

func (s *Server) watchFile() string {
	return strings.TrimSuffix(s.StateFile, ".json") + "-prs.json"
}

func (s *Server) watch(ctx context.Context, id string, prs []string) Reply {
	if s.PRs == nil {
		return fail("no Bitbucket access on the host (TIX_MCP_CONFIG)")
	}
	if len(prs) == 0 || len(prs) > MaxWatched {
		return refuse("give 1 to %d pull requests", MaxWatched)
	}
	set := map[string]PRState{}
	var lines []string
	for _, p := range prs {
		r, err := ParsePR(p)
		if err != nil {
			return refuse("%v", err)
		}
		st, err := s.PRs(ctx, r)
		if err != nil {
			return fail("%s: %v", r, err)
		}
		set[r.String()] = st
		lines = append(lines, fmt.Sprintf("%s: %s, CI %s, merge checks %s", r, st.State, orNone(st.CI), orNone(st.Checks)))
	}
	s.mu.Lock()
	m := s.watched()
	m[id] = set
	s.saveWatched(m)
	s.mu.Unlock()
	return Reply{Status: "ok", Detail: fmt.Sprintf("watching %d pull request(s) of %s", len(set), id), Output: strings.Join(lines, "\n")}
}

func (s *Server) unwatch(id string) Reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.watched()
	if _, ok := m[id]; !ok {
		return Reply{Status: "ok", Detail: id + " had no watched pull requests"}
	}
	delete(m, id)
	s.saveWatched(m)
	return Reply{Status: "ok", Detail: id + "'s pull requests are no longer watched"}
}

func (s *Server) pollPRs(ctx context.Context) map[string]string {
	if s.PRs == nil {
		return nil
	}
	s.mu.Lock()
	m := s.watched()
	s.mu.Unlock()
	events := map[string]string{}
	cur := map[string]map[string]PRState{}
	for id, set := range m {
		cur[id] = map[string]PRState{}
		var ev []string
		for key, old := range set {
			cur[id][key] = old
			r, err := ParsePR(key)
			if err != nil {
				continue
			}
			st, err := s.PRs(ctx, r)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tix main serve: %s: %v\n", r, err)
				continue
			}
			if c := changes(old, st); c != "" {
				ev = append(ev, fmt.Sprintf("PR %s/#%d %s", r.Repo, r.ID, c))
			}
			cur[id][key] = st
		}
		if len(ev) > 0 {
			sort.Strings(ev)
			events[id] = strings.Join(ev, ", ")
		}
	}
	s.mu.Lock()
	now := s.watched()
	for id := range now {
		if set, ok := cur[id]; ok {
			for key := range now[id] {
				if st, ok := set[key]; ok {
					now[id][key] = st
				}
			}
		}
	}
	s.saveWatched(now)
	s.mu.Unlock()
	return events
}

func BitbucketPRs(ctx context.Context, r PRRef) (PRState, error) {
	c, err := mcp.Connect("atlassian")
	if err != nil {
		return PRState{}, err
	}
	read := func(name string, inputs map[string]any, v any) error {
		raw, err := c.Tool("executeRead", map[string]any{"name": name, "inputs": inputs})
		if err != nil {
			return err
		}
		var w struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return err
		}
		return json.Unmarshal(w.Data, v)
	}
	base := map[string]any{"workspaceId": r.Workspace, "repoId": r.Repo}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	var pr struct {
		State   string `json:"state"`
		Updated string `json:"updated_on"`
		Source  struct {
			Branch struct {
				Name string `json:"name"`
			} `json:"branch"`
		} `json:"source"`
	}
	if err := read("getBitbucketRepoPullRequest", with("prId", r.ID), &pr); err != nil {
		return PRState{}, err
	}
	st := PRState{State: pr.State, Updated: pr.Updated}
	if pr.State != "OPEN" {
		return st, nil
	}
	var pl struct {
		Values []struct {
			Build int `json:"build_number"`
			State struct {
				Name   string `json:"name"`
				Result struct {
					Name string `json:"name"`
				} `json:"result"`
			} `json:"state"`
		} `json:"values"`
	}
	if err := read("listBitbucketRepoPipelines", with("targetBranch", pr.Source.Branch.Name, "sort", "-created_on", "pagelen", 1), &pl); err == nil && len(pl.Values) > 0 {
		v := pl.Values[0]
		st.CI = fmt.Sprintf("#%d %s", v.Build, v.State.Name)
		if v.State.Result.Name != "" {
			st.CI += "/" + v.State.Result.Name
		}
	}
	var mg struct {
		Values []mergeCheck `json:"values"`
	}
	if err := read("getBitbucketRepoPullRequestMergeability", with("prId", r.ID), &mg); err == nil {
		st.Checks = failingChecks(mg.Values)
	}
	return st, nil
}

type mergeCheck struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	Check  struct {
		Kind string `json:"kind"`
	} `json:"check"`
}

func failingChecks(cs []mergeCheck) string {
	skip := map[string]bool{"minimum_approvals": true, "minimum_default_reviewer_approvals": true,
		"minimum_successful_builds": true, "failed_builds": true, "in_progress_builds": true}
	var out []string
	for _, c := range cs {
		if c.Status != "FAILED" || skip[c.Check.Kind] {
			continue
		}
		n := c.Type
		if c.Check.Kind != "" {
			n += ":" + c.Check.Kind
		}
		if c.Reason != "" {
			n += "(" + c.Reason + ")"
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
