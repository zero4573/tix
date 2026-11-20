//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func openRegular(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func openRegularIn(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func renameIn(root *os.Root, src, n, dest string) error {
	p, err := root.Open(filepath.Dir(n))
	if err != nil {
		return err
	}
	defer p.Close()
	d, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return err
	}
	defer d.Close()
	return unix.Renameat(int(p.Fd()), filepath.Base(n), int(d.Fd()), filepath.Base(dest))
}
