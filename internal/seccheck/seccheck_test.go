package seccheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Jane Doe", "GIT_AUTHOR_EMAIL=jane@example.com",
		"GIT_COMMITTER_NAME=Jane Doe", "GIT_COMMITTER_EMAIL=jane@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

func goModWithNet(netVersion string) string {
	return "module example.com/acme\n\ngo 1.24\n\nrequire (\n\tgolang.org/x/net " + netVersion + "\n\tgolang.org/x/text v0.3.0\n)\n"
}

func lockfile(entries ...string) string {
	return "{\n  \"packages\": {\n    " + strings.Join(entries, ",\n    ") + "\n  }\n}\n"
}

func baseThenChange(t *testing.T) (dir, base, head string) {
	dir = newRepo(t)
	write(t, dir, "go.mod", goModWithNet("v0.1.0"))
	write(t, dir, "main.go", "package main\n\nfunc main() {\n\told()\n}\n")
	write(t, dir, "package-lock.json", lockfile(`"node_modules/lodash": {"version": "4.17.20"}`))
	base = commitAll(t, dir, "base")
	write(t, dir, "go.mod", goModWithNet("v0.2.0"))
	write(t, dir, "main.go", "package main\n\nfunc main() {\n\told()\n\trun()\n}\n")
	write(t, dir, "package-lock.json", lockfile(`"node_modules/lodash": {"version": "4.17.20"}`, `"node_modules/minimist": {"version": "1.2.0"}`))
	head = commitAll(t, dir, "change")
	return dir, base, head
}

type fake struct {
	out   map[string]string
	err   map[string]error
	calls []string
}

func (f *fake) run(_ context.Context, _, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return []byte(f.out[name]), f.err[name]
}

func everywhere(string) (string, error) { return "/bin/true", nil }

func govulnStream(lines ...string) string { return strings.Join(lines, "\n") }

func scanners(head, base string) *fake {
	return &fake{out: map[string]string{
		"gitleaks": `[{"RuleID":"aws-access-token","Description":"AWS key","File":"config.go","StartLine":3,"Commit":"` + head + `"},
		              {"RuleID":"generic-api-key","Description":"API key","File":"old.env","StartLine":1,"Commit":"` + base + `"}]`,
		"gosec": `{"Issues":[{"severity":"HIGH","confidence":"HIGH","rule_id":"G204","details":"Subprocess launched with variable","file":"main.go","line":"5"},
		                     {"severity":"MEDIUM","confidence":"HIGH","rule_id":"G104","details":"Errors unhandled","file":"main.go","line":"4"},
		                     {"severity":"MEDIUM","confidence":"HIGH","rule_id":"G201","details":"SQL string formatting","file":"main.go","line":"4"},
		                     {"severity":"LOW","confidence":"HIGH","rule_id":"G307","details":"Deferred close","file":"main.go","line":"5"}]}`,
		"govulncheck": govulnStream(
			`{"config":{"scan_level":"symbol"}}`,
			`{"osv":{"id":"GO-2024-0001","summary":"HTTP/2 rapid reset"}}`,
			`{"osv":{"id":"GO-2024-0002","summary":"text panic"}}`,
			`{"osv":{"id":"GO-2024-0004","summary":"text accept-language DoS"}}`,
			`{"finding":{"osv":"GO-2024-0001","fixed_version":"v0.3.0","trace":[{"module":"golang.org/x/net","version":"v0.2.0","function":"Serve"},{"module":"example.com/acme","function":"main","position":{"filename":"main.go","line":4}}]}}`,
			`{"finding":{"osv":"GO-2024-0002","fixed_version":"v0.3.8","trace":[{"module":"golang.org/x/text","version":"v0.3.0","function":"Parse"},{"module":"example.com/acme","function":"main","position":{"filename":"main.go","line":4}}]}}`,
			`{"finding":{"osv":"GO-2024-0004","fixed_version":"v0.3.7","trace":[{"module":"golang.org/x/text","version":"v0.3.0","function":"ParseAcceptLanguage"},{"module":"example.com/acme","function":"run","position":{"filename":"main.go","line":5}}]}}`,
			`{"finding":{"osv":"GO-2024-0005","fixed_version":"v1.24.9","trace":[{"module":"stdlib","version":"v1.24.0","function":"Serve"},{"module":"example.com/acme","function":"run","position":{"filename":"main.go","line":5}}]}}`,
			`{"finding":{"osv":"GO-2024-0003","trace":[{"module":"golang.org/x/text","version":"v0.3.0"}]}}`),
		"npm": `{"vulnerabilities":{
			"minimist":{"severity":"critical","range":"<1.2.6","via":[{"title":"Prototype Pollution","url":"https://github.com/advisories/GHSA-xvch"}]},
			"lodash":{"severity":"high","range":"<4.17.21","via":[{"title":"Command Injection","url":"https://github.com/advisories/GHSA-35jh"}]}}}`,
	}, err: map[string]error{}}
}

