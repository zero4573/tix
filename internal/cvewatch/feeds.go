package cvewatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Sources struct {
	OSV       string
	KEV       string
	EPSS      string
	NVD       string
	EOL       string
	NVDKey    string
	NVDPause  time.Duration
	Retry     time.Duration
	Client    *http.Client
	UserAgent string
}

const maxAttempts = 3

const maxRedirects = 5

func keepKeyOnHost(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.URL.Host != via[0].URL.Host {
		req.Header.Del("apiKey")
	}
	return nil
}

func (s Sources) retryPause() time.Duration {
	if s.Retry > 0 {
		return s.Retry
	}
	return 5 * time.Second
}

func DefaultSources() Sources {
	return Sources{
		OSV:      "https://api.osv.dev/v1",
		KEV:      "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json",
		EPSS:     "https://api.first.org/data/v1/epss",
		NVD:      "https://services.nvd.nist.gov/rest/json/cves/2.0",
		EOL:      "https://endoflife.date/api",
		NVDPause: 6500 * time.Millisecond,
		Client:   &http.Client{Timeout: 90 * time.Second, CheckRedirect: keepKeyOnHost},
	}
}

const maxResponseBytes = 64 << 20

var errNotFound = errors.New("404 Not Found")

func (s Sources) get(ctx context.Context, u string, body any, out any) error {
	var payload []byte
	method := http.MethodGet
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload, method = b, http.MethodPost
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * s.retryPause()):
			}
		}
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		ua := s.UserAgent
		if ua == "" {
			ua = "tix-cve-sweep"
		}
		req.Header.Set("User-Agent", ua)
		if s.NVDKey != "" && strings.HasPrefix(u, s.NVD) {
			req.Header.Set("apiKey", s.NVDKey)
		}
		resp, err := s.Client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden && strings.HasPrefix(u, s.NVD) || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s", resp.Status)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s", resp.Status)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("bad JSON: %w", err)
		}
		return nil
	}
	return lastErr
}

type osvQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version string `json:"version"`
}

