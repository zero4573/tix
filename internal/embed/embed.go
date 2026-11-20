package embed

import (
	"errors"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

var ErrUnavailable = errors.New("embedding unavailable")

type Embedder interface {
	ID() string
	Dims() int
	Embed(texts []string, query bool) ([][]float32, error)
	Close() error
}

type LlamaOptions struct {
	ID, QueryPrefix string
	Threads, Ctx    int
}

var DefaultLibPath string

func Stub(dims int) Embedder {
	if dims <= 0 {
		dims = 64
	}
	return stub{dims}
}

type stub struct{ dims int }

func (s stub) ID() string   { return "stub" }
func (s stub) Dims() int    { return s.dims }
func (s stub) Close() error { return nil }

func (s stub) Embed(texts []string, query bool) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, s.dims)
		words := strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, w := range words {
			h := fnv.New64a()
			h.Write([]byte(w))
			sum := h.Sum64()
			idx := int(sum % uint64(s.dims))
			if sum>>63 == 1 {
				v[idx]--
			} else {
				v[idx]++
			}
		}
		normalize(v)
		out[i] = v
	}
	return out, nil
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

func Cosine(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