func lines(rep Report) string {
	var got []string
	for _, f := range rep.Findings {
		got = append(got, f.Line())
	}
	return strings.Join(got, "\n")
}

func TestRunTellsNewFromLegacy(t *testing.T) {
	dir, base, head := baseThenChange(t)
	f := scanners(head, base)
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, IncludeLegacy: true, Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"new  block  gitleaks  secret  config.go:3  aws-access-token  AWS key (commit " + head[:7] + ")",
		"new  block  gosec  high  main.go:5  G204  Subprocess launched with variable",
		"new  block  govulncheck  vuln  golang.org/x/net@v0.2.0  GO-2024-0001  HTTP/2 rapid reset (fixed in v0.3.0)",
		"new  block  govulncheck  vuln  golang.org/x/text@v0.3.0  GO-2024-0004  text accept-language DoS (fixed in v0.3.7)",
		"new  block  npm audit  critical  minimist <1.2.6  https://github.com/advisories/GHSA-xvch  Prototype Pollution",
		"new  warn  gosec  low  main.go:5  G307  Deferred close",
		"legacy  block  gitleaks  secret  old.env:1  generic-api-key  API key (commit " + base[:7] + ")",
		"legacy  block  gosec  medium  main.go:4  G201  SQL string formatting",
		"legacy  block  govulncheck  vuln  golang.org/x/text@v0.3.0  GO-2024-0002  text panic (fixed in v0.3.8)",
		"legacy  block  govulncheck  vuln  stdlib@v1.24.0  GO-2024-0005  (fixed in v1.24.9)",
		"legacy  block  npm audit  high  lodash <4.17.21  https://github.com/advisories/GHSA-35jh  Command Injection",
		"legacy  warn  gosec  medium  main.go:4  G104  Errors unhandled",
	}, "\n")
	if got := lines(rep); got != want {
		t.Errorf("findings:\n%s\nwant:\n%s", got, want)
	}
	if !rep.Blocking() {
		t.Error("new blocking findings: Blocking() must be true")
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "--log-opts HEAD ") {
		t.Errorf("IncludeLegacy: gitleaks scans the whole history: %v", f.calls)
	}
}

func TestRunWithoutLegacyListsOnlyNew(t *testing.T) {
	dir, base, head := baseThenChange(t)
	f := scanners(head, base)
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range rep.Findings {
		if !x.New {
			t.Errorf("legacy finding listed: %s", x.Line())
		}
	}
	if len(rep.Findings) != 6 {
		t.Errorf("got %d findings, want the 6 new ones:\n%s", len(rep.Findings), lines(rep))
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "--log-opts "+base+"..HEAD") {
		t.Errorf("gitleaks scans only the range: %v", f.calls)
	}
}

func TestGovulncheckRunsWhenOnlyGoCodeChanged(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "go.mod", goModWithNet("v0.1.0"))
	write(t, dir, "main.go", "package main\n\nfunc main() {\n\told()\n}\n")
	base := commitAll(t, dir, "base")
	write(t, dir, "main.go", "package main\n\nfunc main() {\n\told()\n\trun()\n}\n")
	head := commitAll(t, dir, "call the vulnerable function")
	f := scanners(head, base)
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	got := lines(rep)
	if !strings.Contains(got, "new  block  govulncheck  vuln  golang.org/x/text@v0.3.0  GO-2024-0004") {
		t.Errorf("a call the range added into a vulnerable function is new:\n%s", got)
	}
	if strings.Contains(got, "GO-2024-0001") || strings.Contains(got, "GO-2024-0002") {
		t.Errorf("calls the range didn't add, with go.mod untouched, are legacy:\n%s", got)
	}
}

