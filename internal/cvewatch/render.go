package cvewatch

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

var mdEscaper = strings.NewReplacer("|", "\\|", "[[", "[\\[", "]]", "]\\]", "](", "]\\(", "![", "!\\[", "<", "&lt;", ">", "&gt;", "`", "'")

func md(s string) string { return mdEscaper.Replace(oneLine(s)) }

func cell(s string) string { return md(s) }

func cells(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = md(x)
	}
	return strings.Join(out, "<br>")
}

func yamlString(s string) string {
	b, _ := json.Marshal(oneLine(s))
	return string(b)
}

func yamlList(xs []string) string {
	b, _ := json.Marshal(append([]string{}, xs...))
	return string(b)
}

func (r *Row) where() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	switch {
	case r.Finding != nil:
		for _, p := range r.Finding.Packages {
			add(repoName(p.Repo) + " · " + p.File)
		}
	case r.Watch != nil:
		for _, c := range r.Watch.Where {
			img := c.Image
			if img == "" {
				img = c.Product + " " + c.Version
			}
			add(repoName(c.Repo) + " · " + img)
		}
	}
	return out
}

func (r *Row) fixes() []string {
	if r.Finding == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range r.Finding.Packages {
		s := p.Name + " " + p.Version + " → "
		if p.Fix != "" {
			s += p.Fix
		} else {
			s += "no fix yet"
		}
		if p.Name == "stdlib" {
			s += " (the Go that builds it)"
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (r *Row) kev() bool {
	return r.Finding != nil && r.Finding.KEV != nil || r.Watch != nil && r.Watch.KEV != nil
}

func (r *Row) epss() float64 {
	switch {
	case r.Finding != nil:
		return r.Finding.EPSS
	case r.Watch != nil:
		return r.Watch.EPSS
	}
	return 0
}

func (r *Row) severity() string {
	switch {
	case r.Finding != nil:
		return r.Finding.Severity
	case r.Watch != nil:
		return r.Watch.Severity
	}
	return ""
}

func (st *State) rowsWhere(keep func(*Row) bool) []*Row {
	var out []*Row
	for _, id := range st.ids() {
		if r := st.Rows[id]; keep(r) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return less(a.priority(), b.priority(), a.kev(), b.kev(), a.epss(), b.epss(), 0, 0, a.ID, b.ID)
	})
	return out
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return ""
}

func (r *Row) link() string {
	if r.major() || r.Ticket != "" {
		return "[[" + AdvisoryDir + "/" + r.ID + "\\|" + r.ID + "]]"
	}
	return r.ID
}

func Render(st *State, every time.Duration) string {
	var b strings.Builder
	open := st.rowsWhere(func(r *Row) bool { return r.Status != StatusResolved && r.Status != StatusNotAffected })
	p1, p2 := 0, 0
	for _, r := range open {
		switch r.priority() {
		case "P1":
			p1++
		case "P2":
			p2++
		}
	}
	next := ""
	every = st.every(every)
	if !st.Next(every).IsZero() {
		next = st.Next(every).UTC().Format(time.RFC3339)
	}
	ended := 0
	for _, l := range st.Lifecycle {
		if l.Ended {
			ended++
		}
	}
	fmt.Fprintf(&b, "---\ntype: security\ntags: [security]\nlast-sweep: %s\nlast-complete-sweep: %s\nnext-sweep: %s\nrepos: %d\npackages: %d\nimages: %d\nopen: %d\np1: %d\np2: %d\nended-release-lines: %d\n---\n",
		st.LastSweep, st.LastComplete, next, st.Repos, st.Packages, st.Images, len(open), p1, p2, ended)
	b.WriteString("# Security register\n\nWritten by `tix cve update` from `.state.json` (main's host service runs it); " +
		"record a ticket or an assessment with `tix cve set`, never by editing this note.\n")

	b.WriteString("\n## Needs action\n")
	acting := false
	for _, r := range open {
		if a, ok := r.action(); ok {
			acting = true
			fmt.Fprintf(&b, "- %s · %s · %s · %s\n", a.Priority, r.link(), a.Need, cell(a.Summary))
		}
	}
	if !acting {
		b.WriteString("Nothing.\n")
	}

	findingTable := func(title, status string) {
		fmt.Fprintf(&b, "\n## %s\n", title)
		rows := st.rowsWhere(func(r *Row) bool { return r.Kind == KindFinding && r.Status == status })
		if len(rows) == 0 {
			b.WriteString("None.\n")
			return
		}
		b.WriteString("| P | ID | severity | KEV | EPSS | where | version → fix | ticket | since |\n|---|---|---|---|---|---|---|---|---|\n")
		for _, r := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.3f | %s | %s | %s | %s |\n", md(r.priority()), r.link(), md(r.severity()),
				yesNo(r.kev()), r.epss(), cells(r.where()), cells(r.fixes()), md(r.Ticket), md(r.Since))
		}
	}
	findingTable("Resolvable", StatusResolvable)
	findingTable("Unresolvable", StatusUnresolvable)

	b.WriteString("\n## Watch\n")
	watch := st.rowsWhere(func(r *Row) bool {
		return r.Kind == KindWatch && (r.Status == StatusWatch || r.Status == StatusNotAffected)
	})
	if len(watch) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| P | ID | product | images | assessment | ticket | since |\n|---|---|---|---|---|---|---|\n")
		for _, r := range watch {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", md(r.priority()), r.link(), md(r.Watch.Product),
				cells(r.where()), md(r.Assessment), md(r.Ticket), md(r.Since))
		}
	}

	b.WriteString("\n## Release lines\n")
	if len(st.Lifecycle) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| product | version | line | status | where |\n|---|---|---|---|---|\n")
		for _, l := range st.Lifecycle {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", md(l.Product), md(l.Version), md(l.Cycle), md(lifecycleStatus(l)), cells(lifecycleWhere(l)))
		}
	}

	b.WriteString("\n## Images\n")
	if len(st.ImageSummary) == 0 {
		b.WriteString("None scanned.\n")
	} else {
		b.WriteString("P1 and P2 findings in an image are rows above; the lower ones are counted here.\n\n" +
			"| image | where | lower findings | fixed by a rebuild or newer tag |\n|---|---|---|---|\n")
		for _, s := range st.ImageSummary {
			var where []string
			for _, w := range s.Where {
				where = append(where, repoName(w.Repo)+" "+w.File)
			}
			image := []string{s.Image}
			if s.Note != "" {
				image = append(image, s.Note)
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %d |\n", cells(image), cells(where), s.Lower, s.Fixable)
		}
	}

	b.WriteString("\n## Resolved\n")
	resolved := st.rowsWhere(func(r *Row) bool { return r.Status == StatusResolved })
	sort.SliceStable(resolved, func(i, j int) bool { return resolved[i].ResolvedOn > resolved[j].ResolvedOn })
	if len(resolved) > maxResolved {
		resolved = resolved[:maxResolved]
	}
	if len(resolved) == 0 {
		b.WriteString("None.\n")
	}
	for _, r := range resolved {
		fmt.Fprintf(&b, "- %s · %s · %s%s\n", r.ResolvedOn, r.link(), cell(ellipsis(r.summary(), 120)), ticketNote(r.Ticket))
	}

	b.WriteString("\n## Unscanned\n")
	if len(st.Unscanned) == 0 {
		b.WriteString("None.\n")
	}
	for _, u := range st.Unscanned {
		fmt.Fprintf(&b, "- %s · %s · %s\n", md(repoName(u.Repo)), md(u.File), cell(u.Reason))
	}
	if len(st.Skips) > 0 {
		b.WriteString("\n## Sources that failed\n")
		for _, s := range st.Skips {
			fmt.Fprintf(&b, "- %s · %s\n", s.Source, cell(s.Reason))
		}
	}
	b.WriteString("\n## Log\n")
	for i := len(st.Log) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "- %s\n", st.Log[i])
	}
	return b.String()
}

