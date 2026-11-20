package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func shipped(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []struct {
		fs   fs.FS
		name string
	}{{Scaffold, "vault-scaffold"}, {Files, "plugin"}} {
		err := fs.WalkDir(root.fs, root.name, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(root.fs, p)
			out[p] = string(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestNothingShippedRefersToOldSystemNotes(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`projects/system`),
		regexp.MustCompile(`\[\[system\]\]`),
		regexp.MustCompile(`service-map`),
		regexp.MustCompile(`compatibility-matrix`),
		regexp.MustCompile(`system-decisions`),
		regexp.MustCompile(`(?i)microservice`),
		regexp.MustCompile(`(?m)^\s*services:`),
		regexp.MustCompile(`"services"`),
	}
	files := shipped(t)
	if len(files) < 20 {
		t.Fatalf("only %d shipped files found", len(files))
	}
	for p, text := range files {
		for _, re := range banned {
			if loc := re.FindStringIndex(text); loc != nil {
				line := strings.Count(text[:loc[0]], "\n") + 1
				t.Errorf("%s:%d: %q", p, line, text[loc[0]:loc[1]])
			}
		}
	}
}

var (
	commentRe    = regexp.MustCompile(`(?s)<!--.*?-->`)
	fenceRe      = regexp.MustCompile("(?ms)^\\s*```.*?^\\s*```")
	inlineCodeRe = regexp.MustCompile("`[^`\n]*`")
	wikilinkRe   = regexp.MustCompile(`\[\[([^\[\]|#^]*)`)
)

func TestTemplateLinksResolveToNotesInEveryVaultOrDocumentedExamples(t *testing.T) {
	allowed := map[string]bool{
		"AGENTS": true, "my-note-decisions": true, "my-note-compatibility": true,
		"provider-owner-repo": true, "TICKET-ID": true, "feature-flow-sequence": true,
		"feature-note": true, "group-name": true, "note-a": true, "note-b": true,
		"my-note": true, "my-note-resources": true, "my-note-glossary": true, "my-note-notes": true,
		"topic-name": true,
	}
	n := 0
	for p, text := range shipped(t) {
		if !strings.HasPrefix(p, "vault-scaffold/templates/") {
			continue
		}
		n++
		text = strings.NewReplacer("{{title}}", "my-note", "{{date}}", "2026-01-01").Replace(text)
		for _, re := range []*regexp.Regexp{commentRe, fenceRe, inlineCodeRe} {
			text = re.ReplaceAllString(text, "")
		}
		for _, m := range wikilinkRe.FindAllStringSubmatch(text, -1) {
			if !allowed[m[1]] {
				t.Errorf("%s links [[%s]]", p, m[1])
			}
		}
		if base := strings.TrimPrefix(p, "vault-scaffold/templates/"); base == "sequence.md" || base == "feature.md" || base == "decision.md" {
			if !strings.Contains(text, "\nprojects: []\n") {
				t.Errorf("%s has no projects: [] property", p)
			}
		}
	}
	if n < 10 {
		t.Fatalf("only %d templates found", n)
	}
}
