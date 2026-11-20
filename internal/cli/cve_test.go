package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/cvewatch"
	"github.com/zero4573/tix/internal/vault"
)

func cveVault(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	v, projects := filepath.Join(root, "vault"), filepath.Join(root, "projects")
	clone := filepath.Join(projects, "bitbucket", "acme", "api")
	for _, d := range []string{filepath.Join(v, ".obsidian"), filepath.Join(clone, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf, _ := json.Marshal(map[string]string{"workRoot": filepath.Join(root, "work"), "projectsRoot": projects})
	if err := os.WriteFile(filepath.Join(v, ".workflow.json"), wf, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "go.mod"), []byte("module x\n\nrequire golang.org/x/net v0.17.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := config.LaunchVault
	config.LaunchVault = v
	t.Cleanup(func() { config.LaunchVault = prev })
	ctx, err := vault.Require()
	if err != nil || ctx.Vault != v || ctx.ProjectsRoot != projects {
		t.Fatalf("the test vault isn't the one in use (%v): %s, %s", err, ctx.Vault, ctx.ProjectsRoot)
	}
	return v, clone
}

func cveStub(t *testing.T, osvCalls *int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/osv/querybatch":
			*osvCalls++
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"GHSA-xnet-0000-0001"}]}]}`))
		case strings.HasPrefix(r.URL.Path, "/osv/vulns/GHSA-xnet-0000-0001"):
			_, _ = w.Write([]byte(`{"id":"GHSA-xnet-0000-0001","aliases":["CVE-2026-0001"],"summary":"Remote code execution in x/net",
				"severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}],
				"affected":[{"package":{"name":"golang.org/x/net","ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"0.23.0"}]}]}]}`))
		case r.URL.Path == "/kev":
			_, _ = w.Write([]byte(`{"vulnerabilities":[]}`))
		case r.URL.Path == "/epss":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runCVE(t *testing.T, d cveDeps, args ...string) (string, error) {
	t.Helper()
	cmd := cveCmdWith(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCVEUpdateSweepsOncePerInterval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	v, _ := cveVault(t)
	var osvCalls int
	srv := cveStub(t, &osvCalls)
	clock := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	d := cveDeps{
		now: func() time.Time { return clock },
		sources: func() cvewatch.Sources {
			return cvewatch.Sources{OSV: srv.URL + "/osv", KEV: srv.URL + "/kev", EPSS: srv.URL + "/epss", NVD: srv.URL + "/nvd",
				Retry: time.Millisecond, Client: srv.Client()}
		},
	}

	out, err := runCVE(t, d, "update")
	if err != nil || osvCalls != 1 {
		t.Fatalf("update: %v, %d osv calls\n%s", err, osvCalls, out)
	}
	for _, want := range []string{" complete: 1 open, 1 P1, 0 P2; next ", "change P1 CVE-2026-0001 new resolvable", "action ticket P1 CVE-2026-0001"} {
		if !strings.Contains(out, want) {
			t.Errorf("update output lacks %q:\n%s", want, out)
		}
	}
	reg, _ := os.ReadFile(filepath.Join(v, "security", "security.md"))
	if !strings.Contains(string(reg), "golang.org/x/net v0.17.0 → 0.23.0") {
		t.Errorf("register:\n%s", reg)
	}
	if _, err := os.Stat(filepath.Join(v, "security", "advisories", "CVE-2026-0001.md")); err != nil {
		t.Error(err)
	}

	clock = clock.Add(5 * time.Hour)
	out, err = runCVE(t, d, "update")
	if err != nil || osvCalls != 1 || !strings.HasPrefix(out, "not due: last ") {
		t.Errorf("a second update within the interval swept: %v %d\n%s", err, osvCalls, out)
	}
	out, _ = runCVE(t, d, "due")
	if !strings.HasPrefix(out, "not-due last ") || !strings.HasSuffix(strings.TrimSpace(out), "next 2026-11-21T00:00:00Z") {
		t.Errorf("due: %q", out)
	}

	clock = clock.Add(7 * time.Hour)
	out, err = runCVE(t, d, "update", "--json")
	var res cvewatch.UpdateResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || !res.Due || osvCalls != 2 || strings.Join(res.Tell, ",") != "ticket CVE-2026-0001" || len(res.Actions) != 1 {
		t.Errorf("the due update: %v %d %+v\n%s", err, osvCalls, res, out)
	}
}

func TestCVEAckRecordsDeliveredItems(t *testing.T) {
	v, _ := cveVault(t)
	var osvCalls int
	srv := cveStub(t, &osvCalls)
	clock := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	d := cveDeps{
		now: func() time.Time { return clock },
		sources: func() cvewatch.Sources {
			return cvewatch.Sources{OSV: srv.URL + "/osv", KEV: srv.URL + "/kev", EPSS: srv.URL + "/epss", NVD: srv.URL + "/nvd",
				Retry: time.Millisecond, Client: srv.Client()}
		},
	}
	if _, err := runCVE(t, d, "update"); err != nil {
		t.Fatal(err)
	}
	if out, err := runCVE(t, d, "ack", "--", "ticket CVE-2026-0001", "ticket CVE-9999-0001"); err != nil {
		t.Fatalf("ack: %v %s", err, out)
	}
	st, err := cvewatch.LoadState(filepath.Join(v, "security"))
	if err != nil || strings.Join(st.Told, ",") != "ticket CVE-2026-0001" {
		t.Errorf("told %v (%v)", st.Told, err)
	}
	clock = clock.Add(13 * time.Hour)
	out, _ := runCVE(t, d, "update", "--json")
	var res cvewatch.UpdateResult
	if json.Unmarshal([]byte(out), &res) != nil || len(res.Tell) != 0 || len(res.Actions) != 1 {
		t.Errorf("an acknowledged item was told again: %+v", res)
	}
}
