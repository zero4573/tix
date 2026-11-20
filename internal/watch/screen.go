package watch

import (
	"bytes"
	"io"
)

const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	home         = "\x1b[H"
	clearScreen  = "\x1b[2J"
	hideCursor   = "\x1b[?25l"
	showCursor   = "\x1b[?25h"
	eraseLine    = "\x1b[K"
	eraseBelow   = "\x1b[J"
)

func Enter(w io.Writer) error {
	_, err := io.WriteString(w, altScreenOn+home+clearScreen+hideCursor)
	return err
}

func Leave(w io.Writer) error {
	_, err := io.WriteString(w, showCursor+altScreenOff)
	return err
}

// a full-width line leaves the cursor on its last column with a pending wrap, and xterm-like
// terminals and Windows consoles erase from the cursor inclusive, so an erase after the text
// would take its last character
func DrawFrame(w io.Writer, lines []string) error {
	var b bytes.Buffer
	b.WriteString(home)
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		if i == len(lines)-1 {
			b.WriteString(eraseBelow)
		} else {
			b.WriteString(eraseLine)
		}
		b.WriteString(l)
	}
	if len(lines) == 0 {
		b.WriteString(eraseBelow)
	}
	_, err := w.Write(b.Bytes())
	return err
}
