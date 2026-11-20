package cvewatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func finding(id, priority string, major, resolvable bool, file string) Finding {
	fix := ""
	if resolvable {
		fix = "2.0"
	}
	return Finding{ID: id, Priority: priority, Major: major, Resolvable: resolvable, Severity: "HIGH", Summary: id + " summary",
		Packages: []Affected{{Repo: "bb/acme/api", File: file, Ecosystem: "npm", Name: "lib", Version: "1.0", Fix: fix}}}
}

func report(swept string, fs []Finding, ws []Watch, skips ...Skip) Report {
	return Report{Swept: swept, Repos: []string{"bb/acme/api"}, Findings: fs, Watch: ws, Skips: append([]Skip{}, skips...),
		Components: []Component{{Repo: "bb/acme/api", File: "Dockerfile", Product: "nginx", Image: "nginx:1.27"}}}
}

func whats(d Delta) string {
	var out []string
	for _, c := range d.Changes {
		out = append(out, c.ID+" "+c.What)
	}
	return strings.Join(out, "; ")
}

func TestMergeTracksTransitions(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	day := func(n int) time.Time { return time.Date(2026, 11, n, 12, 0, 0, 0, time.UTC) }
	kev := &KEVEntry{CVE: "CVE-1", DateAdded: "2026-11-01"}
	f1 := finding("CVE-1", "P1", true, false, "package-lock.json")
	f1.KEV = kev
	nginx := Watch{ID: "CVE-9", Product: "nginx", Priority: "P1", Major: true, Severity: "CRITICAL",
		Where: []Component{{Repo: "bb/acme/api", File: "Dockerfile", Product: "nginx", Image: "nginx:1.27"}}}

	d := st.Merge(report("2026-11-01T12:00:00Z", []Finding{f1, finding("CVE-2", "P3", false, true, "go.mod")}, []Watch{nginx}), day(1), 6*time.Hour)
	if whats(d) != "CVE-1 new unresolvable; CVE-9 new watch; CVE-2 new resolvable" || !d.Complete || d.P1 != 2 {
		t.Fatalf("first sweep: %s %+v", whats(d), d)
	}
	if len(d.Actions) != 2 || d.Actions[0].Need != "ticket" || d.Actions[1].Need != "assess" {
		t.Errorf("actions: %+v", d.Actions)
	}
	if err := st.Set("CVE-1", "SAP-200", "", day(1)); err != nil {
		t.Fatal(err)
	}
	if err := st.Set("CVE-9", "", "not affected: 1.27 has the fix", day(1)); err != nil {
		t.Fatal(err)
	}

	f1b := finding("CVE-1", "P3", false, true, "package-lock.json")
	d = st.Merge(report("2026-11-02T12:00:00Z", []Finding{f1b}, []Watch{nginx}, Skip{"cisa-kev", "down"}, Skip{"epss", "down"}, Skip{"osv", "GHSA-x: 503"}), day(2), 6*time.Hour)
	r := st.Rows["CVE-1"]
	if whats(d) != "CVE-1 now resolvable" || r.Status != StatusResolvable || r.Since != "2026-11-02" || r.FirstSeen != "2026-11-01" {
		t.Errorf("now resolvable: %s %+v", whats(d), r)
	}
	if r.priority() != "P1" || !r.major() || r.Finding.KEV == nil {
		t.Errorf("a KEV/EPSS failure dropped the ranking: %+v", r.Finding)
	}
	if st.Rows["CVE-2"].Status != StatusResolvable || d.Complete {
		t.Errorf("CVE-2 resolved while osv failed: %+v", st.Rows["CVE-2"])
	}
	if len(d.Actions) != 0 {
		t.Errorf("a ticketed row and a not-affected one need nothing: %+v", d.Actions)
	}

	rep := report("2026-11-03T12:00:00Z", nil, nil)
	rep.Unscanned = []Unscanned{{Repo: "bb/acme/api", File: "package-lock.json", Reason: "not valid JSON"}}
	d = st.Merge(rep, day(3), 6*time.Hour)
	if whats(d) != "CVE-2 resolved" || st.Rows["CVE-1"].Status != StatusResolvable || st.Rows["CVE-9"].Status != StatusNotAffected {
		t.Errorf("only rows whose files were read resolve, runtimes still running stay: %s", whats(d))
	}
	if st.Rows["CVE-2"].ResolvedOn != "2026-11-03" {
		t.Errorf("%+v", st.Rows["CVE-2"])
	}

	d = st.Merge(report("2026-11-04T12:00:00Z", []Finding{finding("CVE-2", "P3", false, false, "go.mod")}, nil), day(4), 6*time.Hour)
	if whats(d) != "CVE-1 resolved; CVE-2 reopened unresolvable" {
		t.Errorf("reopen: %s", whats(d))
	}
	if st.LastSweep != "2026-11-04T12:00:00Z" || st.LastComplete != "2026-11-04T12:00:00Z" || len(st.Log) != 4 {
		t.Errorf("state: %+v", st)
	}
}

