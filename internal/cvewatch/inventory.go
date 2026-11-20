package cvewatch

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Package struct {
	Repo      string `json:"repo"`
	File      string `json:"file"`
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Dev       bool   `json:"dev,omitempty"`
	Image     string `json:"image,omitempty"`
}

type Component struct {
	Repo    string `json:"repo"`
	File    string `json:"file"`
	Product string `json:"product"`
	Image   string `json:"image,omitempty"`
	Version string `json:"version,omitempty"`
}

type Unscanned struct {
	Repo   string `json:"repo"`
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type Inventory struct {
	Repos      []string    `json:"repos"`
	Packages   []Package   `json:"packages"`
	Components []Component `json:"components"`
	Images     []ImageRef  `json:"images,omitempty"`
	Unscanned  []Unscanned `json:"unscanned"`
}

var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "testdata": true, "dist": true, "build": true,
	"target": true, ".venv": true, "venv": true, "__pycache__": true, ".terraform": true, ".direnv": true,
	".dart_tool": true, ".gradle": true, ".idea": true,
}

const maxManifestBytes = 32 << 20

type repoScan struct {
	repo, root string
	fsys       *os.Root
	inv        *Inventory
	files      map[string]bool
	dropped    []string
	noted      bool
}

func Collect(root string, repos []string) Inventory {
	inv := Inventory{Repos: append([]string(nil), repos...)}
	for _, r := range repos {
		s := &repoScan{repo: r, root: filepath.Join(root, filepath.FromSlash(r)), inv: &inv, files: map[string]bool{}}
		s.walk()
	}
	sort.SliceStable(inv.Packages, func(i, j int) bool {
		a, b := inv.Packages[i], inv.Packages[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Name < b.Name
	})
	return inv
}

func (s *repoScan) walk() {
	fsys, err := os.OpenRoot(s.root)
	if err != nil {
		s.unscanned(".", "the clone can't be opened: "+err.Error())
		return
	}
	defer fsys.Close()
	s.fsys = fsys
	var paths []string
	_ = fs.WalkDir(fsys.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p != "." {
				s.unscanned(p, "not readable: "+err.Error())
			}
			return nil
		}
		if d.IsDir() {
			if p != "." && (skippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != ".github") {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		s.files[p] = true
		paths = append(paths, p)
		return nil
	})
	sort.Strings(paths)
	for _, rel := range paths {
		s.scanFile(rel)
	}
}

func (s *repoScan) read(rel string) ([]byte, bool) {
	if st, err := s.fsys.Lstat(rel); err == nil && !st.Mode().IsRegular() {
		s.unscanned(rel, "not a regular file")
		return nil, false
	}
	f, err := s.fsys.Open(rel)
	if err != nil {
		s.unscanned(rel, "not readable: "+err.Error())
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	switch {
	case err != nil:
		s.unscanned(rel, "not readable: "+err.Error())
		return nil, false
	case !st.Mode().IsRegular():
		s.unscanned(rel, "not a regular file")
		return nil, false
	case st.Size() > maxManifestBytes:
		s.unscanned(rel, fmt.Sprintf("over %d MB: not read", maxManifestBytes>>20))
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		s.unscanned(rel, "not readable: "+err.Error())
		return nil, false
	}
	return b, true
}

func (s *repoScan) lines(rel string, each func(line string)) {
	b, ok := s.read(rel)
	if !ok {
		return
	}
	for l := range strings.Lines(string(b)) {
		each(strings.TrimRight(l, "\r\n"))
	}
}

func (s *repoScan) sibling(rel, name string) bool {
	return s.files[path.Join(path.Dir(rel), name)]
}

var pypiSeparators = regexp.MustCompile(`[-_.]+`)

func normName(eco, name string) string {
	switch eco {
	case "PyPI":
		return pypiSeparators.ReplaceAllString(strings.ToLower(name), "-")
	case "Packagist", "NuGet":
		return strings.ToLower(name)
	}
	return name
}

func localSource(version string) bool {
	return strings.Contains(version, ":") || strings.Contains(version, "/") || strings.HasSuffix(version, "-use.local")
}

func (s *repoScan) pkg(file, eco, name, version string, dev bool) {
	name, version = strings.TrimSpace(name), strings.TrimSpace(version)
	switch {
	case name == "":
		return
	case localSource(version):
		return
	case version == "" || strings.ContainsAny(version, "${}*^~<>=!, "):
		s.dropped = append(s.dropped, name)
		return
	}
	s.inv.Packages = append(s.inv.Packages, Package{Repo: s.repo, File: file, Ecosystem: eco, Name: normName(eco, name), Version: version, Dev: dev})
}

func (s *repoScan) component(file, product, image, version string) {
	s.inv.Components = append(s.inv.Components, Component{Repo: s.repo, File: file, Product: product, Image: image, Version: version})
}

func (s *repoScan) unscanned(file, reason string) {
	s.noted = true
	s.inv.Unscanned = append(s.inv.Unscanned, Unscanned{Repo: s.repo, File: file, Reason: reason})
}

var lockfiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"gradle.lockfile": true, "poetry.lock": true, "uv.lock": true, "Cargo.lock": true, "Gemfile.lock": true,
	"pubspec.lock": true, "composer.lock": true,
}

const maxDroppedNames = 5

func (s *repoScan) scanFile(rel string) {
	s.dropped, s.noted = nil, false
	before := len(s.inv.Packages)
	s.file(rel)
	if n := len(s.dropped); n > 0 {
		names := s.dropped
		if n > maxDroppedNames {
			names = append(names[:maxDroppedNames:maxDroppedNames], "…")
		}
		s.unscanned(rel, fmt.Sprintf("%d entries without a pinned version (%s): BOM, parent, range or unresolved property; not checked",
			n, strings.Join(names, ", ")))
	}
	if lockfiles[path.Base(rel)] && len(s.inv.Packages) == before && !s.noted {
		s.unscanned(rel, "no packages read from it: an empty or unknown lockfile format")
	}
}

func (s *repoScan) file(rel string) {
	base := path.Base(rel)
	lower := strings.ToLower(base)
	switch {
	case base == "go.mod":
		s.goMod(rel)
	case base == "package-lock.json" || base == "npm-shrinkwrap.json":
		s.npmLock(rel)
	case base == "yarn.lock":
		s.yarnLock(rel)
	case base == "pnpm-lock.yaml":
		s.pnpmLock(rel)
	case base == "package.json":
		if !s.sibling(rel, "package-lock.json") && !s.sibling(rel, "yarn.lock") && !s.sibling(rel, "pnpm-lock.yaml") &&
			!s.sibling(rel, "npm-shrinkwrap.json") && !s.inWorkspace(rel) {
			s.unscanned(rel, "no lockfile next to it: its versions aren't pinned")
		}
	case base == "pom.xml":
		s.pom(rel)
	case base == "gradle.lockfile":
		s.gradleLock(rel)
	case base == "build.gradle" || base == "build.gradle.kts":
		s.gradleBuild(rel)
	case strings.HasPrefix(lower, "requirements") && strings.HasSuffix(lower, ".txt"):
		s.requirements(rel)
	case base == "poetry.lock" || base == "uv.lock":
		s.tomlPackages(rel, "PyPI")
	case base == "Cargo.lock":
		s.tomlPackages(rel, "crates.io")
	case base == "Gemfile.lock":
		s.gemfileLock(rel)
	case base == "pubspec.lock":
		s.pubspecLock(rel)
	case base == "composer.lock":
		s.composerLock(rel)
	case base == "pyproject.toml":
		if !s.sibling(rel, "poetry.lock") && !s.sibling(rel, "requirements.txt") && !s.sibling(rel, "uv.lock") {
			s.unscanned(rel, "no poetry.lock, uv.lock or requirements.txt next to it: its versions aren't pinned")
		}
	case lower == "dockerfile" || lower == "containerfile" || strings.HasPrefix(lower, "dockerfile.") ||
		strings.HasSuffix(lower, ".dockerfile") || strings.HasSuffix(lower, ".containerfile"):
		s.dockerfile(rel)
	case strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml"):
		s.yamlImages(rel)
	}
}

func (s *repoScan) inWorkspace(rel string) bool {
	for d := path.Dir(path.Dir(rel)); ; d = path.Dir(d) {
		for _, l := range []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml"} {
			if s.files[path.Join(d, l)] {
				return true
			}
		}
		if d == "." || d == "/" {
			return false
		}
	}
}

