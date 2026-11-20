package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/links"
	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/spec"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

func TestPromoteFailureKeepsEveryFile(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	put(filepath.Join(proj, "a", "spec.md"), "a0")
	put(filepath.Join(proj, "b", "spec.md"), "b0")
	put(filepath.Join(cp, "a", "spec.md"), "a1")
	put(filepath.Join(cp, "b", "spec.md"), "b1")
	if err := spec.WriteTree(spec.BaseDir(v, "T-1", r.Slug), spec.Tree{"a/spec.md": []byte("a0"), "b/spec.md": []byte("b0")}); err != nil {
		t.Fatal(err)
	}

	calls := 0
	writeSpecFile = func(p string, b []byte, m os.FileMode) error {
		if calls++; calls == 2 {
			return errors.New("disk full")
		}
		return fsx.WriteFileAtomic(p, b, m)
	}
	defer func() { writeSpecFile = fsx.WriteFileAtomic }()
	if _, err := promoteRepo(ctx, "T-1", r, ""); err == nil {
		t.Fatal("want the injected failure")
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "a", "spec.md")); string(b) != "a1" {
		t.Fatalf("first file not moved: %q", b)
	}
	if _, err := os.Stat(filepath.Join(cp, "b", "spec.md")); err != nil {
		t.Fatalf("second file left in the copy: %v", err)
	}

	writeSpecFile = fsx.WriteFileAtomic
	pr, err := promoteRepo(ctx, "T-1", r, "")
	if err != nil || pr.Status != promoteDone {
		t.Fatalf("rerun: %+v %v", pr, err)
	}
	ver, err := spec.ReadVersion(spec.VersionPath(v, r.Slug, "T-1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ver.Files, ","); got != "a/spec.md,b/spec.md" {
		t.Fatalf("note files: %s", got)
	}
}

func TestPromoteWithoutSnapshot(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	for p, s := range map[string]string{filepath.Join(proj, "a", "spec.md"): "a0", filepath.Join(cp, "a", "spec.md"): "a1"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := promoteRepo(ctx, "T-1", r, "")
	if err == nil || !strings.Contains(err.Error(), "tix spec start T-1 acme-app") {
		t.Fatalf("want a refusal naming tix spec start, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "a", "spec.md")); string(b) != "a0" {
		t.Fatalf("projects/ changed: %q", b)
	}
	for _, d := range []string{spec.BaseDir(v, "T-1", r.Slug), spec.AfterDir(v, "T-1", r.Slug), spec.VersionsDir(v, r.Slug)} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s written: %v", d, err)
		}
	}
	if err := os.Remove(filepath.Join(cp, "a", "spec.md")); err != nil {
		t.Fatal(err)
	}
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteNothing {
		t.Fatalf("empty copy: %+v %v", pr, err)
	}
}

func TestStartInterruptedRebase(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	put(filepath.Join(v, "tickets", "manual", "T-1", "T-1.md"), "---\ntitle: T\n---\n")
	put(spec.ProjectNote(v, r.Slug), "---\ntitle: acme-app\nspecs: vault\n---\n")
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	baseDir, afterDir := spec.BaseDir(v, "T-1", r.Slug), spec.AfterDir(v, "T-1", r.Slug)
	put(filepath.Join(proj, "a", "spec.md"), "a0")
	put(filepath.Join(proj, "b", "spec.md"), "b0")
	put(filepath.Join(proj, "c", "spec.md"), "c0")

	if _, err := specStart(ctx, "T-1", r.Slug, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(cp, "a", "spec.md"), "a1")
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("promote: %+v %v", pr, err)
	}
	put(filepath.Join(proj, "a", "spec.md"), "a-theirs")
	if err := os.RemoveAll(filepath.Join(cp, "a")); err != nil {
		t.Fatal(err)
	}

	writeSnapshot = func(dir string, tr spec.Tree) error {
		if dir == afterDir {
			return errors.New("disk full")
		}
		return spec.WriteTree(dir, tr)
	}
	defer func() { writeSnapshot = spec.WriteTree }()
	_, err := specStart(ctx, "T-1", r.Slug, []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "run tix spec start T-1 acme-app again") {
		t.Fatalf("want the injected failure naming the rerun, got %v", err)
	}
	writeSnapshot = spec.WriteTree
	if got := read(filepath.Join(baseDir, "a", "spec.md")); got != "a-theirs" {
		t.Fatalf("base: %q", got)
	}
	if got := read(filepath.Join(afterDir, "a", "spec.md")); got != "a1" {
		t.Fatalf("after: %q", got)
	}
	if _, err := os.Stat(spec.RebaseMarker(v, "T-1", r.Slug)); err != nil {
		t.Fatalf("no marker: %v", err)
	}

	if _, _, err := specDiffRepo(ctx, "T-1", r); err == nil || !strings.Contains(err.Error(), "run tix spec start T-1 acme-app again") {
		t.Fatalf("diff with the marker: %v", err)
	}

	put(filepath.Join(cp, "c", "spec.md"), "c1")
	_, err = promoteRepo(ctx, "T-1", r, "")
	if err == nil || !errors.Is(err, spec.ErrRebasePending) || !strings.HasPrefix(err.Error(), spec.PendingRebase(v, "T-1", r.Slug).Error()) || !strings.HasSuffix(err.Error(), ", then promote") {
		t.Fatalf("promote with the marker: %v", err)
	}

	res, err := specStart(ctx, "T-1", r.Slug, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Rebased, ",") != "a/spec.md" || strings.Join(res.Moved, ",") != "a/spec.md" {
		t.Fatalf("rebased %v moved %v", res.Rebased, res.Moved)
	}
	if got := read(filepath.Join(afterDir, "a", "spec.md")); got != "a-theirs" {
		t.Fatalf("after not finished: %q", got)
	}
	if _, err := os.Stat(spec.RebaseMarker(v, "T-1", r.Slug)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker left: %v", err)
	}
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("promote after the rerun: %+v %v", pr, err)
	}
}

func TestPromoteRerunKeepsMovedFileInAfter(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	afterDir := spec.AfterDir(v, "T-1", r.Slug)
	for _, c := range []string{"a", "b", "c"} {
		put(filepath.Join(proj, c, "spec.md"), c+"0")
	}
	if err := spec.WriteTree(spec.BaseDir(v, "T-1", r.Slug), spec.Tree{"a/spec.md": []byte("a0"), "b/spec.md": []byte("b0"), "c/spec.md": []byte("c0")}); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(cp, "c", "spec.md"), "c1")
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("first promote: %+v %v", pr, err)
	}

	put(filepath.Join(cp, "a", "spec.md"), "a1")
	put(filepath.Join(cp, "b", "spec.md"), "b1")
	calls := 0
	writeSpecFile = func(p string, b []byte, m os.FileMode) error {
		if calls++; calls == 2 {
			return errors.New("disk full")
		}
		return fsx.WriteFileAtomic(p, b, m)
	}
	defer func() { writeSpecFile = fsx.WriteFileAtomic }()
	if _, err := promoteRepo(ctx, "T-1", r, ""); err == nil {
		t.Fatal("want the injected failure")
	}
	writeSpecFile = fsx.WriteFileAtomic
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("rerun: %+v %v", pr, err)
	}
	after, err := spec.ReadSnapshot(afterDir)
	if err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"a/spec.md": "a1", "b/spec.md": "b1", "c/spec.md": "c1"} {
		if got := string(after[f]); got != want {
			t.Errorf("after %s: %q, want %q", f, got, want)
		}
	}
}

