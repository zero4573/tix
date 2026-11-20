package cvewatch

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Affected struct {
	Repo      string `json:"repo"`
	File      string `json:"file"`
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Fix       string `json:"fix,omitempty"`
	Dev       bool   `json:"dev,omitempty"`
	Image     string `json:"image,omitempty"`
}

type Finding struct {
	ID         string     `json:"id"`
	Aliases    []string   `json:"aliases"`
	Related    []string   `json:"related,omitempty"`
	Priority   string     `json:"priority"`
	Major      bool       `json:"major"`
	Resolvable bool       `json:"resolvable"`
	Severity   string     `json:"severity"`
	Score      float64    `json:"score,omitempty"`
	Vector     string     `json:"vector,omitempty"`
	Network    bool       `json:"network"`
	RCE        bool       `json:"rce"`
	KEV        *KEVEntry  `json:"kev,omitempty"`
	EPSS       float64    `json:"epss"`
	CWEs       []string   `json:"cwes,omitempty"`
	Summary    string     `json:"summary"`
	Published  string     `json:"published,omitempty"`
	Refs       []string   `json:"refs,omitempty"`
	Packages   []Affected `json:"packages"`
}

type Watch struct {
	ID          string      `json:"id"`
	Product     string      `json:"product"`
	Priority    string      `json:"priority"`
	Major       bool        `json:"major"`
	Severity    string      `json:"severity"`
	Score       float64     `json:"score,omitempty"`
	Vector      string      `json:"vector,omitempty"`
	Network     bool        `json:"network"`
	RCE         bool        `json:"rce"`
	KEV         *KEVEntry   `json:"kev,omitempty"`
	EPSS        float64     `json:"epss"`
	Description string      `json:"description"`
	Published   string      `json:"published,omitempty"`
	Refs        []string    `json:"refs,omitempty"`
	Where       []Component `json:"where"`
}

const SourceOSVRecord = "osv-record"

type Skip struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type Report struct {
	Swept        string         `json:"swept"`
	Days         int            `json:"days"`
	Repos        []string       `json:"repos"`
	Packages     int            `json:"packages"`
	Images       int            `json:"images"`
	Findings     []Finding      `json:"findings"`
	Watch        []Watch        `json:"watch"`
	Lifecycle    []Lifecycle    `json:"lifecycle"`
	ImageSummary []ImageSummary `json:"imageSummary"`
	Summarized   []string       `json:"summarized,omitempty"`
	Unscanned    []Unscanned    `json:"unscanned"`
	Skips        []Skip         `json:"skips"`
	Components   []Component    `json:"components"`
}

type Options struct {
	Days    int
	Sources Sources
	Now     func() time.Time
	Images  ImageScanner
}

var (
	rceCWEs = map[string]bool{"CWE-77": true, "CWE-78": true, "CWE-94": true, "CWE-95": true, "CWE-96": true,
		"CWE-98": true, "CWE-434": true, "CWE-502": true, "CWE-917": true, "CWE-1336": true}
	rceText = regexp.MustCompile(`(?i)remote code execution|arbitrary code|code execution|command injection|code injection|os command|deseriali[sz]|\bRCE\b|template injection|expression language injection|execute (?:arbitrary )?(?:commands|code)|remote shell|reverse shell`)
)

type productSpec struct {
	Keywords []string
	CPEs     []string
}

var kevNames = map[string][]string{"mongodb": {"MongoDB"}}

func (p productSpec) kevNamesFor(product string) []string {
	if names, ok := kevNames[product]; ok {
		return names
	}
	return p.Keywords
}

