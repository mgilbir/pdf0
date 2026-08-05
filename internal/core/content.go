package core

import (
	"iter"
	"strconv"
)

// The content-stream tokenizer. It is a document service rather than a
// validator one: text extraction, the PDF/A executed-content walk and the
// PDF/UA content pass all read the same operand/operator stream.

type TokenKind int

const (
	KindOp TokenKind = iota
	KindNumber
	KindString
	KindName
	KindArrayStart
	KindArrayEnd
)

type ContentToken struct {
	Kind TokenKind
	Op   string
	Name string
	Str  []byte
	Raw  []byte // ctNumber: the unparsed digits, sub-sliced from the content
}

// Number parses a ctNumber token's value. Parsing is deferred to the consumer
// because most consumers never look at a Number: the PDF/UA content pass reads
// only operators, names and strings, yet numbers are the most common token in a
// content stream, so parsing every one eagerly was pure waste.
func (t ContentToken) Number() float64 {
	f, _ := strconv.ParseFloat(string(t.Raw), 64)
	return f
}

// TokenizeContent iterates the operand/operator tokens of a content stream. It
// is lenient: unrecognized bytes are skipped. Array and dictionary delimiters are
// surfaced so TJ arrays can be read; inline images (BI…ID…EI) are stepped over.
//
// Tokens are yielded one at a time rather than collected into a slice. Every
// caller consumes them in a single forward pass, and a content stream of a real
// document can hold tens of millions of tokens: materializing them dominated
// PDF/UA validation, where the token slice alone accounted for ~94% of the run's
// allocated bytes (the repeated grow-and-copy of a multi-gigabyte slice, not the
// scan itself). Streaming makes the tokenizer allocation-free apart from the
// string operands it must decode.
//
// The scan stops when cancel fires, checked every cancelScanBytes of input; see
// cancel.go for why the check is gated on the scan position rather than run per
// token.
func TokenizeContent(cancel Canceler, data []byte) iter.Seq[ContentToken] {
	return func(yield func(ContentToken) bool) {
		i := 0
		nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
		for i < len(data) {
			if i >= nextCancelCheck {
				if cancel.Stopped() {
					return
				}
				nextCancelCheck = i + CancelScanBytes
			}
			c := data[i]
			switch {
			case IsContentWS(c):
				i++
			case c == '%':
				for i < len(data) && data[i] != '\n' && data[i] != '\r' {
					i++
				}
			case c == '(':
				s, ni := scanContentLiteral(data, i)
				if !yield(ContentToken{Kind: KindString, Str: s}) {
					return
				}
				i = ni
			case c == '<' && i+1 < len(data) && data[i+1] == '<':
				i += 2 // dictionary start — skip; not needed for text
			case c == '>' && i+1 < len(data) && data[i+1] == '>':
				i += 2
			case c == '<':
				s, ni := scanContentHex(data, i)
				if !yield(ContentToken{Kind: KindString, Str: s}) {
					return
				}
				i = ni
			case c == '/':
				n, ni := scanContentName(data, i)
				if !yield(ContentToken{Kind: KindName, Name: n}) {
					return
				}
				i = ni
			case c == '[':
				if !yield(ContentToken{Kind: KindArrayStart}) {
					return
				}
				i++
			case c == ']':
				if !yield(ContentToken{Kind: KindArrayEnd}) {
					return
				}
				i++
			case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
				raw, ni := scanContentNumberBytes(data, i)
				if !yield(ContentToken{Kind: KindNumber, Raw: raw}) {
					return
				}
				i = ni
			default:
				word, ni := scanContentWord(data, i)
				i = ni
				if word == "" {
					i++
					continue
				}
				if word == "BI" {
					i = SkipContentInlineImage(data, i)
					continue
				}
				if !yield(ContentToken{Kind: KindOp, Op: word}) {
					return
				}
			}
		}
	}
}

