package seccheck

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/gitx"
)

type Finding struct {
	New      bool
	Block    bool
	Tool     string
	Severity string
	Location string
	Rule     string
	Message  string
}

type Skip struct {
	Tool   string
	Reason string
}

type Report struct {
	Findings []Finding
	Skips    []Skip
}

func (r Report) Blocking() bool {
	return slices.ContainsFunc(r.Findings, func(f Finding) bool { return f.New && f.Block })
}

type Runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

type Options struct {
	RangeStart     string
	IncludeLegacy  bool
	ScannerTimeout time.Duration
	Run            Runner
	LookPath       func(name string) (string, error)
	OSV            string
	Client         *http.Client
}

const DefaultScannerTimeout = 10 * time.Minute

var (
	ignoredDirs         = []string{"vendor", "node_modules", "testdata"}
	gosecAlwaysBlocks   = []string{"G201", "G202"}
	npmBlockingSeverity = []string{"high", "critical"}
	slashSuppression    = regexp.MustCompile(`(?://|/\*)\s*(#nosec|gitleaks:allow|nolint:[a-z,]*gosec)`)
	hashSuppression     = regexp.MustCompile(`#\s*(nosec|gitleaks:allow)`)
	jvmSuppression      = regexp.MustCompile(`@(SuppressFBWarnings)\b`)
	jvmExts             = []string{".java", ".kt", ".scala", ".groovy"}
	slashCommentExts    = []string{".go", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".java", ".kt", ".c", ".h", ".cc", ".cpp", ".rs", ".swift", ".dart", ".scala", ".cs", ".php"}
	hashCommentExts     = []string{".py", ".rb", ".sh", ".bash", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".tf", ".pl", ".r", ".env"}
	proseExtensions     = []string{".md", ".txt", ".rst", ".adoc"}
	gitleaksConfigFiles = []string{".gitleaks.toml", ".gitleaksignore"}
	errScannerTimeout   = errors.New("timed out")
	stderrSummaryLimit  = 240
)

func execRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- the scanner names are fixed in this package, the args are flags and a verified commit
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), errScannerTimeout
	}
	if err != nil {
		if msg := stderrSummary(errb.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out.Bytes(), err
}

func stderrSummary(stderr string) string {
	msg := strings.Join(strings.Fields(stderr), " ")
	if len(msg) > stderrSummaryLimit {
		msg = msg[:stderrSummaryLimit] + "…"
	}
	return msg
}

type rangeChange struct {
	addedLines map[string]map[int]string
	commits    map[string]bool
}

func (c rangeChange) touched(match func(p string) bool) bool {
	for p := range c.addedLines {
		if match(p) {
			return true
		}
	}
	return false
}

func (c rangeChange) anyLineAdded(p string, first, last int) bool {
	for l := first; l <= last; l++ {
		if _, ok := c.addedLines[p][l]; ok {
			return true
		}
	}
	return false
}

func (c rangeChange) lineAddedContaining(p, s string) bool {
	for _, text := range c.addedLines[p] {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

func readRangeChange(dir, start string) (rangeChange, error) {
	c := rangeChange{addedLines: map[string]map[int]string{}, commits: map[string]bool{}}
	out, err := gitx.Out(dir, "-c", "core.quotePath=false", "diff", "--relative", "--no-ext-diff", "--no-textconv",
		"--no-color", "--no-renames", "-U0", start, "HEAD", "--")
	if err != nil {
		return c, fmt.Errorf("diff %s..HEAD: %w", start, err)
	}
	parseUnifiedDiff(out, c)
	revs, err := gitx.Out(dir, "rev-list", start+"..HEAD")
	if err != nil {
		return c, fmt.Errorf("rev-list %s..HEAD: %w", start, err)
	}
	for r := range strings.FieldsSeq(revs) {
		c.commits[r] = true
	}
	return c, nil
}

func parseUnifiedDiff(out string, c rangeChange) {
	file, line := "", 0
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = diffPath(strings.TrimPrefix(l, "+++ "))
		case strings.HasPrefix(l, "@@ "):
			line = newSideStartLine(l)
		case strings.HasPrefix(l, "+") && file != "":
			if c.addedLines[file] == nil {
				c.addedLines[file] = map[int]string{}
			}
			c.addedLines[file][line] = l[1:]
			line++
		}
	}
}

func diffPath(p string) string {
	// git ends a path that holds a space with a tab, and C-quotes one with special characters
	p = strings.TrimSuffix(p, "\t")
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			p = u
		}
	}
	if p == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(p, "b/")
}