var (
	goRequire   = regexp.MustCompile(`^\s*(?:require\s+)?([^\s()]+)\s+(v[0-9][^\s]*)(\s*//.*)?$`)
	goReplace   = regexp.MustCompile(`^\s*(?:replace\s+)?([^\s()]+)(?:\s+(v[0-9][^\s]*))?\s*=>\s*([^\s]+)(?:\s+(v[0-9][^\s]*))?\s*$`)
	goDirective = regexp.MustCompile(`^\s*go\s+([0-9][0-9.]*[0-9a-z]*)\s*$`)
	goToolchain = regexp.MustCompile(`^\s*toolchain\s+go([0-9][^\s]*)\s*$`)
)

type goReplacement struct {
	fromVersion, to, toVersion string
}

func (s *repoScan) goMod(rel string) {
	var goVer, tool, block string
	type req struct{ path, version string }
	var reqs []req
	replaces := map[string][]goReplacement{}
	s.lines(rel, func(l string) {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasSuffix(t, "(") && strings.Count(t, " ") == 1:
			block = strings.TrimSpace(strings.TrimSuffix(t, "("))
			return
		case t == ")":
			block = ""
			return
		}
		if m := goDirective.FindStringSubmatch(l); m != nil {
			goVer = m[1]
			return
		}
		if m := goToolchain.FindStringSubmatch(l); m != nil {
			tool = m[1]
			return
		}
		switch {
		case block == "replace" || strings.HasPrefix(t, "replace "):
			if m := goReplace.FindStringSubmatch(l); m != nil {
				replaces[m[1]] = append(replaces[m[1]], goReplacement{m[2], m[3], m[4]})
			}
		case block == "require" || strings.HasPrefix(t, "require "):
			if m := goRequire.FindStringSubmatch(l); m != nil {
				reqs = append(reqs, req{m[1], m[2]})
			}
		}
	})
	for _, r := range reqs {
		p, v, local := applyReplace(replaces[r.path], r.path, r.version)
		if !local {
			s.pkg(rel, "Go", p, v, false)
		}
	}
	if tool != "" {
		goVer = tool
	}
	if goVer != "" {
		if strings.Count(goVer, ".") == 1 {
			goVer += ".0"
		}
		s.pkg(rel, "Go", "stdlib", goVer, false)
	}
}

