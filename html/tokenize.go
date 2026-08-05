package html

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The tokenizer.
//
// HTML5's is an eighty-state machine, and most of those states exist to define
// what a malformed construct recovers to. This one refuses instead (see the
// package comment), so it is a fraction of the size — and every place it
// differs from a browser is a refusal with a message, never a quiet
// reinterpretation.

// maxErrors bounds how many problems are reported from one document, so a file
// of noise cannot turn into an unbounded diagnostic list. The last entry says
// the list was cut, so a truncated report never reads as a complete one.
const maxErrors = 100

// Error is a place the document was not what this engine reads.
type Error struct {
	// Offset is the byte offset in the input where the problem was noticed.
	Offset int
	// Message says what was wrong in terms of the source.
	Message string
	// Unsupported marks correct HTML that this engine does not implement, as
	// against input that is malformed. The two mean opposite things to an
	// author: malformed markup is theirs to fix, an unsupported element is a
	// limit of the renderer. See css.Error, which draws the same line.
	Unsupported bool
}

func (e Error) Error() string { return fmt.Sprintf("byte %d: %s", e.Offset, e.Message) }

// Position converts a byte offset into a line and column, both counted from 1,
// with the column counted in code points — which is what an editor shows and so
// what an author can act on.
func Position(input string, offset int) (line, col int) {
	if offset > len(input) {
		offset = len(input)
	}
	line, col = 1, 1
	for i := 0; i < len(input); {
		_, size := utf8.DecodeRuneInString(input[i:])
		if i+size > offset {
			return line, col
		}
		if input[i] == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
		i += size
	}
	return line, col
}

type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokText
	tokStartTag
	tokEndTag
	tokDoctype
)

type token struct {
	kind        tokenKind
	name        string // lowercased, for tags
	attrs       []Attribute
	text        string
	selfClosing bool
	offset      int
}

type tokenizer struct {
	src  string
	pos  int
	errs []Error
	// raw is the element whose content is being read verbatim, empty when not
	// in one. It is set by the parser, because whether "<" starts a tag depends
	// on which element is open — the one place the two layers are not separable.
	raw    string
	rcdata bool
}

func newTokenizer(src string) *tokenizer { return &tokenizer{src: src} }

func (t *tokenizer) fail(off int, msg string) { t.add(Error{Offset: off, Message: msg}) }
func (t *tokenizer) unsupported(off int, msg string) {
	t.add(Error{Offset: off, Message: msg, Unsupported: true})
}

func (t *tokenizer) add(e Error) {
	switch {
	case len(t.errs) > maxErrors:
		return
	case len(t.errs) == maxErrors:
		t.errs = append(t.errs, Error{
			Offset:  e.Offset,
			Message: "further problems in this document were not reported",
		})
	default:
		t.errs = append(t.errs, e)
	}
}

// next produces one token.
func (t *tokenizer) next() token {
	if t.raw != "" {
		return t.rawText()
	}
	if t.pos >= len(t.src) {
		return token{kind: tokEOF, offset: len(t.src)}
	}

	if t.src[t.pos] == '<' {
		return t.markup()
	}
	return t.text()
}

// text reads character data up to the next "<".
func (t *tokenizer) text() token {
	start := t.pos
	for t.pos < len(t.src) && t.src[t.pos] != '<' {
		t.pos++
	}
	return token{kind: tokText, text: t.decodeRefs(t.src[start:t.pos], start, false), offset: start}
}

// rawText reads the content of a raw-text or RCDATA element, up to its end tag.
func (t *tokenizer) rawText() token {
	start := t.pos
	name := t.raw
	end := t.findEndTag(name, t.pos)
	if end < 0 {
		t.fail(start, "<"+name+"> is never closed")
		t.pos = len(t.src)
		t.raw = ""
		return token{kind: tokText, text: t.rawValue(t.src[start:], start), offset: start}
	}
	body := t.src[start:end]
	t.pos = end
	t.raw = ""
	return token{kind: tokText, text: t.rawValue(body, start), offset: start}
}

// rawValue resolves references in RCDATA and leaves raw text alone.
func (t *tokenizer) rawValue(s string, off int) string {
	if t.rcdata {
		return t.decodeRefs(s, off, false)
	}
	return s
}

