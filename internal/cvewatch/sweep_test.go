package cvewatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type feeds struct {
	vulns    map[string]any
	batch    func(q osvQuery) []string
	kev      []KEVEntry
	epss     map[string]string
	nvd      map[string][]any
	nvdCalls atomic.Int32
	fail     map[string]int
}

func (f *feeds) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := f.fail[r.URL.Path]; n > 0 {
			f.fail[r.URL.Path] = n - 1
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		switch {
		case r.URL.Path == "/osv/querybatch":
			var req struct {
				Queries []osvQuery `json:"queries"`
			}
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &req); err != nil {
				t.Errorf("querybatch body: %v", err)
			}
			var res []map[string]any
			for _, q := range req.Queries {
				var vs []map[string]string
				for _, id := range f.batch(q) {
					vs = append(vs, map[string]string{"id": id})
				}
				res = append(res, map[string]any{"vulns": vs})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
		case strings.HasPrefix(r.URL.Path, "/osv/vulns/"):
			v, ok := f.vulns[strings.TrimPrefix(r.URL.Path, "/osv/vulns/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(v)
		case r.URL.Path == "/kev":
			_ = json.NewEncoder(w).Encode(map[string]any{"vulnerabilities": f.kev})
		case r.URL.Path == "/epss":
			var data []map[string]string
			for c := range strings.SplitSeq(r.URL.Query().Get("cve"), ",") {
				if e, ok := f.epss[c]; ok {
					data = append(data, map[string]string{"cve": c, "epss": e})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.URL.Path == "/nvd":
			f.nvdCalls.Add(1)
			kw := r.URL.Query().Get("keywordSearch")
			if r.URL.Query().Get("pubStartDate") == "" || r.URL.Query().Get("pubEndDate") == "" {
				t.Errorf("nvd call without a date window: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"vulnerabilities": f.nvd[kw]})
		default:
			http.NotFound(w, r)
		}
	}))
}

func sources(url string) Sources {
	return Sources{OSV: url + "/osv", KEV: url + "/kev", EPSS: url + "/epss", NVD: url + "/nvd",
		Retry: time.Millisecond, Client: http.DefaultClient}
}

func nvdItem(id, desc, vector string, score float64, sev string, cwe string, cpes ...string) any {
	var matches []map[string]string
	for _, c := range cpes {
		matches = append(matches, map[string]string{"criteria": c})
	}
	return map[string]any{"cve": map[string]any{
		"configurations": []map[string]any{{"nodes": []map[string]any{{"cpeMatch": matches}}}},
		"id":             id, "published": "2026-11-10T00:00:00.000", "vulnStatus": "Analyzed",
		"descriptions": []map[string]string{{"lang": "en", "value": desc}},
		"metrics": map[string]any{"cvssMetricV31": []map[string]any{{"type": "Primary",
			"cvssData": map[string]any{"baseScore": score, "baseSeverity": sev, "vectorString": vector}}}},
		"weaknesses": []map[string]any{{"description": []map[string]string{{"value": cwe}}}},
		"references": []map[string]string{{"url": "https://example.com/" + id}},
	}}
}

func TestSweepRanksMergesAndSplitsResolvable(t *testing.T) {
	f := &feeds{
		vulns: map[string]any{
			"GHSA-aaaa": map[string]any{"id": "GHSA-aaaa", "aliases": []string{"CVE-2021-44228"},
				"summary":  "Remote code execution in Log4j JNDI lookups",
				"severity": []map[string]string{{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H"}},
				"affected": []map[string]any{{"package": map[string]string{"name": "org.apache.logging.log4j:log4j-core", "ecosystem": "Maven"},
					"ranges": []map[string]any{{"type": "ECOSYSTEM", "events": []map[string]string{{"introduced": "2.0"}, {"fixed": "2.15.0"}}}}}},
				"database_specific": map[string]any{"severity": "CRITICAL", "cwe_ids": []string{"CWE-502"}},
				"references":        []map[string]string{{"type": "ADVISORY", "url": "https://nvd.nist.gov/vuln/detail/CVE-2021-44228"}}},
			"GO-2024-2687": map[string]any{"id": "GO-2024-2687", "aliases": []string{"CVE-2023-45288", "GHSA-4v7x"},
				"summary": "HTTP/2 CONTINUATION flood",
				"affected": []map[string]any{{"package": map[string]string{"name": "golang.org/x/net", "ecosystem": "Go"},
					"ranges": []map[string]any{{"type": "SEMVER", "events": []map[string]string{{"introduced": "0"}, {"fixed": "0.23.0"}}}}}}},
			"GHSA-4v7x": map[string]any{"id": "GHSA-4v7x", "aliases": []string{"CVE-2023-45288", "GO-2024-2687"},
				"summary":           "x/net/http2: close connections when receiving too many headers",
				"severity":          []map[string]string{{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L"}},
				"database_specific": map[string]any{"severity": "MODERATE"}},
			"GHSA-nofix": map[string]any{"id": "GHSA-nofix", "summary": "ReDoS in left-pad",
				"affected": []map[string]any{{"package": map[string]string{"name": "left-pad", "ecosystem": "npm"},
					"ranges": []map[string]any{{"type": "SEMVER", "events": []map[string]string{{"introduced": "0"}}}}}},
				"database_specific": map[string]any{"severity": "HIGH"}},
			"GO-2025-4337": map[string]any{"id": "GO-2025-4337", "aliases": []string{"CVE-2025-61726", "CVE-2099-0001"},
				"summary": "Memory exhaustion in query parameter parsing in net/url",
				"affected": []map[string]any{{"package": map[string]string{"name": "stdlib", "ecosystem": "Go"},
					"ranges": []map[string]any{{"type": "SEMVER", "events": []map[string]string{{"introduced": "0"}, {"fixed": "1.24.12"}, {"introduced": "1.25.0-0"}, {"fixed": "1.25.6"}}}}}}},
			"CVE-2025-61726": map[string]any{"id": "CVE-2025-61726",
				"severity": []map[string]string{{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H"}}},
			"GHSA-gone": map[string]any{"id": "GHSA-gone", "withdrawn": "2025-01-01T00:00:00Z", "summary": "withdrawn"},
		},
		batch: func(q osvQuery) []string {
			switch q.Package.Name {
			case "org.apache.logging.log4j:log4j-core":
				return []string{"GHSA-aaaa"}
			case "golang.org/x/net":
				return []string{"GO-2024-2687", "GHSA-4v7x"}
			case "stdlib":
				return []string{"GO-2025-4337"}
			case "left-pad":
				return []string{"GHSA-nofix", "GHSA-gone"}
			}
			return nil
		},
		kev: []KEVEntry{
			{CVE: "CVE-2021-44228", Vendor: "Apache", Product: "Log4j2", Name: "Apache Log4j2 RCE", DateAdded: "2021-12-10"},
			{CVE: "CVE-2026-1111", Vendor: "F5", Product: "NGINX", Name: "F5 NGINX heap overflow allowing remote code execution", DateAdded: "2026-11-15", Description: "allows remote code execution"},
			{CVE: "CVE-2026-0001", Vendor: "F5", Product: "NGINX", Name: "old", DateAdded: "2025-01-01"},
		},
		epss: map[string]string{"CVE-2021-44228": "0.97", "CVE-2023-45288": "0.20", "CVE-2026-2222": "0.01"},
		nvd: map[string][]any{
			"nginx": {
				nvdItem("CVE-2026-2222", "A flaw in nginx resolver allows a remote attacker to execute arbitrary code.", "CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:H/A:H", 8.1, "HIGH", "CWE-787"),
				nvdItem("CVE-2026-3333", "nginx minor info leak", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N", 5.3, "MEDIUM", "CWE-200"),
				nvdItem("CVE-2026-4444", "Some other product that bundles nothing relevant", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "CRITICAL", "CWE-78"),
				nvdItem("CVE-2026-5555", "nginxWebUI before 3.5.8 allows remote code execution through runCmd.", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "CRITICAL", "CWE-78"),
				nvdItem("CVE-2026-6666", "A WordPress form plugin lets attackers upload a PHP file and execute arbitrary code. "+strings.Repeat("Details. ", 30)+"Sites behind nginx are affected too.", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "CRITICAL", "CWE-434"),
				nvdItem("CVE-2026-7777", "When NGINX Plus or NGINX Open Source proxies HTTP/2, a crafted request can execute arbitrary code.", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "CRITICAL", "CWE-787", "cpe:2.3:a:f5:nginx_open_source:*:*:*:*:*:*:*:*"),
				nvdItem("CVE-2026-8888", "Example Gateway, which embeds nginx, mishandles headers and allows code execution.", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "CRITICAL", "CWE-787", "cpe:2.3:a:example:gateway:*:*:*:*:*:*:*:*"),
			},
		},
		fail: map[string]int{"/osv/querybatch": 1},
	}
	srv := f.server(t)
	defer srv.Close()

	inv := Inventory{
		Repos: []string{"bb/acme/api", "bb/acme/web"},
		Packages: []Package{
			{Repo: "bb/acme/api", File: "pom.xml", Ecosystem: "Maven", Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1"},
			{Repo: "bb/acme/api", File: "go.mod", Ecosystem: "Go", Name: "golang.org/x/net", Version: "v0.17.0"},
			{Repo: "bb/acme/web", File: "go.mod", Ecosystem: "Go", Name: "golang.org/x/net", Version: "v0.17.0"},
			{Repo: "bb/acme/web", File: "yarn.lock", Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"},
			{Repo: "bb/acme/web", File: "go.mod", Ecosystem: "Go", Name: "stdlib", Version: "1.24.0"},
			{Repo: "bb/acme/web", File: "yarn.lock", Ecosystem: "npm", Name: "safe", Version: "1.0.0"},
		},
		Components: []Component{{Repo: "bb/acme/web", File: "Dockerfile", Product: "nginx", Image: "nginx:1.25.3", Version: "1.25.3"}},
	}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	rep := Sweep(context.Background(), inv, Options{Days: 30, Sources: sources(srv.URL), Now: func() time.Time { return now }})

	if len(rep.Skips) != 0 {
		t.Fatalf("skips: %+v", rep.Skips)
	}
	if len(rep.Findings) != 4 {
		t.Fatalf("findings: %+v", rep.Findings)
	}
	log4j, xnet, std, pad := rep.Findings[0], rep.Findings[1], rep.Findings[2], rep.Findings[3]
	if std.ID != "CVE-2025-61726" || std.Severity != "HIGH" || std.Score != 7.5 || std.Packages[0].Fix != "1.24.12" ||
		std.Summary != "Memory exhaustion in query parameter parsing in net/url" {
		t.Errorf("stdlib (scored from its CVE record): %+v", std)
	}
	if log4j.ID != "CVE-2021-44228" || log4j.Priority != "P1" || !log4j.Major || log4j.KEV == nil || !log4j.RCE ||
		log4j.Score != 10 || log4j.EPSS != 0.97 || !log4j.Resolvable || log4j.Packages[0].Fix != "2.15.0" {
		t.Errorf("log4j: %+v", log4j)
	}
	if xnet.ID != "CVE-2023-45288" || xnet.Priority != "P3" || xnet.Major || !xnet.Resolvable || len(xnet.Packages) != 2 ||
		xnet.Packages[0].Fix != "0.23.0" || xnet.Severity != "MODERATE" || xnet.Summary != "x/net/http2: close connections when receiving too many headers" {
		t.Errorf("x/net (GO and GHSA merged by alias): %+v", xnet)
	}
	if strings.Join(xnet.Aliases, ",") != "GHSA-4v7x,GO-2024-2687" {
		t.Errorf("x/net aliases: %v", xnet.Aliases)
	}
	if pad.ID != "GHSA-nofix" || pad.Resolvable || pad.Packages[0].Fix != "" || pad.Priority != "P3" {
		t.Errorf("left-pad (no fix, the withdrawn one dropped): %+v", pad)
	}

	var watched []string
	for _, w := range rep.Watch {
		watched = append(watched, w.ID)
	}
	if strings.Join(watched, ",") != "CVE-2026-1111,CVE-2026-2222,CVE-2026-7777" {
		t.Fatalf("watch (KEV first, then by EPSS and score; compound names, late mentions and other CPEs dropped): %v", watched)
	}
	if w := rep.Watch[0]; w.Priority != "P1" || !w.Major || w.KEV == nil || w.Where[0].Image != "nginx:1.25.3" {
		t.Errorf("the KEV nginx entry: %+v", w)
	}
	if w := rep.Watch[1]; w.Priority != "P1" || !w.RCE || !w.Network || w.Score != 8.1 {
		t.Errorf("the nginx RCE from NVD: %+v", w)
	}
	if rep.Packages != 6 || rep.Days != 30 || rep.Swept != "2026-11-19T12:00:00Z" {
		t.Errorf("report header: %+v", rep)
	}
}

func TestSweepReportsUnreachableSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	inv := Inventory{Packages: []Package{{Repo: "r", File: "go.mod", Ecosystem: "Go", Name: "x", Version: "v1.0.0"}},
		Components: []Component{{Repo: "r", File: "Dockerfile", Product: "nginx"}}}
	rep := Sweep(context.Background(), inv, Options{Sources: sources(srv.URL)})
	var got []string
	for _, s := range rep.Skips {
		got = append(got, s.Source)
	}
	if strings.Join(got, ",") != "cisa-kev,osv,nvd" {
		t.Errorf("skips %v", rep.Skips)
	}
	if len(rep.Findings) != 0 || rep.Days != 30 {
		t.Errorf("report: %+v", rep)
	}
}

func TestRank(t *testing.T) {
	for _, c := range []struct {
		sev           string
		kev, rce, net bool
		epss          float64
		want          string
		major         bool
	}{
		{"LOW", true, false, false, 0, "P1", true},
		{"CRITICAL", false, true, true, 0, "P1", true},
		{"CRITICAL", false, true, false, 0, "P2", false},
		{"HIGH", false, false, true, 0.5, "P2", false},
		{"HIGH", false, false, true, 0.01, "P3", false},
		{"MODERATE", false, true, true, 0.9, "P3", false},
		{"LOW", false, false, false, 0, "P4", false},
		{"UNKNOWN", false, false, false, 0, "P3", false},
	} {
		p, major := rank(c.sev, c.kev, c.rce, c.net, c.epss)
		if p != c.want || major != c.major {
			t.Errorf("rank(%+v) = %s %v", c, p, major)
		}
	}
}

func TestSweepMatchesNormalisedPyPINames(t *testing.T) {
	f := &feeds{
		vulns: map[string]any{
			"GHSA-2gwj": map[string]any{"id": "GHSA-2gwj", "summary": "SQL injection in Django",
				"affected": []map[string]any{{"package": map[string]string{"name": "Django", "ecosystem": "PyPI"},
					"ranges": []map[string]any{{"type": "ECOSYSTEM", "events": []map[string]string{{"introduced": "3.2"}, {"fixed": "3.2.13"}}}}}},
				"database_specific": map[string]any{"severity": "CRITICAL"}},
		},
		batch: func(q osvQuery) []string {
			if q.Package.Name == "django" {
				return []string{"GHSA-2gwj"}
			}
			return nil
		},
	}
	srv := f.server(t)
	defer srv.Close()
	root := t.TempDir()
	write(t, root, "r/requirements.txt", "Django==3.2.0\n")
	rep := Sweep(context.Background(), Collect(root, []string{"r"}), Options{Sources: sources(srv.URL)})
	if len(rep.Findings) != 1 || !rep.Findings[0].Resolvable || rep.Findings[0].Packages[0].Fix != "3.2.13" {
		t.Errorf("findings: %+v", rep.Findings)
	}
}

func TestNVDSearchReadsEveryPage(t *testing.T) {
	const total = 365
	var starts []string
	failAt := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("startIndex")
		starts = append(starts, start)
		if start == failAt {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		from := 0
		_, _ = fmt.Sscan(start, &from)
		var items []any
		for i := from; i < min(from+200, total); i++ {
			items = append(items, nvdItem(fmt.Sprintf("CVE-2026-%05d", i), "nginx flaw", "", 5, "MEDIUM", "CWE-79"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"totalResults": total, "vulnerabilities": items})
	}))
	defer srv.Close()
	src := sources(srv.URL)
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	got, err := src.nvdSearch(context.Background(), "nginx", now.AddDate(0, 0, -30), now)
	if err != nil || len(got) != total || strings.Join(starts, ",") != "0,200" {
		t.Errorf("got %d results, err %v, pages %v", len(got), err, starts)
	}
	starts, failAt = nil, "200"
	got, err = src.nvdSearch(context.Background(), "nginx", now.AddDate(0, 0, -30), now)
	if err == nil || len(got) != 200 || !strings.Contains(err.Error(), "from 200 of 365") {
		t.Errorf("a failed second page: %d results, err %v", len(got), err)
	}
}

func TestSweepScoresCVSS4OnlyRecordsFromTheirCVE(t *testing.T) {
	f := &feeds{
		vulns: map[string]any{
			"PYSEC-1": map[string]any{"id": "PYSEC-1", "aliases": []string{"CVE-2026-0042"}, "summary": "RCE in pkg",
				"severity": []map[string]string{{"type": "CVSS_V4", "score": "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"}},
				"affected": []map[string]any{{"package": map[string]string{"name": "pkg", "ecosystem": "PyPI"},
					"ranges": []map[string]any{{"type": "ECOSYSTEM", "events": []map[string]string{{"introduced": "0"}, {"fixed": "2.0"}}}}}}},
			"CVE-2026-0042": map[string]any{"id": "CVE-2026-0042",
				"severity": []map[string]string{{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}},
		},
		batch: func(q osvQuery) []string {
			if q.Package.Name == "pkg" {
				return []string{"PYSEC-1"}
			}
			return nil
		},
	}
	srv := f.server(t)
	defer srv.Close()
	inv := Inventory{Packages: []Package{{Repo: "r", File: "requirements.txt", Ecosystem: "PyPI", Name: "pkg", Version: "1.0"}}}
	rep := Sweep(context.Background(), inv, Options{Sources: sources(srv.URL)})
	if len(rep.Findings) != 1 || rep.Findings[0].Severity != "CRITICAL" || rep.Findings[0].Priority != "P1" {
		t.Errorf("findings: %+v", rep.Findings)
	}
}

func TestGetRetriesThenGivesUp(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 && r.URL.Path == "/ok" {
			_, _ = w.Write([]byte(`{"v":1}`))
			return
		}
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	src := sources(srv.URL)
	var out struct{ V int }
	if err := src.get(context.Background(), srv.URL+"/ok", nil, &out); err != nil || out.V != 1 || calls.Load() != 2 {
		t.Errorf("a 429 then 200: %v %+v after %d calls", err, out, calls.Load())
	}
	calls.Store(10)
	if err := src.get(context.Background(), srv.URL+"/never", nil, &out); err == nil || !strings.Contains(err.Error(), "429") || calls.Load() != 10+maxAttempts {
		t.Errorf("gave up: %v after %d calls", err, calls.Load()-10)
	}
}