func TestGosecMatchesTheDiffsPaths(t *testing.T) {
	tests := []struct {
		name, file, module, scanDir, gosecLine string
	}{
		{name: "a name with a space", file: "my client.go", module: ".", scanDir: ".", gosecLine: "3"},
		{name: "a non-ASCII name", file: "clïent.go", module: ".", scanDir: ".", gosecLine: "3"},
		{name: "-C at a module below the root", file: "svc/client.go", module: "svc", scanDir: "svc", gosecLine: "3"},
		{name: "a multi-line finding whose first line is unchanged", file: "client.go", module: ".", scanDir: ".", gosecLine: "2-4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			write(t, dir, filepath.Join(tc.module, "go.mod"), "module example.com/m\n\ngo 1.24\n")
			write(t, dir, tc.file, "package m\n\nvar a = 1\n")
			base := commitAll(t, dir, "base")
			write(t, dir, tc.file, "package m\n\nvar a = 2\n")
			commitAll(t, dir, "change")
			scanDir := filepath.Join(dir, tc.scanDir)
			rel, _ := filepath.Rel(scanDir, filepath.Join(dir, tc.file))
			abs, _ := filepath.Abs(filepath.Join(dir, tc.file))
			run := func(_ context.Context, _, name string, _ ...string) ([]byte, error) {
				if name == "gosec" {
					return []byte(`{"Issues":[{"severity":"HIGH","confidence":"HIGH","rule_id":"G402","details":"TLS MinVersion too low.","file":"` + filepath.ToSlash(abs) + `","line":"` + tc.gosecLine + `"}]}`), nil
				}
				return []byte("[]"), nil
			}
			rep, err := Run(context.Background(), scanDir, Options{RangeStart: base, Run: run, LookPath: everywhere})
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Findings) != 1 || !rep.Findings[0].New || !strings.HasPrefix(rep.Findings[0].Location, filepath.ToSlash(rel)+":") {
				t.Errorf("want one new finding at %s:\n%s", rel, lines(rep))
			}
		})
	}
}

func TestSuppressionsAreReported(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.24\n")
	write(t, dir, "a.go", "package m\n")
	base := commitAll(t, dir, "base")
	write(t, dir, "a.go", "package m\n\nvar k = run(x) // #nosec G204 -- fixed binary\n")
	write(t, dir, ".gitleaks.toml", "[allowlist]\npaths = [\".*\"]\n")
	write(t, dir, "b.go", "package m\n\nvar markers = []string{\"#nosec\", \"gitleaks:allow\"}\n")
	write(t, dir, "notes.md", "Suppress with `// #nosec G204 -- why`.\n")
	commitAll(t, dir, "suppress")
	f := &fake{out: map[string]string{"gitleaks": "[]", "gosec": `{"Issues":[]}`, "govulncheck": `{"config":{}}`}, err: map[string]error{}}
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	want := "new  warn  suppression  review  .gitleaks.toml  gitleaks-config  the range changes what gitleaks allows: check every entry\n" +
		"new  warn  suppression  review  a.go:3  #nosec  var k = run(x) // #nosec G204 -- fixed binary"
	if got := lines(rep); got != want {
		t.Errorf("suppressions:\n%s\nwant:\n%s", got, want)
	}
	if rep.Blocking() {
		t.Error("a suppression warns, the review judges it")
	}
}

func TestNestedLockEntryIsNew(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "package-lock.json", lockfile(`"node_modules/a": {"version": "1.0.0"}`))
	base := commitAll(t, dir, "base")
	write(t, dir, "package-lock.json", lockfile(`"node_modules/a": {"version": "1.0.0"}`, `"node_modules/a/node_modules/b": {"version": "0.1.0"}`))
	commitAll(t, dir, "nested b")
	f := &fake{out: map[string]string{"gitleaks": "[]",
		"npm": `{"vulnerabilities":{"b":{"severity":"high","range":"<0.2.0","via":[{"title":"ReDoS","url":"https://github.com/advisories/GHSA-b"}]}}}`}, err: map[string]error{}}
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(rep); got != "new  block  npm audit  high  b <0.2.0  https://github.com/advisories/GHSA-b  ReDoS" {
		t.Errorf("a vulnerable package only nested in node_modules is new when the range adds it:\n%s", got)
	}
}

