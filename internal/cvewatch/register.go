package cvewatch

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	StateFile   = ".state.json"
	SweepLock   = ".sweep.lock"
	RegisterMD  = "security.md"
	AdvisoryDir = "advisories"

	StatusResolvable   = "resolvable"
	StatusUnresolvable = "unresolvable"
	StatusWatch        = "watch"
	StatusNotAffected  = "not-affected"
	StatusResolved     = "resolved"

	KindFinding = "finding"
	KindWatch   = "watch"

	maxLog        = 30
	maxResolved   = 50
	keepResolved  = 180 * 24 * time.Hour
	maxAssessment = 300
	dateLayout    = "2006-01-02"
)

var (
	validID     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	validTicket = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9]+$`)
)

func ValidID(id string) bool { return validID.MatchString(id) && !strings.Contains(id, "..") }

type Config struct {
	Epic    string   `json:"epic,omitempty"`
	Project string   `json:"project,omitempty"`
	Labels  []string `json:"labels,omitempty"`
}

func (c Config) ProjectKey() string {
	if c.Project != "" {
		return c.Project
	}
	p, _, _ := strings.Cut(c.Epic, "-")
	return p
}

type Row struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	FirstSeen  string   `json:"firstSeen"`
	Since      string   `json:"since"`
	ResolvedOn string   `json:"resolvedOn,omitempty"`
	Ticket     string   `json:"ticket,omitempty"`
	Assessment string   `json:"assessment,omitempty"`
	History    []string `json:"history,omitempty"`
	Finding    *Finding `json:"finding,omitempty"`
	Watch      *Watch   `json:"watch,omitempty"`
}

type State struct {
	Config       Config          `json:"config"`
	Every        string          `json:"every,omitempty"`
	LastSweep    string          `json:"lastSweep,omitempty"`
	LastAttempt  string          `json:"lastAttempt,omitempty"`
	LastComplete string          `json:"lastComplete,omitempty"`
	Rows         map[string]*Row `json:"rows"`
	Told         []string        `json:"told,omitempty"`
	Pending      []string        `json:"pending,omitempty"`
	Log          []string        `json:"log,omitempty"`
	Unscanned    []Unscanned     `json:"unscanned,omitempty"`
	Skips        []Skip          `json:"skips,omitempty"`
	Repos        int             `json:"repos"`
	Packages     int             `json:"packages"`
}

func OpenDir(dir string) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	base := filepath.Base(dir)
	if err := parent.Mkdir(base, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	if st, err := parent.Lstat(base); err != nil {
		return nil, err
	} else if st.Mode()&fs.ModeSymlink != 0 || !st.IsDir() {
		return nil, fmt.Errorf("%s is a symlink or not a directory: refusing to write through it", dir)
	}
	return parent.OpenRoot(base)
}

func LoadState(dir string) (*State, error) {
	st := &State{Rows: map[string]*Row{}}
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	root, err := OpenDir(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	b, err := root.ReadFile(StateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("%s: %w", StateFile, err)
	}
	if st.Rows == nil {
		st.Rows = map[string]*Row{}
	}
	for id, r := range st.Rows {
		if r == nil || r.ID != id || !ValidID(id) {
			return nil, fmt.Errorf("%s: row %q has an invalid ID", StateFile, id)
		}
	}
	return st, nil
}

func writeIfChanged(root *os.Root, name string, b []byte) error {
	if old, err := root.ReadFile(name); err == nil && bytes.Equal(old, b) {
		return nil
	}
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(name), "."+path.Base(name)+"."+hex.EncodeToString(nonce[:])+".tmp")
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

func (st *State) every(fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(st.Every); err == nil && d > 0 {
		return d
	}
	return fallback
}

func (st *State) Interval(fallback time.Duration) time.Duration { return st.every(fallback) }

func (st *State) Last() time.Time {
	t, _ := time.Parse(time.RFC3339, st.LastSweep)
	return t
}

func (st *State) Next(every time.Duration) time.Time {
	if st.LastSweep == "" {
		return time.Time{}
	}
	return st.Last().Add(every)
}

func (st *State) Due(now time.Time, every time.Duration) bool {
	return st.LastSweep == "" || !now.Before(st.Next(every))
}

type Change struct {
	ID       string `json:"id"`
	Priority string `json:"priority"`
	What     string `json:"what"`
	Summary  string `json:"summary"`
}

type Action struct {
	ID       string `json:"id"`
	Priority string `json:"priority"`
	Need     string `json:"need"`
	Summary  string `json:"summary"`
}

type Delta struct {
	Swept    string   `json:"swept"`
	Complete bool     `json:"complete"`
	Counted  bool     `json:"counted"`
	Skips    []Skip   `json:"skips,omitempty"`
	Changes  []Change `json:"changes,omitempty"`
	Actions  []Action `json:"actions,omitempty"`
	Tell     []string `json:"tell,omitempty"`
	P1       int      `json:"p1"`
	P2       int      `json:"p2"`
	Open     int      `json:"open"`
}

type UpdateResult struct {
	Delta
	Due  bool   `json:"due"`
	Last string `json:"last,omitempty"`
	Next string `json:"next,omitempty"`
}

const maxEventIDs = 8

var tellOrder = []string{"assess", "ticket", "now resolvable", "failed"}

func (r UpdateResult) Event() string {
	if !r.Due || len(r.Tell) == 0 {
		return ""
	}
	groups := map[string][]string{}
	for _, t := range r.Tell {
		for _, k := range tellOrder {
			if rest, ok := strings.CutPrefix(t, k+" "); ok {
				groups[k] = append(groups[k], rest)
				break
			}
		}
	}
	head := fmt.Sprintf("sweep %d P1 %d P2", r.P1, r.P2)
	if !r.Complete {
		head += " (partial)"
	}
	parts := []string{head}
	for _, k := range tellOrder {
		if ids := groups[k]; len(ids) > 0 {
			parts = append(parts, k+" "+capIDs(ids))
		}
	}
	return strings.Join(parts, ", ")
}

func capIDs(ids []string) string {
	if len(ids) > maxEventIDs {
		return strings.Join(ids[:maxEventIDs], " ") + fmt.Sprintf(" +%d (see Needs action)", len(ids)-maxEventIDs)
	}
	return strings.Join(ids, " ")
}

func skipped(rep Report) map[string]bool {
	m := map[string]bool{}
	for _, s := range rep.Skips {
		m[s.Source] = true
	}
	return m
}

func (r *Row) priority() string {
	switch {
	case r.Finding != nil:
		return r.Finding.Priority
	case r.Watch != nil:
		return r.Watch.Priority
	}
	return "P4"
}

func (r *Row) major() bool {
	switch {
	case r.Finding != nil:
		return r.Finding.Major
	case r.Watch != nil:
		return r.Watch.Major
	}
	return false
}

const maxWatchSummary = 120

func (r *Row) summary() string {
	switch {
	case r.Finding != nil:
		return oneLine(r.Finding.Summary)
	case r.Watch != nil:
		return r.Watch.Product + ": " + ellipsis(oneLine(strings.TrimPrefix(strings.TrimSpace(r.Watch.Description), "Description:")), maxWatchSummary)
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (r *Row) note(date, what string) {
	r.History = append(r.History, date+" "+what)
}

func higher(a, b string) string {
	if a != "" && a < b {
		return a
	}
	return b
}

func keepRanking(prev *Row, priority *string, major *bool, kev **KEVEntry, epss *float64, partial bool) {
	if !partial || prev == nil {
		return
	}
	*priority = higher(prev.priority(), *priority)
	*major = *major || prev.major()
	switch {
	case prev.Finding != nil && *kev == nil:
		*kev = prev.Finding.KEV
	case prev.Watch != nil && *kev == nil:
		*kev = prev.Watch.KEV
	}
	switch {
	case prev.Finding != nil:
		*epss = max(*epss, prev.Finding.EPSS)
	case prev.Watch != nil:
		*epss = max(*epss, prev.Watch.EPSS)
	}
}

func (st *State) aliasIndex() map[string]string {
	idx := map[string]string{}
	for id, r := range st.Rows {
		if r.Finding != nil {
			for _, a := range r.Finding.Aliases {
				idx[a] = id
			}
		}
	}
	return idx
}

func (st *State) rekey(id string, aliases []string, idx map[string]string, today string) *Row {
	if r := st.Rows[id]; r != nil {
		return r
	}
	for _, a := range aliases {
		old := a
		if o, ok := idx[a]; ok {
			old = o
		}
		r := st.Rows[old]
		if r == nil || old == id {
			continue
		}
		delete(st.Rows, old)
		r.ID = id
		r.note(today, "renamed from "+old)
		st.Rows[id] = r
		return r
	}
	return nil
}

func findingStatus(f Finding) string {
	if f.Resolvable {
		return StatusResolvable
	}
	return StatusUnresolvable
}

func (st *State) Merge(rep Report, now time.Time, every time.Duration) Delta {
	today := now.UTC().Format(dateLayout)
	skip := skipped(rep)
	rankingPartial := skip["cisa-kev"] || skip["epss"]
	d := Delta{Swept: rep.Swept, Complete: len(rep.Skips) == 0, Counted: !skip["osv"], Skips: rep.Skips}
	st.Every = every.String()
	st.LastAttempt = rep.Swept
	if d.Counted {
		st.LastSweep = rep.Swept
	}
	if d.Complete {
		st.LastComplete = rep.Swept
	}
	st.Unscanned, st.Skips, st.Repos, st.Packages = rep.Unscanned, rep.Skips, len(rep.Repos), rep.Packages
	seen := map[string]bool{}
	change := func(r *Row, what string) {
		r.note(today, what)
		d.Changes = append(d.Changes, Change{ID: r.ID, Priority: r.priority(), What: what, Summary: r.summary()})
	}
	idx := st.aliasIndex()
	for i := range rep.Findings {
		f := rep.Findings[i]
		if !ValidID(f.ID) {
			d.Skips = append(d.Skips, Skip{"register", fmt.Sprintf("advisory ID %q isn't a valid ID: not recorded", f.ID)})
			continue
		}
		seen[f.ID] = true
		prev := st.rekey(f.ID, f.Aliases, idx, today)
		keepRanking(prev, &f.Priority, &f.Major, &f.KEV, &f.EPSS, rankingPartial)
		status := findingStatus(f)
		if prev == nil {
			r := &Row{ID: f.ID, Kind: KindFinding, Status: status, FirstSeen: today, Since: today, Finding: &f}
			st.Rows[f.ID] = r
			change(r, "new "+status)
			continue
		}
		wasWatch := prev.Kind == KindWatch
		prev.Finding, prev.Kind = &f, KindFinding
		switch {
		case wasWatch:
			prev.Status, prev.Since, prev.ResolvedOn = status, today, ""
			change(prev, "now a package finding, "+status)
		case prev.Status == StatusResolved:
			prev.Status, prev.Since, prev.ResolvedOn = status, today, ""
			change(prev, "reopened "+status)
		case prev.Status == StatusUnresolvable && status == StatusResolvable:
			prev.Status, prev.Since = status, today
			change(prev, "now resolvable")
		case prev.Status == StatusResolvable && status == StatusUnresolvable:
			prev.Status, prev.Since = status, today
			change(prev, "fix withdrawn")
		}
	}
	for i := range rep.Watch {
		w := rep.Watch[i]
		if !ValidID(w.ID) {
			d.Skips = append(d.Skips, Skip{"register", fmt.Sprintf("CVE ID %q isn't a valid ID: not recorded", w.ID)})
			continue
		}
		seen[w.ID] = true
		prev := st.Rows[w.ID]
		keepRanking(prev, &w.Priority, &w.Major, &w.KEV, &w.EPSS, rankingPartial)
		if prev == nil {
			r := &Row{ID: w.ID, Kind: KindWatch, Status: StatusWatch, FirstSeen: today, Since: today, Watch: &w}
			st.Rows[w.ID] = r
			change(r, "new watch")
			continue
		}
		prev.Watch = &w
		if prev.Kind == KindWatch && prev.Status == StatusResolved {
			prev.Status, prev.Since, prev.ResolvedOn = StatusWatch, today, ""
			change(prev, "reopened watch")
		}
	}
	st.resolveMissing(rep, seen, skip, change, today)
	st.prune(now)
	tell := map[string]bool{}
	for _, id := range st.ids() {
		r := st.Rows[id]
		if r.Status == StatusResolved || r.Status == StatusNotAffected {
			continue
		}
		d.Open++
		switch r.priority() {
		case "P1":
			d.P1++
		case "P2":
			d.P2++
		}
		if a, ok := r.action(); ok {
			d.Actions = append(d.Actions, a)
			tell[a.Need+" "+a.ID] = true
		}
	}
	for src := range skip {
		tell["failed "+src] = true
	}
	for _, t := range append(append([]string{}, st.Pending...), resolvableNotices(d.Changes)...) {
		if id, ok := strings.CutPrefix(t, noticeResolvable); ok {
			if r := st.Rows[id]; r != nil && r.Ticket != "" && r.Status == StatusResolvable {
				tell[t] = true
			}
		}
	}
	d.Tell = st.untold(tell)
	st.Pending = sortedSet(tell)
	sortChanges(d.Changes)
	sort.SliceStable(d.Actions, func(i, j int) bool { return d.Actions[i].Priority < d.Actions[j].Priority })
	st.Log = append(st.Log, fmt.Sprintf("%s %s: %d open, %d P1, %d P2, %d changes%s",
		now.UTC().Format("2006-01-02 15:04"), map[bool]string{true: "complete", false: "partial"}[d.Complete],
		d.Open, d.P1, d.P2, len(d.Changes), skipNote(rep.Skips)))
	if len(st.Log) > maxLog {
		st.Log = st.Log[len(st.Log)-maxLog:]
	}
	return d
}

func (st *State) untold(cur map[string]bool) []string {
	var kept []string
	told := map[string]bool{}
	for _, t := range st.Told {
		if cur[t] {
			told[t] = true
			kept = append(kept, t)
		}
	}
	st.Told = kept
	var fresh []string
	for t := range cur {
		if !told[t] {
			fresh = append(fresh, t)
		}
	}
	sort.Strings(fresh)
	return fresh
}

const noticeResolvable = "now resolvable "

func resolvableNotices(cs []Change) []string {
	var out []string
	for _, c := range cs {
		if c.What == "now resolvable" {
			out = append(out, noticeResolvable+c.ID)
		}
	}
	return out
}

func (st *State) Ack(tokens []string) {
	pending := map[string]bool{}
	for _, t := range st.Pending {
		pending[t] = true
	}
	told := map[string]bool{}
	for _, t := range st.Told {
		told[t] = true
	}
	for _, t := range tokens {
		if pending[t] {
			told[t] = true
		}
	}
	st.Told = sortedSet(told)
}

func (st *State) resolveMissing(rep Report, seen, skip map[string]bool, change func(*Row, string), today string) {
	unscanned := map[string]bool{}
	for _, u := range rep.Unscanned {
		unscanned[u.Repo+"|"+u.File] = true
	}
	products := map[string]bool{}
	for _, c := range rep.Components {
		products[c.Product] = true
	}
	for _, id := range st.ids() {
		r := st.Rows[id]
		if seen[id] || r.Status == StatusResolved {
			continue
		}
		switch r.Kind {
		case KindFinding:
			if skip["osv"] || skip[SourceOSVRecord] || r.Finding == nil || r.Finding.inUnscanned(unscanned) {
				continue
			}
		case KindWatch:
			if r.Watch == nil || products[r.Watch.Product] || skip["nvd"] {
				continue
			}
		}
		r.Status, r.ResolvedOn = StatusResolved, today
		change(r, "resolved")
	}
}

func (st *State) prune(now time.Time) {
	for id, r := range st.Rows {
		if r.Status != StatusResolved {
			continue
		}
		if t, err := time.Parse(dateLayout, r.ResolvedOn); err == nil && now.Sub(t) > keepResolved {
			delete(st.Rows, id)
		}
	}
}

func skipNote(skips []Skip) string {
	if len(skips) == 0 {
		return ""
	}
	src := map[string]bool{}
	for _, s := range skips {
		src[s.Source] = true
	}
	return " (failed: " + strings.Join(sortedSet(src), ", ") + ")"
}

func sortChanges(cs []Change) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Priority != cs[j].Priority {
			return cs[i].Priority < cs[j].Priority
		}
		return cs[i].ID < cs[j].ID
	})
}

func (f *Finding) inUnscanned(unscanned map[string]bool) bool {
	for _, p := range f.Packages {
		if unscanned[p.Repo+"|"+p.File] {
			return true
		}
	}
	return false
}

func (r *Row) action() (Action, bool) {
	if !r.major() || r.Ticket != "" {
		return Action{}, false
	}
	a := Action{ID: r.ID, Priority: r.priority(), Summary: r.summary(), Need: "ticket"}
	if r.Kind == KindWatch {
		switch {
		case r.Assessment == "":
			a.Need = "assess"
		case strings.HasPrefix(r.Assessment, "not affected"):
			return Action{}, false
		}
	}
	return a, true
}

func (st *State) ids() []string {
	ids := make([]string, 0, len(st.Rows))
	for id := range st.Rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func validAssessment(a string) error {
	switch {
	case len(a) > maxAssessment:
		return fmt.Errorf("the assessment is %d bytes (at most %d)", len(a), maxAssessment)
	case strings.ContainsFunc(a, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return errors.New("the assessment has a control character (one line only)")
	case a == "affected", a == "unknown", strings.HasPrefix(a, "not affected: ") && len(a) > len("not affected: "):
		return nil
	}
	return errors.New(`the assessment is "affected", "unknown" or "not affected: <why>"`)
}

func (st *State) Set(id, ticket, assessment string, now time.Time) error {
	r, ok := st.Rows[id]
	if !ok {
		return fmt.Errorf("%s isn't in the register", id)
	}
	if ticket != "" && !validTicket.MatchString(ticket) {
		return fmt.Errorf("%q isn't a ticket key (like ABC-123)", ticket)
	}
	if assessment != "" {
		if r.Kind != KindWatch {
			return fmt.Errorf("%s is a package finding: only a runtime row is assessed", id)
		}
		if err := validAssessment(assessment); err != nil {
			return err
		}
	}
	today := now.UTC().Format(dateLayout)
	if ticket != "" && ticket != r.Ticket {
		r.Ticket = ticket
		r.note(today, "ticket "+ticket)
	}
	if assessment != "" && assessment != r.Assessment {
		r.Assessment = assessment
		r.note(today, "assessed: "+assessment)
		if r.Status != StatusResolved {
			r.Status = StatusWatch
			if strings.HasPrefix(assessment, "not affected") {
				r.Status = StatusNotAffected
			}
		}
	}
	return nil
}

func repoName(clone string) string { return path.Base(clone) }
