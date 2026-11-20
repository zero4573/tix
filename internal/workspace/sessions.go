package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/lock"
	"github.com/zero4573/tix/internal/proc"
)

func SessionsFile(dir string) string { return filepath.Join(dir, ".sessions.json") }

type Record struct {
	PID     int    `json:"pid"`
	Host    string `json:"host"`
	Proc    string `json:"proc"`
	Command string `json:"command"`
	Started string `json:"started"`
}

type sessionsDoc struct {
	Sessions []Record `json:"sessions"`
}

var (
	inContainer = gitx.InContainer
	hostname    = os.Hostname
	identity    = proc.Identity
)

func RecordSession(dir, command string) error {
	return updateSessions(dir, func(recs []Record) ([]Record, error) {
		return append(alive(recs), ownRecord(command)), nil
	})
}

type RunningError struct{ Record Record }

func (e *RunningError) Error() string {
	return fmt.Sprintf("a session is running in this workspace (pid %d)", e.Record.PID)
}

func ClaimSession(dir, command string) error {
	return updateSessions(dir, func(recs []Record) ([]Record, error) {
		if !inContainer() {
			for _, r := range recs {
				if recordLiveness(r) == LivenessAlive {
					return nil, &RunningError{r}
				}
			}
		}
		return append(alive(recs), ownRecord(command)), nil
	})
}

func ownRecord(command string) Record {
	pid := os.Getpid()
	host, _ := hostname()
	id, _ := identity(pid)
	return Record{PID: pid, Host: host, Proc: id, Command: command, Started: time.Now().Format(time.RFC3339)}
}

func PruneSessions(dir string) error {
	if _, err := os.Stat(SessionsFile(dir)); err != nil {
		return nil
	}
	return updateSessions(dir, func(recs []Record) ([]Record, error) { return alive(recs), nil })
}

func alive(recs []Record) []Record {
	if inContainer() {
		return recs
	}
	var out []Record
	for _, r := range recs {
		if recordLiveness(r) != LivenessDead {
			out = append(out, r)
		}
	}
	return out
}

func updateSessions(dir string, fn func([]Record) ([]Record, error)) error {
	file := SessionsFile(dir)
	return lock.With(file+".lock", func() error {
		doc := readSessions(file)
		recs, err := fn(doc.Sessions)
		if err != nil {
			return err
		}
		doc.Sessions = recs
		if doc.Sessions == nil {
			doc.Sessions = []Record{}
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		tmp := file + ".tmp"
		if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, file)
	})
}

func readSessions(file string) sessionsDoc {
	var doc sessionsDoc
	if data, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	return doc
}

type Liveness int

const (
	LivenessUnknown Liveness = iota
	LivenessAlive
	LivenessDead
)

func sessionLiveness(dir string) (Liveness, Record) {
	if inContainer() {
		return LivenessUnknown, Record{}
	}
	recs := readSessions(SessionsFile(dir)).Sessions
	if len(recs) == 0 {
		return LivenessUnknown, Record{}
	}
	dead := 0
	for _, r := range recs {
		switch recordLiveness(r) {
		case LivenessAlive:
			return LivenessAlive, r
		case LivenessDead:
			dead++
		}
	}
	if dead == len(recs) {
		return LivenessDead, Record{}
	}
	return LivenessUnknown, Record{}
}

func recordLiveness(r Record) Liveness {
	if host, err := hostname(); err != nil || r.Host != host || r.Proc == "" {
		return LivenessUnknown
	}
	id, err := identity(r.PID)
	switch {
	case errors.Is(err, proc.ErrNotRunning):
		return LivenessDead
	case err != nil:
		return LivenessUnknown
	case id != r.Proc:
		return LivenessDead
	}
	return LivenessAlive
}
