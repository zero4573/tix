package seccheck

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zero4573/tix/internal/cvewatch"
	"github.com/zero4573/tix/internal/gitx"
)

const javaTool = "osv maven"

var (
	javaManifests      = []string{"pom.xml", "build.gradle", "build.gradle.kts", "gradle.lockfile"}
	javaBlockingLevels = []string{"high", "critical"}
)

func isJavaManifest(p string) bool { return slices.Contains(javaManifests, path.Base(p)) }

func (s scan) javaDependencies(tracked []string) {
	if !slices.ContainsFunc(tracked, isJavaManifest) {
		return
	}
	if !s.o.IncludeLegacy && !s.c.touched(isJavaManifest) {
		return
	}
	now := mavenPackages(s.dir)
	if len(now) == 0 {
		return
	}
	src := cvewatch.DefaultSources()
	if s.o.OSV != "" {
		src.OSV = s.o.OSV
	}
	if s.o.Client != nil {
		src.Client = s.o.Client
	}
	findings, err := cvewatch.CheckPackages(s.ctx, src, now)
	if err != nil {
		s.skip(javaTool, err.Error())
		return
	}
	before := s.mavenPackagesAt(s.o.RangeStart)
	for _, f := range findings {
		sev := strings.ToLower(f.Severity)
		if sev == "moderate" {
			sev = "medium"
		}
		for _, p := range f.Packages {
			where := p.File + " " + p.Name + "@" + p.Version
			if p.Fix != "" {
				where += " -> " + p.Fix
			}
			s.add(Finding{New: !before[javaKey(p.File, p.Name, p.Version)], Block: slices.Contains(javaBlockingLevels, sev),
				Tool: javaTool, Severity: sev, Location: where, Rule: f.ID, Message: f.Summary})
		}
	}
}

func javaKey(file, name, version string) string { return file + "|" + name + "|" + version }

func mavenPackages(root string) []cvewatch.Package {
	inv := cvewatch.Collect(root, []string{"."})
	return slices.DeleteFunc(inv.Packages, func(p cvewatch.Package) bool {
		return p.Ecosystem != "Maven" || !isJavaManifest(p.File)
	})
}

func (s scan) mavenPackagesAt(rev string) map[string]bool {
	seen := map[string]bool{}
	names, err := gitx.Out(s.dir, "ls-tree", "-r", "--name-only", rev)
	if err != nil {
		return seen
	}
	tmp, err := os.MkdirTemp("", "tix-sec-check-")
	if err != nil {
		return seen
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, name := range strings.Split(names, "\n") {
		if !isJavaManifest(name) {
			continue
		}
		text, err := gitx.Out(s.dir, "show", "--no-textconv", rev+":./"+name)
		if err != nil {
			continue
		}
		dest := filepath.Join(tmp, filepath.FromSlash(name))
		if os.MkdirAll(filepath.Dir(dest), 0o700) != nil || os.WriteFile(dest, []byte(text), 0o600) != nil {
			continue
		}
	}
	for _, p := range mavenPackages(tmp) {
		seen[javaKey(p.File, p.Name, p.Version)] = true
	}
	return seen
}
