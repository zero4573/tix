package projects

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zero4573/tix/internal/note"
)

const (
	KindProject = "project"
	KindGroup   = "group"
)

type Problem struct {
	File    string `json:"file"`
	Message string `json:"message"`
	Error   bool   `json:"error"`
}

type Cluster struct {
	Groups   []string `json:"groups"`
	Projects []string `json:"projects"`
	Bridges  []string `json:"bridges"`
}

type node struct {
	name, kind, file string
	dependsOn        []string
	groups           []string
	usedBy, members  []string
}

type Graph struct {
	nodes    map[string]*node
	clusters []Cluster
	of       map[string]int
}

func Load(vault string) (*Graph, []Problem) {
	g := &Graph{nodes: map[string]*node{}, of: map[string]int{}}
	dirs, _ := os.ReadDir(filepath.Join(vault, "projects"))
	type raw struct{ deps, groups []string }
	links := map[string]raw{}
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		file := filepath.Join(vault, "projects", d.Name(), d.Name()+".md")
		if _, err := os.Stat(file); err != nil {
			continue
		}
		kind := note.Get(file, "type")
		if kind != KindProject && kind != KindGroup {
			continue
		}
		rel, _ := filepath.Rel(vault, file)
		key := strings.ToLower(d.Name())
		g.nodes[key] = &node{name: d.Name(), kind: kind, file: rel}
		if kind == KindProject {
			links[key] = raw{note.ListField(file, "depends-on"), note.ListField(file, "groups")}
		}
	}

	var problems []Problem
	bad := func(n *node, format string, a ...any) {
		problems = append(problems, Problem{File: n.file, Message: fmt.Sprintf(format, a...), Error: true})
	}
	for _, key := range sortedKeys(links) {
		n, l := g.nodes[key], links[key]
		for _, item := range l.deps {
			t := Target(item)
			switch m := g.nodes[t]; {
			case t == key:
				bad(n, "depends-on links %s itself", n.name)
			case m == nil:
				bad(n, "depends-on links %s, which is not a project note", item)
			case m.kind != KindProject:
				bad(n, "depends-on links %s, which is a group, not a project", item)
			case !contains(n.dependsOn, m.name):
				n.dependsOn = append(n.dependsOn, m.name)
				m.usedBy = append(m.usedBy, n.name)
			}
		}
		for _, item := range l.groups {
			t := Target(item)
			switch m := g.nodes[t]; {
			case m == nil:
				bad(n, "groups links %s, which is not a group note", item)
			case m.kind != KindGroup:
				bad(n, "groups links %s, which is a project, not a group", item)
			case !contains(n.groups, m.name):
				n.groups = append(n.groups, m.name)
				m.members = append(m.members, n.name)
			}
		}
	}
	for _, n := range g.nodes {
		for _, l := range [][]string{n.dependsOn, n.groups, n.usedBy, n.members} {
			sort.Strings(l)
		}
	}
	for _, key := range sortedKeys(g.nodes) {
		if n := g.nodes[key]; n.kind == KindGroup && len(n.members) == 0 {
			problems = append(problems, Problem{File: n.file, Message: "group " + n.name + " has no members"})
		}
	}
	g.cluster()
	return g, problems
}

func Target(link string) string {
	t := strings.TrimSpace(link)
	t = strings.TrimSuffix(strings.TrimPrefix(t, "[["), "]]")
	if i := strings.IndexAny(t, "|#^"); i >= 0 {
		t = t[:i]
	}
	t = path.Base(strings.TrimSpace(t))
	return strings.ToLower(strings.TrimSuffix(t, ".md"))
}

func (g *Graph) cluster() {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" || parent[x] == x {
			parent[x] = x
			return x
		}
		parent[x] = find(parent[x])
		return parent[x]
	}
	union := func(a, b string) { parent[find(a)] = find(b) }
	linked := map[string]bool{}
	for key, n := range g.nodes {
		for _, x := range append(append([]string{}, n.dependsOn...), n.groups...) {
			union(key, strings.ToLower(x))
			linked[key], linked[strings.ToLower(x)] = true, true
		}
	}
	byRoot := map[string]*Cluster{}
	for _, key := range sortedKeys(g.nodes) {
		if !linked[key] {
			continue
		}
		r := find(key)
		c := byRoot[r]
		if c == nil {
			c = &Cluster{Groups: []string{}, Projects: []string{}, Bridges: []string{}}
			byRoot[r] = c
		}
		n := g.nodes[key]
		if n.kind == KindGroup {
			c.Groups = append(c.Groups, n.name)
			continue
		}
		c.Projects = append(c.Projects, n.name)
		if len(n.groups) > 1 {
			c.Bridges = append(c.Bridges, fmt.Sprintf("%s is in groups %s", n.name, strings.Join(n.groups, ", ")))
		}
		for _, d := range n.dependsOn {
			m := g.nodes[strings.ToLower(d)]
			if len(n.groups) > 0 && len(m.groups) > 0 && !shareAny(n.groups, m.groups) {
				c.Bridges = append(c.Bridges, fmt.Sprintf("%s (%s) depends-on %s (%s)",
					n.name, strings.Join(n.groups, ", "), m.name, strings.Join(m.groups, ", ")))
			}
		}
	}
	for _, c := range byRoot {
		g.clusters = append(g.clusters, *c)
	}
	sort.Slice(g.clusters, func(i, j int) bool {
		return strings.ToLower(g.clusters[i].Projects[0]) < strings.ToLower(g.clusters[j].Projects[0])
	})
	for i, c := range g.clusters {
		for _, x := range append(append([]string{}, c.Groups...), c.Projects...) {
			g.of[strings.ToLower(x)] = i
		}
	}
}

func (g *Graph) names(kind string) []string {
	out := []string{}
	for _, key := range sortedKeys(g.nodes) {
		if n := g.nodes[key]; n.kind == kind {
			out = append(out, n.name)
		}
	}
	return out
}

func (g *Graph) Projects() []string { return g.names(KindProject) }

func (g *Graph) Groups() []string { return g.names(KindGroup) }

func (g *Graph) Kind(name string) string {
	if n := g.nodes[strings.ToLower(name)]; n != nil {
		return n.kind
	}
	return ""
}

func (g *Graph) File(name string) string {
	if n := g.nodes[strings.ToLower(name)]; n != nil {
		return n.file
	}
	return ""
}

func (g *Graph) Name(name string) string {
	if n := g.nodes[strings.ToLower(name)]; n != nil {
		return n.name
	}
	return ""
}

func (g *Graph) list(name string, f func(*node) []string) []string {
	if n := g.nodes[strings.ToLower(name)]; n != nil {
		return append([]string{}, f(n)...)
	}
	return []string{}
}

func (g *Graph) DependsOn(p string) []string {
	return g.list(p, func(n *node) []string { return n.dependsOn })
}

func (g *Graph) UsedBy(p string) []string {
	return g.list(p, func(n *node) []string { return n.usedBy })
}

func (g *Graph) GroupsOf(p string) []string {
	return g.list(p, func(n *node) []string { return n.groups })
}

func (g *Graph) Members(grp string) []string {
	return g.list(grp, func(n *node) []string { return n.members })
}

func (g *Graph) Clusters() []Cluster { return g.clusters }

func (g *Graph) ClusterOf(name string) (int, bool) {
	i, ok := g.of[strings.ToLower(name)]
	return i, ok
}

func (g *Graph) Standalone() []string {
	out := []string{}
	for _, p := range g.Projects() {
		if _, ok := g.ClusterOf(p); !ok {
			out = append(out, p)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func shareAny(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}
