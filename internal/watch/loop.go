package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

type Config struct {
	Out      io.Writer
	Interval time.Duration
	Poll     time.Duration
	Refresh  func() []string
	Changed  func() func() bool
	Size     func() (width, height int, ok bool)
	Resize   <-chan os.Signal
	Now      func() time.Time
}

const defaultPoll = 250 * time.Millisecond

func Run(ctx context.Context, c Config) error {
	now := c.Now
	if now == nil {
		now = time.Now
	}
	poll := c.Poll
	if poll <= 0 {
		poll = defaultPoll
	}
	size := func() (int, int) {
		if c.Size == nil {
			return fallbackWidth, fallbackHeight
		}
		w, h, ok := c.Size()
		if !ok || w < 1 || h < 1 {
			return fallbackWidth, fallbackHeight
		}
		return w, h
	}

	type result struct {
		lines []string
		at    time.Time
	}
	results := make(chan result, 1)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	var (
		inFlight, pending bool
		check             func() bool
		frame             []string
		drawnW, drawnH    int
	)
	start := func() {
		timer.Stop()
		inFlight, pending = true, false
		if c.Changed != nil {
			check = c.Changed()
		}
		at := now()
		go func() { results <- result{c.Refresh(), at} }()
	}
	draw := func() error {
		drawnW, drawnH = size()
		return DrawFrame(c.Out, Fit(frame, drawnW, drawnH))
	}
	resized := func() error {
		if frame == nil {
			return nil
		}
		if w, h := size(); w != drawnW || h != drawnH {
			return draw()
		}
		return nil
	}
	header := func(at time.Time) string {
		return fmt.Sprintf("refreshed %s, every %s (Ctrl-C to quit)", at.Format("15:04:05"), shortDuration(c.Interval))
	}

	start()
	for {
		select {
		case <-ctx.Done():
			return nil
		case r := <-results:
			inFlight = false
			frame = append([]string{header(r.at)}, r.lines...)
			if err := draw(); err != nil {
				return err
			}
			if pending {
				start()
			} else {
				timer.Reset(c.Interval)
			}
		case <-timer.C:
			if !inFlight {
				start()
			}
		case <-ticker.C:
			if check != nil && !pending && check() {
				if inFlight {
					pending = true
				} else {
					start()
				}
			}
			if err := resized(); err != nil {
				return err
			}
		case <-c.Resize:
			if err := resized(); err != nil {
				return err
			}
		}
	}
}
