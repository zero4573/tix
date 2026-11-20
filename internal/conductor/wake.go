package conductor

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/note"
)

type Waker struct {
	S         *Server
	Pane      string
	Command   string
	MainFile  string
	Debounce  time.Duration
	IdleAfter time.Duration
	PRPoll    time.Duration
	SyncEach  time.Duration
	Tick      time.Duration

	prev      map[string]string
	status    map[string]string
	idleSince map[string]time.Time
	idleTold  map[string]bool
	events    map[string][]string
	lastWake  time.Time
	lastPR    time.Time
	lastSync  time.Time
}

func (w *Waker) Run(ctx context.Context, poll time.Duration) {
	now := time.Now()
	w.lastWake, w.lastPR, w.lastSync = now, now, now
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
		w.Step(ctx, time.Now())
	}
}

var ticketLine = regexp.MustCompile(`^- ([A-Z][A-Z0-9_]*-[A-Z0-9]+) · ([a-z-]+)`)

func (w *Waker) stages() map[string]string {
	m := map[string]string{}
	f, err := os.Open(w.MainFile)
	if err != nil {
		return m
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(l) == "## Tickets"
			continue
		}
		if mm := ticketLine.FindStringSubmatch(l); in && mm != nil {
			m[mm[1]] = mm[2]
		}
	}
	return m
}

func (w *Waker) add(key, ev string) {
	for _, e := range w.events[key] {
		if e == ev {
			return
		}
	}
	w.events[key] = append(w.events[key], ev)
}

func (w *Waker) Step(ctx context.Context, now time.Time) string {
	if w.events == nil {
		w.events, w.idleSince, w.idleTold, w.status = map[string][]string{}, map[string]time.Time{}, map[string]bool{}, map[string]string{}
	}
	ss, err := w.S.sessions(ctx)
	if err != nil {
		return ""
	}
	stages := w.stages()
	first := w.prev == nil
	cur := map[string]string{}
	for _, x := range ss {
		cur[x.ID] = x.State
		stage, mine := stages[x.ID]
		if x.State == "idle" || x.State == "done" {
			if _, ok := w.idleSince[x.ID]; !ok {
				w.idleSince[x.ID] = now
			}
		} else {
			delete(w.idleSince, x.ID)
			delete(w.idleTold, x.ID)
		}
		if first || !mine {
			continue
		}
		p := w.prev[x.ID]
		switch {
		case p == "working" && (x.State == "idle" || x.State == "done") &&
			(stage == "checking" || stage == "shipping" || stage == "feedback"):
			w.add(x.ID, "finished a turn")
		case stage == "working" && !w.idleTold[x.ID] && w.IdleAfter > 0 &&
			!w.idleSince[x.ID].IsZero() && now.Sub(w.idleSince[x.ID]) >= w.IdleAfter:
			w.idleTold[x.ID] = true
			w.add(x.ID, "idle "+w.IdleAfter.String())
		}
	}
	for id := range w.prev {
		if _, ok := cur[id]; !ok {
			delete(w.idleSince, id)
			delete(w.idleTold, id)
			if _, mine := stages[id]; mine && !w.S.wasStopped(id) {
				w.add(id, "exited")
			}
		}
	}
	for id := range stages {
		st := w.S.status(id)
		if old, ok := w.status[id]; ok && old != st && !first {
			w.add(id, "status "+st)
		}
		w.status[id] = st
	}
	w.prev = cur
	if first {
		return ""
	}
	if w.PRPoll > 0 && now.Sub(w.lastPR) >= w.PRPoll {
		w.lastPR = now
		for id, ev := range w.S.pollPRs(ctx) {
			w.add(id, ev)
		}
	}
	if w.SyncEach > 0 && now.Sub(w.lastSync) >= w.SyncEach {
		w.lastSync = now
		before := w.S.ticketIDs()
		if r := w.S.sync(ctx); r.Status == "ok" {
			var added []string
			for id := range w.S.ticketIDs() {
				if !before[id] {
					added = append(added, id)
				}
			}
			if len(added) > 0 {
				sort.Strings(added)
				w.add("sync", "new tickets "+strings.Join(added, " "))
			}
		}
	}
	since := now.Sub(w.lastWake)
	tick := w.Tick > 0 && since >= w.Tick
	if since < w.Debounce || (len(w.events) == 0 && !tick) || !w.mainIdle(ctx) {
		return ""
	}
	text := w.Command
	if len(w.events) > 0 {
		var ev []string
		for id, e := range w.events {
			ev = append(ev, id+" "+strings.Join(e, ", "))
		}
		sort.Strings(ev)
		text += " " + strings.Join(ev, "; ")
	}
	if _, err := w.S.herdr(ctx, "agent", "prompt", w.Pane, text); err != nil {
		return ""
	}
	w.lastWake, w.events = now, map[string][]string{}
	return text
}

func (s *Server) ticketIDs() map[string]bool {
	m := map[string]bool{}
	dirs, _ := filepath.Glob(filepath.Join(s.Vault, "tickets", "*", "*"))
	for _, d := range dirs {
		id := filepath.Base(d)
		if note.ValidKey(id) {
			if _, err := os.Stat(filepath.Join(d, id+".md")); err == nil {
				m[id] = true
			}
		}
	}
	return m
}

func (w *Waker) mainIdle(ctx context.Context) bool {
	out, err := w.S.herdr(ctx, "agent", "get", w.Pane)
	if err != nil {
		return false
	}
	var r struct {
		Result struct {
			Agent herdrAgent `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil {
		return false
	}
	return r.Result.Agent.Status == "idle" || r.Result.Agent.Status == "done"
}
