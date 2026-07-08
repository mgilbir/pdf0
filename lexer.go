package pdf0

import (
	"bytes"
	"fmt"
	"io"
)

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
	Value  []byte // raw bytes of the token
	Offset int64  // byte offset in input
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

// NewLexerFromReaderAt creates a Lexer from an io.ReaderAt by reading all data.
// A reader that yields fewer than size bytes is an error: the zero padding a
// short read would leave behind counts as PDF whitespace, silently masking
// truncated input.
func NewLexerFromReaderAt(r io.ReaderAt, size int64) (*Lexer, error) {
	data := make([]byte, size)
	n, err := r.ReadAt(data, 0)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading input: %w", err)
	}
	if int64(n) < size {
		return nil, fmt.Errorf("short read: got %d of %d bytes", n, size)
	}
	return NewLexer(data), nil
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

// isWhitespace returns true for PDF whitespace characters (Table 1 in PDF spec).
func isWhitespace(b byte) bool {
	switch b {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// isDelimiter returns true for PDF delimiter characters.
func isDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// isRegular returns true if b is not whitespace and not a delimiter.
func isRegular(b byte) bool {
	return !isWhitespace(b) && !isDelimiter(b)
}

// skipWhitespaceAndComments skips whitespace and comments.
func (l *Lexer) skipWhitespaceAndComments() {
	for !l.atEnd() {
		b := l.peek()
		if isWhitespace(b) {
			l.advance()
			continue
		}
		if b == '%' {
			// Skip comment to end of line
			for !l.atEnd() {
				c := l.advance()
				if c == '\r' || c == '\n' {
					break
				}
			}
			continue
		}
		break
	}
}

// NextToken returns the next token from the input.
func (l *Lexer) NextToken() (Token, error) {
	l.skipWhitespaceAndComments()

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

// scanLiteralString scans a parenthesized string with balanced parens and escapes.
func (l *Lexer) scanLiteralString(offset int64) (Token, error) {
	l.advance() // consume '('
	var buf bytes.Buffer
	depth := 1

	for !l.atEnd() {
		b := l.advance()
		switch b {
		case '(':
			depth++
			buf.WriteByte('(')
		case ')':
			depth--
			if depth == 0 {
				return Token{
					Type:   TokenString,
					Value:  buf.Bytes(),
					Offset: offset,
				}, nil
			}
			buf.WriteByte(')')
		case '\\':
			if l.atEnd() {
				return Token{}, fmt.Errorf("unexpected end of input in string escape at offset %d", offset)
			}
			next := l.advance()
			switch next {
			case 'n':
				buf.WriteByte('\n')
			case 'r':
				buf.WriteByte('\r')
			case 't':
				buf.WriteByte('\t')
			case 'b':
				buf.WriteByte('\b')
			case 'f':
				buf.WriteByte('\f')
			case '(':
				buf.WriteByte('(')
			case ')':
				buf.WriteByte(')')
			case '\\':
				buf.WriteByte('\\')
			case '\r':
				// Line continuation: \<CR> or \<CR><LF>
				if !l.atEnd() && l.peek() == '\n' {
					l.advance()
				}
			case '\n':
				// Line continuation: \<LF>
			default:
				// Octal escape: 1-3 octal digits
				if next >= '0' && next <= '7' {
					octal := int(next - '0')
					for i := 0; i < 2; i++ {
						if !l.atEnd() && l.peek() >= '0' && l.peek() <= '7' {
							octal = octal*8 + int(l.advance()-'0')
						} else {
							break
						}
					}
					buf.WriteByte(byte(octal))
				} else {
					// Unknown escape: just emit the character
					buf.WriteByte(next)
				}
			}
		case '\r':
			// Normalize \r and \r\n to \n within strings
			if !l.atEnd() && l.peek() == '\n' {
				l.advance()
			}
			buf.WriteByte('\n')
		default:
			buf.WriteByte(b)
		}
	}

	return Token{}, fmt.Errorf("unterminated literal string starting at offset %d", offset)
}

// scanHexString scans a hex-encoded string <...>.
func (l *Lexer) scanHexString(offset int64) (Token, error) {
	l.advance() // consume '<'
	var hexDigits []byte

	for !l.atEnd() {
		b := l.advance()
		if b == '>' {
			// Decode hex digits
			decoded, err := decodeHex(hexDigits)
			if err != nil {
				return Token{}, fmt.Errorf("invalid hex string at offset %d: %w", offset, err)
			}
			return Token{
				Type:   TokenString,
				Value:  decoded,
				Offset: offset,
			}, nil
		}
		if isWhitespace(b) {
			continue // ignore whitespace in hex strings
		}
		hexDigits = append(hexDigits, b)
	}

	return Token{}, fmt.Errorf("unterminated hex string starting at offset %d", offset)
}

// decodeHex decodes hex digit bytes into a byte slice.
// If odd number of digits, a trailing 0 is assumed.
func decodeHex(digits []byte) ([]byte, error) {
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

// scanName scans a PDF name token.
func (l *Lexer) scanName(offset int64) (Token, error) {
	l.advance() // consume '/'
	var buf bytes.Buffer

	for !l.atEnd() {
		b := l.peek()
		if isWhitespace(b) || isDelimiter(b) {
			break
		}
		l.advance()
		if b == '#' {
			// Hex-encoded character
			if l.pos+1 >= l.size {
				return Token{}, fmt.Errorf("incomplete hex escape in name at offset %d", offset)
			}
			hi, err := hexVal(l.advance())
			if err != nil {
				return Token{}, fmt.Errorf("invalid hex escape in name at offset %d: %w", offset, err)
			}
			lo, err := hexVal(l.advance())
			if err != nil {
				return Token{}, fmt.Errorf("invalid hex escape in name at offset %d: %w", offset, err)
			}
			if hi<<4|lo == 0 {
				// The spec forbids NUL in names (7.3.5): #00 has no valid
				// meaning and is a common smuggling vector.
				return Token{}, fmt.Errorf("name contains #00 (NUL) at offset %d", offset)
			}
			buf.WriteByte(hi<<4 | lo)
		} else {
			buf.WriteByte(b)
		}
	}

	return Token{
		Type:   TokenName,
		Value:  buf.Bytes(),
		Offset: offset,
	}, nil
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
	for !l.atEnd() && isRegular(l.peek()) {
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
