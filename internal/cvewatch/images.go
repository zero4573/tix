package cvewatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ImageRef struct {
	Repo string `json:"repo"`
	File string `json:"file"`
	Ref  string `json:"ref"`
}

type SBOM struct {
	Scanned       string         `json:"scanned"`
	Distro        string         `json:"distro,omitempty"`
	DistroVersion string         `json:"distroVersion,omitempty"`
	Packages      []ImagePackage `json:"packages"`
}

type ImagePackage struct {
	Type          string `json:"type"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Source        string `json:"source,omitempty"`
	SourceVersion string `json:"sourceVersion,omitempty"`
	PURL          string `json:"purl,omitempty"`
}

type ImageScanner func(ctx context.Context, ref string) (SBOM, error)

var ErrNoSyft = errors.New("syft isn't installed: container images' packages aren't checked")

type Syft struct {
	Bin      string
	CacheDir string
	TTL      time.Duration
	Now      func() time.Time
}

const (
	defaultImageTTL = 7 * 24 * time.Hour
	imageTimeout    = 10 * time.Minute
	imageWorkers    = 2
	maxSBOMBytes    = 1 << 30
)

func (s Syft) Scan(ctx context.Context, ref string) (SBOM, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = defaultImageTTL
	}
	sum := sha256.Sum256([]byte(ref))
	cache := ""
	if s.CacheDir != "" {
		cache = filepath.Join(s.CacheDir, hex.EncodeToString(sum[:16])+".json")
		if sb, ok := readSBOMCache(cache, now(), ttl); ok {
			return sb, nil
		}
	}
	bin := s.Bin
	if bin == "" {
		p, err := exec.LookPath("syft")
		if err != nil {
			return SBOM{}, ErrNoSyft
		}
		bin = p
	}
	sb, err := runSyft(ctx, bin, ref)
	if err != nil {
		return SBOM{}, err
	}
	sb.Scanned = now().UTC().Format(time.RFC3339)
	if cache != "" {
		writeSBOMCache(cache, sb)
	}
	return sb, nil
}

func readSBOMCache(path string, now time.Time, ttl time.Duration) (SBOM, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SBOM{}, false
	}
	var sb SBOM
	if json.Unmarshal(b, &sb) != nil {
		return SBOM{}, false
	}
	t, err := time.Parse(time.RFC3339, sb.Scanned)
	if err != nil || now.Sub(t) > ttl || now.Before(t) {
		return SBOM{}, false
	}
	return sb, true
}

func writeSBOMCache(path string, sb SBOM) {
	b, err := json.Marshal(sb)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sbom-*")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(f.Name(), path) != nil {
		_ = os.Remove(f.Name())
	}
}

type syftDoc struct {
	Distro struct {
		ID        string `json:"id"`
		VersionID string `json:"versionID"`
	} `json:"distro"`
	Artifacts []struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Version  string `json:"version"`
		PURL     string `json:"purl"`
		Metadata struct {
			Source        string `json:"source"`
			SourceVersion string `json:"sourceVersion"`
			Origin        string `json:"originPackage"`
		} `json:"metadata"`
	} `json:"artifacts"`
}

func runSyft(ctx context.Context, bin, ref string) (SBOM, error) {
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	env, cleanup := syftEnv()
	defer cleanup()
	cmd := exec.CommandContext(ctx, bin, "scan", "registry:"+ref, "-o", "syft-json", "-q")
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return SBOM{}, err
	}
	if err := cmd.Start(); err != nil {
		return SBOM{}, err
	}
	var doc syftDoc
	derr := json.NewDecoder(io.LimitReader(out, maxSBOMBytes)).Decode(&doc)
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return SBOM{}, fmt.Errorf("syft: %v: %s", err, lastLine(stderr.String()))
	}
	if derr != nil {
		return SBOM{}, fmt.Errorf("syft output: %v", derr)
	}
	sb := SBOM{Distro: doc.Distro.ID, DistroVersion: doc.Distro.VersionID, Packages: []ImagePackage{}}
	for _, a := range doc.Artifacts {
		src := a.Metadata.Source
		if a.Type == "apk" {
			src = a.Metadata.Origin
		}
		sb.Packages = append(sb.Packages, ImagePackage{Type: a.Type, Name: a.Name, Version: a.Version,
			Source: src, SourceVersion: a.Metadata.SourceVersion, PURL: a.PURL})
	}
	return sb, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return truncate(strings.TrimSpace(lines[len(lines)-1]), 300)
}

func syftEnv() ([]string, func()) {
	env := append(os.Environ(), "SYFT_CHECK_FOR_APP_UPDATE=false")
	noop := func() {}
	if os.Getenv("DOCKER_CONFIG") != "" {
		return env, noop
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".docker", "config.json")); err == nil {
			return env, noop
		}
	}
	auth := podmanAuthFile()
	if auth == "" {
		return env, noop
	}
	dir, err := os.MkdirTemp("", "tix-syft-")
	if err != nil {
		return env, noop
	}
	if os.Symlink(auth, filepath.Join(dir, "config.json")) != nil {
		_ = os.RemoveAll(dir)
		return env, noop
	}
	return append(env, "DOCKER_CONFIG="+dir), func() { _ = os.RemoveAll(dir) }
}

func podmanAuthFile() string {
	var candidates []string
	if f := os.Getenv("REGISTRY_AUTH_FILE"); f != "" {
		candidates = append(candidates, f)
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		candidates = append(candidates, filepath.Join(d, "containers", "auth.json"))
	}
	if d, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(d, "containers", "auth.json"))
	}
	for _, f := range candidates {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	return ""
}

func distroEcosystems(id, version string) []string {
	parts := strings.Split(version, ".")
	major := parts[0]
	majMin := major
	if len(parts) > 1 {
		majMin = parts[0] + "." + parts[1]
	}
	if major == "" {
		return nil
	}
	switch id {
	case "alpine":
		return []string{"Alpine:v" + majMin}
	case "debian":
		return []string{"Debian:" + major}
	case "ubuntu":
		if len(parts) > 1 && parts[1] == "04" && len(major) == 2 && (major[1]-'0')%2 == 0 {
			return []string{"Ubuntu:" + majMin + ":LTS"}
		}
		return []string{"Ubuntu:" + majMin}
	case "rhel":
		return []string{"Red Hat:enterprise_linux:" + major + "::baseos", "Red Hat:enterprise_linux:" + major + "::appstream"}
	case "rocky":
		return []string{"Rocky Linux:" + major}
	case "almalinux":
		return []string{"AlmaLinux:" + major}
	}
	return nil
}

func distroOnly(id string) []string {
	switch id {
	case "wolfi":
		return []string{"Wolfi"}
	case "chainguard":
		return []string{"Chainguard"}
	}
	return nil
}

var purlEcosystems = map[string]string{
	"maven": "Maven", "npm": "npm", "golang": "Go", "pypi": "PyPI", "gem": "RubyGems", "cargo": "crates.io",
	"nuget": "NuGet", "composer": "Packagist", "pub": "Pub", "hex": "Hex",
}

var binaryProducts = map[string]string{
	"node": "node.js", "openjdk": "java", "java": "java", "python": "python", "nginx": "nginx",
	"redis": "redis", "redis-server": "redis", "postgresql": "postgresql", "postgres": "postgresql",
	"httpd": "apache-httpd", "haproxy": "haproxy", "memcached": "memcached", "traefik": "traefik",
	"mysql": "mysql", "mariadb": "mariadb", "php": "php", "ruby": "ruby", "go": "go", "erlang": "erlang",
}

type imagePkg struct{ eco, name, version string }

func parsePURL(p string) (typ, name, version string, ok bool) {
	rest, found := strings.CutPrefix(p, "pkg:")
	if !found {
		return "", "", "", false
	}
	rest, _, _ = strings.Cut(rest, "#")
	rest, _, _ = strings.Cut(rest, "?")
	typ, path, found := strings.Cut(rest, "/")
	if !found {
		return "", "", "", false
	}
	path, version, _ = strings.Cut(path, "@")
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if u, err := url.PathUnescape(s); err == nil {
			segs[i] = u
		}
	}
	if v, err := url.PathUnescape(version); err == nil {
		version = v
	}
	switch typ {
	case "maven":
		if len(segs) != 2 {
			return "", "", "", false
		}
		name = segs[0] + ":" + segs[1]
	default:
		name = strings.Join(segs, "/")
	}
	return typ, name, version, name != "" && version != ""
}

func sbomPackages(sb SBOM) ([]imagePkg, []string, string) {
	osEcos := distroEcosystems(sb.Distro, sb.DistroVersion)
	if osEcos == nil {
		osEcos = distroOnly(sb.Distro)
	}
	var out []imagePkg
	seen := map[imagePkg]bool{}
	var runtimes []string
	osSkipped := 0
	for _, p := range sb.Packages {
		switch p.Type {
		case "apk", "deb", "rpm":
			if osEcos == nil {
				osSkipped++
				continue
			}
			name, version := p.Name, p.Version
			if p.Source != "" && p.Type != "rpm" {
				name = p.Source
			}
			if p.SourceVersion != "" && p.Type == "deb" {
				version = p.SourceVersion
			}
			for _, eco := range osEcos {
				if p := (imagePkg{eco, name, version}); !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		case "binary", "java-jvm-install":
			if prod, ok := binaryProducts[strings.ToLower(p.Name)]; ok && p.Version != "" {
				runtimes = append(runtimes, prod+" "+p.Version)
			}
		default:
			typ, name, version, ok := parsePURL(p.PURL)
			if !ok {
				continue
			}
			if eco, known := purlEcosystems[typ]; known {
				out = append(out, imagePkg{eco, name, normVersion(version)})
			}
		}
	}
	note := ""
	if osSkipped > 0 {
		note = fmt.Sprintf("%d OS packages of %s %s: OSV has no ecosystem for that distro", osSkipped, sb.Distro, sb.DistroVersion)
	}
	return out, runtimes, note
}

func imageFile(file, ref string) string { return file + " (" + ref + ")" }

type imageResult struct {
	packages  []Package
	runtimes  []Component
	unscanned []Unscanned
	images    int
	scanned   map[string][]ImageRef
	notes     map[string]string
}

func scanImages(ctx context.Context, scan ImageScanner, refs []ImageRef) imageResult {
	res := imageResult{scanned: map[string][]ImageRef{}, notes: map[string]string{}}
	if scan == nil || len(refs) == 0 {
		return res
	}
	byRef := map[string][]ImageRef{}
	var order []string
	for _, r := range refs {
		if _, ok := byRef[r.Ref]; !ok {
			order = append(order, r.Ref)
		}
		byRef[r.Ref] = append(byRef[r.Ref], r)
	}
	sort.Strings(order)
	type scanned struct {
		sb  SBOM
		err error
	}
	results := make([]scanned, len(order))
	sem := make(chan struct{}, imageWorkers)
	var wg sync.WaitGroup
	for i, ref := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			sb, err := scan(ctx, ref)
			results[i] = scanned{sb, err}
		}()
	}
	wg.Wait()
	for i, ref := range order {
		r := results[i]
		where := byRef[ref]
		if r.err != nil {
			reason := "image " + ref + " not scanned: " + r.err.Error()
			if errors.Is(r.err, ErrNoSyft) {
				reason = "image " + ref + " not scanned: syft isn't installed"
			}
			for _, w := range where {
				res.unscanned = append(res.unscanned, Unscanned{Repo: w.Repo, File: imageFile(w.File, ref), Reason: reason})
			}
			continue
		}
		res.images++
		res.scanned[ref] = where
		pkgs, runtimes, note := sbomPackages(r.sb)
		if note != "" {
			res.notes[ref] = note
		}
		for _, w := range where {
			file := imageFile(w.File, ref)
			for _, p := range pkgs {
				res.packages = append(res.packages, Package{Repo: w.Repo, File: file, Ecosystem: p.eco, Name: p.name, Version: p.version, Image: ref})
			}
			if r.sb.Distro != "" && r.sb.DistroVersion != "" {
				res.runtimes = append(res.runtimes, Component{Repo: w.Repo, File: w.File, Product: r.sb.Distro, Image: ref, Version: r.sb.DistroVersion})
			}
			for _, rt := range runtimes {
				prod, ver, _ := strings.Cut(rt, " ")
				res.runtimes = append(res.runtimes, Component{Repo: w.Repo, File: w.File, Product: prod, Image: ref, Version: ver})
			}
		}
	}
	return res
}
