package lock

import (
	"context"
	"os"
	"time"
)

func WithContext(ctx context.Context, path string, waiting func(), fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	got, err := tryLockFile(f)
	if err != nil {
		return err
	}
	if !got {
		if waiting != nil {
			waiting()
		}
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for !got {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
			}
			if got, err = tryLockFile(f); err != nil {
				return err
			}
		}
	}
	defer unlockFile(f)
	return fn()
}

func With(path string, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return WithFile(f, fn)
}

func WithFile(f *os.File, fn func() error) error {
	if err := lockFile(f); err != nil {
		return err
	}
	defer unlockFile(f)
	return fn()
}