var productSpecs = map[string]productSpec{
	"nginx":           {[]string{"nginx"}, []string{"f5:nginx", "nginx:nginx"}},
	"ingress-nginx":   {[]string{"ingress-nginx"}, []string{"kubernetes:ingress-nginx"}},
	"java":            {[]string{"Java SE", "OpenJDK"}, []string{"oracle:jdk", "oracle:jre", "oracle:openjdk", "oracle:java_se", "eclipse:temurin", "azul:zulu", "amazon:corretto"}},
	"tomcat":          {[]string{"Apache Tomcat"}, []string{"apache:tomcat"}},
	"jetty":           {[]string{"Eclipse Jetty"}, []string{"eclipse:jetty"}},
	"apache-httpd":    {[]string{"Apache HTTP Server"}, []string{"apache:http_server"}},
	"haproxy":         {[]string{"HAProxy"}, []string{"haproxy:haproxy"}},
	"traefik":         {[]string{"Traefik"}, []string{"traefik:traefik"}},
	"envoy":           {[]string{"Envoy"}, []string{"envoyproxy:envoy"}},
	"node.js":         {[]string{"Node.js"}, []string{"nodejs:node.js"}},
	"python":          {[]string{"CPython"}, []string{"python:python", "python:cpython"}},
	"php":             {[]string{"PHP"}, []string{"php:php"}},
	"ruby":            {[]string{"Ruby"}, []string{"ruby-lang:ruby"}},
	"go":              {[]string{"Golang"}, []string{"golang:go"}},
	"postgresql":      {[]string{"PostgreSQL"}, []string{"postgresql:postgresql"}},
	"mysql":           {[]string{"MySQL Server"}, []string{"oracle:mysql"}},
	"mariadb":         {[]string{"MariaDB"}, []string{"mariadb:mariadb"}},
	"redis":           {[]string{"Redis"}, []string{"redis:redis"}},
	"valkey":          {[]string{"Valkey"}, []string{"lfprojects:valkey", "valkey-io:valkey"}},
	"mongodb":         {[]string{"MongoDB Server"}, []string{"mongodb:mongodb"}},
	"rabbitmq":        {[]string{"RabbitMQ"}, []string{"vmware:rabbitmq", "pivotal_software:rabbitmq", "broadcom:rabbitmq"}},
	"memcached":       {[]string{"Memcached"}, []string{"memcached:memcached"}},
	"elasticsearch":   {[]string{"Elasticsearch"}, []string{"elastic:elasticsearch"}},
	"opensearch":      {[]string{"OpenSearch"}, []string{"amazon:opensearch", "opensearch:opensearch"}},
	"kafka":           {[]string{"Apache Kafka"}, []string{"apache:kafka"}},
	"zookeeper":       {[]string{"ZooKeeper"}, []string{"apache:zookeeper"}},
	"keycloak":        {[]string{"Keycloak"}, []string{"redhat:keycloak", "keycloak:keycloak"}},
	"grafana":         {[]string{"Grafana"}, []string{"grafana:grafana"}},
	"prometheus":      {[]string{"Prometheus"}, []string{"prometheus:prometheus"}},
	"jenkins":         {[]string{"Jenkins"}, []string{"jenkins:jenkins"}},
	"gitlab-runner":   {[]string{"GitLab Runner"}, []string{"gitlab:runner", "gitlab:gitlab_runner"}},
	"hashicorp-vault": {[]string{"HashiCorp Vault"}, []string{"hashicorp:vault"}},
	"consul":          {[]string{"HashiCorp Consul"}, []string{"hashicorp:consul"}},
	"minio":           {[]string{"MinIO"}, []string{"minio:minio"}},
	"wordpress":       {[]string{"WordPress"}, []string{"wordpress:wordpress"}},
	"caddy":           {[]string{"Caddy"}, []string{"caddyserver:caddy"}},
	"squid":           {[]string{"Squid"}, []string{"squid-cache:squid"}},
	"openssh":         {[]string{"OpenSSH"}, []string{"openbsd:openssh"}},
}

const subjectWindow = 200

func (p productSpec) covers(c nvdCVE, kw string) bool {
	if len(c.CPEs) > 0 {
		for _, have := range c.CPEs {
			for _, want := range p.CPEs {
				if strings.HasPrefix(have, want) {
					return true
				}
			}
		}
		return false
	}
	return wordIn(truncate(c.Description, subjectWindow), kw)
}

