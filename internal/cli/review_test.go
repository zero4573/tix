package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBroker(t *testing.T, answer func(req map[string]any) []string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "review.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	t.Setenv(reviewSocketEnv, sock)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadBytes('\n')
				var req map[string]any
				_ = json.Unmarshal(line, &req)
				for _, l := range answer(req) {
					_, _ = conn.Write([]byte(l + "\n"))
				}
			}()
		}
	}()
}

func runReview(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	root := Root()
	root.SetArgs(append([]string{"review"}, args...))
	root.SetOut(&out)
	root.SetErr(&errb)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestReviewKiroAllModels(t *testing.T) {
	fakeBroker(t, func(req map[string]any) []string {
		switch req["op"] {
		case "models":
			return []string{`{"status":"ok","models":["gpt-5.6-sol","glm-5"]}`}
		}
		if req["ticket"] != "MAN-1" || req["wave"] != float64(2) || req["repo"] != "acme-api" || req["range"] != "1111111..2222222" || req["brief"] != "contract change" {
			return []string{`{"status":"refused","detail":"bad request"}`}
		}
		if req["model"] == "glm-5" {
			return []string{`{"log":"Error: monthly usage limit reached"}`, `{"status":"usage-limit","detail":"Error: monthly\nusage limit reached"}`}
		}
		return []string{`{"log":"reviewing"}`, `{"status":"ok","reviewFile":"/v/reviews/wave-2-acme-api-gpt-5.6-sol.md"}`}
	})
	out, errOut, err := runReview(t, "kiro", "MAN-1", "--wave", "2", "--repo", "acme-api", "--range", "1111111..2222222", "--model", "all", "--brief", "contract change")
	if code, ok := err.(exitError); !ok || code != 4 {
		t.Fatalf("err = %v, want exit 4 (a skipped review)", err)
	}
	for _, s := range []string{"ok\tgpt-5.6-sol\t/v/reviews/wave-2-acme-api-gpt-5.6-sol.md\n", "skipped\tglm-5\tusage-limit\tError: monthly usage limit reached\n"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout misses %q:\n%s", s, out)
		}
	}
	if !strings.Contains(errOut, "gpt-5.6-sol: reviewing\n") || !strings.Contains(errOut, "glm-5: Error: monthly usage limit reached\n") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestReviewKiroOK(t *testing.T) {
	fakeBroker(t, func(req map[string]any) []string {
		return []string{`{"status":"ok","reviewFile":"/v/r.md"}`}
	})
	out, _, err := runReview(t, "kiro", "MAN-1", "--wave", "1", "--repo", "a", "--range", "1111111..2222222", "--model", "glm-5")
	if err != nil || out != "ok\tglm-5\t/v/r.md\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestReviewKiroRefused(t *testing.T) {
	fakeBroker(t, func(req map[string]any) []string {
		return []string{`{"status":"refused","detail":"range: <sha>..<sha>"}`}
	})
	out, _, err := runReview(t, "kiro", "MAN-1", "--wave", "1", "--repo", "a", "--range", "HEAD~1..HEAD", "--model", "glm-5")
	if code, ok := err.(exitError); !ok || code != 1 || !strings.Contains(out, "refused\tglm-5\trange: <sha>..<sha>") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestReviewWithoutBroker(t *testing.T) {
	t.Setenv(reviewSocketEnv, "")
	out, _, err := runReview(t, "kiro", "MAN-1", "--wave", "1", "--repo", "a", "--range", "1111111..2222222", "--model", "all")
	if code, ok := err.(exitError); !ok || code != 4 || !strings.HasPrefix(out, "skipped\tall\tunavailable\t") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if _, _, err := runReview(t, "models"); err == nil {
		t.Fatal("models without a broker: exit 0")
	}
}
