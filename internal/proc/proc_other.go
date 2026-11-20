//go:build !linux && !darwin && !windows

package proc

func identity(int) (string, error) { return "", ErrUnsupported }