func TestStartClearsOrphanMarker(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(v, "tickets", "manual", "T-1", "T-1.md"), "---\ntitle: T\n---\n")
	put(spec.ProjectNote(v, r.Slug), "---\ntitle: acme-app\nspecs: vault\n---\n")
	put(filepath.Join(spec.VaultDir(v, r.Slug), "a", "spec.md"), "a0")
	put(filepath.Join(spec.VaultDir(v, r.Slug), "b", "spec.md"), "b0")
	marker := spec.RebaseMarker(v, "T-1", r.Slug)
	put(marker, "a/spec.md\n")
	afterDir := spec.AfterDir(v, "T-1", r.Slug)
	if err := spec.WriteTree(afterDir, spec.Tree{"a/spec.md": []byte("a-mine"), "gone/spec.md": []byte("g")}); err != nil {
		t.Fatal(err)
	}

	res, err := specStart(ctx, "T-1", r.Slug, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Taken || !res.Orphan {
		t.Fatalf("taken %v orphan %v", res.Taken, res.Orphan)
	}
	var out strings.Builder
	printStart(&out, r.Slug, res)
	if !strings.Contains(out.String(), "warning: cleared a rebase marker") {
		t.Fatalf("no warning: %s", out.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker left: %v", err)
	}
	after, err := spec.ReadSnapshot(afterDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || string(after["a/spec.md"]) != "a0" {
		t.Fatalf("after not reset: %v", after)
	}
	if d, _, err := specDiffRepo(ctx, "T-1", r); err != nil || len(d.Files) != 0 {
		t.Fatalf("diff after the reset: %+v %v", d.Files, err)
	}
	put(filepath.Join(spec.CopyDir(v, "T-1", r.Slug), "a", "spec.md"), "a1")
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("promote: %+v %v", pr, err)
	}
}

func TestPromoteRerunIgnoresAnotherTicketsPromotion(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	afterDir := spec.AfterDir(v, "T-1", r.Slug)
	for _, c := range []string{"a", "b", "c"} {
		put(filepath.Join(proj, c, "spec.md"), c+"0")
	}
	if err := spec.WriteTree(spec.BaseDir(v, "T-1", r.Slug), spec.Tree{"a/spec.md": []byte("a0"), "b/spec.md": []byte("b0"), "c/spec.md": []byte("c0")}); err != nil {
		t.Fatal(err)
	}
	defer func() { promoteNow = time.Now; writeSpecFile = fsx.WriteFileAtomic }()
	promoteNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	other := func(file string) {
		t.Helper()
		o := spec.Version{Slug: r.Slug, Ticket: "[[T-2]]", Project: "[[acme-app]]", Promoted: "2026-01-02T00:00:00", Files: []string{file}}
		body, err := spec.RenderVersion(o, "tickets/manual/T-2", spec.Tree{}, spec.Diff{})
		if err != nil {
			t.Fatal(err)
		}
		put(spec.VersionPath(v, r.Slug, "T-2"), string(body))
	}

	put(filepath.Join(cp, "a", "spec.md"), "a1")
	put(filepath.Join(cp, "b", "spec.md"), "b1")
	calls := 0
	writeSpecFile = func(p string, b []byte, m os.FileMode) error {
		if calls++; calls == 2 {
			return errors.New("disk full")
		}
		return fsx.WriteFileAtomic(p, b, m)
	}
	if _, err := promoteRepo(ctx, "T-1", r, ""); err == nil {
		t.Fatal("want the injected failure")
	}
	writeSpecFile = fsx.WriteFileAtomic
	put(filepath.Join(proj, "a", "spec.md"), "a-theirs")
	other("a/spec.md")
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("rerun: %+v %v", pr, err)
	}
	after, err := spec.ReadSnapshot(afterDir)
	if err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"} {
		if got := string(after[f]); got != want {
			t.Errorf("after %s: %q, want %q", f, got, want)
		}
	}

	put(filepath.Join(proj, "c", "spec.md"), "c1")
	nv, err := spec.ReadVersion(spec.VersionPath(v, r.Slug, "T-1"))
	if err != nil {
		t.Fatal(err)
	}
	nv.Files = spec.MergeFiles(nv.Files, []string{"c/spec.md"})
	body, err := spec.RenderVersion(nv, "tickets/manual/T-1", spec.Tree{}, spec.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	put(spec.VersionPath(v, r.Slug, "T-1"), string(body))
	other("c/spec.md")
	put(filepath.Join(cp, "b", "spec.md"), "b2")
	_, err = promoteRepo(ctx, "T-1", r, "")
	if err == nil || !strings.Contains(err.Error(), "T-2 promoted it since") || !strings.Contains(err.Error(), "Run tix spec start T-1 acme-app c to copy it again") {
		t.Fatalf("want a refusal naming T-2: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(proj, "b", "spec.md")); string(got) != "b1" {
		t.Fatalf("the refusal moved b: %q", got)
	}

	put(filepath.Join(proj, "c", "spec.md"), "c-hand")
	_, err = promoteRepo(ctx, "T-1", r, "")
	if err == nil || !strings.Contains(err.Error(), "T-2 promoted it since") {
		t.Fatalf("want a refusal naming T-2 after a hand edit: %v", err)
	}
	put(filepath.Join(proj, "c", "spec.md"), "c1")

	if err := os.Remove(spec.VersionPath(v, r.Slug, "T-2")); err != nil {
		t.Fatal(err)
	}
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("promote without T-2: %+v %v", pr, err)
	}
	if after, _ = spec.ReadSnapshot(afterDir); string(after["c/spec.md"]) != "c1" {
		t.Fatalf("after c: %q", after["c/spec.md"])
	}
}

