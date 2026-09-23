package syntax

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
)

// This file implements the tokenizer: the byte-level scanner over an in-memory
// PDF that yields the tokens the parser consumes — numbers, names, literal and
// hex strings, delimiters and the structural keywords (ISO 32000-2 7.2 lexical
// conventions, 7.3 object syntax). It builds no objects and does no look-ahead;
// the parser owns both.
//
// It runs directly on untrusted bytes, and a bad byte offset drops it into the
// middle of binary stream data, so whitespace and comment skipping is bounded by
// maxTokenGap: without that bound a stray '%' inside a stream reads as a comment
// running to end of file, making parsing quadratic in the file size.

// TokenType identifies the type of a lexer token.
type TokenType int

const (
	TokenBoolean    TokenType = iota // true, false
	TokenInteger                     // 123, -98
	TokenReal                        // 3.14, -.002
	TokenString                      // (literal) or <hex>
	TokenName                        // /SomeName
	TokenArrayStart                  // [
	TokenArrayEnd                    // ]
	TokenDictStart                   // <<
	TokenDictEnd                     // >>
	TokenStream                      // stream keyword
	TokenEndStream                   // endstream keyword
	TokenObj                         // obj keyword
	TokenEndObj                      // endobj keyword
	TokenRef                         // R keyword
	TokenXref                        // xref keyword
	TokenTrailer                     // trailer keyword
	TokenStartXref                   // startxref keyword
	TokenNull                        // null keyword
	TokenEOF
)

var tokenTypeNames = map[TokenType]string{
	TokenBoolean:    "Boolean",
	TokenInteger:    "Integer",
	TokenReal:       "Real",
	TokenString:     "String",
	TokenName:       "Name",
	TokenArrayStart: "ArrayStart",
	TokenArrayEnd:   "ArrayEnd",
	TokenDictStart:  "DictStart",
	TokenDictEnd:    "DictEnd",
	TokenStream:     "Stream",
	TokenEndStream:  "EndStream",
	TokenObj:        "Obj",
	TokenEndObj:     "EndObj",
	TokenRef:        "Ref",
	TokenXref:       "Xref",
	TokenTrailer:    "Trailer",
	TokenStartXref:  "StartXref",
	TokenNull:       "Null",
	TokenEOF:        "EOF",
}

func (t TokenType) String() string {
	if name, ok := tokenTypeNames[t]; ok {
		return name
	}
	return fmt.Sprintf("TokenType(%d)", int(t))
}

// Token represents a single lexer token.
type Token struct {
	Type   TokenType
	Value  []byte // the token's value (decoded, for strings and names)
	Offset int64  // byte offset of the token's first byte in the input
	End    int64  // byte offset just past the token's last byte
}

func (t Token) String() string {
	return fmt.Sprintf("{%s %q @%d}", t.Type, t.Value, t.Offset)
}

// Lexer is a PDF tokenizer that reads from an io.ReaderAt.
type Lexer struct {
	data []byte
	pos  int64
	size int64
}

// NewLexer creates a new Lexer reading from the given data.
func NewLexer(data []byte) *Lexer {
	return &Lexer{
		data: data,
		size: int64(len(data)),
	}
}

// NewLexerFromReaderAt creates a Lexer from an io.ReaderAt by reading its first
// size bytes; see ReadSource for what is checked.
func NewLexerFromReaderAt(r io.ReaderAt, size int64) (*Lexer, error) {
	data, err := ReadSource(r, size)
	if err != nil {
		return nil, err
	}
	return NewLexer(data), nil
}

// readChunk is the first allocation ReadSource makes for a source whose length
// it cannot learn up front; it doubles from there.
const readChunk = 1 << 20