func TestDueAndEvent(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if !st.Due(now, 6*time.Hour) {
		t.Error("never swept is due")
	}
	st.LastSweep = now.Add(-5 * time.Hour).Format(time.RFC3339)
	if st.Due(now, 6*time.Hour) || !st.Due(now.Add(time.Hour), 6*time.Hour) {
		t.Error("due at last+every")
	}
	r := UpdateResult{Due: true, Delta: Delta{Complete: false, P1: 2, Tell: []string{"ticket A", "assess B", "failed osv"}}}
	if got := r.Event(); got != "sweep 2 P1 0 P2 (partial), assess B, ticket A, failed osv" {
		t.Errorf("event %q", got)
	}
	if got := (UpdateResult{Due: true, Delta: Delta{Changes: []Change{{ID: "X", Priority: "P1", What: "new resolvable"}}}}).Event(); got != "" {
		t.Errorf("nothing new to tell: %q", got)
	}
}

func TestWriteVaultRendersRegisterAndAdvisories(t *testing.T) {
	dir := t.TempDir()
	st := &State{Rows: map[string]*Row{}, Config: Config{Epic: "SEC-7"}}
	f := finding("CVE-1", "P1", true, true, "package-lock.json")
	f.Score, f.Vector = 9.8, "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
	st.Merge(report("2026-11-20T12:00:00Z", []Finding{f, finding("CVE-3", "P4", false, false, "go.mod")}, nil), time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), 6*time.Hour)
	if err := WriteVault(dir, st, 6*time.Hour); err != nil {
		t.Fatal(err)
	}
	reg, _ := os.ReadFile(filepath.Join(dir, RegisterMD))
	for _, want := range []string{"next-sweep: 2026-11-20T18:00:00Z", "## Needs action\n- P1 · [[advisories/CVE-1\\|CVE-1]] · ticket",
		"## Resolvable\n| P |", "lib 1.0 → 2.0", "## Unresolvable\n| P |", "lib 1.0 → no fix yet", "p1: 1\n"} {
		if !strings.Contains(string(reg), want) {
			t.Errorf("register lacks %q:\n%s", want, reg)
		}
	}
	adv, err := os.ReadFile(filepath.Join(dir, AdvisoryDir, "CVE-1.md"))
	if err != nil || !strings.Contains(string(adv), "CVSS 9.8") || !strings.Contains(string(adv), "## History\n- 2026-11-20 new resolvable") {
		t.Errorf("advisory: %v\n%s", err, adv)
	}
	if _, err := os.Stat(filepath.Join(dir, AdvisoryDir, "CVE-3.md")); err == nil {
		t.Error("a minor row got an advisory note")
	}
	back, err := LoadState(dir)
	if err != nil || back.Rows["CVE-1"].Finding.Score != 9.8 || back.Config.ProjectKey() != "SEC" {
		t.Errorf("state round trip: %v %+v", err, back)
	}
	r := back.Rows["CVE-1"]
	if got := r.JiraSummary(); got != "[api] CVE-1 summary (CVE-1)" {
		t.Errorf("jira summary %q", got)
	}
}