// findEndTag locates "</name" followed by a tag terminator, from position i.
func (t *tokenizer) findEndTag(name string, i int) int {
	for {
		j := strings.Index(strings.ToLower(t.src[i:]), "</"+name)
		if j < 0 {
			return -1
		}
		at := i + j
		after := at + 2 + len(name)
		if after >= len(t.src) || t.src[after] == '>' || isSpace(t.src[after]) || t.src[after] == '/' {
			return at
		}
		i = at + 1
	}
}

// markup reads whatever begins with "<".
func (t *tokenizer) markup() token {
	start := t.pos

	// A comment. Dropped rather than tokenized: nothing downstream has any use
	// for one.
	if strings.HasPrefix(t.src[t.pos:], "<!--") {
		if end := strings.Index(t.src[t.pos+4:], "-->"); end >= 0 {
			t.pos += 4 + end + 3
			return t.next()
		}
		t.fail(start, "a comment that is never closed")
		t.pos = len(t.src)
		return t.next()
	}

	if strings.HasPrefix(strings.ToLower(t.src[t.pos:]), "<!doctype") {
		return t.doctype()
	}

	if strings.HasPrefix(t.src[t.pos:], "<![") || strings.HasPrefix(t.src[t.pos:], "<!") {
		t.fail(start, "a declaration this engine does not read")
		t.skipTo('>')
		return t.next()
	}

	if strings.HasPrefix(t.src[t.pos:], "<?") {
		t.fail(start, "a processing instruction, which HTML has none of")
		t.skipTo('>')
		return t.next()
	}

	if strings.HasPrefix(t.src[t.pos:], "</") {
		return t.endTag()
	}

	// "<" that cannot begin a tag. A browser reads it as text; this refuses,
	// because in a template it is an unescaped character the author meant to
	// write as "&lt;" and the difference is invisible until it swallows a line.
	if t.pos+1 >= len(t.src) || !isNameStart(t.src[t.pos+1]) {
		t.fail(start, "a \"<\" that does not begin a tag; write \"&lt;\" for a literal one")
		t.pos++
		return t.next()
	}
	return t.startTag()
}

func (t *tokenizer) skipTo(c byte) {
	for t.pos < len(t.src) && t.src[t.pos] != c {
		t.pos++
	}
	if t.pos < len(t.src) {
		t.pos++
	}
}

func (t *tokenizer) doctype() token {
	start := t.pos
	t.skipTo('>')
	return token{kind: tokDoctype, offset: start}
}

func (t *tokenizer) endTag() token {
	start := t.pos
	t.pos += 2
	name := t.readName()
	if name == "" {
		t.fail(start, "an end tag with no name")
		t.skipTo('>')
		return t.next()
	}
	t.skipSpace()
	if t.pos < len(t.src) && t.src[t.pos] == '>' {
		t.pos++
	} else {
		t.fail(start, "the end tag </"+name+" is not closed with \">\"")
		t.skipTo('>')
	}
	return token{kind: tokEndTag, name: name, offset: start}
}

func (t *tokenizer) startTag() token {
	start := t.pos
	t.pos++ // "<"
	name := t.readName()

	out := token{kind: tokStartTag, name: name, offset: start}
	seen := map[string]bool{}

	for {
		t.skipSpace()
		if t.pos >= len(t.src) {
			t.fail(start, "the tag <"+name+" is never closed")
			return out
		}
		switch t.src[t.pos] {
		case '>':
			t.pos++
			return out
		case '/':
			if t.pos+1 < len(t.src) && t.src[t.pos+1] == '>' {
				t.pos += 2
				out.selfClosing = true
				return out
			}
			t.fail(t.pos, "a \"/\" in the middle of the tag <"+name+">")
			t.pos++
			continue
		}

		at := t.pos
		attr, ok := t.attribute(name)
		if !ok {
			// attribute reported the problem and consumed something, or the tag
			// is unsalvageable.
			if t.pos == at {
				t.skipTo('>')
				return out
			}
			continue
		}
		if seen[attr.Name] {
			// A repeated attribute is refused rather than resolved. HTML keeps
			// the first and drops the rest, which means a template with two
			// class attributes silently loses one — and which one is lost is
			// not something an author can see in the output.
			t.fail(at, "the attribute \""+attr.Name+"\" appears twice on <"+name+">")
			continue
		}
		seen[attr.Name] = true
		out.attrs = append(out.attrs, attr)
	}
}

