package proc

import "errors"

var ErrNotRunning = errors.New("no such process")

var ErrUnsupported = errors.New("process identity not supported here")

func Identity(pid int) (string, error) {
	if pid <= 0 {
		return "", ErrNotRunning
	}
	return identity(pid)
}
