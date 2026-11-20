package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/note"
)

const JournalHeader = "promote-journal v2"

const (
	OpReplaceTree = "replace-tree"
	OpWrite       = "write"
	OpPut         = "put"
	OpTrash       = "trash"
	OpDrop        = "drop"
)

const (
	TreeBase    = "base"
	TreeAfter   = "after"
	WriteOfKept = "kept"
	WriteOfNote = "note"
)

const PreAbsent = "absent"

type Op struct {
	Kind, Arg, Dest, Hash string
}

func (o Op) String() string {
	switch o.Kind {
	case OpWrite:
		return o.Kind + " " + o.Arg + " " + o.Dest
	case OpPut, OpTrash, OpDrop:
		return o.Kind + " " + o.Arg + " " + o.Hash
	}
	return o.Kind + " " + o.Arg
}

func Hash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func validHash(h string) bool {
	x, ok := strings.CutPrefix(h, "sha256:")
	if !ok || len(x) != 64 {
		return false
	}
	for _, c := range x {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func StageRoot(vault, id string) string {
	return filepath.Join(note.TicketDir(vault, id), "specs-stage")
}

func StageDir(vault, id, slug string) string {
	return filepath.Join(StageRoot(vault, id), slug)
}

func JournalPath(vault, id, slug string) string {
	return filepath.Join(StageRoot(vault, id), slug+".journal")
}

func DonePath(vault, id, slug string) string {
	return JournalPath(vault, id, slug) + ".done"
}

func HeldPath(vault, id, slug string) string {
	return filepath.Join(StageRoot(vault, id), slug+".held")
}

func RenderJournal(ops []Op) []byte {
	var b strings.Builder
	b.WriteString(JournalHeader + "\n")
	for _, o := range ops {
		b.WriteString(o.String())
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func cleanRel(f string) bool {
	return f != "" && f != "." && !strings.HasPrefix(f, "/") && !strings.Contains(f, `\`) &&
		path.Clean(f) == f && f != ".." && !strings.HasPrefix(f, "../") && filepath.VolumeName(filepath.FromSlash(f)) == ""
}

func PlainName(f string) bool {
	return !strings.ContainsFunc(f, unicode.IsControl)
}

func JournalPathOK(f string) bool {
	return cleanRel(f) && PlainName(f)
}

func ParseJournal(b []byte) ([]Op, error) {
	s := string(b)
	head, rest, _ := strings.Cut(s, "\n")
	if head != JournalHeader {
		return nil, fmt.Errorf("first line %q, want %q", head, JournalHeader)
	}
	var ops []Op
	drops := false
	for i, l := range strings.Split(rest, "\n") {
		if l == "" {
			continue
		}
		kind, rest, _ := strings.Cut(l, " ")
		o := Op{Kind: kind, Arg: rest}
		ok := false
		switch kind {
		case OpReplaceTree:
			ok = rest == TreeBase || rest == TreeAfter
		case OpWrite:
			o.Arg, o.Dest, _ = strings.Cut(rest, " ")
			ok = (o.Arg == WriteOfKept || o.Arg == WriteOfNote) && JournalPathOK(o.Dest)
		case OpPut, OpTrash, OpDrop:
			if j := strings.LastIndex(rest, " "); j >= 0 {
				o.Arg, o.Hash = rest[:j], rest[j+1:]
				ok = JournalPathOK(o.Arg) && (validHash(o.Hash) || (kind == OpPut && o.Hash == PreAbsent))
			}
			if kind == OpDrop {
				drops = true
			} else if kind == OpPut && drops {
				return nil, fmt.Errorf("line %d: a put after a drop: %q", i+1, l)
			}
		}
		if !ok {
			return nil, fmt.Errorf("line %d: %q", i+1, l)
		}
		ops = append(ops, o)
	}
	return ops, nil
}

func ParseDone(b []byte, n int) (done map[int]bool, complete int, err error) {
	done = map[int]bool{}
	s := string(b)
	lines := strings.Split(s, "\n")
	torn := lines[len(lines)-1]
	lines = lines[:len(lines)-1]
	num := func(l string) (int, bool) {
		if l == "" || l[0] == '0' || len(l) > 9 {
			return 0, false
		}
		v := 0
		for _, c := range l {
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		return v, true
	}
	for i, l := range lines {
		v, ok := num(l)
		if !ok || v > n {
			return nil, 0, fmt.Errorf("progress line %d: %q", i+1, l)
		}
		done[v] = true
	}
	if torn != "" {
		if _, ok := num(torn); !ok {
			return nil, 0, fmt.Errorf("progress last line: %q", torn)
		}
	}
	return done, len(s) - len(torn), nil
}

func ParseHeld(b []byte) ([]string, error) {
	var out []string
	for i, l := range strings.Split(string(b), "\n") {
		if l == "" {
			continue
		}
		if !JournalPathOK(l) {
			return nil, fmt.Errorf("held line %d: %q", i+1, l)
		}
		out = append(out, l)
	}
	return out, nil
}

const PrevAbsent = "absent"

func ParseAbsent(b []byte) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{TreeBase: {}, TreeAfter: {}, WriteOfNote: {}}
	for i, l := range strings.Split(string(b), "\n") {
		if l == "" {
			continue
		}
		k, f, _ := strings.Cut(l, " ")
		m, ok := out[k]
		if !ok || !JournalPathOK(f) {
			return nil, fmt.Errorf("prev/absent line %d: %q", i+1, l)
		}
		m[f] = true
	}
	return out, nil
}

func RenderAbsent(base, after []string, note string) []byte {
	var b strings.Builder
	for _, f := range base {
		b.WriteString(TreeBase + " " + f + "\n")
	}
	for _, f := range after {
		b.WriteString(TreeAfter + " " + f + "\n")
	}
	if note != "" {
		b.WriteString(WriteOfNote + " " + note + "\n")
	}
	return []byte(b.String())
}

var ErrJournalDamaged = errors.New("damaged promote journal")

type damagedError struct {
	msg   string
	cause error
}

func (e *damagedError) Error() string { return e.msg }
func (e *damagedError) Unwrap() []error {
	return []error{ErrJournalDamaged, e.cause}
}

func DamagedJournal(vault, id, slug string, cause error) error {
	return &damagedError{
		msg: fmt.Sprintf("damaged promote journal %s (%v); nothing was written. Removing it, its stage %s, %s and %s (if there) discards the pending promote (the copy still holds every file not yet dropped)",
			config.TildePath(JournalPath(vault, id, slug)), cause, config.TildePath(StageDir(vault, id, slug))+string(filepath.Separator), config.TildePath(DonePath(vault, id, slug)), config.TildePath(HeldPath(vault, id, slug))),
		cause: cause,
	}
}

func ReadJournal(vault, id, slug string) (ops []Op, ok bool, err error) {
	b, err := os.ReadFile(JournalPath(vault, id, slug))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	ops, err = ParseJournal(b)
	if err != nil {
		return nil, false, DamagedJournal(vault, id, slug, err)
	}
	return ops, true, nil
}

func PendingPromote(vault, id, slug string) error {
	_, err := os.Lstat(JournalPath(vault, id, slug))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return &pendingPromoteError{fmt.Sprintf("%s: %s", slug, PromotePendingNote(id))}
}

var ErrPromotePending = errors.New("promote pending")

type pendingPromoteError struct{ msg string }

func (e *pendingPromoteError) Error() string        { return e.msg }
func (e *pendingPromoteError) Is(target error) bool { return target == ErrPromotePending }

func PromotePendingNote(id string) string {
	return "promote pending: run tix spec promote " + id
}

func Pending(vault, id, slug string) error {
	if err := PendingRebase(vault, id, slug); err != nil {
		return err
	}
	return PendingPromote(vault, id, slug)
}

func PendingNote(err error, id, slug string) string {
	switch {
	case errors.Is(err, ErrRebasePending):
		return RefreshPending(id, slug)
	case errors.Is(err, ErrPromotePending):
		return PromotePendingNote(id)
	}
	return ""
}
