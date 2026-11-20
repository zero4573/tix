//go:build !windows

package gitx

import "golang.org/x/sys/unix"

func Writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }

func searchable(p string) bool { return unix.Access(p, unix.X_OK) == nil }
