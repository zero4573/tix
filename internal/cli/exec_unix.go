//go:build !windows

package cli

import "syscall"

func execReplace(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }

func ownGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
