package gitx

import (
	"bytes"
	"fmt"
	"strings"
)

type confEntry struct {
	section, sub string
	hasSub       bool
	name         string
	value        string
	hasValue     bool // git reads a bare "key" line as true
	file         string
	line         int
}

func (e confEntry) key() string {
	if e.hasSub {
		return e.section + "." + e.sub + "." + e.name
	}
	return e.section + "." + e.name
}

// git's config.c parse_file, as a state machine over bytes
type confParser struct {
	data []byte
	pos  int
	eof  bool
	line int
}

// git's get_next_char: "\r\n" reads as "\n", and the end of the data as a
// final "\n"
func (p *confParser) next() byte {
	if p.pos >= len(p.data) {
		p.eof = true
		return '\n'
	}
	c := p.data[p.pos]
	p.pos++
	if c == '\r' && p.pos < len(p.data) && p.data[p.pos] == '\n' {
		c = '\n'
		p.pos++
	}
	if c == '\n' {
		p.line++
	}
	return c
}

// git's isspace (sane_ctype) excludes \v and \f
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isKeyChar(c byte) bool { return isAlpha(c) || (c >= '0' && c <= '9') || c == '-' }

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func parseConfig(data []byte, file string) ([]confEntry, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("%s: a NUL byte", file)
	}
	p := &confParser{data: data, line: 1}
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		p.pos = 3
	}
	var out []confEntry
	var section, sub string
	var hasSub, inSection bool
	comment := false
	fail := func(line int, why string) error {
		return fmt.Errorf("%s:%d: %s", file, line, why)
	}
	for {
		c := p.next()
		if c == '\n' {
			if p.eof {
				return out, nil
			}
			comment = false
			continue
		}
		if comment || isSpace(c) {
			continue
		}
		if c == '#' || c == ';' {
			comment = true
			continue
		}
		if c == '[' {
			line := p.line
			s, sb, has, err := p.header()
			if err != nil {
				return nil, fail(line, err.Error())
			}
			section, sub, hasSub, inSection = s, sb, has, true
			continue
		}
		if !isAlpha(c) {
			return nil, fail(p.line, fmt.Sprintf("unexpected %q", c))
		}
		line := p.line
		name, value, hasValue, err := p.keyValue(c)
		if err != nil {
			return nil, fail(line, err.Error())
		}
		if !inSection {
			return nil, fail(line, "a key outside any section")
		}
		out = append(out, confEntry{section: section, sub: sub, hasSub: hasSub, name: name,
			value: value, hasValue: hasValue, file: file, line: line})
	}
}

// git's get_base_var: `name`, `name "sub"` (subsection case kept), or the
// old `name.sub` form, which git lowercases whole
func (p *confParser) header() (section, sub string, hasSub bool, err error) {
	var name []byte
	for {
		c := p.next()
		if p.eof {
			return "", "", false, fmt.Errorf("unterminated section header")
		}
		if c == ']' {
			break
		}
		if isSpace(c) {
			sub, err := p.extendedHeader(c)
			if err != nil {
				return "", "", false, err
			}
			if len(name) == 0 {
				return "", "", false, fmt.Errorf("empty section name")
			}
			return string(name), sub, true, nil
		}
		if !isKeyChar(c) && c != '.' {
			return "", "", false, fmt.Errorf("bad section header")
		}
		name = append(name, lower(c))
	}
	s := string(name)
	if s == "" || strings.HasPrefix(s, ".") {
		return "", "", false, fmt.Errorf("empty section name")
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i], s[i+1:], true, nil
	}
	return s, "", false, nil
}

// git's get_extended_base_var: a backslash escapes any next character
func (p *confParser) extendedHeader(c byte) (string, error) {
	for isSpace(c) {
		if c == '\n' {
			return "", fmt.Errorf("bad section header")
		}
		c = p.next()
	}
	if c != '"' {
		return "", fmt.Errorf("bad section header")
	}
	var sub []byte
	for {
		c = p.next()
		if c == '\n' {
			return "", fmt.Errorf("bad section header")
		}
		if c == '"' {
			break
		}
		if c == '\\' {
			c = p.next()
			if c == '\n' {
				return "", fmt.Errorf("bad section header")
			}
		}
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("a control character in a subsection")
		}
		sub = append(sub, c)
	}
	if p.next() != ']' {
		return "", fmt.Errorf("bad section header")
	}
	return string(sub), nil
}

func (p *confParser) keyValue(c byte) (name, value string, hasValue bool, err error) {
	n := []byte{lower(c)}
	for {
		c = p.next()
		if p.eof || !isKeyChar(c) {
			break
		}
		n = append(n, lower(c))
	}
	for c == ' ' || c == '\t' {
		c = p.next()
	}
	if c == '\n' {
		return string(n), "", false, nil
	}
	if c != '=' {
		return "", "", false, fmt.Errorf("bad key")
	}
	v, err := p.value()
	return string(n), v, true, err
}

// git's parse_value: quoted and unquoted parts concatenate, whitespace
// outside quotes is kept between words and dropped at either end, and
// \\ \" \n \t \b are the only escapes
func (p *confParser) value() (string, error) {
	var v []byte
	quote, comment := false, false
	var pendingSpace []byte
	for {
		c := p.next()
		if c == '\n' {
			if quote {
				return "", fmt.Errorf("unterminated quote")
			}
			return string(v), nil
		}
		if comment {
			continue
		}
		if isSpace(c) && !quote {
			if len(v) > 0 {
				pendingSpace = append(pendingSpace, c)
			}
			continue
		}
		if !quote && (c == ';' || c == '#') {
			comment = true
			continue
		}
		v = append(v, pendingSpace...)
		pendingSpace = pendingSpace[:0]
		if c == '\\' {
			c = p.next()
			switch c {
			case '\n':
				continue
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'n':
				c = '\n'
			case '\\', '"':
			default:
				return "", fmt.Errorf("bad escape")
			}
			v = append(v, c)
			continue
		}
		if c == '"' {
			quote = !quote
			continue
		}
		v = append(v, c)
	}
}
