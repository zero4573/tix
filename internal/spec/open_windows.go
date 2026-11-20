//go:build windows

package spec

import "os"

// Windows has no O_NOFOLLOW; the walk already skipped symlinks.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}

func isSymlinkRefusal(error) bool { return false }
