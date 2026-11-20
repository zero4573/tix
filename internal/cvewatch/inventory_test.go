package cvewatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func has(ps []Package, eco, name, version string) bool {
	return slices.ContainsFunc(ps, func(p Package) bool {
		return p.Ecosystem == eco && p.Name == name && p.Version == version
	})
}

func TestCollectReadsEveryManifest(t *testing.T) {
	root := t.TempDir()
	r := "bitbucket/acme/api"
	write(t, root, r+"/go.mod", "module x\n\ngo 1.24\n\ntoolchain go1.24.3\n\nrequire (\n\tgolang.org/x/net v0.17.0\n\tgithub.com/a/b v1.2.3 // indirect\n)\n\nrequire github.com/c/d v0.1.0\n")
	write(t, root, r+"/web/package-lock.json", `{"packages":{"":{"name":"web"},"node_modules/lodash":{"version":"4.17.20"},"node_modules/a/node_modules/@s/b":{"version":"1.0.0","dev":true},"node_modules/local":{"link":true}}}`)
	write(t, root, r+"/web/package.json", `{}`)
	write(t, root, r+"/old/package-lock.json", `{"lockfileVersion":1,"dependencies":{"minimist":{"version":"0.0.8","dependencies":{"x":{"version":"1.0.0"}}}}}`)
	write(t, root, r+"/loose/package.json", `{}`)
	write(t, root, r+"/y/yarn.lock", "# yarn\n\n\"@babel/core@^7.0.0\", \"@babel/core@^7.1.0\":\n  version \"7.20.0\"\n  resolved \"x\"\n\nleft-pad@^1.0.0:\n  version \"1.3.0\"\n")
	write(t, root, r+"/p/pnpm-lock.yaml", "lockfileVersion: '9.0'\npackages:\n  /express@4.17.1:\n    resolution: {}\n  '@types/node@20.1.0':\n    dev: true\n")
	write(t, root, r+"/svc/pom.xml", `<project><groupId>com.acme</groupId><version>1.0</version>
<parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.1.0</version></parent>
<properties><java.version>17</java.version><log4j.version>2.14.1</log4j.version></properties>
<dependencies>
<dependency><groupId>org.apache.logging.log4j</groupId><artifactId>log4j-core</artifactId><version>${log4j.version}</version></dependency>
<dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>4.12</version><scope>test</scope></dependency>
<dependency><groupId>org.x</groupId><artifactId>managed</artifactId></dependency>
</dependencies></project>`)
	write(t, root, r+"/g/build.gradle", "dependencies { implementation 'com.google.guava:guava:30.0-jre' }\n")
	write(t, root, r+"/g2/build.gradle.kts", "dependencies { implementation(libs.guava) }\n")
	write(t, root, r+"/py/requirements.txt", "requests==2.25.0\nflask>=2\nDjango[argon2]==3.2.0 ; python_version > '3'\n")
	write(t, root, r+"/py2/poetry.lock", "[[package]]\nname = \"urllib3\"\nversion = \"1.26.0\"\n\n[package.dependencies]\nname = \"nope\"\n\n[[package]]\nname = \"idna\"\nversion = \"2.10\"\n")
	write(t, root, r+"/rs/Cargo.lock", "version = 3\n\n[[package]]\nname = \"tokio\"\nversion = \"1.0.0\"\n")
	write(t, root, r+"/rb/Gemfile.lock", "GEM\n  specs:\n    rails (6.0.0)\n      actionpack (= 6.0.0)\n    nokogiri (1.10.0-x86_64-linux)\n")
	write(t, root, r+"/app/pubspec.lock", "packages:\n  http:\n    dependency: \"direct main\"\n    source: hosted\n    version: \"0.13.0\"\n  mine:\n    source: path\n    version: \"1.0.0\"\n")
	write(t, root, r+"/php/composer.lock", `{"packages":[{"name":"guzzlehttp/guzzle","version":"v7.0.0"}],"packages-dev":[]}`)
	write(t, root, r+"/Dockerfile", "FROM --platform=linux/amd64 eclipse-temurin:17-jre AS base\nFROM base\nFROM nginx:1.25.3-alpine\nFROM scratch\nFROM ${IMG}\nFROM registry.acme.io/team/custom:1\n")
	write(t, root, r+"/deploy/compose.yml", "services:\n  web:\n    image: \"nginx:1.27\"\n  db:\n    image: postgres:16\n")
	write(t, root, r+"/node_modules/ignored/package-lock.json", `{"packages":{"node_modules/evil":{"version":"1.0.0"}}}`)
	write(t, root, r+"/vendor/x/go.mod", "module y\n\nrequire golang.org/x/text v0.3.0\n")

	inv := Collect(root, []string{r})
	ps := inv.Packages
	for _, want := range []struct{ eco, name, version string }{
		{"Go", "golang.org/x/net", "v0.17.0"}, {"Go", "github.com/a/b", "v1.2.3"}, {"Go", "github.com/c/d", "v0.1.0"},
		{"Go", "stdlib", "1.24.3"}, {"npm", "lodash", "4.17.20"}, {"npm", "@s/b", "1.0.0"}, {"npm", "minimist", "0.0.8"},
		{"npm", "x", "1.0.0"}, {"npm", "@babel/core", "7.20.0"}, {"npm", "left-pad", "1.3.0"}, {"npm", "express", "4.17.1"},
		{"npm", "@types/node", "20.1.0"}, {"Maven", "org.apache.logging.log4j:log4j-core", "2.14.1"},
		{"Maven", "junit:junit", "4.12"}, {"Maven", "org.springframework.boot:spring-boot", "3.1.0"},
		{"Maven", "com.google.guava:guava", "30.0-jre"}, {"PyPI", "requests", "2.25.0"}, {"PyPI", "django", "3.2.0"},
		{"PyPI", "urllib3", "1.26.0"}, {"PyPI", "idna", "2.10"}, {"crates.io", "tokio", "1.0.0"},
		{"RubyGems", "rails", "6.0.0"}, {"RubyGems", "nokogiri", "1.10.0"}, {"Pub", "http", "0.13.0"},
		{"Packagist", "guzzlehttp/guzzle", "7.0.0"},
	} {
		if !has(ps, want.eco, want.name, want.version) {
			t.Errorf("missing %s %s %s", want.eco, want.name, want.version)
		}
	}
	for _, name := range []string{"evil", "golang.org/x/text", "flask", "org.x:managed", "mine", "nope", "local"} {
		if slices.ContainsFunc(ps, func(p Package) bool { return p.Name == name }) {
			t.Errorf("%s should not be collected", name)
		}
	}
	if i := slices.IndexFunc(ps, func(p Package) bool { return p.Name == "@s/b" }); i < 0 || !ps[i].Dev {
		t.Errorf("@s/b should be dev")
	}

	var images []string
	for _, c := range inv.Components {
		images = append(images, c.Product+"="+c.Version)
	}
	for _, want := range []string{"java=17-jre", "nginx=1.25.3-alpine", "nginx=1.27", "postgresql=16", "java=17"} {
		if !slices.Contains(images, want) {
			t.Errorf("components %v: missing %s", images, want)
		}
	}
	if len(images) != 5 {
		t.Errorf("components %v: want 5 (stages, scratch, templated and unknown images skipped)", images)
	}

	var unscanned []string
	for _, u := range inv.Unscanned {
		unscanned = append(unscanned, u.File)
	}
	for _, want := range []string{"loose/package.json", "g2/build.gradle.kts", "Dockerfile", "svc/pom.xml", "py/requirements.txt"} {
		if !slices.Contains(unscanned, want) {
			t.Errorf("unscanned %v: missing %s", unscanned, want)
		}
	}
	if slices.Contains(unscanned, "web/package.json") {
		t.Errorf("web/package.json has a lockfile: %v", unscanned)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.17.0", "0.23.0", -1}, {"1.22.0-0", "1.22.0", -1}, {"1.21.9", "1.21.10", -1}, {"2.10", "2.9", 1},
		{"1.0", "1.0.0", 0}, {"go1.24.3", "1.24.3", 0}, {"30.0-jre", "30.0", 0}, {"1.0.0-beta", "1.0.0-alpha", 1},
		{"1.0.0-canary.10", "1.0.0-canary.9", 1}, {"1.0.0-beta.2", "1.0.0-beta.10", -1}, {"2.15.0.Final", "2.15.0", 0},
		{"5.3.18.RELEASE", "5.3.18", 0}, {"25.0.2+incompatible", "25.0.2", 0}, {"1.0.post1", "1.0", 1},
		{"1.0rc1", "1.0", -1}, {"1.0a1", "1.0b1", -1}, {"1.0.dev1", "1.0a1", -1}, {"2.0.0-rc.1", "1.9.9", 1},
		{"32.0.0-android", "31.1-jre", 1}, {"1.2.3-SNAPSHOT", "1.2.3", -1}, {"0.0.0-20240101-abcdef", "0.0.0", -1},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCVSS3(t *testing.T) {
	for _, c := range []struct {
		vector  string
		score   float64
		network bool
	}{
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, true},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H", 10.0, true},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L", 5.3, true},
		{"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H", 7.8, false},
		{"CVSS:3.0/AV:N/AC:H/PR:N/UI:R/S:C/C:L/I:L/A:N", 4.7, true},
	} {
		got, ok := parseCVSS3(c.vector)
		if !ok || got.Score != c.score || got.Network != c.network {
			t.Errorf("parseCVSS3(%s) = %+v %v, want %v %v", c.vector, got, ok, c.score, c.network)
		}
	}
	if _, ok := parseCVSS3("CVSS:4.0/AV:N"); ok {
		t.Error("a CVSS 4 vector is not scored")
	}
}

