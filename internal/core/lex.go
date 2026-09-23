package core

import (
	"strconv"

	"github.com/mgilbir/pdf0/syntax"
)

// The content-stream lexer: the one place in the module that cuts a decoded
// content stream into tokens (ISO 32000-2 7.8.2, 8.9.7). Text extraction, the
// graphics-state interpreter, the PDF/A and PDF/UA content rules and PDF/R all
// read content through it.
//
// There used to be five: TokenizeContent, ForEachContentItem,
// ForEachContentToken, ScanStreamForDeviceOps's own, and a family of byte
// scanners in pdfa. They had drifted apart — in whether an inline image's /L
// was honoured, how a stray ')' was consumed, whether a backslash-newline
// continued a string, what a non-hex byte in a hex string meant, and whether an
// over-long run of binary was a token — so the same bytes meant different
// things to different rules (audit 2026-09-22 C149, C150). What each of them
// guarded against is guarded here, all at once:
//
//   - cancellation is polled every CancelScanBytes of input;
//   - every step consumes at least one byte, including a stray ')' '>' '{' '}'
//     that starts no token, so no input can stall the scan;
//   - a run of regular bytes longer than MaxContentTokenLen that is not a
//     number is binary, not an operator, and is dropped whole;
//   - comments, strings and hex strings are single forward scans, so an
//     unterminated one costs the rest of the stream, once;
//   - a dictionary operand is skipped with a bounded nesting depth
//     (maxContentDictDepth);
//   - an inline image's sample data is stepped over by SkipInlineImage, which
//     honours a declared /L so binary that happens to contain "EI" does not
//     end it early.
//
// Strings, hex strings and names are decoded by the shared decoders in the
// syntax package, the same ones the object parser uses, and only when a
// consumer asks: most tokens of most streams are numbers and operators that
// nobody decodes.

// ContentKind classifies a content-stream token.
type ContentKind uint8

const (
	// ContentOperator is a keyword: an operator, or the operand keywords
	// true, false and null.
	ContentOperator ContentKind = iota + 1
	// ContentNumber is a run beginning with a digit, sign or '.'. It is read
	// whole whatever follows — the real-number grammar of Annex C allows any
	// precision — and parsed only on request.
	ContentNumber
	ContentName
	ContentString    // a literal string (...)
	ContentHexString // a hexadecimal string <...>
	ContentArrayStart
	ContentArrayEnd
	ContentDictStart
	ContentDictEnd
	// ContentInlineImage is one whole BI … ID … EI inline image. The lexer
	// reports it as a single token because its sample data is not content:
	// read as tokens, binary bytes become operators the file never wrote.
	ContentInlineImage
)

// ContentTok is one token. Raw and the inline-image fields are sub-slices of
// the content being scanned; they are valid while it is, and the lexer
// overwrites the token on the next call.
type ContentTok struct {
	Kind ContentKind
	// Raw is the token's source bytes: an operator's word, a number's digits,
	// a name without its '/', a string with its delimiters, "[" "]" "<<" ">>",
	// or for an inline image the bytes from BI to EI.
	Raw []byte
	// Pos is the offset of the token's first byte in the content.
	Pos int
	// Params is an inline image's parameter region, between BI and ID, and
	// Data its sample bytes, between the white space after ID and EI.
	Params, Data []byte
}

// Is reports whether the token is the operator op.
func (t *ContentTok) Is(op string) bool {
	return t.Kind == ContentOperator && string(t.Raw) == op
}

// Name returns a name token's value, with #xx escapes expanded. A malformed
// escape is kept as written rather than refused: the lexer is lenient, and a
// rule that cares about the escape reads Raw.
func (t *ContentTok) Name() string { return contentName(t.Raw) }

// contentName is a name token's value from its raw bytes (after the '/').
func contentName(raw []byte) string {
	v, err := syntax.DecodeName(raw)
	if err != nil {
		return string(raw)
	}
	return string(v)
}

// Bytes returns a string token's decoded value (nil for any other kind). The
// slice is freshly allocated.
func (t *ContentTok) Bytes() []byte { return contentString(t.Kind, t.Raw) }

// contentString decodes a string token of the given kind from its raw bytes.
func contentString(kind ContentKind, raw []byte) []byte {
	switch kind {
	case ContentString:
		v, _, _ := syntax.DecodeLiteralString(raw, 0)
		return v
	case ContentHexString:
		v, _ := syntax.DecodeHexString(hexBody(raw))
		return v
	}
	return nil
}