func wordIn(text, kw string) bool {
	re := regexp.MustCompile(`(?i)(?:^|[^\w-])` + regexp.QuoteMeta(kw) + `(?:$|[^\w-])`)
	return re.MatchString(text)
}

func Sweep(ctx context.Context, inv Inventory, opt Options) Report {
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	if opt.Days <= 0 {
		opt.Days = 30
	}
	if opt.Days > 120 {
		opt.Days = 120
	}
	src := opt.Sources
	rep := Report{Swept: now().UTC().Format(time.RFC3339), Days: opt.Days, Repos: inv.Repos,
		Unscanned: append([]Unscanned{}, inv.Unscanned...), Components: append([]Component{}, inv.Components...),
		Findings: []Finding{}, Watch: []Watch{}, Lifecycle: []Lifecycle{}, Skips: []Skip{}}
	img := scanImages(ctx, opt.Images, inv.Images)
	packages := append(append([]Package{}, inv.Packages...), img.packages...)
	rep.Packages, rep.Images = len(packages), img.images
	rep.Unscanned = append(rep.Unscanned, img.unscanned...)

	kev, err := src.kev(ctx)
	if err != nil {
		rep.Skips = append(rep.Skips, Skip{"cisa-kev", err.Error()})
	}

	byKey := map[pkgKey][]Package{}
	var keys []pkgKey
	for _, p := range packages {
		k := pkgKey{p.Ecosystem, p.Name, p.Version}
		if _, ok := byKey[k]; !ok {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], p)
	}
	hits, err := src.osvQuery(ctx, keys)
	if err != nil {
		rep.Skips = append(rep.Skips, Skip{"osv", err.Error()})
	}
	idSet := map[string]bool{}
	for _, ids := range hits {
		for _, id := range ids {
			idSet[id] = true
		}
	}
	ids := sortedSet(idSet)
	recs, failed := src.osvRecords(ctx, ids, false)
	for _, f := range failed {
		rep.Skips = append(rep.Skips, Skip{SourceOSVRecord, f})
	}
	extra, failed := src.osvRecords(ctx, unscoredCVEs(recs), true)
	for _, f := range failed {
		rep.Skips = append(rep.Skips, Skip{SourceOSVRecord, f})
	}
	maps.Copy(recs, extra)
	rep.Findings = group(recs, hits, byKey, kev)

	watch, skips := watchComponents(ctx, src, inv.Components, kev, now(), opt.Days)
	rep.Watch, rep.Skips = append(rep.Watch, watch...), append(rep.Skips, skips...)
	life, skips := lifecycles(ctx, src, append(append([]Component{}, inv.Components...), img.runtimes...), now())
	rep.Lifecycle, rep.Skips = append(rep.Lifecycle, life...), append(rep.Skips, skips...)

	var cves []string
	seen := map[string]bool{}
	for _, f := range rep.Findings {
		for _, a := range f.cveIDs() {
			if strings.HasPrefix(a, "CVE-") && !seen[a] {
				seen[a] = true
				cves = append(cves, a)
			}
		}
	}
	for _, w := range rep.Watch {
		if !seen[w.ID] {
			seen[w.ID] = true
			cves = append(cves, w.ID)
		}
	}
	epss, err := src.epss(ctx, cves)
	if err != nil {
		rep.Skips = append(rep.Skips, Skip{"epss", err.Error()})
	}
	for i := range rep.Findings {
		f := &rep.Findings[i]
		for _, a := range f.cveIDs() {
			f.EPSS = max(f.EPSS, epss[a])
		}
		f.Priority, f.Major = rank(f.Severity, f.KEV != nil, f.RCE, f.Network, f.EPSS)
	}
	rep.Findings, rep.ImageSummary, rep.Summarized = splitImageFindings(rep.Findings, img.scanned, img.notes)
	for i := range rep.Watch {
		w := &rep.Watch[i]
		w.EPSS = epss[w.ID]
		w.Priority, w.Major = rank(w.Severity, w.KEV != nil, w.RCE, w.Network, w.EPSS)
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		return less(a.Priority, b.Priority, a.KEV != nil, b.KEV != nil, a.EPSS, b.EPSS, a.Score, b.Score, a.ID, b.ID)
	})
	sort.SliceStable(rep.Watch, func(i, j int) bool {
		a, b := rep.Watch[i], rep.Watch[j]
		return less(a.Priority, b.Priority, a.KEV != nil, b.KEV != nil, a.EPSS, b.EPSS, a.Score, b.Score, a.ID, b.ID)
	})
	return rep
}

