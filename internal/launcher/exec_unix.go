//go:build !windows

package launcher

import "syscall"

func execReplace(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
