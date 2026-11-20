//go:build windows

package cli

import (
	"errors"
	"syscall"
)

func execReplace(path string, argv, env []string) error { return errors.New("no exec on windows") }

func ownGroup() *syscall.SysProcAttr { return nil }
