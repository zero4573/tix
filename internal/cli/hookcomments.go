package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/diffcheck"
	"github.com/zero4573/tix/internal/gitx"
)

func hookCommentsCmd() *cobra.Command {
	var anywhere bool
	cmd := &cobra.Command{
		Use:   "comment-check",
		Short: "Tell the session when an edit adds comments (PostToolUse on Edit, MultiEdit, Write)",
		Long: `For an Edit, MultiEdit or Write in a ticket or spec session: finds the
whole-line comments the edit added (the lines tix ws diff-check counts as
comment-added, in Go, TS/JS, Dart and the other // languages, and in shell,
Python, Nix, YAML, Makefiles and the other # languages) and tells the session
to remove them unless they are a kind tix:practices keeps. A Write is compared
with the file at HEAD. Comments the file already had are not counted.
Elsewhere it does nothing; never fails.

--anywhere is for the user's own Claude Code settings: it checks every
session outside a tix workspace instead (inside one, the plugin's hook
already runs). TIX_COMMENT_CHECK=0 turns it off, e.g. in a repo's
.claude/settings.json env for a repo whose house style has comments.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("TIX_COMMENT_CHECK") == "0" {
				return nil
			}
			if out := commentHook(os.Stdin, anywhere); out != nil {
				_ = json.NewEncoder(os.Stdout).Encode(out)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&anywhere, "anywhere", false, "check sessions outside tix workspaces (for the user's own settings), not those inside")
	return cmd
}

type blockOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type commentEdit struct {
	Old string `json:"old_string"`
	New string `json:"new_string"`
}

func commentHook(in io.Reader, anywhere bool) *blockOutput {
	var p struct {
		Cwd   string `json:"cwd"`
		Tool  string `json:"tool_name"`
		Input struct {
			File    string        `json:"file_path"`
			Content string        `json:"content"`
			Edits   []commentEdit `json:"edits"`
			commentEdit
		} `json:"tool_input"`
	}
	if json.NewDecoder(in).Decode(&p) != nil || p.Input.File == "" || !diffcheck.IsCommentSource(filepath.ToSlash(p.Input.File)) {
		return nil
	}
	if dir, _ := findWorkspace(p.Cwd); (dir != "") == anywhere {
		return nil
	}
	var edits []commentEdit
	switch p.Tool {
	case "Edit":
		edits = []commentEdit{p.Input.commentEdit}
	case "MultiEdit":
		edits = p.Input.Edits
	case "Write":
		edits = []commentEdit{{Old: fileAtHead(p.Input.File), New: p.Input.Content}}
	default:
		return nil
	}
	var added []string
	for _, e := range edits {
		added = append(added, addedComments(p.Input.File, e)...)
	}
	if len(added) == 0 {
		return nil
	}
	return &blockOutput{Decision: "block", Reason: commentReason(p.Input.File, added)}
}

func addedComments(file string, e commentEdit) []string {
	had := map[string]int{}
	for l := range strings.SplitSeq(e.Old, "\n") {
		if t := diffcheck.CommentText(file, l); t != "" {
			had[t]++
		}
	}
	var added []string
	for l := range strings.SplitSeq(e.New, "\n") {
		t := diffcheck.CommentText(file, l)
		switch {
		case t == "":
		case had[t] > 0:
			had[t]--
		default:
			added = append(added, t)
		}
	}
	return added
}

func fileAtHead(file string) string {
	out, err := gitx.Out(filepath.Dir(file), "show", "HEAD:./"+filepath.Base(file))
	if err != nil {
		return ""
	}
	return out
}

func commentReason(file string, added []string) string {
	shown := added
	if len(shown) > 3 {
		shown = shown[:3]
	}
	return fmt.Sprintf("%s: this edit adds %d comment line(s), e.g. %q. Write no comments: "+
		"say it in names, types and structure, and move design rationale to the docs. Only these stay: a license "+
		"header, a vendor/platform/protocol quirk with what forces it, an issue or RFC link, a lint suppression "+
		"of a faulty or style-only rule with its reason, and doc comments of a package other repos import (not "+
		"an app's own classes and functions). Remove the others now; leave a kept kind as it is.",
		file, len(added), strings.Join(shown, " / "))
}