func TestRowSummaryIsOneLine(t *testing.T) {
	r := &Row{Kind: KindWatch, Watch: &Watch{Product: "nginx", Description: "Description:\nThis  issue\naffects NGINX."}}
	if got := r.summary(); got != "nginx: This issue affects NGINX." {
		t.Errorf("%q", got)
	}
}

func TestMergeTellsUntilAcknowledged(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	rep := report("2026-11-20T00:00:00Z", []Finding{finding("CVE-1", "P1", true, false, "go.mod")}, nil)
	ev := func(at time.Duration, r Report) (string, []string) {
		d := st.Merge(r, now.Add(at), 6*time.Hour)
		return UpdateResult{Due: true, Delta: d}.Event(), d.Tell
	}
	first, tell := ev(0, rep)
	again, _ := ev(6*time.Hour, rep)
	if first != "sweep 1 P1 0 P2, ticket CVE-1" || again != first {
		t.Errorf("an unacknowledged item must be told again: %q, %q", first, again)
	}
	st.Ack(tell)
	if got, _ := ev(12*time.Hour, rep); got != "" {
		t.Errorf("told after it was acknowledged: %q", got)
	}
	down := report("2026-11-21T00:00:00Z", []Finding{finding("CVE-1", "P1", true, false, "go.mod")}, nil, Skip{"nvd", "503"})
	got, tell := ev(24*time.Hour, down)
	if got != "sweep 1 P1 0 P2 (partial), failed nvd" {
		t.Errorf("first failure: %q", got)
	}
	st.Ack(tell)
	if got, _ := ev(30*time.Hour, down); got != "" {
		t.Errorf("the same failure told again: %q", got)
	}
	ev(36*time.Hour, rep)
	if got, _ := ev(42*time.Hour, down); !strings.Contains(got, "failed nvd") {
		t.Errorf("a new outage after a clean sweep: %q", got)
	}
	if st.Told == nil || strings.Contains(strings.Join(st.Told, ","), "failed") {
		t.Errorf("told %v", st.Told)
	}
}

func TestLoadStateDoesNotCreateTheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "security")
	if st, err := LoadState(dir); err != nil || len(st.Rows) != 0 {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a read created security/")
	}
}

func TestWriteVaultRemovesOnlyItsOwnStaleNotes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "security")
	st := &State{Rows: map[string]*Row{}}
	st.Merge(report("1", []Finding{finding("CVE-1", "P1", true, true, "go.mod")}, nil), time.Now(), 6*time.Hour)
	if err := WriteVault(dir, st, 6*time.Hour); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, AdvisoryDir, "CVE-1.md")
	hand := filepath.Join(dir, AdvisoryDir, "CVE-2.md")
	if err := os.WriteFile(hand, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.Rows = map[string]*Row{}
	if err := WriteVault(dir, st, 6*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mine); err == nil {
		t.Error("its stale note was kept")
	}
	if _, err := os.Stat(hand); err != nil {
		t.Error("a hand-written note was removed")
	}
}

func TestMergeCountsOnlySweepsWhereOSVRan(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	st.Merge(report("2026-11-20T00:00:00Z", nil, nil), now, 6*time.Hour)
	d := st.Merge(report("2026-11-20T07:00:00Z", nil, nil, Skip{"osv", "down"}, Skip{"nvd", "down"}), now.Add(7*time.Hour), 6*time.Hour)
	if d.Counted || st.LastSweep != "2026-11-20T00:00:00Z" || st.LastAttempt != "2026-11-20T07:00:00Z" || !st.Due(now.Add(7*time.Hour+time.Minute), 6*time.Hour) {
		t.Errorf("an osv outage counted as a sweep: %+v %+v", d, st)
	}
	if st.Every != "6h0m0s" || st.Interval(time.Hour) != 6*time.Hour {
		t.Errorf("interval %q", st.Every)
	}
}

