package gitx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func safeFlags() []string {
	null := "/dev/null"
	if runtime.GOOS == "windows" {
		null = "NUL"
	}
	return []string{"-c", "core.hooksPath=" + null, "-c", "core.fsmonitor=",
		// git: an empty credential.helper resets the list of helpers read so far
		"-c", "credential.helper=", "-c", "core.gitProxy=",
		"-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false",
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always",
		"-c", "submodule.recurse=false", "-c", "fetch.recurseSubmodules=false", "-c", "diff.ignoreSubmodules=all"}
}

func localFlags(args ...string) []string {
	return append([]string{"-c", "protocol.file.allow=always"}, args...)
}

var strippedEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CONFIG",
	"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"}

func env() []string {
	var e []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if stripped(k) {
			continue
		}
		e = append(e, kv)
	}
	// git: GIT_SSH_COMMAND takes precedence over a repository's core.sshCommand
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		e = append(e, "GIT_SSH_COMMAND=ssh")
	}
	return e
}

func stripped(k string) bool {
	if runtime.GOOS == "windows" {
		k = strings.ToUpper(k)
	}
	for _, s := range strippedEnv {
		if k == s {
			return true
		}
	}
	return strings.HasPrefix(k, "GIT_CONFIG_KEY_") || strings.HasPrefix(k, "GIT_CONFIG_VALUE_")
}

func Command(args ...string) *exec.Cmd {
	cmd := exec.Command("git")
	return guard(cmd, args)
}

func CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git")
	return guard(cmd, args)
}

func CommandIn(dir string, args ...string) *exec.Cmd {
	return Command(append([]string{"-C", dir}, args...)...)
}

func Check(dir string) error {
	gitDir, common, err := locate(dir)
	if err != nil || gitDir == "" {
		return err
	}
	return checkConfig(dir, gitDir, common)
}

func guard(cmd *exec.Cmd, args []string) *exec.Cmd {
	cmd.Env = env()
	args, dir, err := prepare(args)
	cmd.Args = append(append([]string{"git"}, safeFlags()...), args...)
	if err != nil {
		cmd.Err = err
		return cmd
	}
	cmd.Dir = dir
	return cmd
}

func prepare(args []string) ([]string, string, error) {
	for _, a := range globalOpts(args) {
		if a == "--git-dir" || a == "--work-tree" || strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") {
			return args, "", fmt.Errorf("git: refusing %s: tix's git finds the repository through -C only", a)
		}
	}
	args, err := noSubmoduleChildren(args)
	if err != nil {
		return args, "", err
	}
	sub, _ := subcommand(args)
	if dir := dirOf(args); dir != "" {
		if err := Check(dir); err != nil {
			return args, "", err
		}
		if sub == "clone" {
			return args, "", fmt.Errorf("git: refusing clone with -C: tix clones through gitx.Clone")
		}
		return withRefmap(args), "", nil
	}
	if sub == "clone" {
		return prepareClone(args)
	}
	return args, neutralDir(), nil
}

// git: these run git in each submodule and in-tree gitlink repo to see whether it is dirty
var worktreeDiffs = map[string]bool{"status": true, "diff": true, "diff-files": true, "diff-index": true}

// git: only --ignore-submodules on the command line beats .gitmodules' submodule.<n>.ignore, and
// parse-options takes abbreviations with the last value winning. add, describe --dirty/--broken,
// commit -a and worktree remove without --force also diff the work tree but take no such option.
func noSubmoduleChildren(args []string) ([]string, error) {
	sub, rest := subcommand(args)
	switch {
	case worktreeDiffs[sub]:
		end := len(rest)
		for i, a := range rest {
			if a == "--" {
				end = i
				break
			}
			if name, val, eq := strings.Cut(a, "="); abbrevOf(name, "--ignore-submodules", len("--ignore-s")) && (eq && val != "all" || name != "--ignore-submodules") {
				return args, fmt.Errorf("git: refusing %s: tix's git ignores submodules", a)
			}
		}
		at := len(args) - len(rest) + end
		out := append([]string(nil), args[:at]...)
		out = append(out, "--ignore-submodules=all")
		return append(out, args[at:]...), nil
	case sub == "worktree" && len(rest) > 0 && rest[0] == "remove" && !hasOpt(rest[1:], "", "f", "--force"):
		return args, fmt.Errorf("git: refusing worktree remove without --force: it runs status in each submodule")
	case sub == "add",
		sub == "describe" && hasAbbrev(rest, "--dirty", "--broken"),
		sub == "commit" && hasOpt(rest, "mFcCtS", "a", "--all"):
		return args, fmt.Errorf("git: refusing %s %s: it would run git in each submodule; tix doesn't use it", sub, strings.Join(rest, " "))
	}
	return args, nil
}

