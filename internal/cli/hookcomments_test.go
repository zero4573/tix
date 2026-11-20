package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commentPayload(t *testing.T, cwd, tool string, input map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"cwd": cwd, "tool_name": tool, "tool_input": input})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCommentHook(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "MAN-1")
	repo := filepath.Join(ws, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"id":"MAN-1","repos":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dart := filepath.Join(repo, "lib", "queue.dart")
	for name, c := range map[string]struct {
		cwd, tool string
		input     map[string]any
		want      int
	}{
		"dart doc comments": {repo, "Edit", map[string]any{"file_path": dart, "old_string": "void reset() {",
			"new_string": "/// A new playlist loads: queued jobs are dropped.\n/// Running jobs finish.\nvoid reset() {"}, 2},
		"moved comment": {repo, "Edit", map[string]any{"file_path": dart, "old_string": "// kept\nvoid a() {}",
			"new_string": "void a() {}\n// kept"}, 0},
		"code only": {repo, "Edit", map[string]any{"file_path": dart, "old_string": "a()", "new_string": "b()"}, 0},
		"multi edit": {repo, "MultiEdit", map[string]any{"file_path": filepath.Join(repo, "run.sh"), "edits": []map[string]any{
			{"old_string": "echo a", "new_string": "# say a\necho a"}, {"old_string": "echo b", "new_string": "#!/bin/sh\necho b"}}}, 1},
		"new file":         {repo, "Write", map[string]any{"file_path": filepath.Join(repo, "a.nix"), "content": "_: {\n  # the module\n}\n"}, 1},
		"markdown":         {repo, "Write", map[string]any{"file_path": filepath.Join(repo, "README.md"), "content": "# Title\n"}, 0},
		"outside a ticket": {t.TempDir(), "Edit", map[string]any{"file_path": dart, "old_string": "", "new_string": "/// doc"}, 0},
		"read tool":        {repo, "Read", map[string]any{"file_path": dart}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			out := commentHook(strings.NewReader(commentPayload(t, c.cwd, c.tool, c.input)), false)
			if c.want == 0 {
				if out != nil {
					t.Fatalf("got %+v, want nothing", out)
				}
				return
			}
			if out == nil || out.Decision != "block" {
				t.Fatalf("got %+v, want a block", out)
			}
			if want := "adds " + string(rune('0'+c.want)) + " comment line"; !strings.Contains(out.Reason, want) {
				t.Errorf("reason %q doesn't say %q", out.Reason, want)
			}
		})
	}
	if commentHook(strings.NewReader("not json"), false) != nil {
		t.Error("bad payload: want nothing")
	}
}

func TestCommentHookWriteComparesWithHead(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "MAN-1")
	repo := filepath.Join(ws, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"id":"MAN-1","repos":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, "run.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n# podman -t needs a terminal\necho a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-qm", "init"}} {
		diffCheckGit(t, repo, args...)
	}
	in := commentPayload(t, repo, "Write", map[string]any{"file_path": file,
		"content": "#!/bin/sh\n# podman -t needs a terminal\n# then say b\necho a\necho b\n"})
	out := commentHook(strings.NewReader(in), false)
	if out == nil || !strings.Contains(out.Reason, "adds 1 comment line") || !strings.Contains(out.Reason, "then say b") {
		t.Fatalf("got %+v, want the one new comment", out)
	}
}

func TestCommentHookAnywhere(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "MAN-1")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"id":"MAN-1","repos":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := map[string]any{"file_path": "/src/app/lib/q.dart", "old_string": "", "new_string": "/// the queue"}
	if out := commentHook(strings.NewReader(commentPayload(t, t.TempDir(), "Edit", edit)), true); out == nil {
		t.Error("a plain session: want the comment reported")
	}
	if out := commentHook(strings.NewReader(commentPayload(t, ws, "Edit", edit)), true); out != nil {
		t.Errorf("a tix workspace with --anywhere: the plugin's hook covers it, got %+v", out)
	}
}