func TestIntervalFix(t *testing.T) {
	ev := []map[string]string{{"introduced": "0"}, {"fixed": "1.21.9"}, {"introduced": "1.22.0-0"}, {"fixed": "1.22.2"}}
	for _, c := range []struct {
		v, fix string
		in     bool
	}{
		{"1.21.0", "1.21.9", true}, {"1.21.9", "", false}, {"1.22.1", "1.22.2", true}, {"1.22.2", "", false},
	} {
		fix, in := intervalFix("", ev, c.v)
		if fix != c.fix || in != c.in {
			t.Errorf("intervalFix(%s) = %q %v, want %q %v", c.v, fix, in, c.fix, c.in)
		}
	}
	fix, in := intervalFix("", []map[string]string{{"introduced": "1.0"}, {"last_affected": "1.5"}}, "1.2")
	if fix != "" || !in {
		t.Errorf("last_affected: %q %v", fix, in)
	}
}

func TestFixForPrereleaseRange(t *testing.T) {
	var r osvRecord
	if err := json.Unmarshal([]byte(`{"affected":[{"package":{"name":"next","ecosystem":"npm"},"ranges":[{"type":"SEMVER","events":[{"introduced":"15.0.0-canary.0"},{"fixed":"15.0.0-canary.177"}]}]}]}`), &r); err != nil {
		t.Fatal(err)
	}
	if fix := fixFor([]osvRecord{r}, pkgKey{"npm", "next", "15.0.0-canary.20"}); fix != "15.0.0-canary.177" {
		t.Errorf("fixFor(next 15.0.0-canary.20) = %q", fix)
	}
}

