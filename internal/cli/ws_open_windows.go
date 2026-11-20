//go:build windows

package cli

import "os"

func openRegular(path string) (*os.File, error) { return os.Open(path) }

func openRegularIn(root *os.Root, name string) (*os.File, error) { return root.Open(name) }

// Windows has no renameat, so only moveTree's checkInside guards the parent swap
func renameIn(_ *os.Root, src, _, dest string) error { return os.Rename(src, dest) }
