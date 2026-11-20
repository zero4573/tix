package conductor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/cvewatch"
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
	SweepEach time.Duration
	Tick      time.Duration

	prev        map[string]string
	status      map[string]string
	idleSince   map[string]time.Time
	idleTold    map[string]bool
	events      map[string][]string
	lastWake    time.Time
	lastPR      time.Time
	lastSync    time.Time
	sweepTry    time.Time
	sweeping    bool
	toldFailure bool
	swept       chan sweepResult
	ack         []string
}

const (
	SweepRetry   = 30 * time.Minute
	sweepTimeout = 30 * time.Minute
)

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
	w.sweep(ctx, now)
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
	if len(w.ack) > 0 {
		go w.S.ackSweep(context.Background(), w.ack)
		w.ack = nil
	}
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

func (w *Waker) sweep(ctx context.Context, now time.Time) {
	if w.swept == nil {
		w.swept = make(chan sweepResult, 1)
	}
	select {
	case r := <-w.swept:
		w.sweeping = false
		failed := strings.HasPrefix(r.event, sweepFailed)
		if r.event != "" && !(failed && w.toldFailure) {
			w.add("security", r.event)
			w.ack = append(w.ack, r.tell...)
		}
		w.toldFailure = failed
	default:
	}
	if w.SweepEach <= 0 || w.sweeping || (!w.sweepTry.IsZero() && now.Sub(w.sweepTry) < SweepRetry) {
		return
	}
	if st, err := cvewatch.LoadState(filepath.Join(w.S.Vault, SecurityDir)); err == nil && !st.Due(now, w.SweepEach) {
		return
	}
	w.sweeping, w.sweepTry = true, now
	go func() { w.swept <- w.S.runSweep(ctx, w.SweepEach) }()
}

const (
	SecurityDir = "security"
	sweepFailed = "sweep failed: "
)

type sweepResult struct {
	event string
	tell  []string
}

const ackTimeout = 5 * time.Minute

func (s *Server) ackSweep(ctx context.Context, tell []string) {
	cctx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()
	if _, err := s.Run(cctx, []string{"TIX_VAULT=" + s.Vault}, s.Tix, append([]string{"cve", "ack", "--"}, tell...)...); err != nil {
		fmt.Fprintf(os.Stderr, "%s tix cve ack: %v (the items are told again)\n", time.Now().Format(time.RFC3339), err)
	}
}

func (s *Server) runSweep(ctx context.Context, every time.Duration) sweepResult {
	cctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	out, err := s.Run(cctx, []string{"TIX_VAULT=" + s.Vault}, s.Tix, "cve", "update", "--every", every.String(), "--json")
	if err != nil {
		return sweepResult{event: sweepFailed + strings.ReplaceAll(tail(err.Error(), 2), "\n", " ")}
	}
	var r cvewatch.UpdateResult
	if json.Unmarshal(out, &r) != nil {
		return sweepResult{event: sweepFailed + "unreadable result " + tail(string(out), 1)}
	}
	return sweepResult{event: r.Event(), tell: r.Tell}
}