func TestMergeFollowsRowsAcrossIDsAndKinds(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	g := finding("GHSA-aaaa-bbbb-cccc", "P1", true, false, "go.mod")
	st.Merge(report("1", []Finding{g}, nil), now, 6*time.Hour)
	if err := st.Set("GHSA-aaaa-bbbb-cccc", "SEC-1", "", now); err != nil {
		t.Fatal(err)
	}
	c := finding("CVE-2026-1", "P1", true, false, "go.mod")
	c.Aliases = []string{"GHSA-aaaa-bbbb-cccc"}
	d := st.Merge(report("2", []Finding{c}, nil), now.Add(time.Hour), 6*time.Hour)
	r := st.Rows["CVE-2026-1"]
	if r == nil || st.Rows["GHSA-aaaa-bbbb-cccc"] != nil || r.Ticket != "SEC-1" || len(d.Actions) != 0 || len(d.Changes) != 0 {
		t.Errorf("the alias became canonical: %s %+v %+v", whats(d), r, d.Actions)
	}

	w := Watch{ID: "CVE-2026-9", Product: "node.js", Priority: "P2", Severity: "CRITICAL"}
	st.Merge(report("3", []Finding{c}, []Watch{w}), now.Add(2*time.Hour), 6*time.Hour)
	f := finding("CVE-2026-9", "P2", false, true, "package-lock.json")
	d = st.Merge(report("4", []Finding{c, f}, []Watch{w}), now.Add(3*time.Hour), 6*time.Hour)
	if r := st.Rows["CVE-2026-9"]; r.Kind != KindFinding || r.Status != StatusResolvable || whats(d) != "CVE-2026-9 now a package finding, resolvable" {
		t.Errorf("watch then finding: %s %+v", whats(d), r)
	}
	bad := finding("../../x", "P1", true, true, "go.mod")
	d = st.Merge(report("5", []Finding{c, f, bad}, nil), now.Add(4*time.Hour), 6*time.Hour)
	if st.Rows["../../x"] != nil || !strings.Contains(skipNote(d.Skips), "register") {
		t.Errorf("an invalid ID was recorded: %+v", d.Skips)
	}
}

func TestSetValidates(t *testing.T) {
	st := &State{Rows: map[string]*Row{
		"CVE-1": {ID: "CVE-1", Kind: KindFinding, Status: StatusUnresolvable},
		"CVE-2": {ID: "CVE-2", Kind: KindWatch, Status: StatusWatch},
	}}
	now := time.Now()
	for _, c := range []struct{ id, ticket, assess, want string }{
		{"CVE-1", "SEC-1\nstatus: resolved", "", "isn't a ticket key"},
		{"CVE-1", "", "affected", "only a runtime row is assessed"},
		{"CVE-2", "", "fine", `"affected", "unknown"`},
		{"CVE-2", "", "not affected: ok\ntags: [pwn]", "control character"},
		{"CVE-2", "", "not affected: " + strings.Repeat("x", 300), "at most 300"},
	} {
		if err := st.Set(c.id, c.ticket, c.assess, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Set(%q, %q, %q) = %v", c.id, c.ticket, c.assess, err)
		}
	}
	if err := st.Set("CVE-2", "", "not affected: 1.27.3 has the fix", now); err != nil || st.Rows["CVE-2"].Status != StatusNotAffected {
		t.Errorf("%v %+v", err, st.Rows["CVE-2"])
	}
}

