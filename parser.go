package pdf0

import (
	"bytes"
	"fmt"
	"strconv"
)

// maxParseDepth bounds recursion through nested arrays and dictionaries so that
// adversarial input (e.g. millions of nested '[') cannot exhaust the goroutine
// stack, which would abort the process uncatchably. Real PDFs nest only a
// handful of levels deep.
const maxParseDepth = 1000

// Parser builds PDF Object values from a token stream.
type Parser struct {
	lexer  *Lexer
	buf    []Token // look-ahead buffer
	bufLen int
	depth  int // current nesting depth (arrays/dictionaries)
}

// NewParser creates a new Parser for the given data.
func NewParser(data []byte) *Parser {
	return &Parser{
		lexer: NewLexer(data),
	}
}

// NewParserFromLexer creates a new Parser using the given lexer.
func NewParserFromLexer(lexer *Lexer) *Parser {
	return &Parser{
		lexer: lexer,
	}
}

// Lexer returns the underlying lexer (for position access, etc).
func (p *Parser) Lexer() *Lexer {
	return p.lexer
}

func (p *Parser) peekToken(n int) (Token, error) {
	for p.bufLen <= n {
		tok, err := p.lexer.NextToken()
		if err != nil {
			return Token{}, err
		}
		p.buf = append(p.buf, tok)
		p.bufLen++
	}
	return p.buf[n], nil
}

func (p *Parser) nextToken() (Token, error) {
	if p.bufLen > 0 {
		tok := p.buf[0]
		p.buf = p.buf[1:]
		p.bufLen--
		return tok, nil
	}
	return p.lexer.NextToken()
}

func (p *Parser) consumeToken() {
	if p.bufLen > 0 {
		p.buf = p.buf[1:]
		p.bufLen--
	}
}

// ParseObject parses any PDF object from the token stream.
func (p *Parser) ParseObject() (Object, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, fmt.Errorf("maximum nesting depth %d exceeded", maxParseDepth)
	}

	tok, err := p.peekToken(0)
	if err != nil {
		return nil, err
	}

	switch tok.Type {
	case TokenBoolean:
		p.consumeToken()
		return Boolean(string(tok.Value) == "true"), nil

	case TokenInteger:
		// Look ahead for "N G R" (indirect ref) or "N G obj" (indirect obj)
		return p.parseIntegerOrRef(tok)

	case TokenReal:
		p.consumeToken()
		val, err := strconv.ParseFloat(string(tok.Value), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid real %q at offset %d: %w", tok.Value, tok.Offset, err)
		}
		return Real(val), nil

	case TokenString:
		p.consumeToken()
		// Determine if hex based on original input.
		// The lexer already decoded the value; we need to check the raw source.
		// We check offset in the original data to determine if it was hex.
		isHex := false
		if tok.Offset >= 0 && tok.Offset < int64(len(p.lexer.data)) {
			isHex = p.lexer.data[tok.Offset] == '<'
		}
		return String{Value: tok.Value, IsHex: isHex}, nil

	case TokenName:
		p.consumeToken()
		return Name(tok.Value), nil

	case TokenArrayStart:
		return p.parseArray()

	case TokenDictStart:
		return p.parseDictOrStream()

	case TokenNull:
		p.consumeToken()
		return Null{}, nil

	case TokenEOF:
		return nil, fmt.Errorf("unexpected end of input")

	default:
		return nil, fmt.Errorf("unexpected token %v at offset %d", tok.Type, tok.Offset)
	}
}

// parseIntegerOrRef handles the ambiguity between integer, indirect ref, and indirect obj.
func (p *Parser) parseIntegerOrRef(tok Token) (Object, error) {
	// Try to look ahead for "N G R" or "N G obj"
	tok2, err := p.peekToken(1)
	if err != nil {
		// Can't look ahead, just return integer
		p.consumeToken()
		return p.toInteger(tok)
	}

	if tok2.Type == TokenInteger {
		tok3, err := p.peekToken(2)
		if err != nil {
			// Can't look ahead further, return first integer
			p.consumeToken()
			return p.toInteger(tok)
		}

		if tok3.Type == TokenRef {
			// N G R → indirect reference
			p.consumeToken() // consume first int
			p.consumeToken() // consume second int
			p.consumeToken() // consume R

			num, _ := strconv.Atoi(string(tok.Value))
			gen, _ := strconv.Atoi(string(tok2.Value))
			return IndirectRef{Number: num, Generation: gen}, nil
		}

		if tok3.Type == TokenObj {
			// N G obj → indirect object definition
			return p.ParseIndirectObject()
		}
	}

	// Just an integer
	p.consumeToken()
	return p.toInteger(tok)
}

func (p *Parser) toInteger(tok Token) (Integer, error) {
	val, err := strconv.ParseInt(string(tok.Value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q at offset %d: %w", tok.Value, tok.Offset, err)
	}
	return Integer(val), nil
}

// parseArray parses a PDF array: [ obj1 obj2 ... ]
func (p *Parser) parseArray() (Object, error) {
	p.consumeToken() // consume '['
	var items Array

	for {
		tok, err := p.peekToken(0)
		if err != nil {
			return nil, err
		}
		if tok.Type == TokenArrayEnd {
			p.consumeToken()
			return items, nil
		}
		if tok.Type == TokenEOF {
			return nil, fmt.Errorf("unterminated array starting at offset %d", tok.Offset)
		}

		obj, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("parsing array element: %w", err)
		}
		items = append(items, obj)
	}
}

