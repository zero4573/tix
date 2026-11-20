package platform

import (
	"os"
	"path"
	"runtime"
	"strings"
)

type Dirs struct {
	Config string
	Cache  string
	Logs   string
}

const app = "tix"

func DirsFor(goos string, getenv func(string) string, home string) Dirs {
	join := path.Join
	if goos == "windows" {
		join = windowsJoin
	}
	var d Dirs
	switch goos {
	case "darwin":
		lib := join(home, "Library")
		d = Dirs{join(lib, "Application Support", app), join(lib, "Caches", app), join(lib, "Logs", app)}
	case "windows":
		roaming, local := getenv("AppData"), getenv("LocalAppData")
		if roaming == "" {
			roaming = join(home, "AppData", "Roaming")
		}
		if local == "" {
			local = join(home, "AppData", "Local")
		}
		d = Dirs{join(roaming, app), join(local, app, "cache"), join(local, app, "logs")}
	default:
		d = Dirs{join(home, ".config", app), join(home, ".cache", app), join(home, ".local", "state")}
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		d.Config = join(x, app)
	}
	if x := getenv("XDG_CACHE_HOME"); x != "" {
		d.Cache = join(x, app)
	}
	if x := getenv("XDG_STATE_HOME"); x != "" {
		d.Logs = x
	}
	if x := getenv("TIX_CONFIG_DIR"); x != "" {
		d.Config = x
	}
	if x := getenv("TIX_CACHE"); x != "" {
		d.Cache = x
	}
	if x := getenv("TIX_STATE_DIR"); x != "" {
		d.Logs = x
	}
	return d
}

func windowsJoin(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" && out[len(out)-1] != '\\' {
			out += `\`
		}
		out += p
	}
	return out
}

func Home() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func Current() Dirs { return DirsFor(runtime.GOOS, os.Getenv, Home()) }

const legacyApp = "claude-tickets"

func LegacyDirsFor(goos string, getenv func(string) string, home string) Dirs {
	noOverrides := func(k string) string {
		switch k {
		case "TIX_CONFIG_DIR", "TIX_CACHE", "TIX_STATE_DIR":
			return ""
		}
		return getenv(k)
	}
	d := DirsFor(goos, noOverrides, home)
	withLegacyAppName := func(p string) string {
		sep := "/"
		if goos == "windows" {
			sep = `\`
		}
		parts := splitOn(p, sep)
		for i := len(parts) - 1; i >= 0 && i >= len(parts)-2; i-- {
			if parts[i] == app {
				parts[i] = legacyApp
				return joinOn(parts, sep)
			}
		}
		return ""
	}
	return Dirs{withLegacyAppName(d.Config), withLegacyAppName(d.Cache), withLegacyAppName(d.Logs)}
}

type Migration struct {
	From, To string
	Err      error
}

func MigrateLegacy(goos string, getenv func(string) string, home string) []Migration {
	cur, old := DirsFor(goos, getenv, home), LegacyDirsFor(goos, getenv, home)
	pairs := []struct {
		env      string
		from, to string
	}{
		{"TIX_CONFIG_DIR", old.Config, cur.Config},
		{"TIX_CACHE", old.Cache, cur.Cache},
		{"TIX_STATE_DIR", old.Logs, cur.Logs},
	}
	var out []Migration
	for _, p := range pairs {
		if getenv(p.env) != "" || p.from == "" || p.from == p.to {
			continue
		}
		if st, err := os.Stat(p.from); err != nil || !st.IsDir() {
			continue
		}
		if _, err := os.Lstat(p.to); err == nil {
			continue
		}
		err := os.MkdirAll(dirOf(p.to, goos), 0o755)
		if err == nil {
			err = os.Rename(p.from, p.to)
		}
		out = append(out, Migration{p.from, p.to, err})
	}
	if goos == "windows" {
		legacyLocalAppDataDir := dirOf(old.Cache, goos)
		_ = os.Remove(legacyLocalAppDataDir)
	}
	return out
}

func splitOn(p, sep string) []string { return strings.Split(p, sep) }

func joinOn(parts []string, sep string) string { return strings.Join(parts, sep) }

func dirOf(p, goos string) string {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	parts := splitOn(p, sep)
	if len(parts) <= 1 {
		return p
	}
	return joinOn(parts[:len(parts)-1], sep)
}