func reasons(inv Inventory) map[string]string {
	m := map[string]string{}
	for _, u := range inv.Unscanned {
		m[u.File] += u.Reason + "; "
	}
	return m
}

func TestCollectReportsWhatItCannotRead(t *testing.T) {
	root := t.TempDir()
	r := "r"
	write(t, root, r+"/pom.xml", `<project><groupId>com.acme</groupId><version>1.0</version><dependencies>
<dependency><groupId>org.apache.logging.log4j</groupId><artifactId>log4j-core</artifactId></dependency>
<dependency><groupId>com.x</groupId><artifactId>y</artifactId><version>${y.version}</version></dependency>
<dependency><groupId>com.x</groupId><artifactId>z</artifactId><version>1.0</version></dependency>
</dependencies></project>`)
	write(t, root, r+"/p5/pnpm-lock.yaml", "lockfileVersion: 5.4\npackages:\n  /express/4.17.1:\n    resolution: {}\n  /@types/node/20.1.0_abc:\n    dev: true\n")
	write(t, root, r+"/g/go.mod", "module x\n\ngo 1.24\n\nrequire (\n\tgolang.org/x/net v0.17.0 // "+strings.Repeat("x", 70000)+"\n\tgolang.org/x/text v0.3.0\n)\n")
	write(t, root, r+"/empty/yarn.lock", "# yarn lockfile v1\n\n")
	write(t, root, r+"/weird/Cargo.lock", "this is not toml at all\n")
	write(t, root, r+"/py/requirements.txt", "flask>=2\n-r base.txt\n# c\ngit+https://x/y.git#egg=y\nmine @ file:///x\nrequests==2.25.0\n")

	inv := Collect(root, []string{r})
	got := reasons(inv)
	for file, want := range map[string]string{
		"pom.xml":             "2 entries without a pinned version (org.apache.logging.log4j:log4j-core, com.x:y)",
		"empty/yarn.lock":     "no packages read",
		"weird/Cargo.lock":    "no packages read",
		"py/requirements.txt": "1 entries without a pinned version (flask)",
	} {
		if !strings.Contains(got[file], want) {
			t.Errorf("%s: unscanned %q, want %q", file, got[file], want)
		}
	}
	for _, want := range []struct{ eco, name, version string }{
		{"npm", "express", "4.17.1"}, {"npm", "@types/node", "20.1.0"}, {"Go", "golang.org/x/net", "v0.17.0"},
		{"Go", "golang.org/x/text", "v0.3.0"}, {"Maven", "com.x:z", "1.0"}, {"PyPI", "requests", "2.25.0"},
	} {
		if !has(inv.Packages, want.eco, want.name, want.version) {
			t.Errorf("missing %+v in %+v", want, inv.Packages)
		}
	}
}