// ReadSource reads the first size bytes of r into memory. The size is the
// caller's claim, and it is checked rather than trusted:
//
//   - a negative size, or one larger than a slice can hold, is an error;
//   - a source that yields fewer than size bytes is an error, because the zero
//     padding a short read would leave behind reads as PDF whitespace and
//     silently masks truncated input;
//   - memory is committed only as bytes actually arrive. When r reports its
//     length (a Size method, as bytes.Reader, strings.Reader and
//     io.SectionReader have, or Stat, as *os.File has) a claim beyond it fails
//     before anything is allocated. Otherwise the buffer starts at 1 MiB and
//     doubles as it fills, so a false claim costs memory in proportion to what
//     the source actually holds (a small multiple of it), not to the claim.
func ReadSource(r io.ReaderAt, size int64) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("invalid input size %d", size)
	}
	if uint64(size) > uint64(math.MaxInt) {
		return nil, fmt.Errorf("input size %d is too large to hold in memory", size)
	}
	if have, ok := sourceLength(r); ok {
		if have < size {
			return nil, fmt.Errorf("short read: the source holds %d of the %d bytes claimed", have, size)
		}
		data := make([]byte, size)
		n, err := r.ReadAt(data, 0)
		if int64(n) < size {
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("reading input: %w", err)
			}
			return nil, fmt.Errorf("short read: got %d of %d bytes", n, size)
		}
		return data, nil
	}
	buf := make([]byte, 0, min(size, readChunk))
	for int64(len(buf)) < size {
		if len(buf) == cap(buf) {
			grown := make([]byte, len(buf), min(size, 2*int64(cap(buf))))
			copy(grown, buf)
			buf = grown
		}
		want := buf[len(buf):cap(buf)]
		n, err := r.ReadAt(want, int64(len(buf)))
		buf = buf[:len(buf)+n]
		if n < len(want) {
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("reading input: %w", err)
			}
			return nil, fmt.Errorf("short read: got %d of %d bytes", len(buf), size)
		}
	}
	return buf, nil
}

// sourceLength reports r's length when r can say what it is.
func sourceLength(r io.ReaderAt) (int64, bool) {
	switch s := r.(type) {
	case interface{ Size() int64 }:
		return s.Size(), true
	case interface{ Stat() (fs.FileInfo, error) }:
		if fi, err := s.Stat(); err == nil && fi.Mode().IsRegular() {
			return fi.Size(), true
		}
	}
	return 0, false
}

// Position returns the current byte offset.
func (l *Lexer) Position() int64 {
	return l.pos
}

// SetPosition sets the current byte offset for random access.
func (l *Lexer) SetPosition(offset int64) {
	l.pos = offset
}

// Data returns the underlying data slice.
func (l *Lexer) Data() []byte {
	return l.data
}

func (l *Lexer) atEnd() bool {
	return l.pos < 0 || l.pos >= l.size
}

func (l *Lexer) peek() byte {
	if l.pos < 0 || l.pos >= l.size {
		return 0
	}
	return l.data[l.pos]
}

func (l *Lexer) peekAt(offset int64) byte {
	pos := l.pos + offset
	if pos >= l.size || pos < 0 {
		return 0
	}
	return l.data[pos]
}

func (l *Lexer) advance() byte {
	if l.pos < 0 || l.pos >= l.size {
		return 0
	}
	b := l.data[l.pos]
	l.pos++
	return b
}

