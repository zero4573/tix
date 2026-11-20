//go:build linux

package proc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

func identity(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotRunning
	}
	if err != nil {
		return "", ErrUnsupported
	}
	return parseStat(string(data), bootID())
}

func parseStat(stat, boot string) (string, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", ErrUnsupported
	}
	// /proc/<pid>/stat: the command may hold spaces and ')', so fields count
	// after the last ')': state (field 3), ..., starttime (field 22)
	f := strings.Fields(stat[i+1:])
	if len(f) < 20 {
		return "", ErrUnsupported
	}
	if f[0] == "Z" || f[0] == "X" {
		return "", ErrNotRunning
	}
	return fmt.Sprintf("linux:%s:%s", boot, f[19]), nil
}

func bootID() string {
	data, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data))
}
