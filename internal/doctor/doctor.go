package doctor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Transcript struct {
	File       string
	Agent      string
	Sub        bool
	Session    string
	Models     []string
	Start, End time.Time
	Prompts    int
	ToolCalls  int
	ToolErrors int
	Interrupts int
	Compacted  int
	Lines      []string
}

type Limits struct {
	Text   int
	Input  int
	Result int
	Error  int
}

var DefaultLimits = Limits{Text: 1500, Input: 300, Result: 160, Error: 500}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

func ProjectsDir(getenv func(string) string, home string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	return filepath.Join(home, ".claude", "projects")
}

func encode(p string) string { return nonAlnum.ReplaceAllString(p, "-") }

func sessionCwd(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := scanner(f)
	for sc.Scan() {
		var r struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Cwd != "" {
			return r.Cwd
		}
	}
	return ""
}

func Subagents(session string) []string {
	files, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", "*.jsonl"))
	sort.Strings(files)
	return files
}

func AgentType(file string) string {
	data, err := os.ReadFile(strings.TrimSuffix(file, ".jsonl") + ".meta.json")
	if err != nil {
		return "subagent"
	}
	var m struct {
		AgentType string `json:"agentType"`
	}
	if json.Unmarshal(data, &m) != nil || m.AgentType == "" {
		return "subagent"
	}
	return m.AgentType
}

func scanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	return sc
}

type record struct {
	Type      string                 `json:"type"`
	Subtype   string                 `json:"subtype"`
	Timestamp string                 `json:"timestamp"`
	SessionID string                 `json:"sessionId"`
	IsMeta    bool                   `json:"isMeta"`
	Origin    *struct{ Kind string } `json:"origin"`
	Message   *struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

var skillBody = regexp.MustCompile(`^Base directory for this skill: \S*/skills/([^/\s]+)`)

func Condense(file, agent string, sub bool, lim Limits) (Transcript, error) {
	t := Transcript{File: file, Agent: agent, Sub: sub}
	f, err := os.Open(file)
	if err != nil {
		return t, err
	}
	defer f.Close()
	models := map[string]bool{}
	sc := scanner(f)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.SessionID != "" && t.Session == "" {
			t.Session = r.SessionID
		}
		if ts, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil {
			if t.Start.IsZero() || ts.Before(t.Start) {
				t.Start = ts
			}
			if ts.After(t.End) {
				t.End = ts
			}
		}
		switch r.Type {
		case "system":
			if r.Subtype == "compact_boundary" {
				t.Compacted++
				t.add("[context compacted]")
			}
		case "assistant":
			if r.Message == nil {
				continue
			}
			if r.Message.Model != "" && !strings.HasPrefix(r.Message.Model, "<") {
				models[r.Message.Model] = true
			}
			for _, b := range blocks(r.Message.Content) {
				switch b.Type {
				case "text":
					if s := strings.TrimSpace(b.Text); s != "" {
						t.add("ASSISTANT: " + clip(s, lim.Text))
					}
				case "tool_use":
					t.ToolCalls++
					t.add("TOOL " + b.Name + ": " + toolInput(b.Name, b.Input, lim.Input))
				}
			}
		case "user":
			if r.Message == nil {
				continue
			}
			if r.IsMeta {
				for _, b := range blocks(r.Message.Content) {
					if m := skillBody.FindStringSubmatch(b.Text); m != nil {
						t.add("[skill loaded: " + m[1] + "]")
					}
				}
				continue
			}
			human := r.Origin != nil && r.Origin.Kind == "human"
			var s string
			if json.Unmarshal(r.Message.Content, &s) == nil {
				t.user(s, human, lim)
				continue
			}
			for _, b := range blocks(r.Message.Content) {
				switch b.Type {
				case "text":
					t.user(b.Text, human, lim)
				case "tool_result":
					out := resultText(b.Content)
					if b.IsError {
						t.ToolErrors++
						t.add("ERROR: " + clip(out, lim.Error))
					} else {
						t.add("-> " + clip(out, lim.Result))
					}
				}
			}
		}
	}
	for m := range models {
		t.Models = append(t.Models, m)
	}
	sort.Strings(t.Models)
	return t, sc.Err()
}

func (t *Transcript) add(s string) { t.Lines = append(t.Lines, oneLine(s)) }

func (t *Transcript) user(s string, human bool, lim Limits) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
	case human:
		t.Prompts++
		t.add("USER: " + clip(s, lim.Text))
	case strings.HasPrefix(s, "[Request interrupted"):
		t.Interrupts++
		t.add("[user interrupted]")
	case skillBody.MatchString(s):
		t.add("[skill loaded: " + skillBody.FindStringSubmatch(s)[1] + "]")
	case strings.Contains(s, "Another Claude session sent a message"):
		t.add("AGENT MESSAGE: " + clip(s, lim.Input))
	case t.Sub && len(t.Lines) == 0:
		t.add("BRIEF: " + clip(s, lim.Text))
	}
}

func blocks(raw json.RawMessage) []block {
	var bs []block
	_ = json.Unmarshal(raw, &bs)
	return bs
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range blocks(raw) {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

func toolInput(name string, raw json.RawMessage, n int) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return clip(string(raw), n)
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch name {
	case "Bash":
		return clip(str("command"), n)
	case "Read", "Write", "Edit", "NotebookEdit":
		return str("file_path")
	case "Grep", "Glob":
		return clip(str("pattern")+" "+str("path"), n)
	case "Skill":
		return clip(strings.TrimSpace(str("skill")+" "+str("args")), n)
	case "Agent", "Task":
		s := str("subagent_type")
		if m := str("model"); m != "" {
			s += " model=" + m
		}
		return clip(s+" · "+str("description"), n)
	}
	return clip(string(raw), n)
}

func clip(s string, n int) string {
	s = oneLine(s)
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf(" …(+%d)", len(r)-n)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (t Transcript) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "# %s · %s\n", t.Agent, t.Session)
	fmt.Fprintf(bw, "models: %s · %s → %s · prompts %d · tool calls %d · tool errors %d · interrupts %d · compactions %d\n\n",
		strings.Join(t.Models, ","), stamp(t.Start), stamp(t.End), t.Prompts, t.ToolCalls, t.ToolErrors, t.Interrupts, t.Compacted)
	for _, l := range t.Lines {
		fmt.Fprintln(bw, l)
	}
	return bw.Flush()
}

func stamp(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format("2006-01-02T15:04Z")
}