func TestPromoteTheirsKeptWriteFails(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	put(filepath.Join(proj, "a", "spec.md"), "a-theirs")
	put(filepath.Join(cp, "a", "spec.md"), "a-mine")
	baseDir := spec.BaseDir(v, "T-1", r.Slug)
	if err := spec.WriteTree(baseDir, spec.Tree{"a/spec.md": []byte("a0")}); err != nil {
		t.Fatal(err)
	}

	writeStaged = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	defer func() { writeStaged = fsx.WriteFileAtomic }()
	if _, err := promoteRepo(ctx, "T-1", r, keepTheirs); err == nil {
		t.Fatal("want the injected failure")
	}
	if b, err := os.ReadFile(filepath.Join(cp, "a", "spec.md")); err != nil || string(b) != "a-mine" {
		t.Fatalf("copy file gone after a failed kept write: %q %v", b, err)
	}

	writeStaged = fsx.WriteFileAtomic
	pr, err := promoteRepo(ctx, "T-1", r, keepTheirs)
	if err != nil || pr.Resolved == nil || pr.Resolved.Keep != keepTheirs {
		t.Fatalf("rerun: %+v %v", pr, err)
	}
	kept, err := spec.ReadKept(spec.KeptList(v, "T-1", r.Slug))
	if err != nil || strings.Join(kept, ",") != "a/spec.md" {
		t.Fatalf("kept list: %v %v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(cp, "a", "spec.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("copy file left: %v", err)
	}
	base, err := spec.ReadSnapshot(baseDir)
	if err != nil || string(base["a/spec.md"]) != "a-theirs" {
		t.Fatalf("base not moved onto theirs: %v %v", base, err)
	}
}

func TestStartResetsAfterWithoutMarker(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(v, "tickets", "manual", "T-1", "T-1.md"), "---\ntitle: T\n---\n")
	put(spec.ProjectNote(v, r.Slug), "---\ntitle: acme-app\nspecs: vault\n---\n")
	put(filepath.Join(spec.VaultDir(v, r.Slug), "a", "spec.md"), "a0")
	afterDir := spec.AfterDir(v, "T-1", r.Slug)
	if err := spec.WriteTree(afterDir, spec.Tree{"a/spec.md": []byte("a-mine"), "gone/spec.md": []byte("g")}); err != nil {
		t.Fatal(err)
	}

	res, err := specStart(ctx, "T-1", r.Slug, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Taken || res.Orphan || !res.Reset {
		t.Fatalf("taken %v orphan %v reset %v", res.Taken, res.Orphan, res.Reset)
	}
	var out strings.Builder
	printStart(&out, r.Slug, res)
	if !strings.Contains(out.String(), "warning: the snapshot was missing") {
		t.Fatalf("no warning: %q", out.String())
	}
	after, err := spec.ReadSnapshot(afterDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || string(after["a/spec.md"]) != "a0" {
		t.Fatalf("after not reset: %v", after)
	}
	if d, _, err := specDiffRepo(ctx, "T-1", r); err != nil || len(d.Files) != 0 {
		t.Fatalf("diff after the reset: %+v %v", d.Files, err)
	}
}

func promoteFixture(t *testing.T) (vault.Context, workspace.Repo) {
	t.Helper()
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	put(filepath.Join(note.TicketDir(v, "T-1"), "T-1.md"), "# T-1\n")
	put(filepath.Join(proj, "a", "spec.md"), "a-theirs")
	put(filepath.Join(proj, "b", "spec.md"), "b0")
	if err := spec.WriteTree(spec.BaseDir(v, "T-1", r.Slug), spec.Tree{"a/spec.md": []byte("a0"), "b/spec.md": []byte("b0")}); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(cp, "a", "spec.md"), "a-mine")
	nRel := vaultRel(v, filepath.Join(cp, "n", "spec"))
	put(filepath.Join(cp, "b", "spec.md"), "b1 [["+nRel+"]]\n")
	put(filepath.Join(cp, "n", "spec.md"), "n1\n")
	put(filepath.Join(v, "notes", "x.md"), "see [["+nRel+"|n]]\n")
	return ctx, r
}

func vaultState(t *testing.T, v string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(v, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[vaultRel(v, p)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type promoteSeam struct {
	name string
	set  func(fail func() error) (restore func())
}

func promoteSeams() []promoteSeam {
	return []promoteSeam{
		{"journal", func(fail func() error) func() {
			writeJournal = func(p string, b []byte, m os.FileMode) error {
				if err := fail(); err != nil {
					return err
				}
				return fsx.WriteFileAtomic(p, b, m)
			}
			return func() { writeJournal = fsx.WriteFileAtomic }
		}},
		{"replace-tree", func(fail func() error) func() {
			renameTree = func(a, b string) error {
				if err := fail(); err != nil {
					return err
				}
				return os.Rename(a, b)
			}
			return func() { renameTree = os.Rename }
		}},
		{"write", func(fail func() error) func() {
			writeStaged = func(p string, b []byte, m os.FileMode) error {
				if err := fail(); err != nil {
					return err
				}
				return fsx.WriteFileAtomic(p, b, m)
			}
			return func() { writeStaged = fsx.WriteFileAtomic }
		}},
		{"put", func(fail func() error) func() {
			writeSpecFile = func(p string, b []byte, m os.FileMode) error {
				if err := fail(); err != nil {
					return err
				}
				return fsx.WriteFileAtomic(p, b, m)
			}
			return func() { writeSpecFile = fsx.WriteFileAtomic }
		}},
		{"trash", func(fail func() error) func() {
			trashFile = func(a, b string) error {
				if err := fail(); err != nil {
					return err
				}
				return os.Rename(a, b)
			}
			return func() { trashFile = os.Rename }
		}},
		{"drop-links", func(fail func() error) func() {
			redirectLinks = func(v string, rs []links.Redirect) (links.MoveResult, error) {
				if err := fail(); err != nil {
					return links.MoveResult{}, err
				}
				return links.RedirectLinks(v, rs)
			}
			return func() { redirectLinks = links.RedirectLinks }
		}},
		{"drop-remove", func(fail func() error) func() {
			removeFile = func(p string) error {
				if err := fail(); err != nil {
					return err
				}
				return os.Remove(p)
			}
			return func() { removeFile = os.Remove }
		}},
		{"done", func(fail func() error) func() {
			appendDone = func(p string, b []byte) error {
				if err := fail(); err != nil {
					return err
				}
				return appendFile(p, b)
			}
			return func() { appendDone = appendFile }
		}},
		{"restore", func(fail func() error) func() {
			restoreFile = func(p string, b []byte, remove bool) error {
				if err := fail(); err != nil {
					return err
				}
				return restoreWrite(p, b, remove)
			}
			return func() { restoreFile = restoreWrite }
		}},
	}
}

func seamTable(t *testing.T, setup func(t *testing.T) (vaultDir string, run func() (promoteResult, error)), check func(want promoteResult, clean map[string]string, calls map[string]int)) {
	t.Helper()
	seams := promoteSeams()
	v, run := setup(t)
	calls := map[string]int{}
	for _, s := range seams {
		name := s.name
		defer s.set(func() error { calls[name]++; return nil })()
	}
	want, err := run()
	if err != nil {
		t.Fatalf("clean run: %v", err)
	}
	for _, s := range seams {
		s.set(func() error { return nil })()
	}
	clean := vaultState(t, v)
	check(want, clean, calls)
	for _, s := range seams {
		for k := 1; k <= calls[s.name]; k++ {
			t.Run(fmt.Sprintf("%s-%d", s.name, k), func(t *testing.T) {
				v, run := setup(t)
				n := 0
				restore := s.set(func() error {
					if n++; n == k {
						return errors.New("disk full")
					}
					return nil
				})
				_, err := run()
				restore()
				if err == nil {
					t.Fatal("want the injected failure")
				}
				got, err := run()
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("rerun: %+v %v, want %+v", got, err, want)
				}
				state := vaultState(t, v)
				for f, c := range clean {
					if state[f] != c {
						t.Errorf("%s: %q, want %q", f, state[f], c)
					}
				}
				for f := range state {
					if _, ok := clean[f]; !ok {
						t.Errorf("%s left", f)
					}
				}
			})
		}
	}
}

func TestPromoteJournalFailureSeams(t *testing.T) {
	defer func() { promoteNow = time.Now }()
	promoteNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	setup := func(t *testing.T) (string, func() (promoteResult, error)) {
		ctx, r := promoteFixture(t)
		return ctx.Vault, func() (promoteResult, error) { return promoteRepo(ctx, "T-1", r, keepTheirs) }
	}
	seamTable(t, setup, func(want promoteResult, clean map[string]string, calls map[string]int) {
		if want.Status != promoteDone || want.Resolved == nil {
			t.Fatalf("clean run: %+v", want)
		}
		for f, c := range map[string]string{
			"projects/acme-app/specs/b/spec.md":         "b1 [[projects/acme-app/specs/n/spec]]\n",
			"projects/acme-app/specs/n/spec.md":         "n1\n",
			"notes/x.md":                                "see [[projects/acme-app/specs/n/spec|n]]\n",
			".trash/T-1-spec-theirs/acme-app/a/spec.md": "a-mine",
			"projects/acme-app/specs/a/spec.md":         "a-theirs",
		} {
			if clean[f] != c {
				t.Errorf("clean run %s: %q, want %q", f, clean[f], c)
			}
		}
		for f := range clean {
			if strings.Contains(f, "specs-stage") || strings.Contains(f, ".old-") || strings.HasPrefix(f, "tickets/manual/T-1/specs/") {
				t.Errorf("clean run left %s", f)
			}
		}
		for _, s := range promoteSeams() {
			if calls[s.name] == 0 && s.name != "restore" {
				t.Fatalf("the fixture never calls %s", s.name)
			}
		}
		if calls["drop-links"] != 1 {
			t.Fatalf("RedirectLinks called %d times, want 1", calls["drop-links"])
		}
	})
}

func TestPromoteKeptBaseIsTheirs(t *testing.T) {
	v := t.TempDir()
	ctx := vault.Context{Vault: v}
	r := workspace.Repo{Slug: "acme-app", Branch: "feature/T-1"}
	put := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj, cp := spec.VaultDir(v, r.Slug), spec.CopyDir(v, "T-1", r.Slug)
	baseDir := spec.BaseDir(v, "T-1", r.Slug)
	put(filepath.Join(proj, "a", "spec.md"), "a-theirs")
	put(filepath.Join(proj, "b", "spec.md"), "b0")
	if err := spec.WriteTree(baseDir, spec.Tree{"a/spec.md": []byte("a0"), "b/spec.md": []byte("b0")}); err != nil {
		t.Fatal(err)
	}
	if err := spec.WriteKept(spec.KeptList(v, "T-1", r.Slug), []string{"a/spec.md"}); err != nil {
		t.Fatal(err)
	}

	put(filepath.Join(cp, "b", "spec.md"), "b1")
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("promote b: %+v %v", pr, err)
	}
	if base, _ := spec.ReadSnapshot(baseDir); string(base["a/spec.md"]) != "a-theirs" {
		t.Fatalf("base a: %q", base["a/spec.md"])
	}

	put(filepath.Join(cp, "a", "spec.md"), "a-edit")
	pr, err := promoteRepo(ctx, "T-1", r, "")
	if err != nil || pr.Status != promoteDone {
		t.Fatalf("edit of a: %+v %v", pr, err)
	}
	ver, err := spec.ReadVersion(spec.VersionPath(v, r.Slug, "T-1"))
	if err != nil || !ver.HasFile("a/spec.md") {
		t.Fatalf("note: %+v %v", ver, err)
	}
	before, _, err := spec.VersionSides(v, r.Slug, ver)
	if err != nil || string(before["a/spec.md"]) != "a-theirs" {
		t.Fatalf("before a: %q %v", before["a/spec.md"], err)
	}

	put(filepath.Join(cp, "a", "spec.md"), "a-edit2")
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("second edit of a: %+v %v", pr, err)
	}
	if base, _ := spec.ReadSnapshot(baseDir); string(base["a/spec.md"]) != "a-theirs" {
		t.Fatalf("base a after the second promote: %q", base["a/spec.md"])
	}
}

func TestPromotePendingReaders(t *testing.T) {
	ctx, r := promoteFixture(t)
	writeSpecFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	defer func() { writeSpecFile = fsx.WriteFileAtomic }()
	if _, err := promoteRepo(ctx, "T-1", r, keepTheirs); err == nil {
		t.Fatal("want the injected failure")
	}
	writeSpecFile = fsx.WriteFileAtomic
	err := spec.PendingPromote(ctx.Vault, "T-1", r.Slug)
	if !errors.Is(err, spec.ErrPromotePending) || spec.PendingNote(err, "T-1", r.Slug) != "promote pending: run tix spec promote T-1" {
		t.Fatalf("pending: %v", err)
	}
	if _, _, err := vaultSpecSides(ctx, "T-1", r.Slug, spec.VaultDir(ctx.Vault, r.Slug)); err == nil || !strings.Contains(err.Error(), "promote pending: run tix spec promote T-1") {
		t.Fatalf("diff: %v", err)
	}
	if _, _, err := spec.VersionSides(ctx.Vault, r.Slug, spec.Version{Ticket: "[[T-1]]"}); !errors.Is(err, spec.ErrPromotePending) {
		t.Fatalf("show: %v", err)
	}
	if _, _, err := spec.VersionSides(ctx.Vault, r.Slug, spec.Version{Ticket: "[[T-2]]"}); err != nil {
		t.Fatalf("another ticket: %v", err)
	}
	if pr, err := promoteRepo(ctx, "T-1", r, ""); err != nil || pr.Status != promoteDone {
		t.Fatalf("rerun: %+v %v", pr, err)
	}
	if err := spec.PendingPromote(ctx.Vault, "T-1", r.Slug); err != nil {
		t.Fatalf("still pending: %v", err)
	}
}

type heldEnv struct {
	t   *testing.T
	ctx vault.Context
	v   string
	r   workspace.Repo
}

func newHeldEnv(t *testing.T) *heldEnv {
	t.Helper()
	v := t.TempDir()
	e := &heldEnv{t: t, ctx: vault.Context{Vault: v}, v: v, r: workspace.Repo{Slug: "acme-app"}}
	t.Cleanup(func() { promoteNow = time.Now })
	if err := os.MkdirAll(filepath.Join(v, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.put(spec.ProjectNote(v, e.r.Slug), "---\ntitle: acme-app\nspecs: vault\n---\n")
	for _, id := range []string{"T-1", "T-2", "T-3"} {
		e.put(filepath.Join(note.TicketDir(v, id), id+".md"), "# "+id+"\n")
	}
	e.put(e.proj("a/spec.md"), "a0")
	e.put(e.proj("b/spec.md"), "b0")
	return e
}

func (e *heldEnv) put(p, s string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *heldEnv) read(p string) string {
	e.t.Helper()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "<none>"
	} else if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *heldEnv) proj(f string) string {
	return filepath.Join(spec.VaultDir(e.v, e.r.Slug), filepath.FromSlash(f))
}

func (e *heldEnv) copyFile(id, f string) string {
	return filepath.Join(spec.CopyDir(e.v, id, e.r.Slug), filepath.FromSlash(f))
}

func (e *heldEnv) snap(dir func(v, id, slug string) string, id, f string) string {
	return e.read(filepath.Join(dir(e.v, id, e.r.Slug), filepath.FromSlash(f)))
}

func (e *heldEnv) begin(id string, files map[string]string) {
	e.t.Helper()
	tr, err := spec.ReadTree(spec.VaultDir(e.v, e.r.Slug))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := spec.WriteTree(spec.BaseDir(e.v, id, e.r.Slug), tr); err != nil {
		e.t.Fatal(err)
	}
	for f, c := range files {
		e.put(e.copyFile(id, f), c)
	}
}

func (e *heldEnv) repo(id string) workspace.Repo {
	r := e.r
	r.Branch = "feature/" + id
	return r
}

func (e *heldEnv) clock(id string) {
	day := int(id[len(id)-1] - '0')
	promoteNow = func() time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }
}

func (e *heldEnv) promote(id, keep string) (promoteResult, error) {
	e.clock(id)
	return promoteRepo(e.ctx, id, e.repo(id), keep)
}

func (e *heldEnv) mustPromote(id, keep, status string) promoteResult {
	e.t.Helper()
	pr, err := e.promote(id, keep)
	if err != nil || pr.Status != status {
		e.t.Fatalf("promote %s %s: %+v %v, want %s", id, keep, pr, err, status)
	}
	return pr
}

func (e *heldEnv) crash(id string, fail func() (restore func())) {
	e.t.Helper()
	restore := fail()
	_, err := e.promote(id, "")
	restore()
	if err == nil {
		e.t.Fatal("want the injected failure")
	}
}

func failPut(file string) func() func() {
	return func() func() {
		failed := false
		writeSpecFile = func(p string, b []byte, m os.FileMode) error {
			if !failed && strings.HasSuffix(filepath.ToSlash(p), "/"+file) {
				failed = true
				return errors.New("disk full")
			}
			return fsx.WriteFileAtomic(p, b, m)
		}
		return func() { writeSpecFile = fsx.WriteFileAtomic }
	}
}

func (e *heldEnv) failMark(id, kind, arg string) func() func() {
	return func() func() {
		appendDone = func(p string, b []byte) error {
			ops, _, err := spec.ReadJournal(e.v, id, e.r.Slug)
			if err != nil {
				return err
			}
			for i, o := range ops {
				if o.Kind == kind && o.Arg == arg {
					if string(b) == fmt.Sprintf("%d\n", i+1) {
						return errors.New("disk full")
					}
					break
				}
			}
			return appendFile(p, b)
		}
		return func() { appendDone = appendFile }
	}
}

func TestPromoteReplayHoldsBackOvertakenFile(t *testing.T) {
	for _, keep := range []string{keepTheirs, keepOurs, keepMerged} {
		t.Run(keep, func(t *testing.T) {
			e := newHeldEnv(t)
			e.begin("T-2", nil)
			e.begin("T-1", map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"})
			cpRel := func(f string) string { return vaultRel(e.v, strings.TrimSuffix(e.copyFile("T-1", f), ".md")) }
			e.put(filepath.Join(e.v, "notes", "x.md"), "[["+cpRel("a/spec.md")+"]] [["+cpRel("b/spec.md")+"]]\n")
			e.crash("T-1", failPut("b/spec.md"))
			if e.read(e.proj("a/spec.md")) != "a1" || e.read(e.proj("b/spec.md")) != "b0" {
				t.Fatal("the stop is not after a's put")
			}
			e.put(e.copyFile("T-2", "b/spec.md"), "b-T2")
			e.mustPromote("T-2", "", promoteDone)

			pr, err := e.promote("T-1", "")
			if err != nil || pr.Status != promoteRefused || !reflect.DeepEqual(pr.Conflict.Files, []string{"b/spec.md"}) {
				t.Fatalf("rerun: %+v %v", pr, err)
			}
			if len(pr.Conflict.Others) != 1 || pr.Conflict.Others[0].Ticket != "T-2" {
				t.Fatalf("others: %+v", pr.Conflict.Others)
			}
			rp := pr.Replayed
			if rp == nil || !reflect.DeepEqual(rp.Files, []string{"a/spec.md"}) || len(rp.Held) != 1 || rp.Held[0].File != "b/spec.md" || rp.Held[0].Ticket != "T-2" ||
				rp.Held[0].Reason != "projects/ holds T-2's recorded after copy (projects/acme-app/spec-versions/acme-app-spec-T-2.md)" {
				t.Fatalf("replayed: %+v", rp)
			}
			var out strings.Builder
			printPromote(&out, "T-1", pr)
			for _, want := range []string{
				"acme-app: finished the pending promote first:\nacme-app: held back b/spec.md: projects/ holds T-2's recorded after copy",
				"acme-app: promoted 1 files (a); version ",
				"acme-app: refused: another ticket changed b/spec.md",
				"  changed by: T-2 ",
			} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out.String())
				}
			}
			if e.read(e.proj("b/spec.md")) != "b-T2" || e.read(e.copyFile("T-1", "b/spec.md")) != "b1" {
				t.Fatal("b was overwritten or dropped")
			}
			ver, err := spec.ReadVersion(spec.VersionPath(e.v, e.r.Slug, "T-1"))
			if err != nil || !reflect.DeepEqual(ver.Files, []string{"a/spec.md"}) {
				t.Fatalf("note: %+v %v", ver, err)
			}
			if e.snap(spec.AfterDir, "T-1", "a/spec.md") != "a1" || e.snap(spec.AfterDir, "T-1", "b/spec.md") != "<none>" || e.snap(spec.BaseDir, "T-1", "b/spec.md") != "b0" {
				t.Fatal("snapshots not at b's plan-time state")
			}
			if got := e.read(filepath.Join(e.v, "notes", "x.md")); got != "[[projects/acme-app/specs/a/spec]] [["+cpRel("b/spec.md")+"]]\n" {
				t.Fatalf("links: %q", got)
			}
			if _, err := os.Lstat(spec.StageRoot(e.v, "T-1")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stage left: %v", err)
			}

			switch keep {
			case keepTheirs:
				e.mustPromote("T-1", keep, promoteNothing)
				if e.read(e.proj("b/spec.md")) != "b-T2" || e.read(filepath.Join(promoteTrash(e.v, "T-1", e.r.Slug), "b", "spec.md")) != "b1" {
					t.Fatal("theirs")
				}
			case keepOurs:
				e.mustPromote("T-1", keep, promoteDone)
				if e.read(e.proj("b/spec.md")) != "b1" {
					t.Fatal("ours")
				}
			case keepMerged:
				e.put(e.copyFile("T-1", "b/spec.md"), "b-merged")
				e.mustPromote("T-1", keep, promoteDone)
				if e.read(e.proj("b/spec.md")) != "b-merged" || e.snap(spec.BaseDir, "T-1", "b/spec.md") != "b-T2" {
					t.Fatal("merged")
				}
			}
		})
	}
}

