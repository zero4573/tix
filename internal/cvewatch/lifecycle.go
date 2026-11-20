package cvewatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Lifecycle struct {
	Product  string      `json:"product"`
	Version  string      `json:"version"`
	Cycle    string      `json:"cycle"`
	EOL      string      `json:"eol,omitempty"`
	Ended    bool        `json:"ended"`
	EndsSoon bool        `json:"endsSoon,omitempty"`
	Latest   string      `json:"latest,omitempty"`
	Behind   bool        `json:"behind,omitempty"`
	Where    []Component `json:"where"`
}

func (l Lifecycle) Key() string { return l.Product + "@" + l.Cycle }

var eolSlugs = map[string]string{
	"nginx": "nginx", "postgresql": "postgresql", "mongodb": "mongodb", "redis": "redis", "valkey": "valkey",
	"node.js": "nodejs", "rabbitmq": "rabbitmq", "keycloak": "keycloak", "java": "eclipse-temurin",
	"python": "python", "php": "php", "ruby": "ruby", "go": "go", "mysql": "mysql", "mariadb": "mariadb",
	"tomcat": "tomcat", "elasticsearch": "elasticsearch", "opensearch": "opensearch", "kafka": "apache-kafka",
	"grafana": "grafana", "traefik": "traefik", "haproxy": "haproxy", "memcached": "memcached",
	"envoy": "envoy", "caddy": "caddy",
	"alpine": "alpine-linux", "debian": "debian", "ubuntu": "ubuntu", "rhel": "rhel",
	"rocky": "rocky-linux", "almalinux": "almalinux",
}

const endsSoonWithin = 90 * 24 * time.Hour

var leadingVersion = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)`)

func runtimeVersion(product, v string) string {
	m := leadingVersion.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return ""
	}
	out := m[1]
	if product == "java" {
		out = strings.TrimPrefix(out, "1.")
	}
	return out
}

type eolCycle struct {
	Cycle  string
	EOL    string
	Ended  bool
	Latest string
}

func (c *eolCycle) UnmarshalJSON(b []byte) error {
	var raw struct {
		Cycle  json.RawMessage `json:"cycle"`
		EOL    json.RawMessage `json:"eol"`
		Latest json.RawMessage `json:"latest"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.Cycle, c.Latest = scalar(raw.Cycle), scalar(raw.Latest)
	switch s := scalar(raw.EOL); s {
	case "true":
		c.Ended = true
	case "false", "":
	default:
		c.EOL = s
	}
	return nil
}

func scalar(b json.RawMessage) string {
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	return strings.Trim(string(b), `"`)
}

func (s Sources) endOfLife(ctx context.Context, slug string) ([]eolCycle, error) {
	var out []eolCycle
	if err := s.get(ctx, s.EOL+"/"+url.PathEscape(slug)+".json", nil, &out); err != nil {
		return nil, fmt.Errorf("endoflife.date %s: %w", slug, err)
	}
	return out, nil
}

func matchCycle(cycles []eolCycle, v string) (eolCycle, bool) {
	var best eolCycle
	found := false
	for _, c := range cycles {
		if c.Cycle == "" || !(v == c.Cycle || strings.HasPrefix(v, c.Cycle+".")) {
			continue
		}
		if !found || len(c.Cycle) > len(best.Cycle) {
			best, found = c, true
		}
	}
	return best, found
}

func behind(v, latest string) bool {
	if latest == "" {
		return false
	}
	lv := runtimeVersion("", latest)
	if strings.Count(v, ".") < strings.Count(lv, ".") {
		return false
	}
	return compareVersions(lv, v) > 0
}

func lifecycles(ctx context.Context, src Sources, comps []Component, now time.Time) ([]Lifecycle, []Skip) {
	if src.EOL == "" {
		return nil, nil
	}
	type key struct{ product, version string }
	where := map[key][]Component{}
	var keys []key
	for _, c := range mostSpecific(comps) {
		if _, ok := eolSlugs[c.Product]; !ok {
			continue
		}
		v := runtimeVersion(c.Product, c.Version)
		if v == "" {
			continue
		}
		k := key{c.Product, v}
		if _, ok := where[k]; !ok {
			keys = append(keys, k)
		}
		where[k] = append(where[k], c)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].product != keys[j].product {
			return keys[i].product < keys[j].product
		}
		return compareVersions(keys[i].version, keys[j].version) < 0
	})
	cache := map[string][]eolCycle{}
	failed := map[string]bool{}
	var out []Lifecycle
	var skips []Skip
	today := now.UTC().Format("2006-01-02")
	soon := now.Add(endsSoonWithin).UTC().Format("2006-01-02")
	for _, k := range keys {
		slug := eolSlugs[k.product]
		if failed[slug] {
			continue
		}
		cycles, ok := cache[slug]
		if !ok {
			var err error
			cycles, err = src.endOfLife(ctx, slug)
			if err != nil {
				failed[slug] = true
				skips = append(skips, Skip{"endoflife", err.Error()})
				continue
			}
			cache[slug] = cycles
		}
		c, ok := matchCycle(cycles, k.version)
		if !ok {
			continue
		}
		l := Lifecycle{Product: k.product, Version: k.version, Cycle: c.Cycle, EOL: c.EOL, Latest: c.Latest,
			Where: dedupeWhere(where[k])}
		l.Ended = c.Ended || c.EOL != "" && c.EOL <= today
		l.EndsSoon = !l.Ended && c.EOL != "" && c.EOL <= soon
		l.Behind = !l.Ended && behind(k.version, c.Latest)
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return lifecycleRank(out[i]) < lifecycleRank(out[j]) })
	return out, skips
}

func mostSpecific(comps []Component) []Component {
	var out []Component
	at := map[string][]int{}
	for _, c := range comps {
		if c.Image == "" {
			out = append(out, c)
			continue
		}
		k := c.Repo + "|" + c.File + "|" + c.Image + "|" + c.Product
		v := runtimeVersion(c.Product, c.Version)
		merged := false
		for _, i := range at[k] {
			o := runtimeVersion(c.Product, out[i].Version)
			switch {
			case o == v || strings.HasPrefix(o, v+"."):
				merged = true
			case strings.HasPrefix(v, o+"."):
				out[i], merged = c, true
			}
			if merged {
				break
			}
		}
		if !merged {
			at[k] = append(at[k], len(out))
			out = append(out, c)
		}
	}
	return out
}

func lifecycleRank(l Lifecycle) int {
	switch {
	case l.Ended:
		return 0
	case l.EndsSoon:
		return 1
	case l.Behind:
		return 2
	}
	return 3
}

func dedupeWhere(cs []Component) []Component {
	seen := map[string]bool{}
	var out []Component
	for _, c := range cs {
		k := c.Repo + "|" + c.File + "|" + c.Image
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}
