//go:build !windows

package watch

import (
	"os"
	"os/signal"
	"syscall"
)

func EnableVT(f *os.File) (restore func(), err error) { return func() {}, nil }

func ResizeSignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch, func() { signal.Stop(ch) }
}
