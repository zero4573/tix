package watch

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const fallbackWidth, fallbackHeight = 80, 24

func Fit(lines []string, width, height int) []string {
	if width < 1 {
		width = fallbackWidth
	}
	if height < 1 {
		height = fallbackHeight
	}
	shown := lines
	more := ""
	switch {
	case len(lines) <= height:
	case height == 1:
		shown = lines[:1]
	default:
		shown = lines[:height-1]
		more = fmt.Sprintf("... %d more", len(lines)-len(shown))
	}
	out := make([]string, 0, len(shown)+1)
	for _, l := range shown {
		out = append(out, cut(sanitize(l), width))
	}
	if more != "" {
		out = append(out, cut(more, width))
	}
	return out
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
}

func cut(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	n := 0
	for i := range s {
		if n == width {
			return s[:i]
		}
		n++
	}
	return s
}

func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