// HexBody returns the bytes between a hex string's '<' and its '>' (to the end
// of the content when unterminated), for a rule about how the string is
// written rather than what it says.
func (t *ContentTok) HexBody() []byte {
	if t.Kind != ContentHexString {
		return nil
	}
	return hexBody(t.Raw)
}

func hexBody(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	b := raw[1:]
	if n := len(b); n > 0 && b[n-1] == '>' {
		b = b[:n-1]
	}
	return b
}

// Number parses a number token; a malformed one is 0.
func (t *ContentTok) Number() float64 {
	f, err := strconv.ParseFloat(string(t.Raw), 64)
	if err != nil {
		return 0
	}
	return f
}

// Int parses a number token as an integer, reporting false when it is not
// one (a real, or malformed).
func (t *ContentTok) Int() (int, bool) {
	n, err := strconv.Atoi(string(t.Raw))
	return n, err == nil
}

// maxContentDictDepth bounds the nesting of a dictionary operand. A property
// list is a small dictionary; a stream of nothing but "<<" must cost time in
// proportion to its length, not its depth.
const maxContentDictDepth = 64

// ContentLexer scans one decoded content stream. The zero value scans
// nothing; build one with NewContentLexer.
type ContentLexer struct {
	data       []byte
	pos        int
	cancel     Canceler
	nextCancel int
	// params marks a lexer reading an inline image's parameter region, where
	// BI is not an operator. Without it a region of nothing but "BI BI BI …"
	// would recurse once per BI.
	params bool
}

// NewContentLexer returns a lexer over data. It stops early, reporting no
// further tokens, when cancel fires.
func NewContentLexer(cancel Canceler, data []byte) *ContentLexer {
	return &ContentLexer{data: data, cancel: cancel}
}

// Next scans the next token into t, reporting false at the end of the content
// or when the scan was cancelled.
func (l *ContentLexer) Next(t *ContentTok) bool {
	data := l.data
	n := len(data)
	for l.pos < n {
		if l.pos >= l.nextCancel {
			if l.cancel.Stopped() {
				l.pos = n
				return false
			}
			l.nextCancel = l.pos + CancelScanBytes
		}
		i := l.pos
		c := data[i]
		switch {
		case IsContentWS(c):
			l.pos++
			continue
		case c == '%':
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			l.pos = i
			continue
		case c == '(':
			end, _ := syntax.LiteralStringEnd(data, i)
			return l.emit(t, ContentString, i, end)
		case c == '<':
			if i+1 < n && data[i+1] == '<' {
				return l.emit(t, ContentDictStart, i, i+2)
			}
			j := i + 1
			for j < n && data[j] != '>' {
				j++
			}
			if j < n {
				j++
			}
			return l.emit(t, ContentHexString, i, j)
		case c == '>':
			if i+1 < n && data[i+1] == '>' {
				return l.emit(t, ContentDictEnd, i, i+2)
			}
			l.pos++ // a stray '>' starts no token
			continue
		case c == '[':
			return l.emit(t, ContentArrayStart, i, i+1)
		case c == ']':
			return l.emit(t, ContentArrayEnd, i, i+1)
		case c == '/':
			j := i + 1
			for j < n && !IsContentWS(data[j]) && !IsContentDelim(data[j]) {
				j++
			}
			l.emit(t, ContentName, i, j)
			t.Raw = t.Raw[1:]
			return true
		case IsContentDelim(c):
			// ')' '{' '}' outside any string: a delimiter that starts no token.
			// Consumed, so an unmatched one cannot stall the scan.
			l.pos++
			continue
		}
		j := i
		for j < n && !IsContentWS(data[j]) && !IsContentDelim(data[j]) {
			j++
		}
		if c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9') {
			return l.emit(t, ContentNumber, i, j)
		}
		if j-i > MaxContentTokenLen {
			// A binary run, not a keyword. Dropped whole: cutting it at the
			// cap and resuming mid-run would manufacture operators out of its
			// tail.
			l.pos = j
			continue
		}
		if j-i == 2 && data[i] == 'B' && data[i+1] == 'I' && !l.params {
			l.inlineImage(t, i, j)
			return true
		}
		return l.emit(t, ContentOperator, i, j)
	}
	return false
}

