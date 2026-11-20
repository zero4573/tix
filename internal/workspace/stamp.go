package workspace

import (
	"os"
	"path/filepath"
	"time"
)

type stamp struct {
	exists bool
	mod    time.Time
	size   int64
	fi     os.FileInfo
}

type Stamps map[string]stamp

func StateStamps(workRoot string) Stamps {
	s := Stamps{}
	for _, dir := range List(workRoot, false) {
		var st stamp
		if fi, err := os.Stat(filepath.Join(dir, ".agent-state")); err == nil {
			st = stamp{exists: true, mod: fi.ModTime(), size: fi.Size(), fi: fi}
		}
		s[dir] = st
	}
	return s
}

func (a Stamps) Changed(b Stamps) bool {
	if len(a) != len(b) {
		return true
	}
	for dir, x := range a {
		y, ok := b[dir]
		if !ok || x.exists != y.exists {
			return true
		}
		if !x.exists {
			continue
		}
		if !x.mod.Equal(y.mod) || x.size != y.size || !os.SameFile(x.fi, y.fi) {
			return true
		}
	}
	return false
}