func applyReplace(rs []goReplacement, p, v string) (string, string, bool) {
	for _, r := range rs {
		if r.fromVersion != "" && r.fromVersion != v {
			continue
		}
		if r.toVersion == "" {
			return p, v, true
		}
		return r.to, r.toVersion, false
	}
	return p, v, false
}

func (s *repoScan) npmLock(rel string) {
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
			Dev     bool   `json:"dev"`
			Link    bool   `json:"link"`
		} `json:"packages"`
		Dependencies map[string]npmV1Dep `json:"dependencies"`
	}
	b, ok := s.read(rel)
	if !ok {
		return
	}
	if json.Unmarshal(b, &lock) != nil {
		s.unscanned(rel, "not valid JSON")
		return
	}
	if len(lock.Packages) > 0 {
		for k, p := range lock.Packages {
			i := strings.LastIndex(k, "node_modules/")
			if i < 0 || p.Link {
				continue
			}
			s.pkg(rel, "npm", k[i+len("node_modules/"):], p.Version, p.Dev)
		}
		return
	}
	var walk func(map[string]npmV1Dep)
	walk = func(m map[string]npmV1Dep) {
		for name, d := range m {
			s.pkg(rel, "npm", name, d.Version, d.Dev)
			walk(d.Dependencies)
		}
	}
	walk(lock.Dependencies)
}

type npmV1Dep struct {
	Version      string              `json:"version"`
	Dev          bool                `json:"dev"`
	Dependencies map[string]npmV1Dep `json:"dependencies"`
}

var (
	yarnHeader  = regexp.MustCompile(`^"?(@?[^@\s"]+)@`)
	yarnVersion = regexp.MustCompile(`^\s+version:?\s+"?([^"\s]+)"?\s*$`)
	yarnLocal   = regexp.MustCompile(`@(?:workspace|link|portal|file|patch|exec|git\+[a-z]+|https?):`)
)

func (s *repoScan) yarnLock(rel string) {
	name := ""
	s.lines(rel, func(l string) {
		if l == "" || strings.HasPrefix(l, "#") {
			return
		}
		if l[0] != ' ' {
			name = ""
			if m := yarnHeader.FindStringSubmatch(l); m != nil && !strings.HasPrefix(l, "__metadata") && !yarnLocal.MatchString(l) {
				name = m[1]
			}
			return
		}
		if m := yarnVersion.FindStringSubmatch(l); m != nil && name != "" {
			s.pkg(rel, "npm", name, m[1], false)
			name = ""
		}
	})
}

