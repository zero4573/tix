package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/watch"
	"github.com/zero4573/tix/internal/workspace"
)

const minInterval = time.Second

var durationSyntax = regexp.MustCompile(`^[-+]?(0|((\d+\.?\d*|\.\d+)(ns|us|µs|μs|ms|s|m|h))+)$`)

type intervalFlag time.Duration

func (f *intervalFlag) String() string { return time.Duration(*f).String() }
func (f *intervalFlag) Type() string   { return "duration" }
func (f *intervalFlag) Set(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil && !strings.HasPrefix(s, "-") && durationSyntax.MatchString(s) {
		return fmt.Errorf("too long: want a duration of at most %s", time.Duration(math.MaxInt64))
	}
	if err != nil || d < minInterval {
		return fmt.Errorf("want a duration of at least %s, like 2s or 1m", minInterval)
	}
	*f = intervalFlag(d)
	return nil
}

var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

func printStatusOnce(ctx vault.Context, why string) error {
	if err := writeStatus(os.Stdout, buildStatus(ctx)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "tix status: --watch needs %s; printed once\n", why)
	return nil
}

func runStatusWatch(parent context.Context, ctx vault.Context, interval time.Duration) error {
	if parent == nil {
		parent = context.Background()
	}
	sigCtx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := os.Stdout
	restoreVT, err := watch.EnableVT(out)
	if err != nil {
		return printStatusOnce(ctx, "a terminal that handles escape sequences")
	}
	resize, stopResize := watch.ResizeSignals()
	defer stopResize()

	warns := &lineCollector{}
	workspace.Warnings = warns

	if err := watch.Enter(out); err != nil {
		restoreVT()
		return err
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			_ = watch.Leave(out)
			restoreVT()
		})
	}
	defer func() {
		if p := recover(); p != nil {
			restore()
			panic(p)
		}
		restore()
	}()

	return watch.Run(sigCtx, watch.Config{
		Out:      out,
		Interval: interval,
		Refresh: func() []string {
			defer func() {
				if p := recover(); p != nil {
					restore()
					panic(p)
				}
			}()
			var buf bytes.Buffer
			_ = writeStatus(&buf, watchStatus(ctx))
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			return append(lines, warns.drain()...)
		},
		Changed: func() func() bool {
			base := workspace.StateStamps(ctx.WorkRoot)
			return func() bool { return base.Changed(workspace.StateStamps(ctx.WorkRoot)) }
		},
		Size: func() (int, int, bool) {
			w, h, err := term.GetSize(int(out.Fd()))
			return w, h, err == nil
		},
		Resize: resize,
	})
}

var watchStatus = buildStatus

type lineCollector struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *lineCollector) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *lineCollector) drain() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var lines []string
	seen := map[string]bool{}
	for _, l := range strings.Split(c.buf.String(), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" && !seen[l] {
			seen[l] = true
			lines = append(lines, l)
		}
	}
	c.buf.Reset()
	return lines
}

var errIntervalNeedsWatch = errors.New("--interval needs --watch")
