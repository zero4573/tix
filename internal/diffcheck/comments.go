package diffcheck

import (
	"path"
	"regexp"
	"strings"
)

type commentSyntax int

const (
	noComments commentSyntax = iota
	slashComments
	goComments
	hashComments
)

var slashExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".dart": true, ".rs": true, ".java": true, ".kt": true, ".kts": true, ".swift": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".cs": true, ".scala": true,
}

var hashExts = map[string]bool{
	".py": true, ".sh": true, ".bash": true, ".zsh": true, ".rb": true, ".nix": true,
	".yaml": true, ".yml": true, ".toml": true, ".tf": true, ".tfvars": true, ".hcl": true,
	".mk": true, ".pl": true, ".ps1": true, ".r": true, ".R": true,
}

var hashNames = map[string]bool{"Makefile": true, "GNUmakefile": true, "Dockerfile": true, "Containerfile": true, "Justfile": true, "justfile": true}

var commentKept = regexp.MustCompile(`SPDX-License-Identifier|(?i:copyright)|Code generated .* DO NOT EDIT|prettier-ignore|https?://`)

var slashDirective = regexp.MustCompile(`^(?://go:|//line |//export |//\s*\+build|///\s*<reference )`)

var hashDirective = regexp.MustCompile(`^(?:#!|#\s*-\*-|#\s*frozen_string_literal:|#\s*syntax=|#\s*escape=|#\s*yaml-language-server:|#\s*shellcheck\s+(?:shell|source)=)`)

var commentDelimiter = regexp.MustCompile(`^(?:/\*+|\*+/|\*|//+|#+)$`)

type commentMatcher struct{ syntax commentSyntax }

func (commentMatcher) forPath(p string) matcher { return commentMatcher{syntaxOf(p)} }

func (m commentMatcher) count(s string, into map[string]int) {
	if t := m.text(s); t != "" {
		into[t]++
	}
}

func CommentText(p, line string) string { return commentMatcher{syntaxOf(p)}.text(line) }

func (m commentMatcher) text(line string) string {
	t := strings.TrimSpace(line)
	switch m.syntax {
	case slashComments, goComments:
		t = strings.TrimPrefix(t, "{")
		switch {
		case strings.HasPrefix(t, "//"), strings.HasPrefix(t, "/*"):
		case m.syntax == goComments:
			return ""
		case t == "*", strings.HasPrefix(t, "* "), strings.HasPrefix(t, "*/"):
		default:
			return ""
		}
		if slashDirective.MatchString(t) {
			return ""
		}
	case hashComments:
		if !strings.HasPrefix(t, "#") || hashDirective.MatchString(t) {
			return ""
		}
	default:
		return ""
	}
	if commentDelimiter.MatchString(t) || commentKept.MatchString(t) {
		return ""
	}
	return t
}

func syntaxOf(p string) commentSyntax {
	for seg := range strings.SplitSeq(path.Dir(p), "/") {
		switch seg {
		case "testdata", "vendor", "node_modules":
			return noComments
		}
	}
	base := path.Base(p)
	switch ext := path.Ext(base); {
	case ext == ".go":
		return goComments
	case slashExts[ext]:
		return slashComments
	case hashExts[ext], hashNames[base], strings.HasPrefix(base, "Dockerfile."):
		return hashComments
	}
	return noComments
}

func IsCommentSource(p string) bool { return syntaxOf(p) != noComments }

type CommentNote struct {
	Path  string
	Lines int
}

func CheckComments(dir, sha string) ([]CommentNote, error) {
	hits, err := commitIDs(dir, sha, commentMatcher{}, false, IsCommentSource)
	if err != nil {
		return nil, err
	}
	var notes []CommentNote
	for _, h := range hits {
		if n := len(notes); n > 0 && notes[n-1].Path == h.Path {
			notes[n-1].Lines++
			continue
		}
		notes = append(notes, CommentNote{Path: h.Path, Lines: 1})
	}
	return notes, nil
}
