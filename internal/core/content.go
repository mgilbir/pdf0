package core

import (
	"iter"
	"math"
	"strconv"

	"github.com/mgilbir/pdf0/internal/checked"
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
	Raw  []byte // KindNumber: the unparsed digits, sub-sliced from the content
}

// Number parses a KindNumber token's value. Parsing is deferred to the consumer
// because most consumers never look at a Number: the PDF/UA content pass reads
// only operators, names and strings, yet numbers are the most common token in a
// content stream, so parsing every one eagerly was pure waste.
func (t ContentToken) Number() float64 {
	f, _ := strconv.ParseFloat(string(t.Raw), 64)
	return f
}

// TokenizeContent iterates the operand/operator tokens of a content stream,
// with strings decoded. It is a view of ContentLexer for the consumers that
// want decoded operands and nothing else: dictionary delimiters are dropped (so
// a property list's entries arrive as loose names and values) and inline
// images are skipped.
//
// Tokens are yielded one at a time rather than collected into a slice: a
// content stream of a real document can hold tens of millions of tokens, and
// materialising them dominated PDF/UA validation.
func TokenizeContent(cancel Canceler, data []byte) iter.Seq[ContentToken] {
	return func(yield func(ContentToken) bool) {
		lx := NewContentLexer(cancel, data)
		var t ContentTok
		for lx.Next(&t) {
			var ct ContentToken
			switch t.Kind {
			case ContentOperator:
				ct = ContentToken{Kind: KindOp, Op: string(t.Raw)}
			case ContentNumber:
				ct = ContentToken{Kind: KindNumber, Raw: t.Raw}
			case ContentString, ContentHexString:
				ct = ContentToken{Kind: KindString, Str: t.Bytes()}
			case ContentName:
				ct = ContentToken{Kind: KindName, Name: t.Name()}
			case ContentArrayStart:
				ct = ContentToken{Kind: KindArrayStart}
			case ContentArrayEnd:
				ct = ContentToken{Kind: KindArrayEnd}
			default:
				continue
			}
			if !yield(ct) {
				return
			}
		}
	}
}

// SkipContentInlineImage steps past a BI…ID…EI inline image, given i positioned
// just after the BI operator. It delegates to SkipInlineImage — the single,
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
	// The length is the file's number: it is used only when it lies inside the
	// stream, compared against the room left rather than added to the offset,
	// so that no value of it can wrap (audit 2026-09-22 C16).
	if declLen, ok := InlineImageDeclaredLength(data[paramStart:binaryStart]); ok && declLen <= n-binaryStart {
		j := binaryStart + declLen
		for j < n && IsContentWS(data[j]) {
			j++
		}
		if j+1 < n && data[j] == 'E' && data[j+1] == 'I' &&
			(j+2 >= n || IsContentWS(data[j+2]) || IsContentDelim(data[j+2])) {
			*pos = j + 2
			return
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
// the binary sample data: a non-negative int, or false when the value is
// absent or does not fit one. A caller must still check it against the bytes
// that remain.
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
		v, digits, fits := checked.Decimal(params[j:])
		if digits == 0 {
			continue
		}
		if !fits || v > math.MaxInt {
			return 0, false
		}
		return int(v), true
	}
	return 0, false
}
