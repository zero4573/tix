package vindex

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/chunk"
	"github.com/zero4573/tix/internal/embed"
	"github.com/zero4573/tix/internal/links"
	"github.com/zero4573/tix/internal/lock"
	"github.com/zero4573/tix/internal/modelcache"
	"github.com/zero4573/tix/internal/note"
)

const ModelHint = "tix: no embedding model, keyword search only; on the host run: tix vault model fetch"

const batchSize = 32

type UpdateOptions struct {
	Full        bool
	Model       string
	ChunkTokens int
	Files       []string
	Log         io.Writer
}

type UpdateStats struct {
	Notes, Changed, Removed, Chunks, Embedded int
	Model                                     string
	KeywordOnly                               bool
	Took                                      time.Duration
}

type built struct {
	meta   NoteMeta
	chunks []ChunkMeta
	vecs   [][]float32
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func embedInput(name string, c chunk.Chunk) string {
	return name + " > " + strings.Join(c.Heading, " > ") + "\n\n" + cleanLinks(c.Text)
}

func Update(vault string, o UpdateOptions) (UpdateStats, error) {
	return UpdateSource(vault, VaultSource(vault), o)
}

func UpdateSource(vault string, src Source, o UpdateOptions) (UpdateStats, error) {
	start := time.Now()
	var st UpdateStats
	tix := filepath.Join(vault, ".tix")
	MigrateLegacy(vault)
	if err := os.MkdirAll(Dir(vault), 0o755); err != nil {
		return st, err
	}
	if _, err := os.Stat(filepath.Join(tix, ".gitignore")); err != nil {
		if err := os.WriteFile(filepath.Join(tix, ".gitignore"), []byte("*\n"), 0o644); err != nil {
			return st, err
		}
	}
	err := lock.With(filepath.Join(tix, "index.lock"), func() error { return update(vault, src, o, &st) })
	st.Took = time.Since(start)
	return st, err
}

func update(vault string, src Source, o UpdateOptions, st *UpdateStats) error {
	logf := func(f string, a ...any) {
		if o.Log != nil {
			fmt.Fprintf(o.Log, f+"\n", a...)
		}
	}
	old, oerr := load(vault)
	var salvage *Index
	if oerr != nil {
		old = nil
		if errors.Is(oerr, ErrNoIndex) {
			salvage = lenientLoad(vault)
		}
		if !errors.Is(oerr, ErrNoIndex) {
			return oerr
		}
	}
	want := o.Model
	if want == "" && old != nil {
		want = old.Meta.Model
	}
	if _, ok := modelcache.Lookup(want); !ok && o.Model == "" {
		want = modelcache.Default().ID
	}
	chunkTokens := o.ChunkTokens
	if chunkTokens <= 0 && old != nil {
		chunkTokens = old.Meta.ChunkTokens
	}
	if chunkTokens <= 0 {
		chunkTokens = chunk.DefaultMaxTokens
	}

	var emb embed.Embedder
	e, err := openEmbedder(want)
	switch {
	case err == nil:
		emb = e
		defer e.Close()
	case errors.Is(err, embed.ErrUnavailable):
	default:
		return err
	}
	meta := Meta{Format: Format, ChunkTokens: chunkTokens}
	if emb != nil {
		meta.Model, meta.Dims = emb.ID(), emb.Dims()
		if m, ok := modelcache.Lookup(want); ok && emb.ID() == m.ID {
			meta.ModelSHA256 = m.SHA256
		}
	} else if old != nil && old.Meta.Dims > 0 && !o.Full && (o.Model == "" || o.Model == old.Meta.Model) && old.Meta.ChunkTokens == chunkTokens {
		meta.Model, meta.ModelSHA256, meta.Dims = old.Meta.Model, old.Meta.ModelSHA256, old.Meta.Dims
	}
	st.Model, st.KeywordOnly = meta.Model, emb == nil
	hasVecs := meta.Dims > 0

	full := o.Full || old == nil || old.Meta.Model != meta.Model || old.Meta.ModelSHA256 != meta.ModelSHA256 ||
		old.Meta.Dims != meta.Dims || old.Meta.ChunkTokens != chunkTokens
	if full {
		old = nil
	}

	var paths []string
	keep := map[string]int{}
	oldByPath := map[string]int{}
	if old != nil {
		for i, n := range old.Notes {
			oldByPath[n.Path] = i
		}
	}
	if old != nil && o.Files != nil {
		set := map[string]bool{}
		for _, f := range o.Files {
			if f, ok := src.Normalize(f); ok && !set[f] {
				set[f] = true
				paths = append(paths, f)
			}
		}
		for p, i := range oldByPath {
			if !set[p] {
				keep[p] = i
			}
		}
	} else {
		var err error
		if paths, err = src.List(); err != nil {
			return err
		}
		sort.Strings(paths)
	}

	reuse := map[string][]float32{}
	if old != nil && old.Meta.Dims > 0 {
		for i, c := range old.Chunks {
			if v := old.Vector(i); !isZero(v) {
				reuse[c.Hash] = v
			}
		}
	}

	if old == nil && salvage != nil && hasVecs && !o.Full && salvage.Meta.Model == meta.Model &&
		salvage.Meta.ModelSHA256 == meta.ModelSHA256 && salvage.Meta.Dims == meta.Dims &&
		vectorsMatchMeta(vault, salvage.Meta) {
		for i, c := range salvage.Chunks {
			if v := salvage.Vector(i); !isZero(v) {
				reuse[c.Hash] = v
			}
		}
	}

	var notes []*built
	rewriteNotes := false
	type pending struct {
		b      *built
		inputs []string
		idx    []int
	}
	var todo []pending
	oldChunksOf := func(i int) *built {
		n := old.Notes[i]
		b := &built{meta: n}
		p := pending{b: b}
		for r := n.First; r < n.First+n.Count; r++ {
			b.chunks = append(b.chunks, old.Chunks[r])
			if old.Meta.Dims > 0 {
				v := old.Vector(r)
				b.vecs = append(b.vecs, v)
				if emb != nil && isZero(v) {
					c := old.Chunks[r]
					p.inputs = append(p.inputs, n.Name+" > "+strings.Join(c.Heading, " > ")+"\n\n"+c.Text)
					p.idx = append(p.idx, len(b.vecs)-1)
				}
			}
		}
		if len(p.idx) > 0 {
			todo = append(todo, p)
		}
		return b
	}
	for _, i := range keep {
		notes = append(notes, oldChunksOf(i))
	}
	exists := map[string]bool{}
	for _, rel := range paths {
		fi, err := src.Stat(rel)
		if err != nil {
			continue
		}
		exists[rel] = true
		oi, had := oldByPath[rel]
		if had && old.Notes[oi].MTime == fi.MTime && old.Notes[oi].Size == fi.Size {
			notes = append(notes, oldChunksOf(oi))
			continue
		}
		data, err := src.Read(rel)
		if err != nil {
			continue
		}
		h := sum(data)
		if had && old.Notes[oi].SHA256 == h {
			b := oldChunksOf(oi)
			b.meta.MTime, b.meta.Size = fi.MTime, fi.Size
			notes = append(notes, b)
			rewriteNotes = true
			continue
		}
		st.Changed++
		name := strings.TrimSuffix(filepath.Base(rel), ".md")
		fmLines := note.Lines(data)
		fm := note.FrontmatterFrom(bytes.NewReader(data))
		nm := NoteMeta{
			Path: rel, Name: name, MTime: fi.MTime, Size: fi.Size, SHA256: h,
			Type:     fm["type"],
			Tags:     orEmpty(note.ListFieldLines(fmLines, "tags")),
			Projects: orEmpty(note.ListFieldLines(fmLines, "projects")),
		}
		seen := map[string]bool{}
		add := func(t string) {
			if t = links.BareName(t); t != "" && !seen[t] {
				seen[t] = true
				nm.Links = append(nm.Links, t)
			}
		}
		for _, t := range links.Targets(string(data)) {
			add(t)
		}
		for _, f := range []string{"depends-on", "groups"} {
			for _, t := range note.ListFieldLines(fmLines, f) {
				add(unwrap(t))
			}
		}
		nm.Links = orEmpty(nm.Links)
		if d, ok := src.(Describer); ok {
			nm.Name, nm.Type, nm.Projects = d.Describe(rel)
			nm.Projects = orEmpty(nm.Projects)
			name = nm.Name
		}
		b := &built{meta: nm}
		p := pending{b: b}
		for _, c := range chunk.Split(data, chunk.Options{MaxTokens: chunkTokens}) {
			in := embedInput(name, c)
			cm := ChunkMeta{
				Heading: orEmpty(c.Heading), StartLine: c.StartLine, EndLine: c.EndLine,
				Tokens: c.Tokens, Hash: sum([]byte(in))[:16], Text: cleanLinks(c.Text),
			}
			b.chunks = append(b.chunks, cm)
			if hasVecs {
				switch v, ok := reuse[cm.Hash]; {
				case ok:
					b.vecs = append(b.vecs, v)
				case emb == nil:
					b.vecs = append(b.vecs, make([]float32, meta.Dims))
				default:
					b.vecs = append(b.vecs, nil)
					p.inputs = append(p.inputs, in)
					p.idx = append(p.idx, len(b.vecs)-1)
				}
			}
		}
		todo = append(todo, p)
		notes = append(notes, b)
	}
	for p := range oldByPath {
		if _, k := keep[p]; !k && !exists[p] {
			st.Removed++
		}
	}

	if emb != nil {
		var inputs []string
		var dest []*[]float32
		for _, p := range todo {
			for k, row := range p.idx {
				inputs = append(inputs, p.inputs[k])
				dest = append(dest, &p.b.vecs[row])
			}
		}
		if len(inputs) > 0 {
			logf("embedding %d chunks", len(inputs))
		}
		for i := 0; i < len(inputs); i += batchSize {
			j := min(i+batchSize, len(inputs))
			vs, err := emb.Embed(inputs[i:j], false)
			if err != nil {
				return fmt.Errorf("embedding: %w", err)
			}
			if len(vs) != j-i {
				return fmt.Errorf("embedding: got %d vectors for %d texts", len(vs), j-i)
			}
			for k, v := range vs {
				*dest[i+k] = v
			}
			st.Embedded += len(vs)
		}
	}

	sort.Slice(notes, func(i, j int) bool { return notes[i].meta.Path < notes[j].meta.Path })
	var noteRows []NoteMeta
	var chunkRows []ChunkMeta
	var vecRows [][]float32
	for ni, b := range notes {
		b.meta.First, b.meta.Count = len(chunkRows), len(b.chunks)
		for ci, c := range b.chunks {
			c.Note = ni
			chunkRows = append(chunkRows, c)
			if hasVecs {
				vecRows = append(vecRows, b.vecs[ci])
			}
		}
		noteRows = append(noteRows, b.meta)
	}
	st.Notes, st.Chunks = len(noteRows), len(chunkRows)
	meta.Notes, meta.Chunks = st.Notes, st.Chunks

	dir := Dir(vault)
	sg := &stager{}
	defer sg.cleanup()
	unchanged := old != nil && st.Changed == 0 && st.Removed == 0 && st.Embedded == 0 && !rewriteNotes
	if !unchanged {
		structural := full || old == nil || st.Changed > 0 || st.Removed > 0 || st.Embedded > 0
		if structural {
			if err := writeJSONL(sg, filepath.Join(dir, "chunks.jsonl"), chunkRows); err != nil {
				return err
			}
			if hasVecs {
				if err := writeVectors(sg, filepath.Join(dir, "vectors.f32"), vecRows); err != nil {
					return err
				}
			} else {
				sg.remove(filepath.Join(dir, "vectors.f32"))
			}
		}
		first := len(sg.ops)
		if err := writeJSONL(sg, filepath.Join(dir, "notes.jsonl"), noteRows); err != nil {
			return err
		}
		meta.Files = map[string]string{}
		for _, name := range []string{"notes", "chunks", "vectors"} {
			ext := ".jsonl"
			if name == "vectors" {
				ext = ".f32"
			}
			src := sg.staged(filepath.Join(dir, name+ext))
			if src == "" || (name == "vectors" && !hasVecs) {
				meta.Files[name] = ""
				continue
			}
			h, err := hashFile(src)
			if err != nil {
				return err
			}
			meta.Files[name] = h
		}
		meta.Updated = time.Now().UTC().Format(time.RFC3339)
		if err := writeMeta(sg, filepath.Join(dir, "meta.json"), meta); err != nil {
			return err
		}
		if err := lock.With(filepath.Join(vault, ".tix", "index.commit.lock"), func() error {
			return sg.commit(first, beforeNotesWrite)
		}); err != nil {
			return err
		}
	}

	hint := filepath.Join(dir, "model-hint")
	if emb != nil {
		os.Remove(hint)
	} else if _, err := os.Stat(hint); err != nil {
		fmt.Fprintln(os.Stderr, ModelHint)
		_ = os.WriteFile(hint, nil, 0o644)
	}
	return nil
}

func writeMeta(s *stager, path string, m Meta) error {
	return writeAtomic(s, path, func(w io.Writer) error {
		b, err := jsonMarshal(m)
		if err != nil {
			return err
		}
		_, err = w.Write(append(b, '\n'))
		return err
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func unwrap(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		s = s[2 : len(s)-2]
	}
	if i := strings.IndexAny(s, "|#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), `\`)
}

func Refresh(vault string, files ...string) {
	if _, err := os.Stat(filepath.Join(Dir(vault), "meta.json")); err != nil {
		return
	}
	var only []string
	if len(files) > 0 {
		only = files
	}
	if _, err := Update(vault, UpdateOptions{Files: only}); err != nil {
		fmt.Fprintf(os.Stderr, "tix: vault index not updated: %v\n", err)
	}
}

func isZero(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}

var openEmbedder = modelcache.Open

var beforeNotesWrite func()

func vectorsMatchMeta(vault string, m Meta) bool {
	if m.Files == nil {
		return true
	}
	h, err := hashFile(filepath.Join(Dir(vault), "vectors.f32"))
	return err == nil && h == m.Files["vectors"]
}

func lenientLoad(vault string) *Index {
	dir := Dir(vault)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil
	}
	ix := &Index{}
	if json.Unmarshal(data, &ix.Meta) != nil || ix.Meta.Format != Format || ix.Meta.Dims <= 0 {
		return nil
	}
	if readLines(filepath.Join(dir, "chunks.jsonl"), func(b []byte) error {
		var c ChunkMeta
		if err := json.Unmarshal(b, &c); err != nil {
			return err
		}
		ix.Chunks = append(ix.Chunks, c)
		return nil
	}) != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vectors.f32"))
	if err != nil || len(raw) != len(ix.Chunks)*ix.Meta.Dims*4 {
		return nil
	}
	ix.vecs = make([]float32, len(raw)/4)
	for i := range ix.vecs {
		ix.vecs[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return ix
}
