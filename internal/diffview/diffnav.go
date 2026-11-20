package diffview

import (
	"bytes"
	"fmt"
	"os/exec"
)

type Diffnav struct{ Bin string }

func (Diffnav) Interactive() bool { return true }

func (d Diffnav) Available(lookPath func(string) (string, error)) bool {
	_, err := lookPath(d.Bin)
	return err == nil
}

func (d Diffnav) Show(repos []Repo, o Options) error {
	var patch bytes.Buffer
	for _, r := range repos {
		if r.Missing() {
			fmt.Fprintf(o.Stderr, "%s: worktree missing: %s\n", r.Name, r.Dir)
			continue
		}
		if err := Patch(&patch, r); err != nil {
			return err
		}
	}
	if patch.Len() == 0 {
		fmt.Fprintln(o.Stdout, "no changes")
		return nil
	}
	cmd := exec.Command(d.Bin)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &patch, o.Stdout, o.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", d.Bin, err)
	}
	return nil
}
