package cvewatch

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func osvRange(eco, name string, events ...map[string]string) []map[string]any {
	return []map[string]any{{"package": map[string]string{"name": name, "ecosystem": eco},
		"ranges": []map[string]any{{"type": "ECOSYSTEM", "events": events}}}}
}

func TestSweepScansImagesAndReleaseLines(t *testing.T) {
	critical := []map[string]string{{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:H"}}
	low := []map[string]string{{"type": "CVSS_V3", "score": "CVSS:3.1/AV:L/AC:H/PR:H/UI:N/S:U/C:L/I:N/A:N"}}
	f := &feeds{
		vulns: map[string]any{
			"ALPINE-CVE-2026-1000": map[string]any{"id": "ALPINE-CVE-2026-1000", "upstream": []string{"CVE-2026-1000"}, "severity": critical,
				"details": "openssl overflow", "affected": osvRange("Alpine:v3.23", "openssl", map[string]string{"introduced": "0"}, map[string]string{"fixed": "3.5.1-r0"})},
			"ALPINE-CVE-2026-1001": map[string]any{"id": "ALPINE-CVE-2026-1001", "upstream": []string{"CVE-2026-1001"}, "severity": low,
				"details": "openssl nit", "affected": osvRange("Alpine:v3.23", "openssl", map[string]string{"introduced": "0"}, map[string]string{"fixed": "3.5.1-r0"})},
			"USN-0001-1": map[string]any{"id": "USN-0001-1", "upstream": []string{"CVE-2026-1000", "CVE-2026-1001"}, "summary": "OpenSSL vulnerabilities",
				"affected": osvRange("Alpine:v3.23", "openssl", map[string]string{"introduced": "0"}, map[string]string{"fixed": "3.5.1-r0"})},
			"ALPINE-CVE-2026-2000": map[string]any{"id": "ALPINE-CVE-2026-2000", "upstream": []string{"CVE-2026-2000"}, "severity": low,
				"details": "zlib nit", "affected": osvRange("Alpine:v3.23", "zlib", map[string]string{"introduced": "0"})},
			"RHSA-2026:0100": map[string]any{"id": "RHSA-2026:0100", "upstream": []string{"CVE-2026-3000", "CVE-2026-3001"}, "severity": critical,
				"summary":  "Important: openssl security update",
				"affected": osvRange("Red Hat:enterprise_linux:9::baseos", "openssl-libs", map[string]string{"introduced": "0"}, map[string]string{"fixed": "1:3.0.7-29.el9_5"})},
			"GHSA-kc00": map[string]any{"id": "GHSA-kc00", "summary": "Keycloak token leak", "database_specific": map[string]any{"severity": "MODERATE"},
				"affected": osvRange("Maven", "org.keycloak:keycloak-services", map[string]string{"introduced": "0"}, map[string]string{"fixed": "26.0.8"})},
		},
		batch: func(q osvQuery) []string {
			switch q.Package.Ecosystem + " " + q.Package.Name {
			case "Alpine:v3.23 openssl":
				return []string{"ALPINE-CVE-2026-1000", "ALPINE-CVE-2026-1001", "USN-0001-1"}
			case "Alpine:v3.23 zlib":
				return []string{"ALPINE-CVE-2026-2000"}
			case "Red Hat:enterprise_linux:9::baseos openssl-libs":
				return []string{"RHSA-2026:0100"}
			case "Maven org.keycloak:keycloak-services":
				return []string{"GHSA-kc00"}
			}
			return nil
		},
		kev: []KEVEntry{{CVE: "CVE-2026-3001", Vendor: "OpenSSL", Product: "OpenSSL", Name: "OpenSSL bug", DateAdded: "2025-01-01"}},
		eol: map[string]any{
			"nginx":           []map[string]any{{"cycle": "1.29", "eol": "2026-05-13", "latest": "1.29.8"}, {"cycle": "1.31", "eol": false, "latest": "1.31.9"}},
			"alpine-linux":    []map[string]any{{"cycle": "3.23", "eol": "2027-11-01", "latest": "3.23.3"}},
			"eclipse-temurin": []map[string]any{{"cycle": "21", "eol": "2026-12-31", "latest": "21.0.9+10"}},
			"rhel":            []map[string]any{{"cycle": 9, "eol": "2032-05-31", "latest": "9.5"}},
		},
	}
	srv := f.server(t)
	defer srv.Close()
	src := sources(srv.URL)
	src.EOL = srv.URL + "/eol"
	sboms := map[string]SBOM{
		"nginx:1.29.6-alpine3.23-slim": {Distro: "alpine", DistroVersion: "3.23.2", Packages: []ImagePackage{
			{Type: "apk", Name: "libssl3", Version: "3.5.0-r0", Source: "openssl"},
			{Type: "apk", Name: "libcrypto3", Version: "3.5.0-r0", Source: "openssl"},
			{Type: "apk", Name: "zlib", Version: "1.3-r0", Source: "zlib"}}},
		"acme/keycloak:26.0.7-1": {Distro: "rhel", DistroVersion: "9.5", Packages: []ImagePackage{
			{Type: "rpm", Name: "openssl-libs", Version: "1:3.0.7-27.el9", Source: "openssl-3.0.7-27.el9.src.rpm"},
			{Type: "java-archive", Name: "keycloak-services", Version: "26.0.7", PURL: "pkg:maven/org.keycloak/keycloak-services@26.0.7"},
			{Type: "binary", Name: "openjdk", Version: "21.0.5+11-LTS"}}},
	}
	scan := func(_ context.Context, ref string) (SBOM, error) {
		if sb, ok := sboms[ref]; ok {
			return sb, nil
		}
		return SBOM{}, errors.New("manifest unknown")
	}
	inv := Inventory{
		Repos: []string{"bb/acme/stack"},
		Images: []ImageRef{{Repo: "bb/acme/stack", File: "compose.yml", Ref: "nginx:1.29.6-alpine3.23-slim"},
			{Repo: "bb/acme/stack", File: "compose.yml", Ref: "acme/keycloak:26.0.7-1"},
			{Repo: "bb/acme/stack", File: "Dockerfile", Ref: "acme/keycloak:26.0.7-1"},
			{Repo: "bb/acme/stack", File: "compose.yml", Ref: "acme/gone:1.0"}},
		Components: []Component{{Repo: "bb/acme/stack", File: "compose.yml", Product: "nginx", Image: "nginx:1.29.6-alpine3.23-slim", Version: "1.29.6-alpine3.23-slim"}},
	}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	rep := Sweep(context.Background(), inv, Options{Days: 30, Sources: src, Now: func() time.Time { return now }, Images: scan})

	if rep.Images != 2 || len(rep.Skips) != 0 {
		t.Fatalf("images %d, skips %+v", rep.Images, rep.Skips)
	}
	ids := map[string]Finding{}
	for _, x := range rep.Findings {
		ids[x.ID] = x
	}
	if _, ok := ids["USN-0001-1"]; ok || len(rep.Findings) != 2 {
		t.Errorf("findings %v: lower findings only in images belong in the image summary", slices.Sorted(maps.Keys(ids)))
	}
	var sums []string
	for _, s := range rep.ImageSummary {
		sums = append(sums, fmt.Sprintf("%s %d/%d %d", s.Image, s.Fixable, s.Lower, len(s.Where)))
	}
	if got := strings.Join(sums, "; "); got != "acme/keycloak:26.0.7-1 1/1 2; nginx:1.29.6-alpine3.23-slim 1/2 1" {
		t.Errorf("image summary: %s", got)
	}
	if a := ids["CVE-2026-1000"]; !a.Resolvable || len(a.Packages) != 1 || a.Packages[0].Fix != "3.5.1-r0" ||
		a.Packages[0].File != "compose.yml (nginx:1.29.6-alpine3.23-slim)" || a.Packages[0].Name != "openssl" {
		t.Errorf("alpine finding under its CVE: %+v", a)
	}
	rh := ids["RHSA-2026-0100"]
	if rh.KEV == nil || rh.Priority != "P1" || strings.Join(rh.Related, ",") != "CVE-2026-3000,CVE-2026-3001" ||
		len(rh.Packages) != 2 || rh.Packages[0].Fix != "1:3.0.7-29.el9_5" {
		t.Errorf("an RHSA keeps its own row, ranked by its CVEs: %+v", rh)
	}
	var gone bool
	for _, u := range rep.Unscanned {
		gone = gone || u.File == "compose.yml (acme/gone:1.0)" && strings.Contains(u.Reason, "manifest unknown")
	}
	if !gone {
		t.Errorf("an image that can't be scanned isn't listed: %+v", rep.Unscanned)
	}
	var lines []string
	for _, l := range rep.Lifecycle {
		s := l.Product + " " + l.Version + " " + l.Cycle
		switch {
		case l.Ended:
			s += " ended " + l.EOL
		case l.EndsSoon:
			s += " ends " + l.EOL
		case l.Behind:
			s += " behind " + l.Latest
		}
		lines = append(lines, s)
	}
	if got := strings.Join(lines, "; "); got != "nginx 1.29.6 1.29 ended 2026-05-13; java 21.0.5 21 ends 2026-12-31; alpine 3.23.2 3.23 behind 3.23.3; rhel 9.5 9" {
		t.Errorf("release lines: %s", got)
	}
}

func TestSyftScanParsesAndCaches(t *testing.T) {
	dir := t.TempDir()
	doc := `{"distro":{"id":"debian","versionID":"12.13"},"artifacts":[
	{"type":"deb","name":"libssl3","version":"3.0.17-1~deb12u2","purl":"pkg:deb/debian/libssl3@3.0.17-1~deb12u2","metadata":{"source":"openssl","sourceVersion":"3.0.17-1~deb12u2"}},
	{"type":"deb","name":"bash","version":"5.2.15-2+b8","purl":"pkg:deb/debian/bash","metadata":{"source":"","sourceVersion":"5.2.15-2"}},
	{"type":"java-archive","name":"jackson-databind","version":"2.17.2","purl":"pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.17.2","metadata":{}},
	{"type":"npm","name":"@scope/pkg","version":"1.2.3","purl":"pkg:npm/%40scope/pkg@1.2.3","metadata":{}},
	{"type":"go-module","name":"stdlib","version":"go1.24.4","purl":"pkg:golang/stdlib@1.24.4","metadata":{}},
	{"type":"binary","name":"node","version":"20.20.2","purl":"pkg:generic/node@20.20.2","metadata":{}}]}`
	if err := os.WriteFile(filepath.Join(dir, "doc.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "syft")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"scan registry:node:20-bookworm-slim\" ] || { echo bad args \"$@\" >&2; exit 2; }\ncat " + filepath.Join(dir, "doc.json") + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	s := Syft{Bin: bin, CacheDir: filepath.Join(dir, "cache"), Now: func() time.Time { return now }}
	sb, err := s.Scan(context.Background(), "node:20-bookworm-slim")
	if err != nil {
		t.Fatal(err)
	}
	pkgs, runtimes, note := sbomPackages(sb)
	var got []string
	for _, p := range pkgs {
		got = append(got, p.eco+" "+p.name+" "+p.version)
	}
	want := "Debian:12 openssl 3.0.17-1~deb12u2; Debian:12 bash 5.2.15-2; Maven com.fasterxml.jackson.core:jackson-databind 2.17.2; npm @scope/pkg 1.2.3; Go stdlib 1.24.4"
	if strings.Join(got, "; ") != want || strings.Join(runtimes, ",") != "node.js 20.20.2" || note != "" {
		t.Errorf("packages %q, runtimes %v, note %q", strings.Join(got, "; "), runtimes, note)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if again, err := s.Scan(context.Background(), "node:20-bookworm-slim"); err != nil || len(again.Packages) != 6 {
		t.Errorf("the cached SBOM wasn't used: %v", err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if _, err := s.Scan(context.Background(), "node:20-bookworm-slim"); err == nil {
		t.Error("a stale cache entry was used")
	}
	if _, err := (Syft{Bin: filepath.Join(dir, "none"), CacheDir: ""}).Scan(context.Background(), "x:1"); err == nil {
		t.Error("a missing binary passed")
	}
}

func TestDistroEcosystems(t *testing.T) {
	for _, c := range []struct{ id, v, want string }{
		{"alpine", "3.20.5", "Alpine:v3.20"}, {"debian", "12.13", "Debian:12"}, {"ubuntu", "26.04", "Ubuntu:26.04:LTS"},
		{"ubuntu", "25.10", "Ubuntu:25.10"}, {"ubuntu", "23.04", "Ubuntu:23.04"},
		{"rhel", "9.5", "Red Hat:enterprise_linux:9::baseos,Red Hat:enterprise_linux:9::appstream"},
		{"rocky", "9.4", "Rocky Linux:9"}, {"amzn", "2023", ""},
	} {
		if got := strings.Join(distroEcosystems(c.id, c.v), ","); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.id, c.v, got, c.want)
		}
	}
}

func TestInventoryRecordsPinnedImages(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "bb", "acme", "stack")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: nginx:1.31.9-alpine\n  app:\n    image: acme/app\n  db:\n    image: postgres@sha256:abc\n"
	if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := Collect(root, []string{"bb/acme/stack"})
	var refs []string
	for _, i := range inv.Images {
		refs = append(refs, i.Ref)
	}
	if strings.Join(refs, ",") != "nginx:1.31.9-alpine,postgres@sha256:abc" {
		t.Errorf("images %v", refs)
	}
	var floating bool
	for _, u := range inv.Unscanned {
		floating = floating || u.File == "docker-compose.yml (acme/app)" && strings.Contains(u.Reason, "floating tag")
	}
	if !floating {
		t.Errorf("a floating tag isn't listed: %+v", inv.Unscanned)
	}
}

func TestScanImagesNotesAndMissingSyft(t *testing.T) {
	refs := []ImageRef{{Repo: "r", File: "compose.yml", Ref: "amazonlinux:2023"}, {Repo: "r", File: "compose.yml", Ref: "x:1"}}
	scan := func(_ context.Context, ref string) (SBOM, error) {
		if ref == "x:1" {
			return SBOM{}, ErrNoSyft
		}
		return SBOM{Distro: "amzn", DistroVersion: "2023", Packages: []ImagePackage{{Type: "rpm", Name: "openssl", Version: "3.0.8-1.amzn2023"},
			{Type: "java-archive", PURL: "pkg:maven/a/b@1.0"}}}, nil
	}
	res := scanImages(context.Background(), scan, refs)
	if len(res.unscanned) != 1 || res.unscanned[0].File != "compose.yml (x:1)" || !strings.Contains(res.unscanned[0].Reason, "syft isn't installed") {
		t.Errorf("unscanned %+v", res.unscanned)
	}
	if !strings.Contains(res.notes["amazonlinux:2023"], "OSV has no ecosystem") || len(res.packages) != 1 {
		t.Errorf("an unsupported distro: notes %v, packages %+v", res.notes, res.packages)
	}
}

func TestMostSpecificKeepsDistinctRuntimes(t *testing.T) {
	c := func(v string) Component {
		return Component{Repo: "r", File: "f", Image: "i:1", Product: "java", Version: v}
	}
	var got []string
	for _, x := range mostSpecific([]Component{c("21"), c("21.0.13"), c("17.0.2"), c("17")}) {
		got = append(got, x.Version)
	}
	if strings.Join(got, ",") != "21.0.13,17.0.2" {
		t.Errorf("runtimes %v", got)
	}
}