func (t *tokenizer) attribute(tag string) (Attribute, bool) {
	at := t.pos
	name := t.readAttrName()
	if name == "" {
		t.fail(at, "expected an attribute name in <"+tag+">")
		t.pos++
		return Attribute{}, false
	}
	t.skipSpace()
	if t.pos >= len(t.src) || t.src[t.pos] != '=' {
		// A boolean attribute: present, with the empty string for a value.
		return Attribute{Name: name}, true
	}
	t.pos++ // "="
	t.skipSpace()
	if t.pos >= len(t.src) {
		t.fail(at, "the attribute \""+name+"\" has no value")
		return Attribute{}, false
	}

	switch q := t.src[t.pos]; q {
	case '"', '\'':
		t.pos++
		start := t.pos
		for t.pos < len(t.src) && t.src[t.pos] != q {
			t.pos++
		}
		if t.pos >= len(t.src) {
			t.fail(at, "the value of \""+name+"\" is never closed")
			return Attribute{Name: name, Value: t.decodeRefs(t.src[start:], start, true)}, true
		}
		v := t.decodeRefs(t.src[start:t.pos], start, true)
		t.pos++
		return Attribute{Name: name, Value: v}, true
	}

	// Unquoted. HTML allows it; the characters it forbids inside one are the
	// ones that make the end ambiguous, and each is refused rather than guessed
	// at.
	start := t.pos
	for t.pos < len(t.src) && !isSpace(t.src[t.pos]) && t.src[t.pos] != '>' {
		switch t.src[t.pos] {
		case '"', '\'', '<', '=', '`':
			t.fail(t.pos, fmt.Sprintf("%q cannot appear in an unquoted attribute value; quote it",
				t.src[t.pos]))
			t.skipTo('>')
			return Attribute{}, false
		}
		t.pos++
	}
	if t.pos == start {
		t.fail(at, "the attribute \""+name+"\" has no value")
		return Attribute{}, false
	}
	return Attribute{Name: name, Value: t.decodeRefs(t.src[start:t.pos], start, true)}, true
}

func (t *tokenizer) readName() string {
	start := t.pos
	for t.pos < len(t.src) && isNamePart(t.src[t.pos]) {
		t.pos++
	}
	return strings.ToLower(t.src[start:t.pos])
}

// readAttrName reads an attribute name, which admits more characters than an
// element name does.
func (t *tokenizer) readAttrName() string {
	start := t.pos
	for t.pos < len(t.src) {
		c := t.src[t.pos]
		if isSpace(c) || c == '=' || c == '>' || c == '/' || c == '"' || c == '\'' || c == '<' {
			break
		}
		t.pos++
	}
	return strings.ToLower(t.src[start:t.pos])
}

func (t *tokenizer) skipSpace() {
	for t.pos < len(t.src) && isSpace(t.src[t.pos]) {
		t.pos++
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isNameStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNamePart(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9') || c == '-' || c == '_'
}

// decodeRefs resolves character references in a run of text.
//
// There is no expansion budget here, and the absence is deliberate rather than
// an oversight: HTML character references do not nest. "&amp;amp;" is the text
// "&amp;", not an ampersand, because resolution is a single pass over the
// source. The billion-laughs attack needs XML's recursive entity definitions,
// which HTML has none of, so the worst case is the 32:1 of the longest name.
//
// inAttr changes only the diagnostics, since the resolution itself is the same.
func (t *tokenizer) decodeRefs(s string, off int, inAttr bool) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		text, n, ok := t.reference(s[i:], off+i, inAttr)
		if !ok {
			b.WriteByte('&')
			i++
			continue
		}
		b.WriteString(text)
		i += n
	}
	return b.String()
}