type ImageSummary struct {
	Image   string     `json:"image"`
	Where   []ImageRef `json:"where"`
	Lower   int        `json:"lower"`
	Note    string     `json:"note,omitempty"`
	Fixable int        `json:"fixable"`
}

func imageOnly(f Finding) bool {
	for _, p := range f.Packages {
		if p.Image == "" {
			return false
		}
	}
	return len(f.Packages) > 0
}

func splitImageFindings(fs []Finding, scanned map[string][]ImageRef, notes map[string]string) ([]Finding, []ImageSummary, []string) {
	byImage := map[string]*ImageSummary{}
	for ref, where := range scanned {
		byImage[ref] = &ImageSummary{Image: ref, Where: where, Note: notes[ref]}
	}
	kept := fs[:0]
	var summarized []string
	for _, f := range fs {
		if f.Priority == "P1" || f.Priority == "P2" || !imageOnly(f) {
			kept = append(kept, f)
			continue
		}
		summarized = append(append(summarized, f.ID), f.Aliases...)
		counted := map[string]bool{}
		for _, p := range f.Packages {
			s := byImage[p.Image]
			if s == nil || counted[p.Image] {
				continue
			}
			counted[p.Image] = true
			s.Lower++
			if f.Resolvable {
				s.Fixable++
			}
		}
	}
	out := make([]ImageSummary, 0, len(byImage))
	for _, s := range byImage {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Image < out[j].Image })
	sort.Strings(summarized)
	return kept, out, summarized
}

func less(pa, pb string, ka, kb bool, ea, eb, sa, sb float64, ia, ib string) bool {
	switch {
	case pa != pb:
		return pa < pb
	case ka != kb:
		return ka
	case ea != eb:
		return ea > eb
	case sa != sb:
		return sa > sb
	}
	return ia < ib
}

func rank(severity string, kev, rce, network bool, epss float64) (string, bool) {
	switch {
	case kev, rce && network && (severity == "CRITICAL" || severity == "HIGH"):
		return "P1", true
	case severity == "CRITICAL", severity == "HIGH" && epss >= 0.1:
		return "P2", false
	case severity == "HIGH", severity == "MEDIUM", severity == "MODERATE", severity == "UNKNOWN", severity == "":
		return "P3", false
	}
	return "P4", false
}

func scored(r osvRecord) bool {
	if r.DatabaseSpecific.Severity != "" {
		return true
	}
	for _, sv := range r.Severity {
		if strings.HasPrefix(sv.Score, "CVSS:3.") {
			return true
		}
	}
	return false
}

