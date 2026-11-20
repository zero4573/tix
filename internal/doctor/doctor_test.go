package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mainSession = `{"type":"mode","sessionId":"s1"}
{"type":"user","cwd":"WS","sessionId":"s1","timestamp":"2026-11-10T10:00:00Z","origin":{"kind":"human"},"message":{"role":"user","content":"/tix:work-ticket MAN-1"}}
{"type":"user","cwd":"WS","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /nix/store/x-tix-plugin/skills/work-ticket\n\n# work-ticket"}]}}
{"type":"assistant","cwd":"WS","timestamp":"2026-11-10T10:01:00Z","message":{"model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"secret plan"},{"type":"text","text":"Starting\nthe kickoff."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"tix ws add MAN-1 repo"}}]}}
{"type":"user","cwd":"WS","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\nfatal: branch in use"}]}}
{"type":"assistant","cwd":"WS","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t2","name":"Agent","input":{"subagent_type":"tix:lead","model":"opus","description":"Lead for MAN-1","prompt":"long brief"}}]}}
{"type":"user","cwd":"WS","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"launched"}]}]}}
{"type":"user","cwd":"WS","message":{"role":"user","content":[{"type":"text","text":"Another Claude session sent a message: wave 1 merged"}]}}
{"type":"user","cwd":"WS","message":{"role":"user","content":"[Request interrupted by user]"}}
{"type":"system","subtype":"compact_boundary","cwd":"WS"}
{"type":"user","cwd":"WS","message":{"role":"user","content":"This session is being continued from a previous conversation"}}
{"type":"user","cwd":"WS","timestamp":"2026-11-10T11:00:00Z","origin":{"kind":"human"},"message":{"role":"user","content":"drop the cache change"}}
not json
`

const subSession = `{"type":"user","cwd":"WS","sessionId":"s1","timestamp":"2026-11-10T10:05:00Z","message":{"role":"user","content":"You are the lead for MAN-1."}}
{"type":"assistant","cwd":"WS","timestamp":"2026-11-10T10:06:00Z","message":{"model":"claude-sonnet-5-5","content":[{"type":"tool_use","id":"u1","name":"Read","input":{"file_path":"/v/board.md"}}]}}
`

func fixture(t *testing.T) (projects, workRoot string) {
	t.Helper()
	root := t.TempDir()
	workRoot = filepath.Join(root, "work-a")
	ws := filepath.Join(workRoot, "MAN-1")
	projects = filepath.Join(root, "projects")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(projects, encode(ws))
	write(filepath.Join(dir, "s1.jsonl"), strings.ReplaceAll(mainSession, "WS", ws))
	write(filepath.Join(dir, "s1", "subagents", "agent-abc123456789.jsonl"), strings.ReplaceAll(subSession, "WS", ws))
	write(filepath.Join(dir, "s1", "subagents", "agent-abc123456789.meta.json"), `{"agentType":"tix:lead"}`)
	other := filepath.Join(workRoot, "MAN-10")
	write(filepath.Join(projects, encode(other), "s2.jsonl"), `{"type":"user","cwd":"`+other+`"}`+"\n")
	write(filepath.Join(projects, encode(workRoot+"/.teach-a"), "s3.jsonl"), `{"type":"user","cwd":"`+workRoot+`/.teach-a"}`+"\n")
	write(filepath.Join(projects, encode(filepath.Join(ws, "repo")), "s4.jsonl"), `{"type":"user","cwd":"`+filepath.Join(ws, "repo")+`"}`+"\n")
	return projects, workRoot
}

func TestTicketsGroupsSessionsByWorkspace(t *testing.T) {
	projects, workRoot := fixture(t)
	got, err := Tickets(projects, workRoot)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{}
	for _, tk := range got {
		ids[tk.ID] = len(tk.Sessions)
	}
	if ids["MAN-1"] != 2 || ids["MAN-10"] != 1 || len(ids) != 2 {
		t.Fatalf("tickets = %v, want MAN-1 with its own and its repo's session, MAN-10 apart, no dot-dirs", ids)
	}
}