func TestNestedLockfileUnderC(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "web/package-lock.json", lockfile(`"node_modules/a": {"version": "1.0.0"}`))
	base := commitAll(t, dir, "base")
	write(t, dir, "web/package-lock.json", lockfile(`"node_modules/a": {"version": "1.0.1"}`))
	commitAll(t, dir, "bump a")
	f := &fake{out: map[string]string{"gitleaks": "[]",
		"npm": `{"vulnerabilities":{"a":{"severity":"high","range":"1.0.1","via":[{"title":"Bad","url":"https://github.com/advisories/GHSA-a"}]}}}`}, err: map[string]error{}}
	rep, err := Run(context.Background(), filepath.Join(dir, "web"), Options{RangeStart: base, Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(rep); got != "new  block  npm audit  high  a 1.0.1  https://github.com/advisories/GHSA-a  Bad" {
		t.Errorf("a version bump in a lockfile below -C is new:\n%s", got)
	}
}

func TestRunSkips(t *testing.T) {
	dir, _, _ := baseThenChange(t)
	write(t, dir, "README.md", "docs\n")
	commitAll(t, dir, "docs")
	f := &fake{out: map[string]string{"gitleaks": "[]"}, err: map[string]error{}}
	rep, err := Run(context.Background(), dir, Options{RangeStart: "HEAD~1", Run: f.run, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0], "gitleaks ") {
		t.Errorf("a docs-only range runs gitleaks alone: %v", f.calls)
	}
	if len(rep.Findings)+len(rep.Skips) != 0 || rep.Blocking() {
		t.Errorf("clean: %+v", rep)
	}

	gitleaksMissing := func(name string) (string, error) {
		if name == "gitleaks" {
			return "", errors.New("not found")
		}
		return "/bin/true", nil
	}
	f = &fake{out: map[string]string{"govulncheck": `{"config":{}}`},
		err: map[string]error{"govulncheck": errors.New("exit status 1: loading packages: missing go.sum entry")}}
	rep, err = Run(context.Background(), dir, Options{RangeStart: "HEAD~2", Run: f.run, LookPath: gitleaksMissing})
	if err != nil {
		t.Fatal(err)
	}
	var skips []string
	for _, s := range rep.Skips {
		skips = append(skips, s.Tool+": "+s.Reason)
	}
	got := strings.Join(skips, "\n")
	for _, want := range []string{"gitleaks: not installed", "govulncheck: exit status 1: loading packages: missing go.sum entry", "gosec: unreadable output"} {
		if !strings.Contains(got, want) {
			t.Errorf("skips %q: missing %q", got, want)
		}
	}
	if rep.Blocking() {
		t.Error("skips never block")
	}
}

func TestScannerTimeoutIsASkip(t *testing.T) {
	dir, base, _ := baseThenChange(t)
	hang := func(ctx context.Context, _, name string, _ ...string) ([]byte, error) {
		if name == "gitleaks" {
			<-ctx.Done()
			return nil, errScannerTimeout
		}
		return nil, errors.New("not run")
	}
	start := time.Now()
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, ScannerTimeout: 50 * time.Millisecond, Run: hang, LookPath: everywhere})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("a hanging scanner held the check for %s", time.Since(start))
	}
	if len(rep.Skips) == 0 || rep.Skips[0].Tool != "gitleaks" || rep.Skips[0].Reason != "timed out after 50ms" {
		t.Errorf("skips: %+v", rep.Skips)
	}
}

func TestNewSideStartLine(t *testing.T) {
	for h, want := range map[string]int{"@@ -1,2 +3,4 @@ x": 3, "@@ -0,0 +1 @@": 1, "@@ bad": 0} {
		if got := newSideStartLine(h); got != want {
			t.Errorf("%q: %d, want %d", h, got, want)
		}
	}
}