var (
	pnpmKey   = regexp.MustCompile(`^/?(@?[^@/][^@]*(?:/[^@]+)?)@([0-9][^(:]*)`)
	pnpmKeyV5 = regexp.MustCompile(`^/((?:@[^/]+/)?[^/@]+)/([0-9][^/_(]*)`)
)

func (s *repoScan) pnpmLock(rel string) {
	var lock struct {
		Packages map[string]struct {
			Dev bool `yaml:"dev"`
		} `yaml:"packages"`
	}
	b, ok := s.read(rel)
	if !ok {
		return
	}
	if yaml.Unmarshal(b, &lock) != nil {
		s.unscanned(rel, "not valid YAML")
		return
	}
	for k, p := range lock.Packages {
		k = strings.Trim(k, "'\"")
		if m := pnpmKey.FindStringSubmatch(k); m != nil {
			s.pkg(rel, "npm", m[1], m[2], p.Dev)
		} else if m := pnpmKeyV5.FindStringSubmatch(k); m != nil {
			s.pkg(rel, "npm", m[1], m[2], p.Dev)
		}
	}
}

type pomDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
}

type pomProps struct {
	Entries map[string]string
}

func (p *pomProps) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	p.Entries = map[string]string{}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var v string
			if err := d.DecodeElement(&v, &t); err != nil {
				return err
			}
			p.Entries[t.Name.Local] = strings.TrimSpace(v)
		case xml.EndElement:
			return nil
		}
	}
}

var pomRef = regexp.MustCompile(`\$\{([^}]+)\}`)

func (s *repoScan) pom(rel string) {
	var pom struct {
		GroupID    string   `xml:"groupId"`
		Version    string   `xml:"version"`
		Parent     pomDep   `xml:"parent"`
		Properties pomProps `xml:"properties"`
		Deps       []pomDep `xml:"dependencies>dependency"`
		Managed    []pomDep `xml:"dependencyManagement>dependencies>dependency"`
		Plugins    []pomDep `xml:"build>plugins>plugin"`
	}
	b, ok := s.read(rel)
	if !ok {
		return
	}
	if xml.Unmarshal(b, &pom) != nil {
		s.unscanned(rel, "not valid XML")
		return
	}
	props := pom.Properties.Entries
	if props == nil {
		props = map[string]string{}
	}
	props["project.version"] = pom.Version
	props["project.parent.version"] = pom.Parent.Version
	resolve := func(v string) string {
		for i := 0; i < 5 && strings.Contains(v, "${"); i++ {
			v = pomRef.ReplaceAllStringFunc(v, func(m string) string {
				if r, ok := props[m[2:len(m)-1]]; ok && r != "" {
					return r
				}
				return m
			})
		}
		return v
	}
	if pom.Parent.GroupID != "" && pom.Parent.Version != "" {
		pv := resolve(pom.Parent.Version)
		s.pkg(rel, "Maven", pom.Parent.GroupID+":"+pom.Parent.ArtifactID, pv, false)
		if pom.Parent.GroupID == "org.springframework.boot" {
			s.pkg(rel, "Maven", "org.springframework.boot:spring-boot", pv, false)
		}
	}
	for _, d := range pom.Deps {
		s.pkg(rel, "Maven", resolve(d.GroupID)+":"+d.ArtifactID, resolve(d.Version), d.Scope == "test")
	}
	for _, d := range append(pom.Managed, pom.Plugins...) {
		if d.Version != "" {
			s.pkg(rel, "Maven", resolve(d.GroupID)+":"+d.ArtifactID, resolve(d.Version), d.Scope == "test")
		}
	}
	for _, k := range []string{"java.version", "maven.compiler.release", "maven.compiler.target", "maven.compiler.source"} {
		if v := props[k]; v != "" && !strings.Contains(v, "$") {
			s.component(rel, "java", "", v)
			break
		}
	}
}

var gradleCoord = regexp.MustCompile(`["']([A-Za-z0-9_.\-]+):([A-Za-z0-9_.\-]+):([0-9][A-Za-z0-9_.\-+]*)["']`)