func TestCollectAppliesGoReplacesAndSkipsLocalSources(t *testing.T) {
	root := t.TempDir()
	write(t, root, "r/go.mod", "module x\n\ngo 1.24\n\nrequire (\n\tgolang.org/x/net v0.17.0\n\texample.com/local v1.0.0\n\texample.com/pinned v1.0.0\n)\n\nreplace golang.org/x/net => golang.org/x/net v0.30.0\n\nreplace (\n\texample.com/local => ../local\n\texample.com/pinned v0.9.0 => example.com/fork v2.0.0\n)\n")
	write(t, root, "r/web/yarn.lock", "__metadata:\n  version: 6\n\n\"app@workspace:.\":\n  version: 0.0.0-use.local\n\n\"left-pad@npm:^1.3.0\":\n  version: 1.3.0\n\n\"mine@file:../mine::locator=app\":\n  version: 1.0.0\n")
	write(t, root, "r/rb/Gemfile.lock", "PATH\n  remote: ../engine\n  specs:\n    engine (0.1.0)\n\nGIT\n  remote: https://x/y.git\n  specs:\n    forked (2.0.0)\n\nGEM\n  remote: https://rubygems.org/\n  specs:\n    rails (6.0.0)\n")
	write(t, root, "r/npm/package-lock.json", `{"packages":{"node_modules/a":{"version":"1.0.0"},"node_modules/b":{"version":"file:../b"}}}`)
	inv := Collect(root, []string{"r"})
	for _, want := range []struct{ eco, name, version string }{
		{"Go", "golang.org/x/net", "v0.30.0"}, {"Go", "example.com/pinned", "v1.0.0"}, {"npm", "left-pad", "1.3.0"},
		{"RubyGems", "rails", "6.0.0"}, {"npm", "a", "1.0.0"},
	} {
		if !has(inv.Packages, want.eco, want.name, want.version) {
			t.Errorf("missing %+v in %+v", want, inv.Packages)
		}
	}
	for _, name := range []string{"example.com/local", "app", "mine", "engine", "forked", "b"} {
		if slices.ContainsFunc(inv.Packages, func(p Package) bool { return p.Name == name }) {
			t.Errorf("%s is a local, workspace or git source and stays on the machine: %+v", name, inv.Packages)
		}
	}
	if has(inv.Packages, "Go", "golang.org/x/net", "v0.17.0") {
		t.Error("the replaced x/net version is checked")
	}
	if len(inv.Unscanned) != 0 {
		t.Errorf("unscanned: %+v", inv.Unscanned)
	}
}

func TestCollectStaysInsideTheClone(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret/go.mod", "module s\n\nrequire example.com/secret-internal v1.0.0\n")
	write(t, root, "r/README.md", "x")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "r", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret", "go.mod"), filepath.Join(root, "r", "go.mod")); err != nil {
		t.Fatal(err)
	}
	inv := Collect(root, []string{"r"})
	if len(inv.Packages) != 0 {
		t.Errorf("read through a symlink out of the clone: %+v", inv.Packages)
	}
}

func TestCollectSkipsOversizedFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "r/package-lock.json", `{"packages":{"node_modules/a":{"version":"1.0.0"}}}`+strings.Repeat(" ", maxManifestBytes))
	inv := Collect(root, []string{"r"})
	if got := reasons(inv)["package-lock.json"]; !strings.Contains(got, "over 32 MB") || strings.Contains(got, "no packages") {
		t.Errorf("unscanned %q", got)
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	if got := ellipsis("aé", 2); got != "a…" {
		t.Errorf("ellipsis cut inside a rune: %q", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("%q", got)
	}
}
