//go:build !linux && !darwin

package embed

import "fmt"

func OpenLlama(libDir, modelPath string, o LlamaOptions) (Embedder, error) {
	return nil, fmt.Errorf("%w: llama.cpp is not supported on this platform", ErrUnavailable)
}

var depLibs = []string{}