func TestCondense(t *testing.T) {
	projects, workRoot := fixture(t)
	file := filepath.Join(projects, encode(filepath.Join(workRoot, "MAN-1")), "s1.jsonl")
	tr, err := Condense(file, "main", false, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Prompts != 2 || tr.ToolCalls != 2 || tr.ToolErrors != 1 || tr.Interrupts != 1 || tr.Compacted != 1 {
		t.Fatalf("counts = %+v", tr)
	}
	if len(tr.Models) != 1 || tr.Models[0] != "claude-opus-5-5" {
		t.Fatalf("models = %v", tr.Models)
	}
	text := strings.Join(tr.Lines, "\n")
	for _, want := range []string{
		"USER: /tix:work-ticket MAN-1",
		"[skill loaded: work-ticket]",
		"ASSISTANT: Starting the kickoff.",
		"TOOL Bash: tix ws add MAN-1 repo",
		"ERROR: Exit code 1 fatal: branch in use",
		"TOOL Agent: tix:lead model=opus · Lead for MAN-1",
		"-> launched",
		"AGENT MESSAGE: Another Claude session sent a message: wave 1 merged",
		"[user interrupted]",
		"[context compacted]",
		"USER: drop the cache change",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	for _, not := range []string{"secret plan", "long brief", "previous conversation"} {
		if strings.Contains(text, not) {
			t.Errorf("kept %q", not)
		}
	}
}

func TestCondenseSubagentBrief(t *testing.T) {
	projects, workRoot := fixture(t)
	sub := filepath.Join(projects, encode(filepath.Join(workRoot, "MAN-1")), "s1", "subagents", "agent-abc123456789.jsonl")
	if got := AgentType(sub); got != "tix:lead" {
		t.Fatalf("agent type = %q", got)
	}
	tr, err := Condense(sub, "tix:lead", true, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Prompts != 0 || len(tr.Lines) < 2 || tr.Lines[0] != "BRIEF: You are the lead for MAN-1." || tr.Lines[1] != "TOOL Read: /v/board.md" {
		t.Fatalf("lines = %q prompts = %d", tr.Lines, tr.Prompts)
	}
}

func TestClip(t *testing.T) {
	if got := clip("a  b\nc", 10); got != "a b c" {
		t.Fatalf("clip = %q", got)
	}
	if got := clip("abcdef", 3); got != "abc …(+3)" {
		t.Fatalf("clip = %q", got)
	}
}

func TestBundle(t *testing.T) {
	projects, workRoot := fixture(t)
	got, _ := Tickets(projects, workRoot)
	var man1 Ticket
	for _, tk := range got {
		if tk.ID == "MAN-1" {
			man1 = tk
		}
	}
	ticketDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ticketDir, "board.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	s, err := Bundle(out, "MAN-1", ticketDir, man1.Sessions, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if s.Transcripts != 2 || s.ToolErrors != 1 {
		t.Fatalf("summary = %+v", s)
	}
	idx, err := os.ReadFile(filepath.Join(out, "MAN-1", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- " + filepath.Join(ticketDir, "board.md"),
		"| transcripts/001-main-s1.md | main | claude-opus-5-5 | 2026-11-10T10:00Z | 2026-11-10T11:00Z | 2 | 2 | 1 | 1 | 1 |",
		"| transcripts/002-tix-lead-abc12345.md | tix:lead | claude-sonnet-5-5 |",
	} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("index misses %q:\n%s", want, idx)
		}
	}
	if strings.Contains(string(idx), "main-s4") {
		t.Errorf("index lists an empty transcript")
	}
	if strings.Contains(string(idx), "plan.md") {
		t.Errorf("index lists a missing evidence file")
	}
}
