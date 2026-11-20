package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zero4573/tix/internal/scaffold"
)

var historicRoots = []string{"assets/vault-scaffold", "tools/vault-scaffold"}

func main() {
	out := flag.String("o", "scaffold-history.json", "the history file to merge into and write")
	flag.Parse()
	if err := run(".", *out); err != nil {
		fmt.Fprintln(os.Stderr, "genhistory: "+err.Error())
		os.Exit(1)
	}
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func run(dir, out string) error {
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	top = strings.TrimSpace(top)
	shallow, err := git(top, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(shallow) == "true" {
		return errors.New("a shallow clone lacks the scaffold's history: git fetch --unshallow first")
	}

	files := map[string][]string{}
	if data, err := os.ReadFile(out); err == nil {
		if files, err = scaffold.ParseHistory(data); err != nil {
			return fmt.Errorf("%s: %w (take either side of a merge conflict, then run it again)", out, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	add := func(rel string, data []byte) {
		if rel != scaffold.Skipped {
			files[rel] = append(files[rel], scaffold.Hash(data))
			if k := scaffold.Key(rel, data); k != scaffold.Hash(data) {
				files[rel] = append(files[rel], k)
			}
		}
	}

	commits := ""
	if _, err := git(top, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		args := append([]string{"rev-list", "HEAD", "--"}, historicRoots...)
		if commits, err = git(top, args...); err != nil {
			return err
		}
	}
	blobs := map[string][]byte{}
	for _, c := range strings.Fields(commits) {
		for _, root := range historicRoots {
			tree, err := git(top, "ls-tree", "-r", "-z", c, "--", root+"/")
			if err != nil {
				return err
			}
			for _, rec := range strings.Split(tree, "\x00") {
				meta, path, ok := strings.Cut(rec, "\t")
				f := strings.Fields(meta)
				if !ok || len(f) != 3 || f[1] != "blob" {
					continue
				}
				data, seen := blobs[f[2]]
				if !seen {
					s, err := git(top, "cat-file", "blob", f[2])
					if err != nil {
						return err
					}
					data = []byte(s)
					blobs[f[2]] = data
				}
				add(strings.TrimPrefix(path, root+"/"), data)
			}
		}
	}

	cur, err := scaffold.FromFS(os.DirFS(filepath.Join(top, filepath.FromSlash(historicRoots[0]))), ".")
	if err != nil {
		return err
	}
	for _, f := range cur {
		add(f.Rel, f.Data)
	}

	data, err := scaffold.FormatHistory(files)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o644)
}