func (s *repoScan) gradleLock(rel string) {
	s.lines(rel, func(l string) {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "empty=") {
			return
		}
		coord, confs, _ := strings.Cut(l, "=")
		parts := strings.Split(coord, ":")
		if len(parts) != 3 {
			return
		}
		dev := confs != "" && !strings.Contains(confs, "untime") && !strings.Contains(confs, "ompileClasspath")
		s.pkg(rel, "Maven", parts[0]+":"+parts[1], parts[2], dev)
	})
}

func (s *repoScan) gradleBuild(rel string) {
	if s.sibling(rel, "gradle.lockfile") {
		return
	}
	b, ok := s.read(rel)
	if !ok {
		return
	}
	found := false
	for _, m := range gradleCoord.FindAllStringSubmatch(string(b), -1) {
		s.pkg(rel, "Maven", m[1]+":"+m[2], m[3], false)
		found = true
	}
	if !found {
		s.unscanned(rel, "no literal dependency versions and no gradle.lockfile (catalogs, BOMs and plugins aren't read)")
	}
}

var (
	reqLine = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-]+)(?:\[[^\]]*\])?\s*===?\s*([0-9][^\s;#]*)`)
	reqName = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9_.\-]*)`)
)

func (s *repoScan) requirements(rel string) {
	dev := strings.Contains(path.Base(rel), "dev") || strings.Contains(path.Base(rel), "test")
	s.lines(rel, func(l string) {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.Contains(t, "://") || strings.Contains(t, " @ ") {
			return
		}
		if m := reqLine.FindStringSubmatch(l); m != nil {
			s.pkg(rel, "PyPI", m[1], m[2], dev)
		} else if m := reqName.FindStringSubmatch(l); m != nil {
			s.pkg(rel, "PyPI", m[1], "", dev)
		}
	})
}

var (
	tomlName    = regexp.MustCompile(`^name\s*=\s*"([^"]+)"`)
	tomlVersion = regexp.MustCompile(`^version\s*=\s*"([^"]+)"`)
)

func (s *repoScan) tomlPackages(rel, eco string) {
	var name, version string
	in := false
	flush := func() {
		if in {
			s.pkg(rel, eco, name, version, false)
		}
		name, version = "", ""
	}
	s.lines(rel, func(l string) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			flush()
			in = l == "[[package]]"
			return
		}
		if !in {
			return
		}
		if m := tomlName.FindStringSubmatch(l); m != nil && name == "" {
			name = m[1]
		} else if m := tomlVersion.FindStringSubmatch(l); m != nil && version == "" {
			version = m[1]
		} else if strings.HasPrefix(l, "source = { path") || strings.HasPrefix(l, "source = { editable") ||
			strings.HasPrefix(l, "source = { virtual") || strings.HasPrefix(l, "source = { git") || strings.HasPrefix(l, "source = \"git+") {
			in = false
		}
	})
	flush()
}

var gemSpec = regexp.MustCompile(`^ {4}([A-Za-z0-9_.\-]+) \(([0-9][^)\-]*)(?:-[^)]*)?\)$`)

func (s *repoScan) gemfileLock(rel string) {
	section := ""
	s.lines(rel, func(l string) {
		if l != "" && l[0] != ' ' {
			section = strings.TrimSpace(l)
			return
		}
		if section != "GEM" {
			return
		}
		if m := gemSpec.FindStringSubmatch(l); m != nil {
			s.pkg(rel, "RubyGems", m[1], m[2], false)
		}
	})
}

func (s *repoScan) pubspecLock(rel string) {
	var lock struct {
		Packages map[string]struct {
			Version    string `yaml:"version"`
			Dependency string `yaml:"dependency"`
			Source     string `yaml:"source"`
		} `yaml:"packages"`
	}
	b, ok := s.read(rel)
	if !ok {
		return
	}
	if yaml.Unmarshal(b, &lock) != nil {
		s.unscanned(rel, "not valid YAML")
		return
	}
	for name, p := range lock.Packages {
		if p.Source == "hosted" {
			s.pkg(rel, "Pub", name, p.Version, strings.Contains(p.Dependency, "dev"))
		}
	}
}