func abbrevOf(name, long string, min int) bool {
	return len(name) >= min && strings.HasPrefix(long, name)
}

func hasAbbrev(rest []string, long ...string) bool {
	for _, a := range rest {
		if a == "--" {
			return false
		}
		name, _, _ := strings.Cut(a, "=")
		for _, l := range long {
			if abbrevOf(name, l, len("--")+1) {
				return true
			}
		}
	}
	return false
}

func hasOpt(rest []string, shortWithValue, short string, long ...string) bool {
	for _, a := range rest {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "--") {
			for _, l := range long {
				if a == l || strings.HasPrefix(a, l+"=") {
					return true
				}
			}
			continue
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		for _, c := range a[1:] {
			if strings.ContainsRune(short, c) {
				return true
			}
			if strings.ContainsRune(shortWithValue, c) {
				break
			}
		}
	}
	return false
}

func neutralDir() string {
	if runtime.GOOS == "windows" {
		if v := filepath.VolumeName(os.TempDir()); v != "" {
			return v + `\`
		}
		return `C:\`
	}
	return "/"
}

func globalOpts(args []string) []string {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[:i]
		}
		switch args[i] {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			i++
		}
	}
	return args
}

func subcommand(args []string) (string, []string) {
	g := globalOpts(args)
	if len(g) == len(args) {
		return "", nil
	}
	return args[len(g)], args[len(g)+1:]
}

// git applies --refmap only with refspecs on the command line, so a fetch
// without any gets the same refspec appended.
func withRefmap(args []string) []string {
	sub, rest := subcommand(args)
	if sub != "fetch" {
		return args
	}
	remote, refspecs := "", 0
	for _, a := range rest {
		if strings.HasPrefix(a, "--refmap") || a == "--" {
			return args
		}
		if strings.HasPrefix(a, "-") {
			if !valuelessFetchFlags[a] {
				return args
			}
			continue
		}
		if remote == "" {
			remote = a
		} else {
			refspecs++
		}
	}
	if !remoteName(remote) {
		return args
	}
	spec := "+refs/heads/*:refs/remotes/" + remote + "/*"
	at := len(args) - len(rest)
	out := append([]string(nil), args[:at]...)
	out = append(out, "--refmap="+spec)
	out = append(out, args[at:]...)
	if refspecs == 0 {
		out = append(out, spec)
	}
	return out
}

var valuelessFetchFlags = map[string]bool{"-q": true, "--quiet": true, "-p": true, "--prune": true, "--prune-tags": true,
	"--no-tags": true, "-t": true, "--tags": true, "-f": true, "--force": true, "-v": true, "--verbose": true,
	"--dry-run": true, "--atomic": true, "--progress": true}

var allowedCloneFlags = map[string]bool{"-q": true, "--quiet": true, "-s": true, "--shared": true,
	"-n": true, "--no-checkout": true, "--progress": true, "--no-tags": true}

func prepareClone(args []string) ([]string, string, error) {
	g := globalOpts(args)
	var pos []string
	noCheckout, dashdash := false, false
	for _, a := range args[len(g)+1:] {
		switch {
		case dashdash || !strings.HasPrefix(a, "-"):
			pos = append(pos, a)
		case a == "--":
			dashdash = true
		case allowedCloneFlags[a]:
			noCheckout = noCheckout || a == "-n" || a == "--no-checkout"
		default:
			return args, "", fmt.Errorf("git: refusing clone option %s", strconv.Quote(a))
		}
	}
	if !noCheckout {
		return args, "", fmt.Errorf("git: refusing clone without --no-checkout")
	}
	if len(pos) != 2 {
		return args, "", fmt.Errorf("git: refusing clone: it needs a source and a destination")
	}
	src, dest := pos[0], pos[1]
	if !urlOK(src) {
		abs, err := filepath.Abs(src)
		if err != nil || !isDir(abs) {
			return args, "", fmt.Errorf("git: refusing clone of %s: not an https or ssh URL, nor a local repository", strconv.Quote(src))
		}
		if gd, _, err := locate(abs); err != nil {
			return args, "", err
		} else if gd == "" {
			return args, "", fmt.Errorf("git: refusing clone of %s: not a repository", abs)
		}
		if err := Check(abs); err != nil {
			return args, "", err
		}
		src = abs
	}
	dest, err := filepath.Abs(dest)
	if err != nil {
		return args, "", err
	}
	parent := filepath.Dir(dest)
	dir := neutralDir()
	if isDir(parent) {
		if err := Check(parent); err != nil {
			return args, "", err
		}
		dir = parent
	}
	out := append([]string(nil), args[:len(g)+1]...)
	for _, a := range args[len(g)+1:] {
		if strings.HasPrefix(a, "-") && a != "--" {
			out = append(out, a)
		}
	}
	return append(out, "--", src, dest), dir, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func Clone(url, dest string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := Command("clone", "--no-checkout", "--", url, dest)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if !HasRef(dest, "HEAD") {
		return nil
	}
	co := CommandIn(dest, "checkout", "--quiet")
	co.Stdout, co.Stderr = stdout, stderr
	return co.Run()
}

func Out(dir string, args ...string) (string, error) {
	out, err := Command(append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func OutIn(dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := Command(append([]string{"-C", dir}, args...)...)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

func Lines(dir string, fn func(line string), args ...string) error {
	cmd := Command(append([]string{"-C", dir}, args...)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReaderSize(out, 64<<10)
	for {
		l, rerr := r.ReadString('\n')
		if l != "" {
			fn(strings.TrimSuffix(l, "\n"))
		}
		if rerr != nil {
			break
		}
	}
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

func Ok(dir string, args ...string) bool {
	return Command(append([]string{"-C", dir}, args...)...).Run() == nil
}

func Run(dir string, args ...string) error {
	cmd := Command(append([]string{"-C", dir}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func Timeout(d time.Duration, dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return CommandContext(ctx, append([]string{"-C", dir}, args...)...).Run()
}

func HasRef(dir, ref string) bool {
	return Ok(dir, "rev-parse", "-q", "--verify", ref)
}

func BaseRef(dir, base string) string {
	if HasRef(dir, "refs/remotes/origin/"+base) {
		return "origin/" + base
	}
	if HasRef(dir, "refs/heads/"+base) {
		return base
	}
	if out, err := Out(dir, "for-each-ref", "--format=%(refname:short)", "--sort=refname", "refs/remotes/*/"+base); err == nil {
		if refs := strings.Fields(out); len(refs) > 0 {
			return refs[0]
		}
	}
	return base
}

func Dirty(dir string, extra ...string) bool {
	out, err := Out(dir, append([]string{"--no-optional-locks", "status", "--porcelain", "--untracked-files=normal"}, extra...)...)
	return err != nil || out != ""
}

func Unpushed(dir string) (bool, error) {
	n, err := Out(dir, "rev-list", "--count", "HEAD", "--branches", "--not", "--remotes")
	if err != nil {
		return false, err
	}
	stash, err := Out(dir, "stash", "list")
	if err != nil {
		return false, err
	}
	return n != "0" || stash != "", nil
}

func UnpushedHead(dir string) (bool, error) {
	n, err := Out(dir, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return false, err
	}
	return n != "0", nil
}

func Lost(dir, base string) (bool, error) {
	if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		stash, err := Out(dir, "stash", "list")
		if err != nil || stash != "" {
			return true, err
		}
		other, err := Out(dir, "rev-list", "--count", "--branches", "--not", "--remotes", "HEAD")
		if err != nil || other != "0" {
			return true, err
		}
	}
	unpushed, err := UnpushedHead(dir)
	if err != nil || !unpushed {
		return unpushed, err
	}
	if base == "" || !HasRef(dir, "refs/remotes/origin/"+base) {
		return true, nil
	}
	merged, err := MergedInto(dir, "origin/"+base)
	return !merged, err
}

func MergedInto(dir, base string) (bool, error) {
	mb, err := Out(dir, "merge-base", base, "HEAD")
	if err != nil {
		return false, err
	}
	names, err := Out(dir, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "--no-renames", "-z", mb, "HEAD")
	if err != nil {
		return false, err
	}
	var files []string
	for _, f := range strings.Split(names, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return true, nil
	}
	if Ok(dir, append([]string{"diff", "--no-ext-diff", "--no-textconv", "--quiet", base, "HEAD", "--"}, files...)...) {
		return true, nil
	}
	// git cherry skips merge commits, so a conflict resolution or evil merge would go unseen
	merges, err := Out(dir, "rev-list", "--count", "--merges", mb+"..HEAD")
	if err != nil {
		return false, err
	}
	if merges == "0" {
		if cherry, err := Out(dir, "cherry", base, "HEAD"); err == nil && !strings.Contains("\n"+cherry, "\n+") {
			return true, nil
		}
	}
	mine, err := patchIDs(dir, "diff", "--no-ext-diff", "--no-textconv", mb, "HEAD")
	if err != nil || len(mine) != 1 {
		return false, err
	}
	theirs, err := patchIDs(dir, "log", "--no-ext-diff", "--no-textconv", "--no-merges", "-p", "--format=commit %H", mb+".."+base)
	if err != nil {
		return false, err
	}
	for _, id := range theirs {
		if id == mine[0] {
			return true, nil
		}
	}
	return false, nil
}

func patchIDs(dir string, args ...string) ([]string, error) {
	patch, err := Command(append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return nil, err
	}
	cmd := Command("-C", dir, "patch-id", "--stable")
	cmd.Stdin = strings.NewReader(string(patch))
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id, _, ok := strings.Cut(l, " "); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func SharedClone(main, dest string) error {
	// git remote get-url would apply url.insteadOf
	url, err := Out(main, "config", "--get", "remote.origin.url")
	if err != nil {
		return err
	}
	if err := Command(localFlags("clone", "--quiet", "--shared", "--no-checkout", main, dest)...).Run(); err != nil {
		return err
	}
	conf, err := filepath.Abs(filepath.Join(dest, ".git", "config"))
	if err != nil {
		return err
	}
	if err := Command("config", "-f", conf, "remote.origin.url", url).Run(); err != nil {
		return err
	}
	return RefreshSharedClone(main, dest)
}

func RefreshSharedClone(main, dest string) error {
	if err := Check(main); err != nil {
		return err
	}
	return Command(localFlags("-C", dest, "fetch", "--quiet", "--prune", main,
		"+refs/remotes/origin/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")...).Run()
}

var ContainerOverride func() bool

func InContainer() bool {
	if ContainerOverride != nil {
		return ContainerOverride()
	}
	for _, f := range []string{"/run/.containerenv", "/.dockerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

func PopulatedSubmodule(dir string) string {
	gitDir, err := Out(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "?"
	}
	out, err := Out(dir, "ls-files", "--stage", "-z")
	if err != nil {
		return "?"
	}
	for _, e := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(e, "\t")
		if !ok || !strings.HasPrefix(meta, "160000 ") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(path), ".git")); err == nil {
			return path
		}
	}
	// git keeps submodule git dirs in <gitdir>/modules/ after the gitlink leaves the index
	if ents, err := os.ReadDir(filepath.Join(gitDir, "modules")); err == nil && len(ents) > 0 {
		return filepath.Join(gitDir, "modules", ents[0].Name())
	}
	return ""
}