type osvRecord struct {
	ID        string   `json:"id"`
	Aliases   []string `json:"aliases"`
	Upstream  []string `json:"upstream"`
	Summary   string   `json:"summary"`
	Details   string   `json:"details"`
	Published string   `json:"published"`
	Modified  string   `json:"modified"`
	Withdrawn string   `json:"withdrawn"`
	Severity  []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Type   string              `json:"type"`
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
		Versions []string `json:"versions"`
	} `json:"affected"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
	DatabaseSpecific struct {
		Severity string   `json:"severity"`
		CWEs     []string `json:"cwe_ids"`
	} `json:"database_specific"`
}

type pkgKey struct{ Ecosystem, Name, Version string }

func (s Sources) osvQuery(ctx context.Context, keys []pkgKey) (map[pkgKey][]string, error) {
	out := map[pkgKey][]string{}
	for start := 0; start < len(keys); start += 1000 {
		end := min(start+1000, len(keys))
		var req struct {
			Queries []osvQuery `json:"queries"`
		}
		for _, k := range keys[start:end] {
			var q osvQuery
			q.Package.Name, q.Package.Ecosystem, q.Version = k.Name, k.Ecosystem, k.Version
			req.Queries = append(req.Queries, q)
		}
		var resp struct {
			Results []struct {
				Vulns []struct {
					ID string `json:"id"`
				} `json:"vulns"`
			} `json:"results"`
		}
		if err := s.get(ctx, s.OSV+"/querybatch", req, &resp); err != nil {
			return out, fmt.Errorf("osv querybatch: %w", err)
		}
		for i, r := range resp.Results {
			if start+i >= end {
				break
			}
			for _, v := range r.Vulns {
				out[keys[start+i]] = append(out[keys[start+i]], v.ID)
			}
		}
	}
	return out, nil
}

func (s Sources) osvRecords(ctx context.Context, ids []string, optional bool) (map[string]osvRecord, []string) {
	var mu sync.Mutex
	recs := map[string]osvRecord{}
	var failed []string
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			var r osvRecord
			err := s.get(ctx, s.OSV+"/vulns/"+url.PathEscape(id), nil, &r)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !optional || !errors.Is(err, errNotFound) {
					failed = append(failed, id+": "+err.Error())
				}
				return
			}
			if len(r.Upstream) == 1 {
				r.Aliases = append(r.Aliases, r.Upstream...)
			}
			recs[id] = r
		}(id)
	}
	wg.Wait()
	sort.Strings(failed)
	return recs, failed
}

type KEVEntry struct {
	CVE         string `json:"cveID"`
	Vendor      string `json:"vendorProject"`
	Product     string `json:"product"`
	Name        string `json:"vulnerabilityName"`
	DateAdded   string `json:"dateAdded"`
	Description string `json:"shortDescription"`
	Action      string `json:"requiredAction"`
	DueDate     string `json:"dueDate"`
	Ransomware  string `json:"knownRansomwareCampaignUse"`
}

func (s Sources) kev(ctx context.Context) (map[string]KEVEntry, error) {
	var feed struct {
		Vulnerabilities []KEVEntry `json:"vulnerabilities"`
	}
	if err := s.get(ctx, s.KEV, nil, &feed); err != nil {
		return nil, fmt.Errorf("cisa kev: %w", err)
	}
	m := make(map[string]KEVEntry, len(feed.Vulnerabilities))
	for _, v := range feed.Vulnerabilities {
		m[v.CVE] = v
	}
	return m, nil
}

func (s Sources) epss(ctx context.Context, cves []string) (map[string]float64, error) {
	out := map[string]float64{}
	for start := 0; start < len(cves); start += 100 {
		end := min(start+100, len(cves))
		var resp struct {
			Data []struct {
				CVE  string `json:"cve"`
				EPSS string `json:"epss"`
			} `json:"data"`
		}
		u := s.EPSS + "?cve=" + url.QueryEscape(strings.Join(cves[start:end], ","))
		if err := s.get(ctx, u, nil, &resp); err != nil {
			return out, fmt.Errorf("epss: %w", err)
		}
		for _, d := range resp.Data {
			if f, err := strconv.ParseFloat(d.EPSS, 64); err == nil {
				out[d.CVE] = f
			}
		}
	}
	return out, nil
}

type nvdCVE struct {
	ID          string `json:"id"`
	Published   string `json:"published"`
	Status      string `json:"vulnStatus"`
	KEVAdded    string `json:"cisaExploitAdd"`
	Description string
	Score       float64
	Severity    string
	Vector      string
	CWEs        []string
	Refs        []string
	CPEs        []string
}

const nvdPageSize = 2000

func (s Sources) nvdSearch(ctx context.Context, keyword string, from, to time.Time) ([]nvdCVE, error) {
	q := url.Values{}
	q.Set("keywordSearch", keyword)
	q.Set("pubStartDate", from.UTC().Format("2006-01-02T15:04:05.000Z"))
	q.Set("pubEndDate", to.UTC().Format("2006-01-02T15:04:05.000Z"))
	q.Set("resultsPerPage", strconv.Itoa(nvdPageSize))
	var all []nvdCVE
	total := 0
	for start := 0; ; {
		q.Set("startIndex", strconv.Itoa(start))
		page, n, err := s.nvdPage(ctx, q)
		if err != nil {
			return all, fmt.Errorf("nvd %q from %d of %d: %w", keyword, start, max(total, start), err)
		}
		total = n
		all = append(all, page...)
		start += len(page)
		if len(page) == 0 || start >= total {
			if start < total {
				return all, fmt.Errorf("nvd %q: %d of %d results returned", keyword, start, total)
			}
			return all, nil
		}
		if err := s.pause(ctx); err != nil {
			return all, err
		}
	}
}

func (s Sources) pause(ctx context.Context) error {
	if s.NVDPause <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.NVDPause):
		return nil
	}
}

func (s Sources) nvdPage(ctx context.Context, q url.Values) ([]nvdCVE, int, error) {
	var resp struct {
		TotalResults    int `json:"totalResults"`
		Vulnerabilities []struct {
			CVE struct {
				ID           string `json:"id"`
				Published    string `json:"published"`
				Status       string `json:"vulnStatus"`
				KEVAdded     string `json:"cisaExploitAdd"`
				Descriptions []struct {
					Lang  string `json:"lang"`
					Value string `json:"value"`
				} `json:"descriptions"`
				Metrics    map[string][]nvdMetric `json:"metrics"`
				Weaknesses []struct {
					Description []struct {
						Value string `json:"value"`
					} `json:"description"`
				} `json:"weaknesses"`
				References []struct {
					URL string `json:"url"`
				} `json:"references"`
				Configurations []nvdConfig `json:"configurations"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	if err := s.get(ctx, s.NVD+"?"+q.Encode(), nil, &resp); err != nil {
		return nil, 0, err
	}
	var out []nvdCVE
	for _, v := range resp.Vulnerabilities {
		c := v.CVE
		n := nvdCVE{ID: c.ID, Published: c.Published, Status: c.Status, KEVAdded: c.KEVAdded}
		for _, d := range c.Descriptions {
			if d.Lang == "en" {
				n.Description = d.Value
				break
			}
		}
		for _, key := range []string{"cvssMetricV40", "cvssMetricV31", "cvssMetricV30"} {
			if ms := c.Metrics[key]; len(ms) > 0 {
				m := pickMetric(ms)
				n.Score, n.Severity, n.Vector = m.Data.BaseScore, strings.ToUpper(m.Data.BaseSeverity), m.Data.Vector
				break
			}
		}
		for _, w := range c.Weaknesses {
			for _, d := range w.Description {
				if strings.HasPrefix(d.Value, "CWE-") {
					n.CWEs = append(n.CWEs, d.Value)
				}
			}
		}
		n.CPEs = vendorProducts(c.Configurations)
		for i, r := range c.References {
			if i == 5 {
				break
			}
			n.Refs = append(n.Refs, r.URL)
		}
		out = append(out, n)
	}
	return out, resp.TotalResults, nil
}

type nvdMetric struct {
	Type string `json:"type"`
	Data struct {
		BaseScore    float64 `json:"baseScore"`
		BaseSeverity string  `json:"baseSeverity"`
		Vector       string  `json:"vectorString"`
	} `json:"cvssData"`
	BaseSeverity string `json:"baseSeverity"`
}

func pickMetric(ms []nvdMetric) nvdMetric {
	best := ms[0]
	for _, m := range ms {
		if m.Type == "Primary" {
			best = m
			break
		}
	}
	if best.Data.BaseSeverity == "" {
		best.Data.BaseSeverity = best.BaseSeverity
	}
	if best.Data.BaseSeverity == "" {
		best.Data.BaseSeverity = severityOfScore(best.Data.BaseScore)
	}
	return best
}

type nvdConfig struct {
	Nodes []struct {
		CPEMatch []struct {
			Criteria string `json:"criteria"`
		} `json:"cpeMatch"`
	} `json:"nodes"`
}

func vendorProducts(configs []nvdConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range configs {
		for _, n := range c.Nodes {
			for _, m := range n.CPEMatch {
				parts := strings.Split(m.Criteria, ":")
				if len(parts) < 5 {
					continue
				}
				vp := strings.ToLower(parts[3] + ":" + parts[4])
				if !seen[vp] {
					seen[vp] = true
					out = append(out, vp)
				}
			}
		}
	}
	return out
}
