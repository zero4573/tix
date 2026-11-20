package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Ticket struct {
	ID       string
	Sessions []string
	Last     time.Time
}

func Tickets(projects, workRoot string) ([]Ticket, error) {
	roots := []string{filepath.Clean(workRoot)}
	if r, err := filepath.EvalSymlinks(workRoot); err == nil && r != roots[0] {
		roots = append(roots, r)
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	byID := map[string]*Ticket{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, root := range roots {
			if !strings.HasPrefix(e.Name(), encode(root)+"-") {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(projects, e.Name(), "*.jsonl"))
			for _, f := range files {
				rel, err := filepath.Rel(root, sessionCwd(f))
				if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
					continue
				}
				id := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
				if strings.HasPrefix(id, ".") {
					continue
				}
				t := byID[id]
				if t == nil {
					t = &Ticket{ID: id}
					byID[id] = t
				}
				t.Sessions = append(t.Sessions, f)
				if st, err := os.Stat(f); err == nil && st.ModTime().After(t.Last) {
					t.Last = st.ModTime()
				}
			}
			break
		}
	}
	out := make([]Ticket, 0, len(byID))
	for _, t := range byID {
		sort.Strings(t.Sessions)
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

var evidence = []string{"board.md", "plan.md", "relay.md", "backlog.md", "design.md", "verification.md", "board-archive.md", "reviews", "tasks"}

type Summary struct {
	ID          string
	Dir         string
	Transcripts int
	ToolErrors  int
	Bytes       int64
}

func Bundle(out, id, ticketDir string, sessions []string, lim Limits) (Summary, error) {
	dir := filepath.Join(out, id)
	tdir := filepath.Join(dir, "transcripts")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		return Summary{}, err
	}
	s := Summary{ID: id, Dir: dir}
	var ts []Transcript
	for _, sess := range sessions {
		main, err := Condense(sess, "main", false, lim)
		if err != nil {
			return s, fmt.Errorf("%s: %w", sess, err)
		}
		if len(main.Lines) > 0 {
			ts = append(ts, main)
		}
		for _, sub := range Subagents(sess) {
			t, err := Condense(sub, AgentType(sub), true, lim)
			if err != nil {
				return s, fmt.Errorf("%s: %w", sub, err)
			}
			if len(t.Lines) > 0 {
				ts = append(ts, t)
			}
		}
	}
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].Start.IsZero() != ts[j].Start.IsZero() {
			return ts[j].Start.IsZero()
		}
		return ts[i].Start.Before(ts[j].Start)
	})
	var idx strings.Builder
	fmt.Fprintf(&idx, "# %s\n\nticket folder: %s\n", id, orDash(ticketDir))
	if ticketDir != "" {
		for _, e := range evidence {
			if _, err := os.Stat(filepath.Join(ticketDir, e)); err == nil {
				fmt.Fprintf(&idx, "- %s\n", filepath.Join(ticketDir, e))
			}
		}
	}
	idx.WriteString("\n| file | agent | models | start | end | prompts | tool calls | tool errors | interrupts | compactions | lines |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for i, t := range ts {
		name := fmt.Sprintf("%03d-%s-%s.md", i+1, safe(t.Agent), short(t.Session, t.File))
		f, err := os.Create(filepath.Join(tdir, name))
		if err != nil {
			return s, err
		}
		werr := t.Write(f)
		st, _ := f.Stat()
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return s, werr
		}
		if st != nil {
			s.Bytes += st.Size()
		}
		s.Transcripts++
		s.ToolErrors += t.ToolErrors
		fmt.Fprintf(&idx, "| transcripts/%s | %s | %s | %s | %s | %d | %d | %d | %d | %d | %d |\n",
			name, t.Agent, orDash(strings.Join(t.Models, ",")), stamp(t.Start), stamp(t.End),
			t.Prompts, t.ToolCalls, t.ToolErrors, t.Interrupts, t.Compacted, len(t.Lines))
	}
	return s, os.WriteFile(filepath.Join(dir, "index.md"), []byte(idx.String()), 0o644)
}

func safe(s string) string { return strings.Trim(nonAlnum.ReplaceAllString(s, "-"), "-") }

func short(session, file string) string {
	if session == "" {
		session = strings.TrimSuffix(filepath.Base(file), ".jsonl")
	}
	if b := strings.TrimSuffix(filepath.Base(file), ".jsonl"); strings.HasPrefix(b, "agent-") {
		session = strings.TrimPrefix(b, "agent-")
	}
	if len(session) > 8 {
		session = session[:8]
	}
	return session
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