func newSideStartLine(hunkHeader string) int {
	f := strings.Fields(hunkHeader)
	if len(f) < 3 || !strings.HasPrefix(f[2], "+") {
		return 0
	}
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(f[2], "+"), ",", 2)[0])
	return n
}

func Run(ctx context.Context, dir string, o Options) (Report, error) {
	if o.Run == nil {
		o.Run = execRunner
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.ScannerTimeout == 0 {
		o.ScannerTimeout = DefaultScannerTimeout
	}
	var rep Report
	c, err := readRangeChange(dir, o.RangeStart)
	if err != nil {
		return rep, err
	}
	files, err := gitx.Out(dir, "ls-files")
	if err != nil {
		return rep, fmt.Errorf("ls-files: %w", err)
	}
	tracked := strings.Split(files, "\n")
	s := scan{ctx: ctx, dir: dir, o: o, c: c, rep: &rep}
	s.gitleaks()
	for _, mod := range moduleDirsOf(tracked, "go.mod") {
		s.gosec(mod)
		s.govulncheck(mod)
	}
	for _, mod := range moduleDirsOf(tracked, "package-lock.json") {
		s.npmAudit(mod)
	}
	s.javaDependencies(tracked)
	s.suppressions()
	if !o.IncludeLegacy {
		rep.Findings = slices.DeleteFunc(rep.Findings, func(f Finding) bool { return !f.New })
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if a.New != b.New {
			return a.New
		}
		return a.Block && !b.Block
	})
	return rep, nil
}

func moduleDirsOf(tracked []string, fileName string) []string {
	var dirs []string
	for _, f := range tracked {
		if path.Base(f) != fileName {
			continue
		}
		if !slices.ContainsFunc(strings.Split(f, "/"), func(part string) bool { return slices.Contains(ignoredDirs, part) }) {
			dirs = append(dirs, path.Dir(f))
		}
	}
	sort.Strings(dirs)
	return dirs
}

type scan struct {
	ctx context.Context
	dir string
	o   Options
	c   rangeChange
	rep *Report
}

func (s scan) skip(tool, reason string) {
	s.rep.Skips = append(s.rep.Skips, Skip{Tool: tool, Reason: reason})
}

func (s scan) add(f Finding) { s.rep.Findings = append(s.rep.Findings, f) }

func (s scan) installed(tool string) bool {
	if _, err := s.o.LookPath(tool); err != nil {
		s.skip(tool, "not installed")
		return false
	}
	return true
}

func (s scan) run(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.o.ScannerTimeout)
	defer cancel()
	out, err := s.o.Run(ctx, dir, name, args...)
	if errors.Is(err, errScannerTimeout) {
		err = fmt.Errorf("timed out after %s", s.o.ScannerTimeout)
	}
	return out, err
}

func inModule(mod, p string) bool {
	return mod == "." || p == mod || strings.HasPrefix(p, mod+"/")
}

func inMod(mod, name string) string {
	if mod == "." {
		return name
	}
	return path.Join(mod, name)
}

func label(tool, mod string) string {
	if mod == "." {
		return tool
	}
	return tool + " " + mod
}

func (s scan) gitleaksLogOpts() string {
	if s.o.IncludeLegacy {
		return "HEAD"
	}
	return s.o.RangeStart + "..HEAD"
}