// parseDictOrStream parses a dictionary, and if followed by 'stream', parses it as a Stream.
func (p *Parser) parseDictOrStream() (Object, error) {
	p.consumeToken() // consume '<<'
	dict := Dictionary{}

	for {
		tok, err := p.peekToken(0)
		if err != nil {
			return nil, err
		}
		if tok.Type == TokenDictEnd {
			p.consumeToken()
			break
		}
		if tok.Type == TokenEOF {
			return nil, fmt.Errorf("unterminated dictionary starting at offset %d", tok.Offset)
		}

		// Key must be a name
		if tok.Type != TokenName {
			return nil, fmt.Errorf("expected name key in dictionary, got %v at offset %d", tok.Type, tok.Offset)
		}
		p.consumeToken()
		key := Name(tok.Value)

		// Value
		val, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("parsing dictionary value for key %s: %w", key, err)
		}

		dict.Set(key, val)
	}

	// Check if followed by 'stream'
	tok, err := p.peekToken(0)
	if err != nil {
		return &dict, nil
	}
	if tok.Type == TokenStream {
		return p.parseStream(dict)
	}

	return &dict, nil
}

// parseStream parses stream data after the dictionary has been parsed.
func (p *Parser) parseStream(dict Dictionary) (Object, error) {
	p.consumeToken() // consume 'stream'

	// After 'stream' keyword, there must be a single EOL marker (\r\n or \n)
	// The lexer has already advanced past the keyword, so we need to check
	// for the EOL at the current lexer position.
	pos := p.lexer.pos
	if pos < p.lexer.size {
		if p.lexer.data[pos] == '\r' {
			pos++
			if pos < p.lexer.size && p.lexer.data[pos] == '\n' {
				pos++
			}
		} else if p.lexer.data[pos] == '\n' {
			pos++
		}
	}

	// Get the Length from the dictionary
	lengthObj := dict.Get("Length")
	var length int64

	switch l := lengthObj.(type) {
	case Integer:
		length = int64(l)
	case IndirectRef:
		// Length is an indirect reference - we can't resolve it here.
		// We'll need to find endstream and calculate the length.
		length = -1
	case nil:
		// No Length specified, try to find endstream
		length = -1
	default:
		return nil, fmt.Errorf("invalid Length type %T in stream dictionary", lengthObj)
	}

	var data []byte
	if length >= 0 {
		endPos := pos + length
		if endPos > p.lexer.size {
			return nil, fmt.Errorf("stream data extends beyond input (need %d bytes at offset %d)", length, pos)
		}
		data = make([]byte, length)
		copy(data, p.lexer.data[pos:endPos])
		p.lexer.pos = endPos
	} else {
		// Search for endstream keyword
		endstreamMarker := []byte("endstream")
		searchStart := pos
		idx := bytes.Index(p.lexer.data[searchStart:], endstreamMarker)
		if idx < 0 {
			return nil, fmt.Errorf("could not find endstream marker")
		}
		endPos := searchStart + int64(idx)
		// Remove trailing EOL before endstream
		dataEnd := endPos
		if dataEnd > pos && p.lexer.data[dataEnd-1] == '\n' {
			dataEnd--
			if dataEnd > pos && p.lexer.data[dataEnd-1] == '\r' {
				dataEnd--
			}
		} else if dataEnd > pos && p.lexer.data[dataEnd-1] == '\r' {
			dataEnd--
		}
		data = make([]byte, dataEnd-pos)
		copy(data, p.lexer.data[pos:dataEnd])
		p.lexer.pos = endPos
	}

	// Skip whitespace and expect 'endstream'
	p.lexer.skipWhitespaceAndComments()
	tok, err := p.nextToken()
	if err != nil {
		return nil, fmt.Errorf("expecting endstream: %w", err)
	}
	if tok.Type != TokenEndStream {
		return nil, fmt.Errorf("expected endstream, got %v at offset %d", tok.Type, tok.Offset)
	}

	return &Stream{Dict: dict, Data: data}, nil
}

// ParseIndirectObject parses an indirect object definition: N G obj ... endobj
func (p *Parser) ParseIndirectObject() (*IndirectObject, error) {
	numTok, err := p.nextToken()
	if err != nil {
		return nil, err
	}
	if numTok.Type != TokenInteger {
		return nil, fmt.Errorf("expected integer for object number, got %v at offset %d", numTok.Type, numTok.Offset)
	}
	num, _ := strconv.Atoi(string(numTok.Value))

	genTok, err := p.nextToken()
	if err != nil {
		return nil, err
	}
	if genTok.Type != TokenInteger {
		return nil, fmt.Errorf("expected integer for generation number, got %v at offset %d", genTok.Type, genTok.Offset)
	}
	gen, _ := strconv.Atoi(string(genTok.Value))

	objTok, err := p.nextToken()
	if err != nil {
		return nil, err
	}
	if objTok.Type != TokenObj {
		return nil, fmt.Errorf("expected 'obj' keyword, got %v at offset %d", objTok.Type, objTok.Offset)
	}

	value, err := p.ParseObject()
	if err != nil {
		return nil, fmt.Errorf("parsing indirect object %d %d: %w", num, gen, err)
	}

	endTok, err := p.nextToken()
	if err != nil {
		return nil, fmt.Errorf("expecting endobj for %d %d: %w", num, gen, err)
	}
	if endTok.Type != TokenEndObj {
		return nil, fmt.Errorf("expected 'endobj', got %v at offset %d", endTok.Type, endTok.Offset)
	}

	return &IndirectObject{
		Number:     num,
		Generation: gen,
		Value:      value,
	}, nil
}