func (s *repoScan) composerLock(rel string) {
	var lock struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages"`
		Dev []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages-dev"`
	}
	b, ok := s.read(rel)
	if !ok {
		return
	}
	if json.Unmarshal(b, &lock) != nil {
		s.unscanned(rel, "not valid JSON")
		return
	}
	for _, p := range lock.Packages {
		s.pkg(rel, "Packagist", p.Name, normVersion(p.Version), false)
	}
	for _, p := range lock.Dev {
		s.pkg(rel, "Packagist", p.Name, normVersion(p.Version), true)
	}
}

var (
	fromLine  = regexp.MustCompile(`(?i)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?`)
	imageLine = regexp.MustCompile(`^\s*-?\s*image:\s*["']?([^\s"'#]+)["']?`)
)

func (s *repoScan) dockerfile(rel string) {
	stages := map[string]bool{}
	s.lines(rel, func(l string) {
		m := fromLine.FindStringSubmatch(l)
		if m == nil {
			return
		}
		if m[2] != "" {
			stages[strings.ToLower(m[2])] = true
		}
		ref := m[1]
		if stages[strings.ToLower(ref)] || strings.EqualFold(ref, "scratch") {
			return
		}
		s.image(rel, ref)
	})
}

func (s *repoScan) yamlImages(rel string) {
	b, ok := s.read(rel)
	if !ok || !bytes.Contains(b, []byte("image:")) {
		return
	}
	for l := range strings.Lines(string(b)) {
		if m := imageLine.FindStringSubmatch(l); m != nil {
			s.image(rel, m[1])
		}
	}
}

func (s *repoScan) image(rel, ref string) {
	if strings.Contains(ref, "$") || strings.Contains(ref, "{{") {
		s.unscanned(rel, "image "+ref+" is templated: its version isn't known here")
		return
	}
	name, tag := splitImage(ref)
	if tag == "latest" && !strings.Contains(ref, "@") {
		s.inv.Unscanned = append(s.inv.Unscanned, Unscanned{Repo: s.repo, File: imageFile(rel, ref),
			Reason: "floating tag: pin a version to check the image's packages"})
	} else {
		s.inv.Images = append(s.inv.Images, ImageRef{Repo: s.repo, File: rel, Ref: ref})
	}
	product := imageProduct(name)
	if product == "" {
		return
	}
	s.component(rel, product, ref, tag)
}

func splitImage(ref string) (string, string) {
	ref, _, _ = strings.Cut(ref, "@")
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

var imageProducts = map[string]string{
	"nginx": "nginx", "nginx-unprivileged": "nginx", "openresty": "nginx", "nginx-ingress": "nginx",
	"ingress-nginx/controller": "ingress-nginx",
	"eclipse-temurin":          "java", "openjdk": "java", "amazoncorretto": "java", "ibm-semeru-runtimes": "java",
	"ibmjava": "java", "sapmachine": "java", "zulu-openjdk": "java", "maven": "java", "gradle": "java",
	"tomcat": "tomcat", "jetty": "jetty", "httpd": "apache-httpd", "haproxy": "haproxy", "traefik": "traefik",
	"envoy": "envoy", "node": "node.js", "python": "python", "php": "php", "ruby": "ruby", "golang": "go",
	"postgres": "postgresql", "postgresql": "postgresql", "mysql": "mysql", "mariadb": "mariadb",
	"redis": "redis", "valkey": "valkey", "mongo": "mongodb", "rabbitmq": "rabbitmq", "memcached": "memcached",
	"elasticsearch": "elasticsearch", "opensearch": "opensearch", "kafka": "kafka", "zookeeper": "zookeeper",
	"keycloak": "keycloak", "grafana": "grafana", "prometheus": "prometheus", "jenkins": "jenkins",
	"gitlab-runner": "gitlab-runner", "vault": "hashicorp-vault", "consul": "consul", "minio": "minio",
	"wordpress": "wordpress", "caddy": "caddy", "squid": "squid", "openssh-server": "openssh",
}

func imageProduct(name string) string {
	n := strings.ToLower(name)
	for _, p := range []string{"docker.io/", "library/", "public.ecr.aws/docker/library/"} {
		n = strings.TrimPrefix(n, p)
	}
	if p, ok := imageProducts[n]; ok {
		return p
	}
	parts := strings.Split(n, "/")
	if p, ok := imageProducts[parts[len(parts)-1]]; ok {
		return p
	}
	if len(parts) >= 2 {
		if p, ok := imageProducts[strings.Join(parts[len(parts)-2:], "/")]; ok {
			return p
		}
	}
	return ""
}