// emit fills t field by field rather than assigning a whole ContentTok: this
// runs once per token of every content stream, and a whole-struct store writes
// (and, while the collector runs, write-barriers) the two inline-image slices
// that almost no token has.
func (l *ContentLexer) emit(t *ContentTok, kind ContentKind, start, end int) bool {
	t.Kind = kind
	t.Raw = l.data[start:end]
	t.Pos = start
	if t.Params != nil || t.Data != nil {
		t.Params, t.Data = nil, nil
	}
	l.pos = end
	return true
}

// inlineImage is the inline-image hook: BI at [start, afterBI). The parameter
// region runs to the ID operator, read with this lexer's own rules; the end of
// the sample data is where SkipInlineImage says it is, which is where /L says
// when the image declares it.
func (l *ContentLexer) inlineImage(t *ContentTok, start, afterBI int) {
	data := l.data
	end := afterBI
	SkipInlineImage(data, &end)

	params := data[afterBI:end]
	var body []byte
	sub := ContentLexer{data: data[:end], pos: afterBI, nextCancel: len(data), params: true}
	var tk ContentTok
	for sub.Next(&tk) {
		if tk.Is("ID") {
			params = data[afterBI:tk.Pos]
			binStart := tk.Pos + 2
			if binStart < end && IsContentWS(data[binStart]) {
				binStart++
			}
			binEnd := end
			if binEnd-2 >= binStart && data[binEnd-2] == 'E' && data[binEnd-1] == 'I' {
				binEnd -= 2
				if binEnd > binStart && IsContentWS(data[binEnd-1]) {
					binEnd--
				}
			}
			if binEnd >= binStart {
				body = data[binStart:binEnd]
			}
			break
		}
	}
	*t = ContentTok{Kind: ContentInlineImage, Raw: data[start:end], Pos: start, Params: params, Data: body}
	l.pos = end
}

// SkipDict consumes the rest of a dictionary operand whose "<<" was the token
// just returned, and returns its source bytes from "<<" to the matching ">>".
// Nested dictionaries and strings are stepped over, so a ">>" inside "(a>>b)"
// does not end it. An unterminated dictionary, or one nested deeper than
// maxContentDictDepth, runs to the end of the content: a truncated stream must
// leave the scan at its end rather than at a delimiter it cannot pass.
func (l *ContentLexer) SkipDict(open *ContentTok) []byte {
	start := open.Pos
	depth := 1
	var t ContentTok
	for l.Next(&t) {
		switch t.Kind {
		case ContentDictStart:
			depth++
			if depth > maxContentDictDepth {
				l.pos = len(l.data)
				return l.data[start:]
			}
		case ContentDictEnd:
			depth--
			if depth == 0 {
				return l.data[start:l.pos]
			}
		}
	}
	return l.data[start:l.pos]
}

// InlineImageParam is one entry of an inline image's parameter dictionary.
type InlineImageParam struct {
	// Key is the entry's key, without its '/' and with #xx expanded.
	Key string
	// Value is the entry's value: one token, or for an array the tokens
	// between its brackets. A dictionary value (/DecodeParms << … >>) is one
	// ContentDictStart token whose Raw spans the whole dictionary.
	Value []ContentTok
	// Array reports that the value was written as an array.
	Array bool
}

// ParseInlineImageParams reads an inline image's parameter region (a
// ContentInlineImage token's Params) into its entries, in order.
func ParseInlineImageParams(params []byte) []InlineImageParam {
	var out []InlineImageParam
	lx := ContentLexer{data: params, nextCancel: len(params) + 1, params: true}
	var t ContentTok
	var cur *InlineImageParam
	for lx.Next(&t) {
		if cur == nil {
			if t.Kind == ContentName {
				out = append(out, InlineImageParam{Key: t.Name()})
				cur = &out[len(out)-1]
			}
			continue
		}
		switch t.Kind {
		case ContentArrayStart:
			cur.Array = true
			for lx.Next(&t) && t.Kind != ContentArrayEnd {
				v := t
				if t.Kind == ContentDictStart {
					v.Raw = lx.SkipDict(&t)
				}
				cur.Value = append(cur.Value, v)
			}
		case ContentDictStart:
			v := t
			v.Raw = lx.SkipDict(&t)
			cur.Value = append(cur.Value, v)
		default:
			cur.Value = append(cur.Value, t)
		}
		cur = nil
	}
	return out
}