func (s scan) gitleaks() {
	if !s.installed("gitleaks") {
		return
	}
	out, err := s.run(s.dir, "gitleaks", "git", "--no-banner", "--redact", "--exit-code", "0",
		"--log-opts", s.gitleaksLogOpts(), "--report-format", "json", "--report-path", "-", ".")
	var leaks []struct {
		RuleID, Description, File, Commit string
		StartLine                         int
	}
	if reason := unusable(err, json.Unmarshal(bytes.TrimSpace(out), &leaks)); reason != "" {
		s.skip("gitleaks", reason)
		return
	}
	for _, l := range leaks {
		s.add(Finding{New: s.c.commits[l.Commit], Block: true, Tool: "gitleaks", Severity: "secret",
			Location: fmt.Sprintf("%s:%d", l.File, l.StartLine), Rule: l.RuleID,
			Message: l.Description + " (commit " + short(l.Commit) + ")"})
	}
}

func gosecBlocks(rule, severity, confidence string) bool {
	return slices.Contains(gosecAlwaysBlocks, rule) || (severity == "HIGH" && confidence != "LOW")
}

func lineSpan(gosecLine string) (first, last int) {
	from, to, ranged := strings.Cut(gosecLine, "-")
	first, _ = strconv.Atoi(from)
	last = first
	if ranged {
		last, _ = strconv.Atoi(to)
	}
	return first, max(first, last)
}

func (s scan) gosec(mod string) {
	tool := label("gosec", mod)
	isGo := func(p string) bool { return inModule(mod, p) && strings.HasSuffix(p, ".go") }
	if !s.o.IncludeLegacy && !s.c.touched(isGo) {
		return
	}
	if !s.installed("gosec") {
		return
	}
	out, err := s.run(filepath.Join(s.dir, filepath.FromSlash(mod)), "gosec", "-no-fail", "-fmt", "json", "./...")
	var res struct {
		Issues []struct {
			Severity   string `json:"severity"`
			Confidence string `json:"confidence"`
			RuleID     string `json:"rule_id"`
			Details    string `json:"details"`
			File       string `json:"file"`
			Line       string `json:"line"`
		} `json:"Issues"`
	}
	if reason := unusable(err, json.Unmarshal(bytes.TrimSpace(out), &res)); reason != "" {
		s.skip(tool, reason)
		return
	}
	for _, is := range res.Issues {
		rel := relativeTo(s.dir, is.File)
		first, last := lineSpan(is.Line)
		sev, conf := strings.ToUpper(is.Severity), strings.ToUpper(is.Confidence)
		s.add(Finding{New: s.c.anyLineAdded(rel, first, last), Block: gosecBlocks(is.RuleID, sev, conf),
			Tool: "gosec", Severity: strings.ToLower(sev), Location: fmt.Sprintf("%s:%d", rel, first),
			Rule: is.RuleID, Message: is.Details})
	}
}

func relativeTo(dir, file string) string {
	if !filepath.IsAbs(file) {
		return filepath.ToSlash(file)
	}
	root, _ := filepath.Abs(dir)
	for _, r := range []string{root, evalSymlinks(root)} {
		if rel, err := filepath.Rel(r, file); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(file)
}

func evalSymlinks(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

func (s scan) mainModulePath(mod string) string {
	text, err := gitx.Out(s.dir, "show", "--no-textconv", "HEAD:./"+inMod(mod, "go.mod"))
	if err != nil {
		return ""
	}
	for l := range strings.SplitSeq(text, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
			return strings.Trim(strings.TrimSpace(name), `"`)
		}
	}
	return ""
}

type vulnFrame struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Function string `json:"function"`
	Position *struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	} `json:"position"`
}

type vulnHit struct {
	id, fixed  string
	vulnerable vulnFrame
	isNew      bool
}

func (s scan) dependencyLineAdded(mod string, frame vulnFrame) bool {
	gomod := inMod(mod, "go.mod")
	if frame.Module == "stdlib" {
		return s.c.lineAddedContaining(gomod, "go ") || s.c.lineAddedContaining(gomod, "toolchain ")
	}
	return s.c.lineAddedContaining(gomod, frame.Module+" ")
}

func (s scan) callAdded(mod, mainModule string, trace []vulnFrame) bool {
	return slices.ContainsFunc(trace, func(f vulnFrame) bool {
		return f.Module == mainModule && f.Position != nil &&
			s.c.anyLineAdded(inMod(mod, f.Position.Filename), f.Position.Line, f.Position.Line)
	})
}

