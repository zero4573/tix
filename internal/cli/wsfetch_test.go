package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/vault"
)

func fetchVault(t *testing.T) vault.Context {
	t.Helper()
	root := t.TempDir()
	v, projects, work := filepath.Join(root, "vault"), filepath.Join(root, "projects"), filepath.Join(root, "work")
	for _, d := range []string{filepath.Join(v, ".obsidian"), filepath.Join(projects, "bb", "acme", "api", ".git"),
		filepath.Join(work, "T-1", "planted", ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf, _ := json.Marshal(map[string]string{"workRoot": work, "projectsRoot": projects})
	if err := os.WriteFile(filepath.Join(v, ".workflow.json"), wf, 0o644); err != nil {
		t.Fatal(err)
	}
	prev := config.LaunchVault
	config.LaunchVault = v
	t.Cleanup(func() { config.LaunchVault = prev })
	ctx, err := vault.Require()
	if err != nil || ctx.ProjectsRoot != projects {
		t.Fatalf("the test vault isn't the one in use (%v): %s", err, ctx.ProjectsRoot)
	}
	return ctx
}

func stampTime(t *testing.T, ctx vault.Context) time.Time {
	t.Helper()
	st, err := os.Stat(filepath.Join(ctx.ProjectsRoot, "bb", "acme", "api", ".git", fetchStamp))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func TestWsFetchStampsOnlyAFullSuccessfulFetch(t *testing.T) {
	ctx := fetchVault(t)
	api := filepath.Join(ctx.ProjectsRoot, "bb", "acme", "api")
	if err := os.WriteFile(filepath.Join(api, ".git", "FETCH_HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if fetchedWithin(api, time.Hour) {
		t.Fatal("a FETCH_HEAD from another fetch counted as a full one")
	}
	var calls int
	ok := func(string) error { calls++; return nil }
	failing := func(string) error { calls++; return errors.New("unreachable") }
	if err := wsFetchWith(ctx, []string{"bb/acme/api"}, 10*time.Minute, ok); err != nil {
		t.Fatal(err)
	}
	first := stampTime(t, ctx)
	if first.IsZero() || !fetchedWithin(api, 10*time.Minute) {
		t.Fatal("a full fetch wasn't stamped")
	}
	old := first.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(api, ".git", fetchStamp), old, old); err != nil {
		t.Fatal(err)
	}
	if err := wsFetchWith(ctx, []string{"bb/acme/api"}, 10*time.Minute, failing); err != nil {
		t.Fatal(err)
	}
	if got := stampTime(t, ctx); !got.Equal(old) || calls != 2 {
		t.Errorf("a failed fetch moved the stamp: %v (%d calls)", got, calls)
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(api, ".git", fetchStamp), now, now); err != nil {
		t.Fatal(err)
	}
	if err := wsFetchWith(ctx, []string{"bb/acme/api"}, 10*time.Minute, ok); err != nil || calls != 2 {
		t.Errorf("fetched again within 10 minutes: %v (%d calls)", err, calls)
	}
}

func TestResolveClonesAcceptsOnlyMainClones(t *testing.T) {
	ctx := fetchVault(t)
	if got, err := resolveClones(ctx, []string{"bb/acme/api"}); err != nil || !slices.Equal(got, []string{"bb/acme/api"}) {
		t.Errorf("%v %v", got, err)
	}
	for _, spec := range []string{"../work/T-1/planted", "bb/acme/nope", "/etc"} {
		if _, err := resolveClones(ctx, []string{spec}); err == nil {
			t.Errorf("%q resolved", spec)
		}
	}
}

func TestTicketClonesReadsWorkspaceThenProjects(t *testing.T) {
	ctx := fetchVault(t)
	d := filepath.Join(ctx.Vault, "tickets", "jira", "PROJ-1")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "PROJ-1.md"), []byte("---\nprojects:\n  - \"[[bb-acme-api]]\"\n  - gone-repo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ticketClones(ctx, "PROJ-1"); !slices.Equal(got, []string{"bb/acme/api"}) {
		t.Errorf("from projects: %v", got)
	}
	if got := ticketClones(ctx, "PROJ-2"); len(got) != 0 {
		t.Errorf("an unknown ticket: %v", got)
	}
}
