//go:build windows

package watch

import (
	"os"

	"golang.org/x/sys/windows"
)

func EnableVT(f *os.File) (restore func(), err error) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return nil, err
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
		if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
			return nil, err
		}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }, nil
}

func ResizeSignals() (<-chan os.Signal, func()) { return nil, func() {} }
