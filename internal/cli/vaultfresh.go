package cli

import (
	"path/filepath"

	"github.com/zero4573/tix/internal/vindex"
)

func init() { afterNoteWrite = func(vault, rel string) { refreshIndex(vault, rel) } }

var refreshIndex = vindex.Refresh

func relTo(vault, path string) string {
	if r, err := filepath.Rel(vault, path); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}