func (s scan) govulncheck(mod string) {
	tool := label("govulncheck", mod)
	gomod, gosum := inMod(mod, "go.mod"), inMod(mod, "go.sum")
	relevant := func(p string) bool {
		return p == gomod || p == gosum || (inModule(mod, p) && strings.HasSuffix(p, ".go"))
	}
	if !s.o.IncludeLegacy && !s.c.touched(relevant) {
		return
	}
	if !s.installed("govulncheck") {
		return
	}
	dir := filepath.Join(s.dir, filepath.FromSlash(mod))
	out, err := s.run(dir, "govulncheck", "-format", "json", "./...")
	if err != nil {
		s.skip(tool, err.Error())
		return
	}
	mainModule := s.mainModulePath(mod)
	summaries := map[string]string{}
	hits := map[string]*vulnHit{}
	var order []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m struct {
			OSV *struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
			} `json:"osv"`
			Finding *struct {
				OSV          string      `json:"osv"`
				FixedVersion string      `json:"fixed_version"`
				Trace        []vulnFrame `json:"trace"`
			} `json:"finding"`
		}
		jerr := dec.Decode(&m)
		if errors.Is(jerr, io.EOF) {
			break
		}
		if jerr != nil {
			s.skip(tool, "unreadable output: "+jerr.Error())
			return
		}
		if m.OSV != nil {
			summaries[m.OSV.ID] = m.OSV.Summary
		}
		f := m.Finding
		if f == nil || len(f.Trace) == 0 || f.Trace[0].Function == "" {
			continue
		}
		h := hits[f.OSV]
		if h == nil {
			h = &vulnHit{id: f.OSV, fixed: f.FixedVersion, vulnerable: f.Trace[0]}
			hits[f.OSV] = h
			order = append(order, f.OSV)
		}
		h.isNew = h.isNew || s.dependencyLineAdded(mod, f.Trace[0]) ||
			(f.Trace[0].Module != "stdlib" && s.callAdded(mod, mainModule, f.Trace))
	}
	for _, id := range order {
		h := hits[id]
		fix := "no fixed version"
		if h.fixed != "" {
			fix = "fixed in " + h.fixed
		}
		s.add(Finding{New: h.isNew, Block: true, Tool: "govulncheck", Severity: "vuln",
			Location: h.vulnerable.Module + "@" + h.vulnerable.Version, Rule: id,
			Message: strings.TrimSpace(summaries[id] + " (" + fix + ")")})
	}
}

func packageIsNew(before, after map[string][]string, pkg string) bool {
	return !slices.Equal(before[pkg], after[pkg])
}

func (s scan) npmAudit(mod string) {
	tool := label("npm audit", mod)
	lock := inMod(mod, "package-lock.json")
	if !s.o.IncludeLegacy && !s.c.touched(func(p string) bool { return p == lock }) {
		return
	}
	if !s.installed("npm") {
		return
	}
	// npm audit exits 1 whenever it finds an advisory, so only unreadable output is a failure
	out, err := s.run(filepath.Join(s.dir, filepath.FromSlash(mod)), "npm", "audit", "--json", "--omit=dev", "--package-lock-only")
	var res struct {
		Vulnerabilities map[string]struct {
			Severity string            `json:"severity"`
			Via      []json.RawMessage `json:"via"`
			Range    string            `json:"range"`
		} `json:"vulnerabilities"`
		Error *struct {
			Summary string `json:"summary"`
		} `json:"error"`
	}
	jerr := json.Unmarshal(bytes.TrimSpace(out), &res)
	switch {
	case res.Error != nil:
		s.skip(tool, res.Error.Summary)
		return
	case jerr != nil:
		s.skip(tool, unusable(err, jerr))
		return
	}
	before, after := s.lockedVersions(s.o.RangeStart+":./"+lock), s.lockedVersions("HEAD:./"+lock)
	names := make([]string, 0, len(res.Vulnerabilities))
	for n := range res.Vulnerabilities {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := res.Vulnerabilities[n]
		rule, title := firstAdvisory(v.Via)
		sev := strings.ToLower(v.Severity)
		s.add(Finding{New: packageIsNew(before, after, n), Block: slices.Contains(npmBlockingSeverity, sev),
			Tool: "npm audit", Severity: sev, Location: n + " " + v.Range, Rule: rule, Message: title})
	}
}