func lifecycleStatus(l Lifecycle) string {
	switch {
	case l.Ended && l.EOL != "":
		return "ended " + l.EOL
	case l.Ended:
		return "ended"
	case l.EndsSoon:
		return "ends " + l.EOL
	case l.Behind:
		return "behind: latest " + l.Latest
	case l.EOL != "":
		return "supported until " + l.EOL
	}
	return "supported"
}

func lifecycleWhere(l Lifecycle) []string {
	var out []string
	for _, c := range l.Where {
		s := repoName(c.Repo) + " " + c.File
		if c.Image != "" {
			s += " (" + c.Image + ")"
		}
		out = append(out, s)
	}
	return out
}

func ticketNote(t string) string {
	if t == "" {
		return ""
	}
	return " · " + md(t)
}

func (r *Row) Body() string {
	var b strings.Builder
	switch {
	case r.Finding != nil:
		f := r.Finding
		fmt.Fprintf(&b, "## Summary\n%s\n\n## Exposure\n", md(f.Summary))
		for _, p := range f.Packages {
			dev := ""
			if p.Dev {
				dev = " (dev/test only)"
			}
			fmt.Fprintf(&b, "- %s · %s · %s %s %s%s\n", md(repoName(p.Repo)), md(p.File), md(p.Ecosystem), md(p.Name), md(p.Version), dev)
		}
		b.WriteString("\n## Fix\n")
		for _, x := range r.fixes() {
			fmt.Fprintf(&b, "- %s\n", md(x))
		}
		if !f.Resolvable {
			b.WriteString("- No patched version for every package yet: follow the advisory's mitigation and the references.\n")
		}
		fmt.Fprintf(&b, "\n## Evidence\n- Severity: %s", md(f.Severity))
		if f.Score > 0 {
			fmt.Fprintf(&b, ", CVSS %.1f", f.Score)
		}
		if f.Vector != "" {
			fmt.Fprintf(&b, " (%s)", md(f.Vector))
		}
		fmt.Fprintf(&b, "\n- EPSS: %.3f\n- Remote code execution: %s; network: %s\n", f.EPSS, yesNoWord(f.RCE), yesNoWord(f.Network))
		if len(f.Aliases) > 0 {
			fmt.Fprintf(&b, "- Also known as: %s\n", md(strings.Join(f.Aliases, ", ")))
		}
		writeKEV(&b, f.KEV)
		for _, ref := range f.Refs {
			fmt.Fprintf(&b, "- <%s>\n", oneLine(strings.NewReplacer("<", "%3C", ">", "%3E").Replace(ref)))
		}
	case r.Watch != nil:
		w := r.Watch
		fmt.Fprintf(&b, "## Summary\n%s\n\n## Exposure\nImages running %s (the tag alone doesn't prove the version is affected):\n", md(w.Description), md(w.Product))
		for _, x := range r.where() {
			fmt.Fprintf(&b, "- %s\n", md(x))
		}
		fmt.Fprintf(&b, "\n## Assessment\n%s\n\n## Fix\nUpgrade the image to a release the vendor's advisory names as fixed (see the references).\n\n## Evidence\n- Severity: %s",
			md(orDash(r.Assessment)), md(w.Severity))
		if w.Score > 0 {
			fmt.Fprintf(&b, ", CVSS %.1f", w.Score)
		}
		if w.Vector != "" {
			fmt.Fprintf(&b, " (%s)", md(w.Vector))
		}
		fmt.Fprintf(&b, "\n- EPSS: %.3f\n- Remote code execution: %s; network: %s\n", w.EPSS, yesNoWord(w.RCE), yesNoWord(w.Network))
		writeKEV(&b, w.KEV)
		for _, ref := range w.Refs {
			fmt.Fprintf(&b, "- <%s>\n", oneLine(strings.NewReplacer("<", "%3C", ">", "%3E").Replace(ref)))
		}
	}
	return b.String()
}

