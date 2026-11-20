package diffview

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func found(string) (string, error)    { return "/bin/x", nil }
func notFound(string) (string, error) { return "", errors.New("not found") }

func TestPick(t *testing.T) {
	cases := []struct {
		name string
		c    Choice
		want Viewer
		err  string
	}{
		{"terminal with diffnav", Choice{Terminal: true, LookPath: found}, viewers["diffnav"], ""},
		{"terminal without diffnav", Choice{Terminal: true, LookPath: notFound}, Git{}, ""},
		{"piped", Choice{LookPath: found}, Git{}, ""},
		{"stat in a terminal", Choice{Terminal: true, Stat: true, LookPath: found}, Git{}, ""},
		{"flag over env", Choice{Flag: "git", Env: "diffnav", Terminal: true, LookPath: found}, Git{}, ""},
		{"env", Choice{Env: "git", Terminal: true, LookPath: found}, Git{}, ""},
		{"named diffnav with stat", Choice{Flag: "diffnav", Stat: true, Terminal: true, LookPath: found}, Git{}, ""},
		{"named diffnav piped", Choice{Flag: "diffnav", LookPath: found}, nil, "needs a terminal"},
		{"unknown", Choice{Flag: "nope", LookPath: found}, nil, `unknown diff viewer "nope" (one of diffnav, git)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Pick(tc.c)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Pick = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repoWithChanges(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "kept.txt", "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "kept.txt", "two\n")
	git(t, dir, "commit", "-q", "-am", "change")
	write(t, dir, "dirty.txt", "uncommitted\n")
	write(t, dir, "sub/new.txt", "untracked\n")
	write(t, dir, ".gitignore", "ignored.txt\n")
	write(t, dir, "ignored.txt", "secret\n")
	return Repo{Name: "acme-billing", Branch: "feature/x", Base: "main", Dir: dir, MergeBase: base}
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPatchPrefixesPathsWithTheRepoAndAddsUntrackedFiles(t *testing.T) {
	r := repoWithChanges(t)
	var b bytes.Buffer
	if err := Patch(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"diff --git a/acme-billing/kept.txt b/acme-billing/kept.txt\n",
		"-one\n+two\n",
		"+++ b/acme-billing/sub/new.txt\n",
		"+untracked\n",
		"+++ b/acme-billing/dirty.txt\n",
		"+++ b/acme-billing/.gitignore\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("patch lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "\x1b[") {
		t.Errorf("patch has an ignored file or colour:\n%s", out)
	}
}

func TestDiffnavGetsOnePatchForEveryRepo(t *testing.T) {
	a, b := repoWithChanges(t), repoWithChanges(t)
	b.Name = "acme-web"
	missing := Repo{Name: "gone", Dir: filepath.Join(t.TempDir(), "gone")}
	bin := filepath.Join(t.TempDir(), "diffnav")
	got := filepath.Join(t.TempDir(), "stdin")
	write(t, filepath.Dir(bin), "diffnav", "#!/bin/sh\ncat > "+got+"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := (Diffnav{Bin: bin}).Show([]Repo{a, missing, b}, Options{Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a/acme-billing/kept.txt", "a/acme-web/kept.txt"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("diffnav input lacks %q", want)
		}
	}
	if !strings.Contains(stderr.String(), "gone: worktree missing") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDiffnavWithNothingChangedSaysSo(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	var stdout bytes.Buffer
	r := Repo{Name: "r", Dir: dir, MergeBase: "HEAD"}
	if err := (Diffnav{Bin: "/nonexistent"}).Show([]Repo{r}, Options{Stdout: &stdout, Stderr: &stdout}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "no changes\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
