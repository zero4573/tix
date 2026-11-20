package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/vault"
)

var sandboxProjectLabels = []string{"claude-sandbox.project", "kiro-sandbox.project"}

var podman = func(args ...string) ([]byte, error) {
	out, err := exec.Command("podman", args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = fmt.Errorf("podman %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

type ticketNet struct {
	Pod, NetNS, ResolvConf string
}

func findTicketNet(dir, pod string) (ticketNet, error) {
	var pods []string
	for _, l := range sandboxProjectLabels {
		out, err := podman("pod", "ps", "--filter", "label="+l+"="+dir, "--filter", "status=running", "--format", "{{.Name}}")
		if err != nil {
			return ticketNet{}, err
		}
		pods = append(pods, strings.Fields(string(out))...)
	}
	switch {
	case pod != "":
		found := false
		for _, p := range pods {
			found = found || p == pod
		}
		if !found {
			return ticketNet{}, fmt.Errorf("no running sandbox pod %s for %s (running: %s)", pod, dir, orNone(pods))
		}
	case len(pods) == 0:
		return ticketNet{}, fmt.Errorf("no running sandbox for %s: start its session first (tix start, or tix main start)", dir)
	case len(pods) > 1:
		return ticketNet{}, fmt.Errorf("%d sandboxes run in %s, each with its own network: pick one with --pod (%s)", len(pods), dir, strings.Join(pods, ", "))
	default:
		pod = pods[0]
	}
	infra, err := podman("pod", "inspect", pod, "--format", "{{.InfraContainerID}}")
	if err != nil {
		return ticketNet{}, err
	}
	out, err := podman("inspect", strings.TrimSpace(string(infra)), "--format", "{{.NetworkSettings.SandboxKey}}\t{{.ResolvConfPath}}")
	if err != nil {
		return ticketNet{}, err
	}
	ns, rc, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	if ns == "" {
		return ticketNet{}, fmt.Errorf("pod %s has no network namespace of its own", pod)
	}
	return ticketNet{Pod: pod, NetNS: ns, ResolvConf: rc}, nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// the host's resolv.conf names 127.0.0.53, which is the sandbox's own loopback inside the pod's network
const netnsScript = `[ -z "$1" ] || mount --bind "$1" /etc/resolv.conf || exit 125
ns=$2 u=$3 g=$4; shift 4
exec nsenter --net="$ns" -- unshare --user --map-user="$u" --map-group="$g" -- "$@"`

func netnsArgv(n ticketNet, uid, gid int, argv []string) []string {
	return append([]string{"podman", "unshare", "unshare", "--mount", "--", "sh", "-c", netnsScript, "tix-ws-run",
		n.ResolvConf, n.NetNS, strconv.Itoa(uid), strconv.Itoa(gid)}, argv...)
}

func ticketNetCmd(key, pod string, argv []string) (*exec.Cmd, ticketNet, error) {
	if runtime.GOOS != "linux" {
		return nil, ticketNet{}, errors.New("needs Linux (rootless podman's namespaces)")
	}
	if gitx.InContainer() {
		return nil, ticketNet{}, errors.New("run it on the host: it joins a sandbox's network from outside")
	}
	if err := requireKey("ws run", key); err != nil {
		return nil, ticketNet{}, err
	}
	ctx, err := vault.Require()
	if err != nil {
		return nil, ticketNet{}, err
	}
	n, err := findTicketNet(filepath.Join(ctx.WorkRoot, key), pod)
	if err != nil {
		return nil, ticketNet{}, err
	}
	a := netnsArgv(n, os.Getuid(), os.Getgid(), argv)
	c := exec.Command(a[0], a[1:]...)
	c.Env = append(os.Environ(), "TIX_NET="+key)
	return c, n, nil
}

func wsRunCmd() *cobra.Command {
	var pod string
	cmd := &cobra.Command{
		Use:   "run <ID> [--pod <name>] [-- <command> [args...]]",
		Short: "Run a host program on a ticket sandbox's network (its localhost)",
		Long: `Runs a program of the host (default: your $SHELL) inside the network of the
ticket's running sandbox, so localhost is the sandbox's: every server the
session runs, on any port, including containers it started there, without
publishing ports (two tickets never collide). Everything else is the
host's: your user, files, display and tools. DNS uses the sandbox's
resolver. $TIX_NET is the ticket ID.

  tix ws run SAP-85 -- psql -h localhost -U app
  tix ws run SAP-85 -- curl localhost:8090/health
  tix ws run SAP-85                     # a shell on SAP-85's network

The session must be running. Several sessions in one workspace each have a
network: --pod picks one. For a browser, tix ws browse. Host only (rootless
podman, Linux).`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := args[1:]
			if len(argv) == 0 {
				sh := os.Getenv("SHELL")
				if sh == "" {
					sh = "sh"
				}
				argv = []string{sh}
			}
			c, n, err := ticketNetCmd(args[0], pod, argv)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "tix: on %s's network (pod %s)\n", args[0], n.Pod)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return exitError(ee.ExitCode())
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pod, "pod", "", "the sandbox pod, when several sessions run in the workspace")
	return cmd
}

var browsers = []string{"chromium", "google-chrome-stable", "google-chrome", "brave"}

func findBrowser() (string, error) {
	if b := os.Getenv("TIX_BROWSER"); b != "" {
		return exec.LookPath(b)
	}
	for _, b := range browsers {
		if p, err := exec.LookPath(b); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chromium-family browser found (%s): set $TIX_BROWSER", strings.Join(browsers, ", "))
}

func browseArgv(browser, profile string, urls []string) []string {
	return append([]string{browser, "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check"}, urls...)
}

func wsBrowseCmd() *cobra.Command {
	var pod string
	cmd := &cobra.Command{
		Use:   "browse <ID> [--pod <name>] [URL...]",
		Short: "Open a browser on a ticket sandbox's network (its localhost)",
		Long: `Opens a Chromium-family browser ($TIX_BROWSER, else chromium,
google-chrome-stable, google-chrome or brave) on the network of the ticket's
running sandbox (as tix ws run): http://localhost:5173 there is the
ticket's dev server, at the ports and addresses its apps expect, so logins
and callbacks registered for localhost work. Each ticket has its own browser
profile (<state dir>/browse/<ID>), never yours: cookies and logins stay per
ticket. Opening it again while it runs opens a window in the same browser.

  tix ws browse SAP-85 http://localhost:5173`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := findBrowser()
			if err != nil {
				return err
			}
			profile := filepath.Join(config.StateDir(), "browse", args[0])
			if err := os.MkdirAll(profile, 0o700); err != nil {
				return err
			}
			c, n, err := ticketNetCmd(args[0], pod, browseArgv(b, profile, args[1:]))
			if err != nil {
				return err
			}
			logf, err := os.Create(filepath.Join(profile, "tix-browse.log"))
			if err != nil {
				return err
			}
			defer logf.Close()
			c.Stdout, c.Stderr = logf, logf
			c.SysProcAttr = ownGroup()
			if err := c.Start(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s's browser on its network (pod %s); profile %s, log tix-browse.log there\n", args[0], n.Pod, profile)
			return c.Process.Release()
		},
	}
	cmd.Flags().StringVar(&pod, "pod", "", "the sandbox pod, when several sessions run in the workspace")
	return cmd
}