func writeKEV(b *strings.Builder, k *KEVEntry) {
	if k == nil {
		return
	}
	fmt.Fprintf(b, "- CISA KEV: added %s, due %s: %s\n", md(k.DateAdded), md(k.DueDate), md(k.Action))
	if k.Ransomware == "Known" {
		b.WriteString("- Known use in ransomware campaigns\n")
	}
}

func yesNoWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "not assessed yet"
	}
	return s
}

func (r *Row) Advisory() string {
	var b strings.Builder
	var aliases []string
	if r.Finding != nil {
		aliases = r.Finding.Aliases
	}
	fmt.Fprintf(&b, "---\ntype: security-advisory\ntags: [security]\nid: %s\naliases-of: %s\npriority: %s\nstatus: %s\nticket: %s\nfirst-seen: %s\n---\n# %s\n\nWritten by `tix cve update`; see [[%s]].\n\n",
		yamlString(r.ID), yamlList(aliases), yamlString(r.priority()), yamlString(r.Status), yamlString(r.Ticket),
		yamlString(r.FirstSeen), md(r.ID), strings.TrimSuffix(RegisterMD, ".md"))
	b.WriteString(r.Body())
	b.WriteString("\n## History\n")
	for _, h := range r.History {
		fmt.Fprintf(&b, "- %s\n", md(h))
	}
	return b.String()
}

func (r *Row) JiraSummary() string {
	subject := ""
	switch {
	case r.Watch != nil && r.Kind == KindWatch:
		subject = r.Watch.Product
	case r.Finding != nil:
		repos := map[string]bool{}
		for _, p := range r.Finding.Packages {
			repos[repoName(p.Repo)] = true
		}
		names := sortedSet(repos)
		subject = strings.Join(names, ", ")
		if len(names) > 2 {
			subject = strings.Join(names[:2], ", ") + fmt.Sprintf(" +%d", len(names)-2)
		}
	}
	return fmt.Sprintf("[%s] %s (%s)", subject, ellipsis(firstLine(r.summary()), 90), r.ID)
}

func (r *Row) JiraDescription(swept string) string {
	return r.Body() + fmt.Sprintf("\nFound by the tix security sweep on %s.\n", swept)
}

func WriteVault(dir string, st *State, every time.Duration) error {
	root, err := OpenDir(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := writeIfChanged(root, RegisterMD, []byte(Render(st, every))); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, id := range st.ids() {
		r := st.Rows[id]
		if !r.major() && r.Ticket == "" {
			continue
		}
		name := AdvisoryDir + "/" + id + ".md"
		keep[name] = true
		if err := writeIfChanged(root, name, []byte(r.Advisory())); err != nil {
			return err
		}
	}
	if entries, err := fs.ReadDir(root.FS(), AdvisoryDir); err == nil {
		for _, e := range entries {
			name := AdvisoryDir + "/" + e.Name()
			if id, ok := strings.CutSuffix(e.Name(), ".md"); ok && e.Type().IsRegular() && ValidID(id) && !keep[name] && written(root, name) {
				_ = root.Remove(name)
			}
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeIfChanged(root, StateFile, append(b, '\n'))
}

const writtenMarker = "\ntype: security-advisory\n"

func written(root *os.Root, name string) bool {
	b, err := root.ReadFile(name)
	return err == nil && strings.Contains(string(b), writtenMarker) && strings.Contains(string(b), "Written by `tix cve update`")
}