// IsWhitespace returns true for PDF whitespace characters (Table 1 in PDF spec).
func IsWhitespace(b byte) bool {
	switch b {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// IsDelimiter returns true for PDF delimiter characters.
func IsDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// IsRegular returns true if b is not whitespace and not a delimiter.
func IsRegular(b byte) bool {
	return !IsWhitespace(b) && !IsDelimiter(b)
}

// maxTokenGap bounds how far skipWhitespaceAndComments advances looking for the
// next token. Legitimate inter-token whitespace and comments are tiny; a run
// this long means the cursor is inside binary data — e.g. an xref offset that
// points into a stream, where the stream bytes (often containing a stray '%'
// that reads as a giant no-newline comment) are being tokenized. Continuing is
// pointless, and repeated across many such offsets it makes parsing quadratic in
// the file size. Stop so the caller fails fast instead of scanning to EOF per
// object.
const maxTokenGap = 1 << 20 // 1 MiB

// ErrTokenGap is returned, wrapped, when more than 1 MiB of whitespace and
// comments separates one token from the next. Real files never do that; it
// means the offset being read points into binary data.
var ErrTokenGap = errors.New("no token within 1 MiB: the run of whitespace and comments is too long (is the offset inside binary data?)")

// skipWhitespaceAndComments skips whitespace and comments, and reports false
// when it stopped because the run exceeded maxTokenGap rather than because it
// reached a token or the end of the input.
func (l *Lexer) skipWhitespaceAndComments() bool {
	start := l.pos
	for !l.atEnd() {
		if l.pos-start > maxTokenGap {
			return false
		}
		b := l.peek()
		if IsWhitespace(b) {
			l.advance()
			continue
		}
		if b == '%' {
			// Skip comment to end of line (bounded: a "comment" that runs past
			// the gap limit with no newline is binary data, not a real comment).
			for !l.atEnd() {
				if l.pos-start > maxTokenGap {
					return false
				}
				c := l.advance()
				if c == '\r' || c == '\n' {
					break
				}
			}
			continue
		}
		break
	}
	return true
}

// NextToken returns the next token from the input.
func (l *Lexer) NextToken() (Token, error) {
	start := l.pos
	if !l.skipWhitespaceAndComments() {
		return Token{}, fmt.Errorf("at offset %d: %w", start, ErrTokenGap)
	}
	tok, err := l.scanToken()
	if err != nil {
		return Token{}, err
	}
	tok.End = l.pos
	return tok, nil
}

// scanToken scans the token starting at the current position, which is not
// whitespace or a comment.
func (l *Lexer) scanToken() (Token, error) {
	if l.atEnd() {
		return Token{Type: TokenEOF, Offset: l.pos}, nil
	}

	offset := l.pos
	b := l.peek()

	switch {
	case b == '(':
		return l.scanLiteralString(offset)
	case b == '<':
		if l.peekAt(1) == '<' {
			l.pos += 2
			return Token{Type: TokenDictStart, Value: []byte("<<"), Offset: offset}, nil
		}
		return l.scanHexString(offset)
	case b == '>':
		if l.peekAt(1) == '>' {
			l.pos += 2
			return Token{Type: TokenDictEnd, Value: []byte(">>"), Offset: offset}, nil
		}
		return Token{}, fmt.Errorf("unexpected '>' at offset %d", offset)
	case b == '[':
		l.advance()
		return Token{Type: TokenArrayStart, Value: []byte("["), Offset: offset}, nil
	case b == ']':
		l.advance()
		return Token{Type: TokenArrayEnd, Value: []byte("]"), Offset: offset}, nil
	case b == '/':
		return l.scanName(offset)
	case b == '+' || b == '-' || b == '.' || (b >= '0' && b <= '9'):
		return l.scanNumber(offset)
	default:
		return l.scanKeyword(offset)
	}
}

// scanLiteralString scans a parenthesized string with balanced parens and
// escapes, through the shared decoder (decode.go).
func (l *Lexer) scanLiteralString(offset int64) (Token, error) {
	value, end, ok := DecodeLiteralString(l.data, int(l.pos))
	if !ok {
		l.pos = int64(len(l.data))
		return Token{}, fmt.Errorf("unterminated literal string starting at offset %d", offset)
	}
	l.pos = int64(end)
	return Token{Type: TokenString, Value: value, Offset: offset}, nil
}

// scanHexString scans a hex-encoded string <...>, through the shared decoder
// (decode.go). A byte that is neither a hex digit nor white space makes the
// string invalid: the object lexer is strict where the content lexer is not.
func (l *Lexer) scanHexString(offset int64) (Token, error) {
	start := int(l.pos) + 1 // past '<'
	rel := bytes.IndexByte(l.data[start:], '>')
	if rel < 0 {
		l.pos = int64(len(l.data))
		return Token{}, fmt.Errorf("unterminated hex string starting at offset %d", offset)
	}
	l.pos = int64(start + rel + 1)
	decoded, ok := DecodeHexString(l.data[start : start+rel])
	if !ok {
		return Token{}, fmt.Errorf("invalid hex string at offset %d: a byte that is neither a hex digit nor white space", offset)
	}
	return Token{Type: TokenString, Value: decoded, Offset: offset}, nil
}

// DecodeHex decodes hex digit bytes into a byte slice.
// If odd number of digits, a trailing 0 is assumed.
func DecodeHex(digits []byte) ([]byte, error) {
	if len(digits)%2 != 0 {
		digits = append(digits, '0')
	}
	result := make([]byte, len(digits)/2)
	for i := 0; i < len(digits); i += 2 {
		hi, err := hexVal(digits[i])
		if err != nil {
			return nil, err
		}
		lo, err := hexVal(digits[i+1])
		if err != nil {
			return nil, err
		}
		result[i/2] = hi<<4 | lo
	}
	return result, nil
}

func hexVal(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	}
	return 0, fmt.Errorf("invalid hex digit: %c", b)
}

// scanName scans a PDF name token, expanding #xx escapes through the shared
// decoder (decode.go).
func (l *Lexer) scanName(offset int64) (Token, error) {
	l.advance() // consume '/'
	start := l.pos
	for !l.atEnd() && IsRegular(l.peek()) {
		l.advance()
	}
	raw := l.data[start:l.pos]
	value, err := DecodeName(raw)
	if err != nil {
		return Token{}, fmt.Errorf("%w at offset %d", err, offset)
	}
	if len(value) > 0 && &value[0] == &raw[0] {
		value = append([]byte(nil), raw...) // a token's Value never aliases the source
	}
	return Token{Type: TokenName, Value: value, Offset: offset}, nil
}

// scanNumber scans an integer or real number token.
func (l *Lexer) scanNumber(offset int64) (Token, error) {
	start := l.pos
	isReal := false

	if l.peek() == '+' || l.peek() == '-' {
		l.advance()
	}

	for !l.atEnd() {
		b := l.peek()
		if b == '.' {
			if isReal {
				// A second '.' would silently split "1.2.3" into two reals,
				// changing element counts; it is a malformed number.
				return Token{}, fmt.Errorf("malformed number with multiple '.' at offset %d", offset)
			}
			isReal = true
			l.advance()
			continue
		}
		if b >= '0' && b <= '9' {
			l.advance()
			continue
		}
		break
	}

	value := l.data[start:l.pos]
	if isReal {
		return Token{Type: TokenReal, Value: value, Offset: offset}, nil
	}
	return Token{Type: TokenInteger, Value: value, Offset: offset}, nil
}

// scanKeyword scans a keyword token (regular characters until whitespace/delimiter).
func (l *Lexer) scanKeyword(offset int64) (Token, error) {
	start := l.pos
	for !l.atEnd() && IsRegular(l.peek()) {
		l.advance()
	}

	word := string(l.data[start:l.pos])
	switch word {
	case "true", "false":
		return Token{Type: TokenBoolean, Value: l.data[start:l.pos], Offset: offset}, nil
	case "null":
		return Token{Type: TokenNull, Value: l.data[start:l.pos], Offset: offset}, nil
	case "obj":
		return Token{Type: TokenObj, Value: l.data[start:l.pos], Offset: offset}, nil
	case "endobj":
		return Token{Type: TokenEndObj, Value: l.data[start:l.pos], Offset: offset}, nil
	case "stream":
		return Token{Type: TokenStream, Value: l.data[start:l.pos], Offset: offset}, nil
	case "endstream":
		return Token{Type: TokenEndStream, Value: l.data[start:l.pos], Offset: offset}, nil
	case "R":
		return Token{Type: TokenRef, Value: l.data[start:l.pos], Offset: offset}, nil
	case "xref":
		return Token{Type: TokenXref, Value: l.data[start:l.pos], Offset: offset}, nil
	case "trailer":
		return Token{Type: TokenTrailer, Value: l.data[start:l.pos], Offset: offset}, nil
	case "startxref":
		return Token{Type: TokenStartXref, Value: l.data[start:l.pos], Offset: offset}, nil
	default:
		return Token{}, fmt.Errorf("unknown keyword %q at offset %d", word, offset)
	}
}
