//go:build windows

package gitx

import "os"

func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tix-writable-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// git for Windows' access() drops X_OK, so it only tests that p exists
func searchable(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
