package diffview

import (
	"bytes"
	"fmt"
	"io"
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
	var failure bytes.Buffer
	cmd := exec.Command(d.Bin)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &patch, o.Stdout, &failure
	if err := cmd.Run(); err != nil {
		io.WriteString(o.Stdout, terminalReset)
		o.Stderr.Write(failure.Bytes())
		return fmt.Errorf("%s: %w", d.Bin, err)
	}
	o.Stderr.Write(failure.Bytes())
	return nil
}

// diffnav exits on a patch it cannot parse without leaving the alternate screen or turning
// off mouse reporting and the kitty keyboard protocol, which leaves the shell unusable.
const terminalReset = "\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[=0;1u\x1b[>4m\x1b[?25h"
