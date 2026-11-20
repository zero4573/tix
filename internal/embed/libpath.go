package embed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var relWarn sync.Once

func warnRelOnce(d string) {
	relWarn.Do(func() {
		fmt.Fprintf(os.Stderr, "tix: ignoring relative entry %q of TIX_LIB_PATH (use absolute paths)\n", d)
	})
}

func libSearchDirs() []string {
	var dirs []string
	for _, list := range []string{os.Getenv("TIX_LIB_PATH"), DefaultLibPath} {
		for _, d := range strings.Split(list, string(os.PathListSeparator)) {
			if d == "" {
				continue
			}
			if !filepath.IsAbs(d) {
				warnRelOnce(d)
				continue
			}
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func findLib(dirs []string, name string) string {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
