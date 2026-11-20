package modelcache

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/embed"
)

//go:embed pins.json
var pinsJSON []byte

type libFile struct {
	Member string `json:"member"`
	As     string `json:"as"`
}

type asset struct {
	URL    string    `json:"url"`
	SHA256 string    `json:"sha256"`
	Dir    string    `json:"dir"`
	Files  []libFile `json:"files"`
}

type modelPin struct {
	File        string `json:"file"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Dims        int    `json:"dims"`
	QueryPrefix string `json:"query_prefix"`
	Default     bool   `json:"default"`
}

type pinFile struct {
	Llama struct {
		Version string           `json:"version"`
		Assets  map[string]asset `json:"assets"`
	} `json:"llama"`
	Models map[string]modelPin `json:"models"`
}

var (
	pinOnce sync.Once
	pinned  pinFile
)

func pins() *pinFile {
	pinOnce.Do(func() {
		if err := json.Unmarshal(pinsJSON, &pinned); err != nil {
			panic("modelcache: pins.json: " + err.Error())
		}
	})
	return &pinned
}

type Model struct {
	ID, File, URL, SHA256, QueryPrefix string
	Dims                               int
}

func modelOf(p *pinFile, id string) (Model, bool) {
	m, ok := p.Models[id]
	if !ok {
		return Model{}, false
	}
	return Model{ID: id, File: m.File, URL: m.URL, SHA256: m.SHA256, QueryPrefix: m.QueryPrefix, Dims: m.Dims}, true
}

func Default() Model {
	p := pins()
	ids := make([]string, 0, len(p.Models))
	for id := range p.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if p.Models[id].Default {
			m, _ := modelOf(p, id)
			return m
		}
	}
	panic("modelcache: pins.json has no default model")
}

func IDs() []string {
	var ids []string
	for id := range pins().Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func Lookup(id string) (Model, bool) { return modelOf(pins(), id) }

func Dir() string { return filepath.Join(config.CacheDir(), "models") }

func ModelPath(m Model) string { return filepath.Join(Dir(), m.ID, m.File) }

func platformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

func libDirFor(p *pinFile) (string, error) {
	if _, ok := p.Llama.Assets[platformKey()]; !ok {
		return "", fmt.Errorf("no prebuilt llama.cpp is pinned for %s (semantic search is unavailable here; keyword search still works)", platformKey())
	}
	return filepath.Join(Dir(), "llama-"+p.Llama.Version, platformKey()), nil
}

func LibDir() (string, error) { return libDirFor(pins()) }

type Status struct {
	Model             Model
	ModelOK, LibOK    bool
	ModelPath, LibDir string
	Problem           string
}

func StatusOf(id string) Status { return statusOf(pins(), id) }

func statusOf(p *pinFile, id string) Status {
	m, ok := modelOf(p, id)
	if !ok {
		return Status{Model: Model{ID: id}, Problem: fmt.Sprintf("unknown model %q (tix vault model list)", id)}
	}
	st := Status{Model: m, ModelPath: ModelPath(m)}
	var problems []string
	unpinned := false
	if fileOK(st.ModelPath) {
		st.ModelOK = true
	} else {
		problems = append(problems, "model not fetched")
	}
	dir, err := libDirFor(p)
	if err != nil {
		unpinned = true
		problems = append(problems, err.Error())
	} else {
		st.LibDir = dir
		st.LibOK = true
		for _, f := range p.Llama.Assets[platformKey()].Files {
			if !fileOK(filepath.Join(dir, f.As)) {
				st.LibOK = false
				break
			}
		}
		if !st.LibOK {
			problems = append(problems, "llama.cpp library not fetched")
		}
	}
	if len(problems) > 0 {
		st.Problem = strings.Join(problems, "; ") + hostHint(unpinned)
	}
	return st
}

func fileOK(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

func Open(id string) (embed.Embedder, error) {
	switch os.Getenv("TIX_EMBEDDER") {
	case "stub":
		return embed.Stub(64), nil
	case "none":
		return nil, fmt.Errorf("%w: disabled by TIX_EMBEDDER=none", embed.ErrUnavailable)
	}
	if _, ok := Lookup(id); !ok {
		return nil, fmt.Errorf("unknown model %q", id)
	}
	st := StatusOf(id)
	if !st.ModelOK || !st.LibOK {
		return nil, fmt.Errorf("%w: %s", embed.ErrUnavailable, st.Problem)
	}
	return embed.OpenLlama(st.LibDir, st.ModelPath, embed.LlamaOptions{ID: st.Model.ID, QueryPrefix: st.Model.QueryPrefix})
}

func hostHint(unpinned bool) string {
	if unpinned {
		return ""
	}
	return " (on the host: tix vault model fetch)"
}
