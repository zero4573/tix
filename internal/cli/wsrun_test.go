package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func fakePodman(t *testing.T, pods map[string]string) *[][]string {
	t.Helper()
	var calls [][]string
	old := podman
	t.Cleanup(func() { podman = old })
	podman = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch strings.Join(args[:2], " ") {
		case "pod ps":
			return []byte(pods[args[3]]), nil
		case "pod inspect":
			return []byte("infra-" + args[2] + "\n"), nil
		}
		if args[0] == "inspect" {
			if args[1] == "infra-bare" {
				return []byte("\t\n"), nil
			}
			return []byte("/run/user/1000/netns/ns-" + args[1] + "\t/run/rc-" + args[1] + "\n"), nil
		}
		return nil, errors.New("unexpected podman call")
	}
	return &calls
}

func TestFindTicketNet(t *testing.T) {
	const dir = "/work/PROJ-1"
	fakePodman(t, map[string]string{
		"label=claude-sandbox.project=" + dir:    "claude-sandbox-proj-1\n",
		"label=kiro-sandbox.project=/work/TWO":   "kiro-sandbox-two\n",
		"label=claude-sandbox.project=/work/TWO": "claude-sandbox-two\n",
		"label=claude-sandbox.project=/work/B":   "bare\n",
	})
	n, err := findTicketNet(dir, "")
	if err != nil || n != (ticketNet{"claude-sandbox-proj-1", "/run/user/1000/netns/ns-infra-claude-sandbox-proj-1", "/run/rc-infra-claude-sandbox-proj-1"}) {
		t.Fatalf("one pod: %+v %v", n, err)
	}
	if _, err := findTicketNet("/work/NONE", ""); err == nil || !strings.Contains(err.Error(), "no running sandbox") {
		t.Errorf("no pod: %v", err)
	}
	if _, err := findTicketNet("/work/TWO", ""); err == nil || !strings.Contains(err.Error(), "--pod (claude-sandbox-two, kiro-sandbox-two)") {
		t.Errorf("two pods: %v", err)
	}
	if n, err := findTicketNet("/work/TWO", "kiro-sandbox-two"); err != nil || n.Pod != "kiro-sandbox-two" {
		t.Errorf("--pod: %+v %v", n, err)
	}
	if _, err := findTicketNet("/work/TWO", "other"); err == nil || !strings.Contains(err.Error(), "no running sandbox pod other") {
		t.Errorf("--pod not running: %v", err)
	}
	if _, err := findTicketNet("/work/B", ""); err == nil || !strings.Contains(err.Error(), "no network namespace") {
		t.Errorf("shared network: %v", err)
	}
}

func TestNetnsArgv(t *testing.T) {
	a := netnsArgv(ticketNet{"p", "/ns", "/rc"}, 1000, 100, []string{"curl", "localhost:80"})
	want := []string{"podman", "unshare", "unshare", "--mount", "--", "sh", "-c", netnsScript, "tix-ws-run", "/rc", "/ns", "1000", "100", "curl", "localhost:80"}
	if !slices.Equal(a, want) {
		t.Errorf("argv:\n got %q\nwant %q", a, want)
	}
	b := browseArgv("/bin/chromium", "/state/browse/PROJ-1", []string{"http://localhost:5173"})
	if !slices.Contains(b, "--user-data-dir=/state/browse/PROJ-1") || b[len(b)-1] != "http://localhost:5173" {
		t.Errorf("browse argv: %q", b)
	}
}