func scanContentLiteral(data []byte, i int) ([]byte, int) {
	i++ // '('
	var out []byte
	depth := 1
	for i < len(data) {
		c := data[i]
		switch c {
		case '\\':
			i++
			if i >= len(data) {
				return out, i
			}
			switch e := data[i]; e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			default:
				if e >= '0' && e <= '7' {
					v := 0
					for k := 0; k < 3 && i < len(data) && data[i] >= '0' && data[i] <= '7'; k++ {
						v = v*8 + int(data[i]-'0')
						i++
					}
					out = append(out, byte(v))
					continue
				}
				out = append(out, e)
			}
			i++
		case '(':
			depth++
			out = append(out, c)
			i++
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, c)
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

func scanContentHex(data []byte, i int) ([]byte, int) {
	i++ // '<'
	var digits []byte
	for i < len(data) && data[i] != '>' {
		if !IsContentWS(data[i]) {
			digits = append(digits, data[i])
		}
		i++
	}
	if i < len(data) {
		i++ // '>'
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for k := 0; k < len(out); k++ {
		out[k] = hexNibble(digits[2*k])<<4 | hexNibble(digits[2*k+1])
	}
	return out, i
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func scanContentName(data []byte, i int) (string, int) {
	i++ // '/'
	start := i
	for i < len(data) && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

// scanContentNumberBytes returns the numeric literal starting at i as a
// sub-slice of data, leaving the parse to contentToken.number.
func scanContentNumberBytes(data []byte, i int) ([]byte, int) {
	start := i
	if data[i] == '-' || data[i] == '+' {
		i++
	}
	for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == '.') {
		i++
	}
	return data[start:i], i
}

func scanContentWord(data []byte, i int) (string, int) {
	start := i
	for i < len(data) && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

// SkipContentInlineImage steps past a BI…ID…EI inline image, given i positioned
// just after the BI operator. It delegates to skipInlineImage — the single,
// robust skipper — which parses the parameter dictionary and honors a declared
// /L (or /Length) so binary sample data that happens to contain the bytes "EI"
// does not truncate the image early and spew the rest as bogus tokens (audit
// C35; the previous whitespace-delimited-EI search ignored /L).
func SkipContentInlineImage(data []byte, i int) int {
	pos := i
	SkipInlineImage(data, &pos)
	return pos
}

// SkipInlineImage advances *pos past an inline image: the parameter
// dictionary tokens up to ID, then binary data until a whitespace-delimited
// EI token.
func SkipInlineImage(data []byte, pos *int) {
	n := len(data)
	i := *pos
	paramStart := i
	// Scan tokens until the ID keyword that starts the binary section.
	for i < n {
		for i < n && IsContentWS(data[i]) {
			i++
		}
		if i >= n {
			break
		}
		if data[i] == 'I' && i+1 < n && data[i+1] == 'D' && (i+2 >= n || IsContentWS(data[i+2])) {
			i += 2
			if i < n && IsContentWS(data[i]) {
				i++ // single whitespace after ID
			}
			break
		}
		prev := i
		if IsContentDelim(data[i]) {
			i++
			if data[prev] == '(' { // string value inside the param dict
				depth := 1
				for i < n && depth > 0 {
					switch data[i] {
					case '\\':
						i++
					case '(':
						depth++
					case ')':
						depth--
					}
					i++
				}
			}
		} else {
			for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
				i++
			}
		}
		if i == prev {
			i++
		}
	}
	// Inline-image sample data is arbitrary binary and can contain the bytes
	// "EI" by chance, which the boundary search below would mistake for the end
	// (spewing the rest of the image as bogus operators/hex strings). When the
	// dictionary declares /L (or /Length), skip exactly that many bytes and
	// confirm EI follows; only fall back to the search if it is absent or
	// inconsistent, so behaviour never regresses (audit C25).
	binaryStart := i
	if declLen, ok := InlineImageDeclaredLength(data[paramStart:binaryStart]); ok {
		end := binaryStart + declLen
		if end <= n {
			j := end
			for j < n && IsContentWS(data[j]) {
				j++
			}
			if j+1 < n && data[j] == 'E' && data[j+1] == 'I' &&
				(j+2 >= n || IsContentWS(data[j+2]) || IsContentDelim(data[j+2])) {
				*pos = j + 2
				return
			}
		}
	}

	// Skip binary data until EI at a token boundary.
	for i < n {
		if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
			atBoundary := i == 0 || IsContentWS(data[i-1])
			endBoundary := i+2 >= n || IsContentWS(data[i+2]) || IsContentDelim(data[i+2])
			if atBoundary && endBoundary {
				i += 2
				break
			}
		}
		i++
	}
	*pos = i
}

func IsContentDelim(b byte) bool {
	return contentByteClass[b]&ctbDelim != 0
}

func IsContentWS(b byte) bool {
	return contentByteClass[b]&ctbWS != 0
}

// contentByteClass classifies a byte for content-stream scanning. These two
// predicates sit in the innermost loop of every content walker in the package
// and are called once per byte of every decoded content stream — hundreds of
// millions of times on a large document — so they read a single table rather
// than run a chain of comparisons. The two classes share one 256-byte table to
// keep the pair in one cache line's worth of memory, since the walkers almost
// always test both.
const (
	ctbWS byte = 1 << iota
	ctbDelim
)

var contentByteClass = func() (t [256]byte) {
	for _, b := range []byte{' ', '\t', '\n', '\r', '\x00', '\x0c'} {
		t[b] |= ctbWS
	}
	for _, b := range []byte("()<>[]{}/%") {
		t[b] |= ctbDelim
	}
	return t
}()

// InlineImageDeclaredLength extracts the /L (or /Length) value from an inline
// image's parameter region, if present. It reports the declared byte count of
// the binary sample data.
func InlineImageDeclaredLength(params []byte) (int, bool) {
	for i := 0; i < len(params); i++ {
		if params[i] != '/' {
			continue
		}
		// Read the key name.
		j := i + 1
		for j < len(params) && !IsContentWS(params[j]) && !IsContentDelim(params[j]) {
			j++
		}
		key := string(params[i+1 : j])
		if key != "L" && key != "Length" {
			continue
		}
		// Skip whitespace to the value.
		for j < len(params) && IsContentWS(params[j]) {
			j++
		}
		start := j
		v := 0
		for j < len(params) && params[j] >= '0' && params[j] <= '9' {
			v = v*10 + int(params[j]-'0')
			j++
		}
		if j == start {
			continue
		}
		return v, true
	}
	return 0, false
}
