package gitx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zero4573/tix/internal/fsx"
)

var (
	rootsMu sync.Mutex
	roots   []string
)

func TrustRoots(dirs ...string) {
	rootsMu.Lock()
	defer rootsMu.Unlock()
	roots = roots[:0]
	for _, d := range dirs {
		if d != "" {
			roots = append(roots, resolve(d))
		}
	}
}

func trusted() []string {
	rootsMu.Lock()
	defer rootsMu.Unlock()
	return append([]string(nil), roots...)
}

func resolve(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func checkDir(dir string) error {
	_, _, err := locate(dir)
	return err
}

// git's discovery (setup_git_directory_gently_1): at each level a .git file
// is a gitdir pointer, a .git folder counts only when it passes
// is_git_directory, then the folder itself may be a bare repository.
func locate(dir string) (gitDir, common string, err error) {
	dotgit := ""
	for d := resolve(dir); ; d = filepath.Dir(d) {
		p := filepath.Join(d, ".git")
		if st, err := os.Lstat(p); err == nil {
			if !st.IsDir() {
				dotgit = p
				break
			}
			if _, err := os.Lstat(filepath.Join(p, "commondir")); err == nil {
				return "", "", fmt.Errorf("git: refusing %s: a main clone's .git has a commondir file, which points git at another repository's config; remove it", p)
			}
			if isGitDirectory(p) {
				return p, p, nil
			}
		}
		if isGitDirectory(d) {
			return checkGitDir(d, d)
		}
		if filepath.Dir(d) == d {
			return "", "", nil
		}
	}
	lst, err := os.Lstat(dotgit)
	if err != nil {
		return "", "", fmt.Errorf("git: can't read %s: %w", dotgit, err)
	}
	if lst.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("git: refusing %s: it is a symlink", dotgit)
	}
	if !lst.Mode().IsRegular() || lst.Size() > 4096 {
		return "", "", fmt.Errorf("git: refusing %s: not a gitdir pointer", dotgit)
	}
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return "", "", fmt.Errorf("git: can't read %s: %w", dotgit, err)
	}
	// git's read_gitfile_gently: the whole file, trailing CRs and LFs cut, is
	// "gitdir: <path>", the path taken as it is
	gd, ok := strings.CutPrefix(strings.TrimRight(string(data), "\r\n"), "gitdir: ")
	if !ok || gd == "" || strings.ContainsAny(gd, "\r\n") {
		return "", "", fmt.Errorf("git: refusing %s: not a gitdir pointer", dotgit)
	}
	return checkGitDir(dotgit, pointer(filepath.Dir(dotgit), gd))
}

func checkGitDir(what, gitDir string) (string, string, error) {
	common := gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = pointer(gitDir, strings.TrimRight(string(c), "\r\n")) // as get_common_dir_noenv
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("git: can't read %s: %w", filepath.Join(gitDir, "commondir"), err)
	}
	if common != gitDir {
		if _, err := os.Lstat(filepath.Join(common, "commondir")); err == nil {
			return "", "", fmt.Errorf("git: refusing %s: its common dir %s has a commondir file of its own", what, common)
		}
		if !fsx.Inside(gitDir, filepath.Join(common, "worktrees")) {
			return "", "", fmt.Errorf("git: refusing %s: its git dir %s is not one of %s's worktrees", what, gitDir, common)
		}
	}
	if what == gitDir && common == gitDir {
		return gitDir, common, nil
	}
	rs := trusted()
	if len(rs) == 0 {
		return gitDir, common, nil
	}
	for _, r := range rs {
		if fsx.Within(common, r) {
			return gitDir, common, nil
		}
	}
	return "", "", fmt.Errorf("git: refusing %s: it points at %s, outside %s", what, common, strings.Join(rs, " and "))
}

// git's is_git_directory (setup.c): a valid HEAD and, in the common dir,
// objects and refs that access(X_OK) accepts
func isGitDirectory(dir string) bool {
	if !validHeadRef(filepath.Join(dir, "HEAD")) {
		return false
	}
	common := dir
	if c, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
		common = pointer(dir, strings.TrimRight(string(c), "\r\n"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return true // git dies on an unreadable commondir, so checkGitDir must see and refuse it
	}
	return searchable(filepath.Join(common, "objects")) && searchable(filepath.Join(common, "refs"))
}

// git's validate_headref: a symlink to refs/..., "ref:" then whitespace then
// "refs/", or a full sha1 or sha256 hex object id
func validHeadRef(path string) bool {
	st, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		return err == nil && strings.HasPrefix(t, "refs/")
	}
	if st.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 255)
	n, _ := io.ReadFull(f, buf)
	head := string(buf[:n])
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		if strings.HasPrefix(strings.TrimLeft(ref, " \t\n\r"), "refs/") {
			return true
		}
	}
	return hexPrefix(head, 64) || hexPrefix(head, 40)
}

func hexPrefix(s string, n int) bool {
	if len(s) < n {
		return false
	}
	for i := 0; i < n; i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

func pointer(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return resolve(p)
}

// git composes repeated leading -C options; a -C after the subcommand
// (log -C) is the subcommand's
func dirOf(args []string) string {
	dir := ""
	for i := 0; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		switch args[i] {
		case "-C":
			if i+1 < len(args) {
				if next := args[i+1]; dir == "" || filepath.IsAbs(next) {
					dir = next
				} else {
					dir = filepath.Join(dir, next)
				}
			}
			i++
		case "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			i++
		}
	}
	return dir
}