func firstAdvisory(via []json.RawMessage) (url, title string) {
	for _, raw := range via {
		var adv struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if json.Unmarshal(raw, &adv) == nil && adv.URL != "" {
			return adv.URL, adv.Title
		}
	}
	return "", "through a vulnerable dependency"
}

type lockEntry struct {
	Version      string               `json:"version"`
	Dependencies map[string]lockEntry `json:"dependencies"`
}

func (s scan) lockedVersions(object string) map[string][]string {
	versions := map[string][]string{}
	text, err := gitx.Out(s.dir, "show", "--no-textconv", object)
	if err != nil {
		return versions
	}
	var lock struct {
		Packages     map[string]lockEntry `json:"packages"`
		Dependencies map[string]lockEntry `json:"dependencies"`
	}
	if json.Unmarshal([]byte(text), &lock) != nil {
		return versions
	}
	addVersion := func(name, version string) {
		if !slices.Contains(versions[name], version) {
			versions[name] = append(versions[name], version)
		}
	}
	var walkLockV1 func(deps map[string]lockEntry)
	walkLockV1 = func(deps map[string]lockEntry) {
		for name, d := range deps {
			addVersion(name, d.Version)
			walkLockV1(d.Dependencies)
		}
	}
	walkLockV1(lock.Dependencies)
	for key, p := range lock.Packages {
		if i := strings.LastIndex(key, "node_modules/"); i >= 0 {
			addVersion(key[i+len("node_modules/"):], p.Version)
		}
	}
	for name := range versions {
		sort.Strings(versions[name])
	}
	return versions
}

func (s scan) suppressions() {
	files := make([]string, 0, len(s.c.addedLines))
	for p := range s.c.addedLines {
		files = append(files, p)
	}
	sort.Strings(files)
	for _, p := range files {
		if slices.Contains(gitleaksConfigFiles, path.Base(p)) {
			s.add(Finding{New: true, Tool: "suppression", Severity: "review", Location: p, Rule: "gitleaks-config",
				Message: "the range changes what gitleaks allows: check every entry"})
			continue
		}
		if slices.Contains(proseExtensions, strings.ToLower(path.Ext(p))) {
			continue
		}
		lines := make([]int, 0, len(s.c.addedLines[p]))
		for l := range s.c.addedLines[p] {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		for _, l := range lines {
			text := s.c.addedLines[p][l]
			if m := suppressionIn(p, text); m != nil {
				s.add(Finding{New: true, Tool: "suppression", Severity: "review", Location: fmt.Sprintf("%s:%d", p, l),
					Rule: m[1], Message: strings.TrimSpace(text)})
			}
		}
	}
}

func suppressionIn(file, text string) []string {
	ext := strings.ToLower(path.Ext(file))
	if slices.Contains(jvmExts, ext) {
		if m := jvmSuppression.FindStringSubmatch(text); m != nil {
			return m
		}
	}
	switch {
	case slices.Contains(slashCommentExts, ext):
		return slashSuppression.FindStringSubmatch(text)
	case slices.Contains(hashCommentExts, ext):
		return hashSuppression.FindStringSubmatch(text)
	}
	if m := slashSuppression.FindStringSubmatch(text); m != nil {
		return m
	}
	return hashSuppression.FindStringSubmatch(text)
}

func unusable(runErr, parseErr error) string {
	switch {
	case runErr != nil:
		return runErr.Error()
	case parseErr != nil:
		return "unreadable output: " + parseErr.Error()
	}
	return ""
}

func short(sha string) string {
	return sha[:min(7, len(sha))]
}

func (f Finding) Line() string {
	age, action, rule := "legacy", "warn", f.Rule
	if f.New {
		age = "new"
	}
	if f.Block {
		action = "block"
	}
	if rule == "" {
		rule = "-"
	}
	return strings.Join([]string{age, action, f.Tool, f.Severity, f.Location, rule, f.Message}, "  ")
}