// reference resolves one character reference at the start of s, returning the
// text it stands for and how many bytes it spanned.
func (t *tokenizer) reference(s string, off int, inAttr bool) (string, int, bool) {
	if len(s) < 2 {
		return "", 0, false
	}

	if s[1] == '#' {
		return t.numericReference(s, off)
	}

	// A named reference. The semicolon is required: without it the standard
	// matches one of 106 historical names, so "&notit;" is "¬it;" in a browser,
	// and a reader that guesses differently is a reader that silently changes
	// the text.
	end := -1
	limit := min(len(s), maxEntityNameLen+2)
	for j := 1; j < limit; j++ {
		if s[j] == ';' {
			end = j
			break
		}
		if !isEntityNamePart(s[j]) {
			break
		}
	}
	if end > 1 {
		name := s[1 : end+1] // including the ";", which is how the table is keyed
		if text, found := namedEntities[name]; found {
			return text, end + 1, true
		}
		t.fail(off, "\"&"+name+"\" is not a character reference; write \"&amp;\" for a literal ampersand")
		return "", 0, false
	}

	// No semicolon. This is a literal ampersand here and in a browser — unless
	// a historical name matches, which is the one case where the two differ, and
	// so the one case worth reporting.
	if legacy, n := longestLegacyName(s); legacy != "" {
		if inAttr {
			t.fail(off, "\"&"+legacy+"\" without a \";\" is a literal ampersand here "+
				"and a character reference in some browsers; write \"&amp;\" or \"&"+legacy+";\"")
		} else {
			t.fail(off, "\"&"+legacy+"\" is missing its \";\"; write \"&"+legacy+";\", "+
				"or \"&amp;\" for a literal ampersand")
		}
		_ = n
	}
	return "", 0, false
}

// longestLegacyName finds the longest semicolon-less name in the table that
// matches at the start of s, which is what a browser would resolve here.
func longestLegacyName(s string) (string, int) {
	limit := min(len(s)-1, maxEntityNameLen)
	for n := limit; n >= 2; n-- {
		name := s[1 : 1+n]
		if _, ok := namedEntities[name]; ok {
			return name, n
		}
	}
	return "", 0
}

func (t *tokenizer) numericReference(s string, off int) (string, int, bool) {
	i := 2
	base, digits := 10, "0123456789"
	if i < len(s) && (s[i] == 'x' || s[i] == 'X') {
		i++
		base, digits = 16, "0123456789abcdefABCDEF"
	}
	start := i
	for i < len(s) && strings.IndexByte(digits, s[i]) >= 0 {
		i++
	}
	if i == start {
		t.fail(off, "\"&#\" with no number after it; write \"&amp;\" for a literal ampersand")
		return "", 0, false
	}
	if i >= len(s) || s[i] != ';' {
		t.fail(off, "a numeric character reference with no \";\"")
		return "", 0, false
	}

	// A number too long to be a code point is rejected before it is parsed, so
	// a run of a million digits costs nothing.
	if i-start > 8 {
		t.fail(off, "a numeric character reference far outside Unicode")
		return "", 0, false
	}
	v, err := strconv.ParseInt(s[start:i], base, 64)
	if err != nil {
		t.fail(off, "a numeric character reference that is not a number")
		return "", 0, false
	}
	r, ok := t.codePoint(v, off)
	if !ok {
		return "", 0, false
	}
	return string(r), i + 1, true
}

// codePoint turns a numeric reference's value into a rune, refusing the ones
// that are not characters.
//
// The surrogates and the out-of-range values are refused rather than replaced,
// because a document asking for one is a document built wrong — most often by
// something that encoded UTF-16 as if it were code points — and silently
// substituting U+FFFD would put a replacement character in the page with nothing
// to say where it came from.
func (t *tokenizer) codePoint(v int64, off int) (rune, bool) {
	switch {
	case v == 0:
		t.fail(off, "a character reference to U+0000, which is not text")
		return 0, false
	case v >= 0xD800 && v <= 0xDFFF:
		t.fail(off, fmt.Sprintf("a character reference to U+%04X, half of a surrogate pair "+
			"and not a character", v))
		return 0, false
	case v > 0x10FFFF:
		t.fail(off, fmt.Sprintf("a character reference to %d, which is outside Unicode", v))
		return 0, false
	}
	return rune(v), true
}

func isEntityNamePart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
