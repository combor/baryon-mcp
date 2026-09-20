package mailparse

import (
	"html"
	"strings"
	"unicode"
)

// hiddenElements hold markup or metadata rather than readable text. head is
// absent on purpose: everything in it that carries text is hidden in its own
// right, so skipping the element buys nothing and costs the whole message when
// the optional </head> is omitted.
var hiddenElements = map[string]bool{"script": true, "style": true, "title": true}

// breakElements end a line of readable text.
var breakElements = map[string]bool{
	"br": true, "p": true, "div": true, "tr": true, "li": true, "table": true,
	"blockquote": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "hr": true, "ul": true, "ol": true, "section": true,
}

// cellElements separate values along a row rather than starting a new line, so
// a table of amounts does not read as one number.
var cellElements = map[string]bool{"td": true, "th": true}

// HTMLToText reduces an HTML body to the text a reader would see. It is a
// shortening aid, not a renderer: markup is dropped rather than laid out.
func HTMLToText(s string) string {
	var out strings.Builder
	out.Grow(len(s) / 2)

	for i := 0; i < len(s); {
		c := s[i]
		if c != '<' {
			out.WriteByte(c)
			i++
			continue
		}
		if rest := s[i:]; strings.HasPrefix(rest, "<!--") {
			if end := strings.Index(rest, "-->"); end >= 0 {
				i += end + 3
				continue
			}
			break // unterminated comment: the remainder is markup
		}
		if !startsTag(s, i) {
			out.WriteByte('<') // a comparison, not a tag
			i++
			continue
		}
		name, closing, selfClosing, end := scanTag(s, i)
		if end < 0 {
			break // unterminated tag: the remainder is markup
		}
		switch {
		// An element written as empty has no content to skip. HTML5 ignores the
		// marker on raw-text elements, but honouring it keeps a body that opens
		// with "<script src=.../>" from reading as no body at all.
		case hiddenElements[name] && !closing && !selfClosing:
			// The element's own content decides where it ends: script text is
			// free to contain '<', which tag scanning would read as markup.
			end = skipElement(s, end, name)
		case breakElements[name]:
			out.WriteByte('\n')
		case cellElements[name]:
			out.WriteByte(' ')
		}
		if end < 0 {
			break // element never closed: the remainder is markup
		}
		i = end
	}
	return collapse(html.UnescapeString(out.String()))
}

// startsTag reports whether the '<' at s[i] opens a tag rather than standing as
// text. A '<' with no name after it is a comparison, which is how a body
// carrying "total < 10" keeps the rest of its sentence.
func startsTag(s string, i int) bool {
	j := i + 1
	if j >= len(s) {
		return false
	}
	switch {
	case s[j] == '!' || s[j] == '?': // declaration or processing instruction
		return true
	case s[j] == '/':
		j++
	}
	return j < len(s) && isAlpha(s[j])
}

// skipElement returns the index just past the closing tag for name, or -1 when
// the element is never closed.
func skipElement(s string, i int, name string) int {
	for i < len(s) {
		j := strings.Index(s[i:], "</")
		if j < 0 {
			return -1
		}
		i += j
		if closesElement(s, i, name) {
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return -1 // never terminated
			}
			return i + end + 1
		}
		// A "</" in raw text is not a tag, so resume just past the candidate:
		// scanning to its '>' would swallow the real closing tag behind it.
		i += 2
	}
	return -1
}

// closesElement reports whether the "</" at s[i] begins the closing tag for
// name. Comparing the name before any scan for '>' is what keeps skipElement
// linear: a candidate sitting in raw text costs its own length, not the rest
// of the element.
func closesElement(s string, i int, name string) bool {
	j := i + 2
	if len(s)-j < len(name) || !strings.EqualFold(s[j:j+len(name)], name) {
		return false
	}
	k := j + len(name)
	return k == len(s) || !isAlpha(s[k]) && !isDigit(s[k])
}

// scanTag reads the tag starting at s[i] and returns its lowercased name,
// whether it closes an element, whether it carries the self-closing marker,
// and the index just past its '>'. A quoted attribute may contain '>' or a
// trailing '/', so quotes are tracked while scanning.
func scanTag(s string, i int) (name string, closing, selfClosing bool, end int) {
	j := i + 1
	if j < len(s) && s[j] == '/' {
		closing = true
		j++
	}
	start := j
	for j < len(s) && (isAlpha(s[j]) || isDigit(s[j])) {
		j++
	}
	name = strings.ToLower(s[start:j])

	var quote byte
	slash := false
	for ; j < len(s); j++ {
		switch {
		case quote != 0:
			if s[j] == quote {
				quote = 0
			}
		case s[j] == '"' || s[j] == '\'':
			quote, slash = s[j], false
		case s[j] == '>':
			return name, closing, slash, j + 1
		case s[j] == '/':
			slash = true
		case s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r':
			// Whitespace keeps the marker, so "<img />" still reads as empty.
		default:
			slash = false
		}
	}
	return name, closing, false, -1
}

// collapse squeezes the whitespace that markup leaves behind. A run of blank
// lines becomes a single break: nesting emits one per boundary, and a preview
// should spend its budget on text.
func collapse(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	broke, spaced := false, false
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029':
			broke = true
		case invisible(r):
			// Renders as nothing, so it is not even a space.
		case unicode.IsSpace(r):
			spaced = true
		default:
			switch {
			case out.Len() == 0:
			case broke:
				out.WriteByte('\n')
			case spaced:
				out.WriteByte(' ')
			}
			broke, spaced = false, false
			out.WriteRune(r)
		}
	}
	return out.String()
}

// invisible reports whether r occupies no width when rendered. Marketing mail
// pads the preheader — the line a client shows beside the subject — with long
// runs of these, which would otherwise spend the whole preview budget before
// the message begins. The combining grapheme joiner is named on its own
// because it is a mark rather than a format character, and the rest of its
// category carries real accents.
func invisible(r rune) bool {
	return r == '\u034f' || unicode.Is(unicode.Cf, r)
}

func isAlpha(b byte) bool { return b|0x20 >= 'a' && b|0x20 <= 'z' }
func isDigit(b byte) bool { return b >= '0' && b <= '9' }