func TestPromoteReplayPutDoneUnmarked(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"})
	e.crash("T-1", e.failMark("T-1", spec.OpPut, "b/spec.md"))
	puts := 0
	writeSpecFile = func(p string, b []byte, m os.FileMode) error { puts++; return fsx.WriteFileAtomic(p, b, m) }
	defer func() { writeSpecFile = fsx.WriteFileAtomic }()
	pr := e.mustPromote("T-1", "", promoteDone)
	if puts != 0 || len(pr.Held) != 0 || e.read(e.proj("b/spec.md")) != "b1" {
		t.Fatalf("rerun: %d puts, %+v", puts, pr)
	}
}

func TestPromoteReplayMarkedPutOvertaken(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-2", nil)
	e.begin("T-1", map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"})
	e.crash("T-1", func() func() {
		removeFile = func(p string) error {
			if strings.HasSuffix(filepath.ToSlash(p), "/b/spec.md") {
				return errors.New("disk full")
			}
			return os.Remove(p)
		}
		return func() { removeFile = os.Remove }
	})
	e.put(e.copyFile("T-2", "b/spec.md"), "b-T2")
	e.mustPromote("T-2", "", promoteRefused)
	e.mustPromote("T-2", keepOurs, promoteDone)
	pr := e.mustPromote("T-1", "", promoteDone)
	if len(pr.Held) != 0 || e.read(e.proj("b/spec.md")) != "b-T2" || e.read(e.copyFile("T-1", "b/spec.md")) != "<none>" {
		t.Fatalf("rerun: %+v", pr)
	}
}

func TestPromoteCommandReplaysFirst(t *testing.T) {
	defer func() { promoteNow = time.Now }()
	promoteNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ctx, r := promoteFixture(t)
	n := 0
	renameTree = func(a, b string) error {
		if n++; n == 2 {
			return errors.New("disk full")
		}
		return os.Rename(a, b)
	}
	_, err := promoteRepo(ctx, "T-1", r, keepTheirs)
	renameTree = os.Rename
	if err == nil {
		t.Fatal("want the injected failure")
	}
	if err := needsStart(ctx, "T-1", r.Slug); err == nil {
		t.Fatal("the stop left a base snapshot: the test checks nothing")
	}
	res, err := runPromote(ctx, "T-1", []workspace.Repo{r}, map[string]string{r.Slug: keepTheirs})
	if err != nil || len(res) != 1 || res[0].Status != promoteDone {
		t.Fatalf("tix spec promote: %+v %v", res, err)
	}
}

func TestPromoteReplayKeepsEditedCopyFile(t *testing.T) {
	defer func() { promoteNow = time.Now }()
	promoteNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	t.Run("trash", func(t *testing.T) {
		ctx, r := promoteFixture(t)
		trashFile = func(string, string) error { return errors.New("disk full") }
		_, err := promoteRepo(ctx, "T-1", r, keepTheirs)
		trashFile = os.Rename
		if err == nil {
			t.Fatal("want the injected failure")
		}
		a := filepath.Join(spec.CopyDir(ctx.Vault, "T-1", r.Slug), "a", "spec.md")
		if err := os.WriteFile(a, []byte("a-edited"), 0o644); err != nil {
			t.Fatal(err)
		}
		pr, err := promoteRepo(ctx, "T-1", r, "")
		if err != nil || pr.Replayed == nil || len(pr.Replayed.Warnings) != 1 || !strings.HasPrefix(pr.Replayed.Warnings[0], "a/spec.md changed in the copy") {
			t.Fatalf("rerun: %+v %v", pr, err)
		}
		if b, _ := os.ReadFile(filepath.Join(spec.VaultDir(ctx.Vault, r.Slug), "a", "spec.md")); pr.Status != promoteDone || string(b) != "a-edited" {
			t.Fatalf("projects a: %q, %+v", b, pr)
		}
		if _, err := os.Lstat(filepath.Join(promoteTrash(ctx.Vault, "T-1", r.Slug), "a", "spec.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("the edited file went to the trash")
		}
		var out strings.Builder
		printPromote(&out, "T-1", pr)
		if !strings.Contains(out.String(), "acme-app: warning: a/spec.md changed in the copy") {
			t.Fatalf("output: %s", out.String())
		}
	})
	t.Run("drop", func(t *testing.T) {
		ctx, r := promoteFixture(t)
		removeFile = func(p string) error {
			if strings.HasSuffix(filepath.ToSlash(p), "/n/spec.md") {
				return errors.New("disk full")
			}
			return os.Remove(p)
		}
		_, err := promoteRepo(ctx, "T-1", r, keepTheirs)
		removeFile = os.Remove
		if err == nil {
			t.Fatal("want the injected failure")
		}
		n := filepath.Join(spec.CopyDir(ctx.Vault, "T-1", r.Slug), "n", "spec.md")
		if err := os.WriteFile(n, []byte("n2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pr, err := promoteRepo(ctx, "T-1", r, "")
		if err != nil || pr.Status != promoteDone || pr.Replayed == nil || len(pr.Replayed.Warnings) != 1 || !strings.HasPrefix(pr.Replayed.Warnings[0], "n/spec.md changed in the copy") {
			t.Fatalf("rerun: %+v %v", pr, err)
		}
		if b, _ := os.ReadFile(filepath.Join(spec.VaultDir(ctx.Vault, r.Slug), "n", "spec.md")); string(b) != "n2\n" {
			t.Fatalf("projects n: %q", b)
		}
	})
}

func TestPromoteDamagedJournal(t *testing.T) {
	stage := func(e *heldEnv, parts ...string) string {
		return filepath.Join(append([]string{spec.StageDir(e.v, "T-1", e.r.Slug)}, parts...)...)
	}
	for _, c := range []struct {
		name   string
		damage func(e *heldEnv)
	}{
		{"no header", func(e *heldEnv) {
			p := spec.JournalPath(e.v, "T-1", e.r.Slug)
			_, rest, _ := strings.Cut(e.read(p), "\n")
			e.put(p, rest)
		}},
		{"bad hash", func(e *heldEnv) {
			p := spec.JournalPath(e.v, "T-1", e.r.Slug)
			e.put(p, strings.Replace(e.read(p), "sha256:", "sha256:0", 1))
		}},
		{"done x", func(e *heldEnv) { e.put(spec.DonePath(e.v, "T-1", e.r.Slug), "1\nx\n") }},
		{"done 0", func(e *heldEnv) { e.put(spec.DonePath(e.v, "T-1", e.r.Slug), "0\n") }},
		{"no moves", func(e *heldEnv) {
			if err := os.Remove(stage(e, "moves", "b", "spec.md")); err != nil {
				e.t.Fatal(err)
			}
		}},
		{"no prev entry", func(e *heldEnv) {
			if err := os.Remove(stage(e, "prev", "base", "b", "spec.md")); err != nil {
				e.t.Fatal(err)
			}
		}},
		{"prev entry twice", func(e *heldEnv) {
			p := stage(e, "prev", spec.PrevAbsent)
			e.put(p, e.read(p)+"base b/spec.md\n")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newHeldEnv(t)
			e.begin("T-1", map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"})
			e.crash("T-1", failPut("b/spec.md"))
			c.damage(e)
			before := vaultState(t, e.v)
			_, err := e.promote("T-1", "")
			if !errors.Is(err, spec.ErrJournalDamaged) || !strings.Contains(err.Error(), "discards the pending promote") || !strings.Contains(err.Error(), "acme-app.journal.done") {
				t.Fatalf("want a damaged journal: %v", err)
			}
			if after := vaultState(t, e.v); !reflect.DeepEqual(after, before) {
				t.Fatal("a damaged journal wrote something")
			}
		})
	}
	t.Run("torn mark", func(t *testing.T) {
		e := newHeldEnv(t)
		e.begin("T-1", map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"})
		e.crash("T-1", failPut("b/spec.md"))
		e.put(spec.DonePath(e.v, "T-1", e.r.Slug), "1")
		e.mustPromote("T-1", "", promoteDone)
		if e.read(e.proj("b/spec.md")) != "b1" || e.read(spec.DonePath(e.v, "T-1", e.r.Slug)) != "<none>" {
			t.Fatal("torn mark rerun")
		}
	})
}

func TestPromoteRefusesControlCharacterName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names can't hold a newline")
	}
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"x\ny/spec.md": "x1"})
	before := vaultState(t, e.v)
	if _, err := e.promote("T-1", ""); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("want a refusal: %v", err)
	}
	if !reflect.DeepEqual(vaultState(t, e.v), before) {
		t.Fatal("the refusal wrote something")
	}
}

func TestPromoteHoldBackFailureSeams(t *testing.T) {
	setup := func(t *testing.T) (string, func() (promoteResult, error)) {
		e := newHeldEnv(t)
		e.begin("T-2", nil)
		e.begin("T-1", map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"})
		e.crash("T-1", failPut("b/spec.md"))
		e.put(e.copyFile("T-2", "b/spec.md"), "b-T2")
		e.mustPromote("T-2", "", promoteDone)
		return e.v, func() (promoteResult, error) { return e.promote("T-1", "") }
	}
	seamTable(t, setup, func(want promoteResult, clean map[string]string, calls map[string]int) {
		if want.Status != promoteRefused || want.Replayed == nil || len(want.Replayed.Held) != 1 {
			t.Fatalf("clean run: %+v", want)
		}
		for _, s := range []string{"restore", "done", "drop-links", "drop-remove"} {
			if calls[s] == 0 {
				t.Fatalf("the fixture never calls %s", s)
			}
		}
		if clean["projects/acme-app/specs/b/spec.md"] != "b-T2" {
			t.Fatal("b overwritten")
		}
	})
}

func TestPromoteReplayTwoTicketsAndHandEdit(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.begin("T-2", map[string]string{"b/spec.md": "b-T2"})
	e.crash("T-1", failPut("b/spec.md"))
	e.crash("T-2", failPut("b/spec.md"))
	e.mustPromote("T-1", "", promoteDone)
	pr := e.mustPromote("T-2", "", promoteRefused)
	if pr.Replayed == nil || len(pr.Replayed.Held) != 1 || pr.Replayed.Held[0].Ticket != "T-1" || e.read(e.proj("b/spec.md")) != "b1" {
		t.Fatalf("second: %+v", pr.Replayed)
	}

	h := newHeldEnv(t)
	h.begin("T-1", map[string]string{"b/spec.md": "b1"})
	h.crash("T-1", failPut("b/spec.md"))
	h.put(h.proj("b/spec.md"), "b-hand")
	pr = h.mustPromote("T-1", "", promoteRefused)
	if held := pr.Replayed.Held; len(held) != 1 || held[0].Ticket != "" || held[0].Reason != "projects/ changed after this promote planned; no version note holds it" {
		t.Fatalf("hand edit: %+v", held)
	}
	if h.read(h.proj("b/spec.md")) != "b-hand" {
		t.Fatal("the hand edit was overwritten")
	}
}

func TestPromoteReplayStopsOnDamagedJournal(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.crash("T-1", failPut("b/spec.md"))
	e.put(e.copyFile("T-1", "c/spec.md"), "c1")
	other := workspace.Repo{Slug: "acme-lib"}
	e.put(spec.JournalPath(e.v, "T-1", other.Slug), "garbage\n")
	e.clock("T-1")
	_, err := runPromote(e.ctx, "T-1", []workspace.Repo{e.repo("T-1"), other}, nil)
	if !errors.Is(err, spec.ErrJournalDamaged) {
		t.Fatalf("want the damaged journal: %v", err)
	}
	if e.read(e.proj("b/spec.md")) != "b1" || e.read(spec.JournalPath(e.v, "T-1", e.r.Slug)) != "<none>" {
		t.Fatal("the first repo was not replayed")
	}
	if e.read(e.proj("c/spec.md")) != "<none>" || e.read(e.copyFile("T-1", "c/spec.md")) != "c1" {
		t.Fatal("the first repo was planned")
	}
}

func TestPromoteReplayFalseRefusal(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.begin("T-2", map[string]string{"b/spec.md": "b-T2"})
	e.crash("T-1", e.failMark("T-1", spec.OpPut, "b/spec.md"))
	e.mustPromote("T-2", "", promoteRefused)
	e.put(e.copyFile("T-2", "b/spec.md"), "b-merged")
	e.mustPromote("T-2", keepMerged, promoteDone)
	pr := e.mustPromote("T-1", "", promoteRefused)
	if pr.Replayed == nil || len(pr.Replayed.Held) != 1 {
		t.Fatalf("rerun: %+v", pr)
	}
	e.mustPromote("T-1", keepTheirs, promoteNothing)
	if e.read(e.proj("b/spec.md")) != "b-merged" {
		t.Fatal("theirs")
	}
}

func TestPromoteHoldBackNote(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.mustPromote("T-1", "", promoteDone)
	e.put(e.copyFile("T-1", "b/spec.md"), "b2")
	e.begin("T-2", map[string]string{"b/spec.md": "b-T2"})
	e.crash("T-1", failPut("b/spec.md"))
	e.mustPromote("T-2", "", promoteDone)
	e.mustPromote("T-1", "", promoteRefused)
	notePath := spec.VersionPath(e.v, e.r.Slug, "T-1")
	ver, err := spec.ReadVersion(notePath)
	if err != nil || !ver.HasFile("b/spec.md") || e.snap(spec.AfterDir, "T-1", "b/spec.md") != "b1" || strings.Contains(e.read(notePath), "b2") {
		t.Fatalf("note: %+v %v; after b %q", ver, err, e.snap(spec.AfterDir, "T-1", "b/spec.md"))
	}

	f := newHeldEnv(t)
	f.begin("T-1", map[string]string{"b/spec.md": "b1"})
	f.begin("T-2", map[string]string{"b/spec.md": "b-T2"})
	f.crash("T-1", failPut("b/spec.md"))
	if f.read(spec.VersionPath(f.v, f.r.Slug, "T-1")) == "<none>" {
		t.Fatal("the note is written after the puts: the test checks nothing")
	}
	f.mustPromote("T-2", "", promoteDone)
	pr := f.mustPromote("T-1", "", promoteRefused)
	if f.read(spec.VersionPath(f.v, f.r.Slug, "T-1")) != "<none>" || pr.Replayed.Version != "" {
		t.Fatalf("a note left for no file: %+v", pr.Replayed)
	}
}

func TestPromoteHoldBackAfterRenamedIn(t *testing.T) {
	for _, withBase := range []bool{false, true} {
		t.Run(fmt.Sprint("base-", withBase), func(t *testing.T) {
			e := newHeldEnv(t)
			files := map[string]string{"a/spec.md": "a1", "b/spec.md": "b1"}
			if withBase {
				e.put(e.proj("c/spec.md"), "c0")
			}
			e.begin("T-2", nil)
			e.begin("T-1", files)
			if withBase {
				e.put(e.proj("c/spec.md"), "c-theirs")
				e.put(e.copyFile("T-1", "c/spec.md"), "c-theirs")
			}
			e.crash("T-1", e.failMark("T-1", spec.OpReplaceTree, spec.TreeAfter))
			ops, _, err := spec.ReadJournal(e.v, "T-1", e.r.Slug)
			if err != nil || (ops[0].Kind == spec.OpReplaceTree && ops[0].Arg == spec.TreeBase) != withBase {
				t.Fatalf("journal: %+v %v", ops, err)
			}
			e.put(e.copyFile("T-2", "b/spec.md"), "b-T2")
			e.mustPromote("T-2", "", promoteDone)
			e.mustPromote("T-1", "", promoteRefused)
			if e.snap(spec.AfterDir, "T-1", "a/spec.md") != "a1" || e.snap(spec.AfterDir, "T-1", "b/spec.md") != "<none>" || e.snap(spec.BaseDir, "T-1", "b/spec.md") != "b0" {
				t.Fatal("after/base")
			}
			if withBase && (e.snap(spec.AfterDir, "T-1", "c/spec.md") != "c-theirs" || e.snap(spec.BaseDir, "T-1", "c/spec.md") != "c-theirs") {
				t.Fatal("c")
			}
		})
	}
}

func TestPromoteHeldReasonIgnoresOlderNotes(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-0", map[string]string{"b/spec.md": "b-T0"})
	e.mustPromote("T-0", "", promoteDone)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.crash("T-1", failPut("b/spec.md"))
	e.put(e.proj("b/spec.md"), "b-hand")
	pr := e.mustPromote("T-1", "", promoteRefused)
	if held := pr.Replayed.Held; len(held) != 1 || held[0].Ticket != "" || held[0].Note != "" || held[0].Reason != "projects/ changed after this promote planned; no version note holds it" {
		t.Fatalf("held: %+v", held)
	}
}

func TestPromoteReplayDropLinkingToHeldFile(t *testing.T) {
	for name, form := range map[string]func(string) string{
		"plain": func(s string) string { return s },
		"md":    func(s string) string { return s + ".md" },
		"upper": strings.ToUpper,
	} {
		t.Run(name, func(t *testing.T) {
			e := newHeldEnv(t)
			e.begin("T-2", nil)
			e.begin("T-1", map[string]string{"a/spec.md": "", "b/spec.md": "b1", "c/spec.md": "c1"})
			cpRel := func(f string) string { return vaultRel(e.v, strings.TrimSuffix(e.copyFile("T-1", f), ".md")) }
			e.put(e.copyFile("T-1", "a/spec.md"), "[["+form(cpRel("b/spec.md"))+"]] [["+cpRel("c/spec.md")+"]]\n")
			e.put(filepath.Join(e.v, "notes", "x.md"), "[["+cpRel("b/spec.md")+"]]\n")
			e.crash("T-1", failPut("b/spec.md"))
			e.put(e.copyFile("T-2", "b/spec.md"), "b-T2")
			e.mustPromote("T-2", "", promoteDone)
			pr := e.mustPromote("T-1", "", promoteRefused)
			if rp := pr.Replayed; rp == nil || len(rp.Held) != 1 || len(rp.Warnings) != 0 {
				t.Fatalf("replayed: %+v", rp)
			}
			if e.read(e.copyFile("T-1", "a/spec.md")) != "<none>" || e.read(e.copyFile("T-1", "c/spec.md")) != "<none>" {
				t.Fatal("a drop was skipped")
			}
			if e.read(e.copyFile("T-1", "b/spec.md")) != "b1" {
				t.Fatal("the held file's copy was dropped")
			}
			if got := e.read(filepath.Join(e.v, "notes", "x.md")); got != "[["+cpRel("b/spec.md")+"]]\n" {
				t.Fatalf("note: %q", got)
			}
			if got := e.read(e.proj("a/spec.md")); got != "[[projects/acme-app/specs/b/spec]] [[projects/acme-app/specs/c/spec]]\n" {
				t.Fatalf("projects a: %q", got)
			}
		})
	}
}

func TestPromoteReplayErrorKeepsFinishedReplays(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-2", nil)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.crash("T-1", failPut("b/spec.md"))
	e.put(e.copyFile("T-2", "b/spec.md"), "b-T2")
	e.mustPromote("T-2", "", promoteDone)
	other := workspace.Repo{Slug: "acme-lib"}
	e.put(spec.JournalPath(e.v, "T-1", other.Slug), "garbage\n")
	e.clock("T-1")
	results, err := runPromote(e.ctx, "T-1", []workspace.Repo{e.repo("T-1"), other}, nil)
	if !errors.Is(err, spec.ErrJournalDamaged) {
		t.Fatalf("want the damaged journal: %v", err)
	}
	if len(results) != 1 || results[0].Slug != e.r.Slug || results[0].Replayed == nil || len(results[0].Replayed.Held) != 1 || results[0].Replayed.Held[0].File != "b/spec.md" {
		t.Fatalf("results: %+v", results)
	}
	var out strings.Builder
	printPromote(&out, "T-1", results[0])
	if !strings.Contains(out.String(), "held back b/spec.md") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestPromoteStaleHeldListIgnored(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"a/spec.md": "a1"})
	e.put(spec.HeldPath(e.v, "T-1", e.r.Slug), "a/spec.md\n")
	e.mustPromote("T-1", "", promoteDone)
	if e.read(e.proj("a/spec.md")) != "a1" || e.read(spec.HeldPath(e.v, "T-1", e.r.Slug)) != "<none>" {
		t.Fatal("the stale held list skipped the put or stayed")
	}
	if err := spec.DamagedJournal(e.v, "T-1", e.r.Slug, errors.New("x")); !strings.Contains(err.Error(), ".held") {
		t.Fatalf("message: %v", err)
	}
}

func TestPromoteReplayCrossingNamesWriter(t *testing.T) {
	e := newHeldEnv(t)
	e.begin("T-1", map[string]string{"b/spec.md": "b1"})
	e.begin("T-2", map[string]string{"b/spec.md": "b-T2", "c/spec.md": "c2"})
	e.crash("T-1", failPut("b/spec.md"))
	e.crash("T-2", failPut("b/spec.md"))
	e.mustPromote("T-1", "", promoteDone)
	pr := e.mustPromote("T-2", "", promoteRefused)
	rp := pr.Replayed
	if rp == nil || len(rp.Held) != 1 || rp.Held[0].Ticket != "T-1" || rp.Held[0].Note == "" || rp.Version == "" {
		t.Fatalf("replayed: %+v", rp)
	}
	if !strings.Contains(rp.Held[0].Reason, "holds T-1's recorded after copy (") {
		t.Fatalf("reason: %q", rp.Held[0].Reason)
	}
	if c := pr.Conflict; c == nil || len(c.Others) != 1 || c.Others[0].Ticket != "T-1" {
		t.Fatalf("conflict: %+v", pr.Conflict)
	}
	if e.read(e.proj("b/spec.md")) != "b1" || e.read(e.proj("c/spec.md")) != "c2" {
		t.Fatal("projects/ is not T-1's b and T-2's c")
	}
}
