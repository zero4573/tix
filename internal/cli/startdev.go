package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/zero4573/tix/assets"
	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/session"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

const devNone = "none"

const devHelp = "run the workspace's sessions on the plugin and tix built from its tix checkout <slug> (kept for later starts; none: back to the installed ones)"

var goBuild = func(checkout, out string, env map[string]string) ([]byte, error) {
	return goBuildCmd(checkout, out, env).CombinedOutput()
}

func goBuildCmd(checkout, out string, env map[string]string) *exec.Cmd {
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "./cmd/tix")
	cmd.Dir = checkout
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func tixFile() string {
	if runtime.GOOS == "windows" {
		return "tix.exe"
	}
	return "tix"
}

func devCheckout(info workspace.Info, slug string) (string, bool) {
	for _, r := range info.Repos {
		if r.Slug == slug && fsx.IsDir(filepath.Join(r.Path, "cmd", "tix")) && fsx.IsDir(filepath.Join(r.Path, "assets", "plugin")) {
			return r.Path, true
		}
	}
	return "", false
}

func notDevCheckout(slug string) error {
	fmt.Fprintf(os.Stderr, "tix start: %s is not a tix checkout\n", slug)
	return exitError(1)
}

func checkDev(ctx vault.Context, key, slug string) error {
	if slug == devNone {
		return nil
	}
	info, err := workspace.Read(filepath.Join(ctx.WorkRoot, key))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !slices.ContainsFunc(info.Repos, func(r workspace.Repo) bool { return r.Slug == slug }) {
		fmt.Fprintf(os.Stderr, "tix start: %s is not a repo of %s's workspace (tix ws add first)\n", slug, key)
		return exitError(1)
	}
	if _, ok := devCheckout(info, slug); !ok {
		return notDevCheckout(slug)
	}
	return nil
}

func setDev(ctx vault.Context, keys []string, slug string) error {
	for _, key := range keys {
		if err := checkDev(ctx, key, slug); err != nil {
			return err
		}
	}
	for _, key := range keys {
		file := workspace.File(filepath.Join(ctx.WorkRoot, key))
		if slug == devNone {
			if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err := workspace.SetDev(file, nil); err != nil {
				return err
			}
			continue
		}
		if err := workspace.SetDev(file, &workspace.Dev{Slug: slug}); err != nil {
			return err
		}
	}
	return nil
}

type devBuild struct {
	plugin, bin, tix string
}

type devMode int

const (
	devIgnore devMode = iota
	devRebuild
)

func prepareDev(dir string, mode devMode) (*devBuild, error) {
	if mode == devIgnore {
		return nil, nil
	}
	info, err := workspace.Read(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Dev == nil {
		return nil, nil
	}
	b := devBuild{plugin: filepath.Join(session.DevDir(dir), "plugin"), bin: filepath.Join(session.DevDir(dir), "bin")}
	b.tix = filepath.Join(b.bin, tixFile())
	slug := info.Dev.Slug
	checkout, ok := devCheckout(info, slug)
	if !ok {
		return nil, notDevCheckout(slug)
	}
	if err := os.MkdirAll(b.bin, 0o755); err != nil {
		return nil, err
	}
	if out, err := goBuild(checkout, b.tix, session.GoEnv(dir)); err != nil {
		os.Stderr.Write(out)
		fmt.Fprintf(os.Stderr, "tix start: building tix in %s failed (%v)\n", slug, err)
		return nil, exitError(1)
	}
	if _, err := os.Lstat(filepath.Join(checkout, "assets")); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("copying the plugin of %s: no assets dir in %s", slug, slug)
	}
	if err := assets.MaterializeTo(checkout, "assets/plugin", b.plugin, b.tix); err != nil {
		return nil, fmt.Errorf("copying the plugin of %s: %w", slug, err)
	}
	fmt.Printf("tix start: dev mode: plugin and tix from %s (%s)\n", slug, devVersion(checkout))
	return &b, nil
}

func devVersion(checkout string) string {
	sha, err := gitx.Out(checkout, "rev-parse", "--short", "HEAD")
	sha = strings.TrimSpace(sha)
	if err != nil || sha == "" {
		return "unknown HEAD"
	}
	if st, err := gitx.Out(checkout, "status", "--porcelain"); err == nil && strings.TrimSpace(st) != "" {
		sha += "-dirty"
	}
	return sha
}
