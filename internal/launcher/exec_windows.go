//go:build windows

package launcher

import "errors"

func execReplace(path string, argv, env []string) error { return errors.New("no exec on windows") }
