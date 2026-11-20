package launcher

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

type Session struct {
	Dir       string
	Argv      []string
	Env       map[string]string
	PathFirst string
}

func Run(w Session) error {
	if len(w.Argv) == 0 {
		return errors.New("no command to run")
	}
	path, err := exec.LookPath(w.Argv[0])
	if err != nil {
		return err
	}
	if err := os.Chdir(w.Dir); err != nil {
		return err
	}
	env := mergeEnv(os.Environ(), withPath(w.Env, w.PathFirst, os.Getenv("PATH")))
	if execReplace(path, w.Argv, env) == nil {
		return nil
	}
	cmd := exec.Command(path, w.Argv[1:]...)
	cmd.Dir, cmd.Env = w.Dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// the Windows console sends Ctrl-C to both processes; the program handles it
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	return cmd.Run()
}

func withPath(env map[string]string, first, path string) map[string]string {
	if first == "" {
		return env
	}
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out["PATH"] = first
	if path != "" {
		out["PATH"] = first + string(os.PathListSeparator) + path
	}
	return out
}

func mergeEnv(environ []string, set map[string]string) []string {
	out := make([]string, 0, len(environ)+len(set))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; !ok {
			out = append(out, kv)
		}
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}