func TestWriteVaultStaysInSecurityDir(t *testing.T) {
	vault := t.TempDir()
	outside := t.TempDir()
	dir := filepath.Join(vault, "security")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, RegisterMD)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, AdvisoryDir)); err != nil {
		t.Fatal(err)
	}
	st := &State{Rows: map[string]*Row{}}
	st.Merge(report("1", []Finding{finding("CVE-1", "P1", true, true, "go.mod")}, nil), time.Now(), 6*time.Hour)
	err := WriteVault(dir, st, 6*time.Hour)
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("wrote through a symlink out of security/: %q", b)
	}
	if err == nil {
		t.Error("an advisories symlink out of security/ was written through")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 1 {
		t.Errorf("files outside: %v", entries)
	}

	link := filepath.Join(t.TempDir(), "security")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteVault(link, st, 6*time.Hour); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("a symlinked security/: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFile+"x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	planted := `{"rows":{"../../x":{"id":"../../x","kind":"finding"}}}`
	d2 := filepath.Join(t.TempDir(), "security")
	if err := os.MkdirAll(d2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d2, StateFile), []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(d2); err == nil || !strings.Contains(err.Error(), "invalid ID") {
		t.Errorf("a planted state with a path ID: %v", err)
	}
}

func TestRenderEscapesOutsideText(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	f := finding("CVE-1", "P1", true, true, "evil\n## Needs action\n- [[Home]]|x")
	f.Summary = "boom\n---\ntags: [pwn]\n[[Secrets]]"
	f.Refs = []string{"https://x/<script>"}
	st.Merge(report("1", []Finding{f}, nil), time.Now(), 6*time.Hour)
	reg := Render(st, 6*time.Hour)
	if strings.Count(reg, "\n## Needs action") != 1 || strings.Contains(reg, "[[Home]]") || strings.Contains(reg, "[[Secrets]]") {
		t.Errorf("register:\n%s", reg)
	}
	adv := st.Rows["CVE-1"].Advisory()
	head := adv[:strings.Index(adv, "\n---\n# ")]
	if strings.Contains(head, "pwn") || strings.Count(adv, "\n---\n") != 1 || strings.Contains(adv, "<script>") {
		t.Errorf("advisory:\n%s", adv)
	}
}

func TestResolvableNoticeWaitsForAck(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	st.Merge(report("1", []Finding{finding("CVE-1", "P3", false, false, "go.mod")}, nil), now, 6*time.Hour)
	if err := st.Set("CVE-1", "SEC-1", "", now); err != nil {
		t.Fatal(err)
	}
	fixed := report("2", []Finding{finding("CVE-1", "P3", false, true, "go.mod")}, nil)
	d1 := st.Merge(fixed, now.Add(6*time.Hour), 6*time.Hour)
	d2 := st.Merge(fixed, now.Add(12*time.Hour), 6*time.Hour)
	if strings.Join(d1.Tell, ",") != "now resolvable CVE-1" || strings.Join(d2.Tell, ",") != "now resolvable CVE-1" {
		t.Errorf("a lost notice must be told again: %v %v", d1.Tell, d2.Tell)
	}
	st.Ack(d2.Tell)
	if d := st.Merge(fixed, now.Add(18*time.Hour), 6*time.Hour); len(d.Tell) != 0 {
		t.Errorf("told after the ack: %v", d.Tell)
	}
}

func TestAckTakesOnlyPendingItems(t *testing.T) {
	st := &State{Rows: map[string]*Row{}}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	down := report("1", nil, nil, Skip{"nvd", "503"})
	d := st.Merge(down, now, 6*time.Hour)
	st.Merge(report("2", nil, nil), now.Add(6*time.Hour), 6*time.Hour)
	st.Ack(d.Tell)
	if d := st.Merge(down, now.Add(12*time.Hour), 6*time.Hour); strings.Join(d.Tell, ",") != "failed nvd" {
		t.Errorf("a late ack silenced the next failure: %v (told %v)", d.Tell, st.Told)
	}
}