func unscoredCVEs(recs map[string]osvRecord) []string {
	scoredAlias := map[string]bool{}
	for id, r := range recs {
		if scored(r) {
			scoredAlias[id] = true
			for _, a := range r.Aliases {
				scoredAlias[a] = true
			}
		}
	}
	want := map[string]bool{}
	for id, r := range recs {
		if scoredAlias[id] {
			continue
		}
		for _, a := range r.Aliases {
			if _, have := recs[a]; !have && strings.HasPrefix(a, "CVE-") {
				want[a] = true
			}
		}
	}
	return sortedSet(want)
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func canonical(ids []string) string {
	sort.Strings(ids)
	for _, pfx := range []string{"CVE-", "GHSA-"} {
		for _, id := range ids {
			if strings.HasPrefix(id, pfx) {
				return id
			}
		}
	}
	return strings.ReplaceAll(ids[0], ":", "-")
}

func group(recs map[string]osvRecord, hits map[pkgKey][]string, byKey map[pkgKey][]Package, kev map[string]KEVEntry) []Finding {
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
	for id, r := range recs {
		find(id)
		for _, a := range r.Aliases {
			union(a, id)
		}
	}
	members := map[string][]string{}
	for id := range parent {
		root := find(id)
		members[root] = append(members[root], id)
	}
	pkgsOf := map[string][]pkgKey{}
	for k, ids := range hits {
		for _, id := range ids {
			pkgsOf[find(id)] = append(pkgsOf[find(id)], k)
		}
	}
	live := map[string]bool{}
	for id, r := range recs {
		if r.Withdrawn == "" {
			live[find(id)] = true
		}
	}
	var out []Finding
	for root, ids := range members {
		var rs []osvRecord
		for _, id := range ids {
			if r, ok := recs[id]; ok && r.Withdrawn == "" {
				rs = append(rs, r)
			}
		}
		if len(rs) == 0 || len(pkgsOf[root]) == 0 {
			continue
		}
		if bundleCovered(rs, pkgsOf[root], parent, find, pkgsOf, live) {
			continue
		}
		f := merge(canonical(ids), ids, rs, kev)
		seen := map[pkgKey]bool{}
		listed := map[string]bool{}
		resolvable := true
		for _, k := range pkgsOf[root] {
			if seen[k] {
				continue
			}
			seen[k] = true
			fix := fixFor(rs, k)
			if fix == "" {
				resolvable = false
			}
			for _, p := range byKey[k] {
				a := Affected{Repo: p.Repo, File: p.File, Ecosystem: p.Ecosystem, Name: p.Name, Version: p.Version, Fix: fix, Dev: p.Dev, Image: p.Image}
				if pk := a.Repo + "|" + a.File + "|" + a.Name + "|" + a.Version; !listed[pk] {
					listed[pk] = true
					f.Packages = append(f.Packages, a)
				}
			}
		}
		sort.Slice(f.Packages, func(i, j int) bool {
			a, b := f.Packages[i], f.Packages[j]
			if a.Repo != b.Repo {
				return a.Repo < b.Repo
			}
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Name < b.Name
		})
		f.Resolvable = resolvable
		out = append(out, f)
	}
	return out
}

func bundleCovered(rs []osvRecord, keys []pkgKey, parent map[string]string, find func(string) string, pkgsOf map[string][]pkgKey, live map[string]bool) bool {
	var ups []string
	for _, r := range rs {
		if len(r.Upstream) < 2 {
			return false
		}
		ups = append(ups, r.Upstream...)
	}
	for _, u := range ups {
		if _, ok := parent[u]; !ok || !live[find(u)] {
			return false
		}
		for _, k := range keys {
			if !slices.Contains(pkgsOf[find(u)], k) {
				return false
			}
		}
	}
	return true
}

func merge(id string, ids []string, rs []osvRecord, kev map[string]KEVEntry) Finding {
	f := Finding{ID: id}
	aliases := map[string]bool{}
	related := map[string]bool{}
	cwes := map[string]bool{}
	refs := map[string]bool{}
	var text strings.Builder
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	for _, r := range rs {
		for _, a := range append([]string{r.ID}, r.Aliases...) {
			if a != id {
				aliases[a] = true
			}
		}
		for _, a := range ids {
			if a != id {
				aliases[a] = true
			}
		}
		if f.Summary == "" || strings.HasPrefix(r.ID, "GHSA-") && r.Summary != "" {
			f.Summary = firstNonEmpty(r.Summary, firstLine(r.Details))
		}
		if len(r.Upstream) > 1 {
			for _, u := range r.Upstream {
				related[u] = true
			}
		}
		fmt.Fprintf(&text, "%s %s ", r.Summary, r.Details)
		if r.Published != "" && (f.Published == "" || r.Published < f.Published) {
			f.Published = r.Published
		}
		for _, s := range r.Severity {
			switch {
			case strings.HasPrefix(s.Score, "CVSS:3."):
				if c, ok := parseCVSS3(s.Score); ok && c.Score >= f.Score {
					f.Score, f.Vector, f.Network = c.Score, s.Score, c.Network
				}
			case strings.HasPrefix(s.Score, "CVSS:4.") && f.Vector == "":
				f.Vector, f.Network = s.Score, cvss4Network(s.Score)
			}
		}
		if sev := strings.ToUpper(r.DatabaseSpecific.Severity); sevRank(sev) > sevRank(f.Severity) {
			f.Severity = sev
		}
		for _, c := range r.DatabaseSpecific.CWEs {
			cwes[c] = true
		}
		for _, ref := range r.References {
			if ref.Type == "ADVISORY" || ref.Type == "FIX" || ref.Type == "WEB" {
				refs[ref.URL] = true
			}
		}
	}
	if s := severityOfScore(f.Score); sevRank(s) > sevRank(f.Severity) {
		f.Severity = s
	}
	if f.Severity == "" {
		f.Severity = "UNKNOWN"
	}
	f.Aliases = sortedSet(aliases)
	for a := range aliases {
		delete(related, a)
	}
	delete(related, id)
	f.Related = sortedSet(related)
	f.CWEs = sortedSet(cwes)
	for _, a := range f.cveIDs() {
		if e, ok := kev[a]; ok {
			f.KEV = &e
			fmt.Fprintf(&text, "%s %s", e.Name, e.Description)
			break
		}
	}
	f.RCE = rceText.MatchString(text.String())
	for c := range cwes {
		f.RCE = f.RCE || rceCWEs[c]
	}
	f.Refs = sortedSet(refs)
	if len(f.Refs) > 5 {
		f.Refs = f.Refs[:5]
	}
	f.Summary = ellipsis(f.Summary, maxSummary)
	return f
}

func (f Finding) cveIDs() []string {
	return append(append([]string{f.ID}, f.Aliases...), f.Related...)
}

func sevRank(s string) int {
	switch s {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM", "MODERATE":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func fixFor(rs []osvRecord, k pkgKey) string {
	v := normVersion(k.Version)
	best := ""
	var later []string
	for _, r := range rs {
		for _, a := range r.Affected {
			if !strings.EqualFold(a.Package.Ecosystem, k.Ecosystem) || normName(k.Ecosystem, a.Package.Name) != normName(k.Ecosystem, k.Name) {
				continue
			}
			for _, rg := range a.Ranges {
				if rg.Type == "GIT" {
					continue
				}
				if fix, in := intervalFix(k.Ecosystem, rg.Events, v); in {
					if fix != "" && (best == "" || compareIn(k.Ecosystem, fix, best) > 0) {
						best = fix
					}
				}
				for _, e := range rg.Events {
					if fx := e["fixed"]; fx != "" && compareIn(k.Ecosystem, fx, v) > 0 {
						later = append(later, fx)
					}
				}
			}
		}
	}
	if best != "" {
		return best
	}
	sort.Slice(later, func(i, j int) bool { return compareIn(k.Ecosystem, later[i], later[j]) < 0 })
	if len(later) > 0 {
		return later[0]
	}
	return ""
}

func intervalFix(eco string, events []map[string]string, v string) (string, bool) {
	in := false
	for _, e := range events {
		switch {
		case e["introduced"] != "":
			intro := e["introduced"]
			in = intro == "0" || compareIn(eco, v, intro) >= 0
		case e["fixed"] != "":
			if in && compareIn(eco, v, e["fixed"]) < 0 {
				return e["fixed"], true
			}
			in = false
		case e["last_affected"] != "":
			if in && compareIn(eco, v, e["last_affected"]) <= 0 {
				return "", true
			}
			in = false
		case e["limit"] != "":
			if in && compareIn(eco, v, e["limit"]) < 0 {
				return "", true
			}
			in = false
		}
	}
	return "", in
}

func watchComponents(ctx context.Context, src Sources, comps []Component, kev map[string]KEVEntry, now time.Time, days int) ([]Watch, []Skip) {
	where := map[string][]Component{}
	var products []string
	for _, c := range comps {
		if _, ok := where[c.Product]; !ok {
			products = append(products, c.Product)
		}
		where[c.Product] = append(where[c.Product], c)
	}
	sort.Strings(products)
	from := now.AddDate(0, 0, -days)
	var out []Watch
	var skips []Skip
	seen := map[string]bool{}
	add := func(w Watch) {
		key := w.Product + " " + w.ID
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, w)
	}
	first := true
	for _, p := range products {
		spec, ok := productSpecs[p]
		if !ok {
			spec = productSpec{Keywords: []string{p}}
		}
		for _, kw := range spec.Keywords {
			if !first {
				if err := src.pause(ctx); err != nil {
					return out, append(skips, Skip{"nvd", err.Error()})
				}
			}
			first = false
			cves, err := src.nvdSearch(ctx, kw, from, now)
			if err != nil {
				skips = append(skips, Skip{"nvd", err.Error()})
			}
			for _, c := range cves {
				if c.Status == "Rejected" || !spec.covers(c, kw) {
					continue
				}
				sev := c.Severity
				if sevRank(sev) < 3 {
					if _, inKEV := kev[c.ID]; !inKEV && c.KEVAdded == "" {
						continue
					}
				}
				w := Watch{ID: c.ID, Product: p, Severity: sev, Score: c.Score, Vector: c.Vector,
					Description: c.Description, Published: c.Published, Refs: c.Refs, Where: where[p]}
				if v, ok := parseCVSS3(c.Vector); ok {
					w.Network = v.Network
				} else {
					w.Network = cvss4Network(c.Vector)
				}
				w.RCE = rceText.MatchString(c.Description)
				for _, cwe := range c.CWEs {
					w.RCE = w.RCE || rceCWEs[cwe]
				}
				if e, ok := kev[c.ID]; ok {
					w.KEV = &e
				}
				w.Description = ellipsis(w.Description, maxDescription)
				add(w)
			}
		}
		for _, id := range sortedKEV(kev) {
			e := kev[id]
			if e.DateAdded >= from.Format("2006-01-02") && kevMatches(e, spec.kevNamesFor(p)) {
				add(Watch{ID: e.CVE, Product: p, Severity: "UNKNOWN", RCE: rceText.MatchString(e.Name + " " + e.Description),
					Network: true, KEV: &e, Description: e.Description, Published: e.DateAdded, Where: where[p]})
			}
		}
	}
	return out, skips
}

func sortedKEV(kev map[string]KEVEntry) []string {
	ids := make([]string, 0, len(kev))
	for id := range kev {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func kevMatches(e KEVEntry, kws []string) bool {
	for _, k := range kws {
		if wordIn(e.Vendor+" "+e.Product, k) {
			return true
		}
	}
	return false
}

const (
	maxSummary     = 300
	maxDescription = 400
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func ellipsis(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return truncate(s, n) + "…"
}

func CheckPackages(ctx context.Context, src Sources, pkgs []Package) ([]Finding, error) {
	byKey := map[pkgKey][]Package{}
	var keys []pkgKey
	for _, p := range pkgs {
		k := pkgKey{p.Ecosystem, p.Name, p.Version}
		if _, ok := byKey[k]; !ok {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], p)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	hits, err := src.osvQuery(ctx, keys)
	if err != nil {
		return nil, err
	}
	idSet := map[string]bool{}
	for _, ids := range hits {
		for _, id := range ids {
			idSet[id] = true
		}
	}
	recs, failed := src.osvRecords(ctx, sortedSet(idSet), false)
	if len(failed) > 0 {
		return nil, fmt.Errorf("osv records: %s", strings.Join(failed, "; "))
	}
	extra, _ := src.osvRecords(ctx, unscoredCVEs(recs), true)
	maps.Copy(recs, extra)
	fs := group(recs, hits, byKey, nil)
	for i := range fs {
		fs[i].Priority, fs[i].Major = rank(fs[i].Severity, false, fs[i].RCE, fs[i].Network, 0)
	}
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		return less(a.Priority, b.Priority, false, false, 0, 0, a.Score, b.Score, a.ID, b.ID)
	})
	return fs, nil
}
