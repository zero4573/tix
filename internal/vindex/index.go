package vindex

import (
	"bufio"
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

	"github.com/zero4573/tix/internal/lock"
)

const Format = 1

var ErrNoIndex = errors.New("no vault index")

func Dir(vault string) string { return filepath.Join(vault, ".tix", "index") }

func MigrateLegacy(vault string) {
	old, cur := filepath.Join(vault, ".ct"), filepath.Join(vault, ".tix")
	if st, err := os.Lstat(old); err != nil || !st.IsDir() {
		return
	}
	if _, err := os.Lstat(cur); err == nil {
		return
	}
	_ = os.Rename(old, cur)
}

type Meta struct {
	Format      int               `json:"format"`
	Model       string            `json:"model"`
	ModelSHA256 string            `json:"model_sha256"`
	Dims        int               `json:"dims"`
	ChunkTokens int               `json:"chunk_tokens"`
	Notes       int               `json:"notes"`
	Chunks      int               `json:"chunks"`
	Updated     string            `json:"updated"`
	Files       map[string]string `json:"files,omitempty"`
}

type NoteMeta struct {
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	MTime    int64    `json:"mtime"`
	Size     int64    `json:"size"`
	SHA256   string   `json:"sha256"`
	Type     string   `json:"type"`
	Tags     []string `json:"tags"`
	Projects []string `json:"projects"`
	Links    []string `json:"links"`
	First    int      `json:"first"`
	Count    int      `json:"count"`
}

type ChunkMeta struct {
	Note      int      `json:"note"`
	Heading   []string `json:"heading"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Tokens    int      `json:"tokens"`
	Hash      string   `json:"hash"`
	Text      string   `json:"text"`
}

type Index struct {
	Meta   Meta
	Notes  []NoteMeta
	Chunks []ChunkMeta
	vecs   []float32
}

func (ix *Index) Vector(i int) []float32 {
	d := ix.Meta.Dims
	if d == 0 || i < 0 || (i+1)*d > len(ix.vecs) {
		return nil
	}
	return ix.vecs[i*d : (i+1)*d]
}

func Load(vault string) (*Index, error) {
	MigrateLegacy(vault)
	if _, err := os.Stat(filepath.Join(Dir(vault), "meta.json")); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoIndex, err)
	}
	var ix *Index
	var lerr error
	ran := false
	if err := lock.With(filepath.Join(vault, ".tix", "index.commit.lock"), func() error {
		ran = true
		ix, lerr = load(vault)
		return nil
	}); err != nil && !ran {
		return load(vault)
	}
	return ix, lerr
}

func load(vault string) (*Index, error) {
	dir := Dir(vault)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoIndex, err)
	}
	ix := &Index{}
	if err := json.Unmarshal(data, &ix.Meta); err != nil {
		return nil, fmt.Errorf("%w: meta.json: %v", ErrNoIndex, err)
	}
	if ix.Meta.Format != Format {
		return nil, fmt.Errorf("%w: format %d", ErrNoIndex, ix.Meta.Format)
	}
	if ix.Meta.Files == nil {
		return nil, fmt.Errorf("%w: meta.json has no file hashes (older index)", ErrNoIndex)
	}
	for _, name := range []string{"notes", "chunks"} {
		if h, err := hashFile(filepath.Join(dir, name+".jsonl")); err != nil || h != ix.Meta.Files[name] {
			return nil, fmt.Errorf("%w: %s.jsonl doesn't match meta.json (torn index)", ErrNoIndex, name)
		}
	}
	if err := readLines(filepath.Join(dir, "notes.jsonl"), func(b []byte) error {
		var n NoteMeta
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		ix.Notes = append(ix.Notes, n)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("%w: notes.jsonl: %v", ErrNoIndex, err)
	}
	if err := readLines(filepath.Join(dir, "chunks.jsonl"), func(b []byte) error {
		var c ChunkMeta
		if err := json.Unmarshal(b, &c); err != nil {
			return err
		}
		ix.Chunks = append(ix.Chunks, c)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("%w: chunks.jsonl: %v", ErrNoIndex, err)
	}
	if len(ix.Notes) != ix.Meta.Notes || len(ix.Chunks) != ix.Meta.Chunks {
		return nil, fmt.Errorf("%w: files don't match meta.json", ErrNoIndex)
	}
	next := 0
	for i, n := range ix.Notes {
		if n.First != next || n.Count < 0 || n.First+n.Count > len(ix.Chunks) {
			return nil, fmt.Errorf("%w: note %q doesn't tile the chunks", ErrNoIndex, n.Path)
		}
		for r := n.First; r < n.First+n.Count; r++ {
			if ix.Chunks[r].Note != i {
				return nil, fmt.Errorf("%w: chunk %d is not note %d's", ErrNoIndex, r, i)
			}
		}
		next += n.Count
	}
	if next != len(ix.Chunks) {
		return nil, fmt.Errorf("%w: chunks without a note", ErrNoIndex)
	}
	if ix.Meta.Dims > 0 {
		raw, err := os.ReadFile(filepath.Join(dir, "vectors.f32"))
		if err != nil || len(raw) != ix.Meta.Chunks*ix.Meta.Dims*4 {
			return nil, fmt.Errorf("%w: vectors.f32 doesn't match meta.json", ErrNoIndex)
		}
		if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != ix.Meta.Files["vectors"] {
			return nil, fmt.Errorf("%w: vectors.f32 doesn't match meta.json (torn index)", ErrNoIndex)
		}
		ix.vecs = make([]float32, len(raw)/4)
		for i := range ix.vecs {
			ix.vecs[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
	}
	return ix, nil
}

func readLines(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			if e := fn(t); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type stager struct{ ops []stagedOp }

type stagedOp struct{ tmp, path string }

func (s *stager) cleanup() {
	for _, o := range s.ops {
		if o.tmp != "" {
			os.Remove(o.tmp)
		}
	}
	s.ops = nil
}

func (s *stager) remove(path string) { s.ops = append(s.ops, stagedOp{path: path}) }

func (s *stager) commit(n int, after func()) error {
	for i, o := range s.ops {
		if i == n && after != nil {
			after()
		}
		var err error
		if o.tmp == "" {
			if err = os.Remove(o.path); os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = os.Rename(o.tmp, o.path)
			s.ops[i].tmp = ""
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(s *stager, path string, fn func(w io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	w := bufio.NewWriterSize(tmp, 1<<20)
	err = fn(w)
	if err == nil {
		err = w.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(name)
		return err
	}
	if s != nil {
		s.ops = append(s.ops, stagedOp{tmp: name, path: path})
		return nil
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func writeJSONL[T any](s *stager, path string, rows []T) error {
	return writeAtomic(s, path, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeVectors(s *stager, path string, rows [][]float32) error {
	return writeAtomic(s, path, func(w io.Writer) error {
		var b [4]byte
		for _, r := range rows {
			for _, f := range r {
				binary.LittleEndian.PutUint32(b[:], math.Float32bits(f))
				if _, err := w.Write(b[:]); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *stager) staged(path string) string {
	for i := len(s.ops) - 1; i >= 0; i-- {
		if s.ops[i].path == path {
			return s.ops[i].tmp
		}
	}
	return path
}
