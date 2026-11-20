package diffview

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/zero4573/tix/internal/gitx"
)

type Repo struct {
	Name      string
	Branch    string
	Base      string
	Dir       string
	MergeBase string
}

func (r Repo) Missing() bool {
	_, err := os.Stat(filepath.Join(r.Dir, ".git"))
	return err != nil
}

type Options struct {
	Stat   bool
	Stdout io.Writer
	Stderr io.Writer
}

type Viewer interface {
	Show(repos []Repo, o Options) error
	Interactive() bool
	Available(lookPath func(string) (string, error)) bool
}

var viewers = map[string]Viewer{
	"git":     Git{},
	"diffnav": Diffnav{Bin: "diffnav"},
}

var preferred = []string{"diffnav", "git"}

const EnvVar = "TIX_DIFF_VIEWER"

func Names() []string {
	names := make([]string, 0, len(viewers))
	for n := range viewers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type Choice struct {
	Flag     string
	Env      string
	Stat     bool
	Terminal bool
	LookPath func(string) (string, error)
}

func Pick(c Choice) (Viewer, error) {
	name := c.Flag
	if name == "" {
		name = c.Env
	}
	if name == "" {
		for _, n := range preferred {
			v := viewers[n]
			if v.Interactive() && (c.Stat || !c.Terminal) {
				continue
			}
			if v.Available(c.LookPath) {
				return v, nil
			}
		}
		return viewers["git"], nil
	}
	v, ok := viewers[name]
	if !ok {
		return nil, fmt.Errorf("unknown diff viewer %q (one of %s)", name, strings.Join(Names(), ", "))
	}
	if v.Interactive() {
		if c.Stat {
			return viewers["git"], nil
		}
		if !c.Terminal {
			return nil, fmt.Errorf("diff viewer %s needs a terminal; use --viewer git", name)
		}
	}
	return v, nil
}

func untracked(dir string) []string {
	out, err := gitx.Out(dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || out == "" {
		return nil
	}
	return slices.DeleteFunc(strings.Split(out, "\x00"), func(s string) bool { return s == "" })
}

func Patch(w io.Writer, r Repo) error {
	prefix := []string{"--no-ext-diff", "--no-color", "--src-prefix=a/" + r.Name + "/", "--dst-prefix=b/" + r.Name + "/"}
	tracked := gitx.CommandIn(r.Dir, append(append([]string{"--no-pager", "diff"}, prefix...), r.MergeBase)...)
	tracked.Stdout = w
	if err := tracked.Run(); err != nil {
		return fmt.Errorf("%s: git diff: %w", r.Name, err)
	}
	for _, f := range untracked(r.Dir) {
		added := gitx.CommandIn(r.Dir, append(append([]string{"--no-pager", "diff", "--no-index"}, prefix...), "--", os.DevNull, f)...)
		added.Stdout = w
		var exit *exec.ExitError
		if err := added.Run(); err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			return fmt.Errorf("%s: git diff %s: %w", r.Name, f, err)
		}
	}
	return nil
}
