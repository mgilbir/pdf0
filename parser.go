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

	// resolveLength, when set, resolves an indirect stream /Length reference to
	// its integer value (typically via the cross-reference table). It lets
	// parseStream honour a forward-referenced /Length instead of falling back to
	// the endstream search, which can catastrophically over-read (see
	// parseStream). It returns false when the reference cannot be resolved to a
	// plain non-negative integer, in which case the search fallback is used.
	resolveLength func(ref IndirectRef) (int64, bool)
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
// Look-ahead failures are real lexer errors and are propagated: the lexer only
// returns an error for malformed input (clean end of input is TokenEOF), and
// swallowing it here would silently drop the diagnostic while the lexer has
// already advanced past the bad bytes.
func (p *Parser) parseIntegerOrRef(tok Token) (Object, error) {
	// Try to look ahead for "N G R" or "N G obj"
	tok2, err := p.peekToken(1)
	if err != nil {
		return nil, err
	}

	if tok2.Type == TokenInteger {
		tok3, err := p.peekToken(2)
		if err != nil {
			return nil, err
		}

		if tok3.Type == TokenRef {
			// N G R → indirect reference
			p.consumeToken() // consume first int
			p.consumeToken() // consume second int
			p.consumeToken() // consume R

			num, err := p.toObjectNumber(tok)
			if err != nil {
				return nil, err
			}
			gen, err := p.toObjectNumber(tok2)
			if err != nil {
				return nil, err
			}
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

// toObjectNumber parses an object or generation number, rejecting values that
// overflow or are negative. Overflow was previously swallowed (strconv.Atoi
// error dropped), yielding garbage references like Number=MaxInt64.
func (p *Parser) toObjectNumber(tok Token) (int, error) {
	val, err := strconv.Atoi(string(tok.Value))
	if err != nil {
		return 0, fmt.Errorf("invalid object number %q at offset %d: %w", tok.Value, tok.Offset, err)
	}
	if val < 0 {
		return 0, fmt.Errorf("negative object number %d at offset %d", val, tok.Offset)
	}
	return val, nil
}

// integerObjectValue reads an indirect object of the exact shape
// "N G obj <integer>" and returns its non-negative integer value. It is used to
// resolve an indirect stream /Length without parsing an arbitrarily large
// value: it reads only four tokens and never recurses, so an adversarial
// /Length pointing at a huge composite object costs nothing. It returns false
// unless the four tokens are exactly integer, integer, 'obj', integer with a
// non-negative value.
func (p *Parser) integerObjectValue() (int64, bool) {
	var toks [4]Token
	for i := range toks {
		t, err := p.nextToken()
		if err != nil {
			return 0, false
		}
		toks[i] = t
	}
	if toks[0].Type != TokenInteger || toks[1].Type != TokenInteger ||
		toks[2].Type != TokenObj || toks[3].Type != TokenInteger {
		return 0, false
	}
	n, err := strconv.ParseInt(string(toks[3].Value), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
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

// dictIndexThreshold is the key count past which parseDictOrStream stops
// deduplicating keys by a linear scan (Dictionary.Set) and builds a name→index
// map instead. Below it the linear scan is cheaper than a map allocation;
// above it the O(n²) scan is what makes a dictionary with hundreds of thousands
// of keys (from a crafted object stream) take minutes to parse.
const dictIndexThreshold = 64

// parseDictOrStream parses a dictionary, and if followed by 'stream', parses it as a Stream.
func (p *Parser) parseDictOrStream() (Object, error) {
	p.consumeToken() // consume '<<'
	dict := Dictionary{}
	// index maps a key to its slot in dict once the dictionary grows past
	// dictIndexThreshold, so duplicate-key handling stays O(1) instead of a
	// linear scan per key. It is nil (and unused) for small dictionaries.
	var index map[Name]int

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

		// Duplicate keys are legal to tolerate but undefined by the spec; keep
		// the last value at the key's first position, matching Dictionary.Set
		// and common reader behavior.
		if index != nil {
			if i, ok := index[key]; ok {
				dict.Values[i] = val
			} else {
				index[key] = len(dict.Keys)
				dict.Keys = append(dict.Keys, key)
				dict.Values = append(dict.Values, val)
			}
		} else {
			dict.Set(key, val)
			if len(dict.Keys) >= dictIndexThreshold {
				index = make(map[Name]int, len(dict.Keys)*2)
				for i, k := range dict.Keys {
					index[k] = i
				}
			}
		}
	}

	// Check if followed by 'stream'. A look-ahead failure is a real lexer
	// error (clean end of input is TokenEOF, not an error).
	tok, err := p.peekToken(0)
	if err != nil {
		return nil, err
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
	// Some producers emit spurious spaces/tabs between the keyword and the EOL
	// ("stream \r\n") — non-conformant (ISO 32000-1 7.3.8.1) but common. Skip
	// them so the stream data does not absorb the whitespace: absorbing it both
	// corrupts the data (e.g. bytes before a FlateDecode header, breaking the
	// filter) and shifts the byte count so the declared /Length no longer
	// matches, forcing an endstream search and an unstable round-trip. Only skip
	// when an EOL actually follows, so data that legitimately begins with a
	// space is never consumed.
	if q := pos; q < p.lexer.size {
		for q < p.lexer.size && (p.lexer.data[q] == ' ' || p.lexer.data[q] == '\t') {
			q++
		}
		if q < p.lexer.size && (p.lexer.data[q] == '\r' || p.lexer.data[q] == '\n') {
			pos = q
		}
	}
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
		// Length is an indirect reference to an integer object defined
		// elsewhere in the file (often a forward reference). Resolve it through
		// the cross-reference table when a resolver is available; this is what
		// conforming readers do. Resolving avoids a pathological over-read:
		// without the true length the code falls back to searching for
		// endstream, and that search skips any endstream keyword not preceded by
		// whitespace — but binary stream data may end in any byte, so a
		// legitimate endstream is skipped and the search slurps forward to a
		// distant one. Across many such streams that is O(n^2) in the file size
		// (a 10 MB file was observed to expand to 8 GB of stream data on read).
		// If resolution fails, or the resolved length does not actually place
		// endstream where expected, the search fallback below still runs.
		length = -1
		if p.resolveLength != nil {
			if n, ok := p.resolveLength(l); ok {
				length = n
			}
		}
	case nil:
		// No Length specified, try to find endstream
		length = -1
	default:
		// A wrong-typed /Length (e.g. a Real) is malformed but recoverable:
		// fall back to locating endstream by search rather than aborting the
		// whole read (ISO 32000-1 7.3.8.1, NOTE 2).
		length = -1
	}

	var data []byte
	// A declared Length is authoritative only when the endstream keyword
	// actually follows the indicated data (allowing one EOL). If it does not
	// — an incorrect Length, which PDF/A forbids but which a conforming
	// reader must recover from (ISO 32000-1 7.3.8.1, NOTE 2) — fall back to
	// locating endstream by search. The resulting Stream.Data then reflects
	// the true byte count, letting the validator flag the mismatch.
	if length >= 0 && endstreamFollowsAt(p.lexer.data, pos+length) {
		endPos := pos + length
		data = make([]byte, length)
		copy(data, p.lexer.data[pos:endPos])
		p.lexer.pos = endPos
	} else {
		// Search for the endstream keyword. It must stand alone as a token —
		// followed by a non-regular character or end of input — but must NOT be
		// required to be preceded by whitespace: a stream's raw data may end in
		// any byte (binary FlateDecode/DCTDecode), and ISO 32000-1 7.3.8.1 only
		// recommends (does not require) an EOL before endstream. Requiring a
		// leading whitespace made the search step over a legitimate endstream
		// that follows binary data and slurp forward to a distant one — an
		// O(n^2) over-read across many streams (a 55 MB file with streams
		// sharing one wrong /Length expanded to 6.3 GB of stream data on read).
		endPos := findDelimitedKeyword(p.lexer.data, pos, "endstream", false)
		if endPos < 0 {
			return nil, fmt.Errorf("could not find endstream marker")
		}
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

// findDelimitedKeyword returns the offset of the first occurrence of keyword
// at or after start that stands alone as a token: followed by a non-regular
// character or end of input, and — when requireLeadingWS is set — preceded by
// whitespace (or at start). Returns -1 if none exists.
//
// The endstream search passes requireLeadingWS=false: a stream's raw data may
// end in any byte, and the spec only recommends an EOL before endstream, so a
// real endstream is often preceded by a non-whitespace byte; requiring leading
// whitespace there made the search skip it and over-read (see parseStream).
// Callers scanning file structure for the "stream"/"endobj" keywords pass true:
// those keywords are whitespace-delimited, and it is what lets the "stream"
// search avoid matching the trailing "stream" inside "endstream".
func findDelimitedKeyword(data []byte, start int64, keyword string, requireLeadingWS bool) int64 {
	marker := []byte(keyword)
	for from := start; from < int64(len(data)); {
		idx := bytes.Index(data[from:], marker)
		if idx < 0 {
			return -1
		}
		at := from + int64(idx)
		end := at + int64(len(marker))
		beforeOK := !requireLeadingWS || at == start || isWhitespace(data[at-1])
		afterOK := end >= int64(len(data)) || !isRegular(data[end])
		if beforeOK && afterOK {
			return at
		}
		from = at + 1
	}
	return -1
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
	num, err := p.toObjectNumber(numTok)
	if err != nil {
		return nil, err
	}

	genTok, err := p.nextToken()
	if err != nil {
		return nil, err
	}
	if genTok.Type != TokenInteger {
		return nil, fmt.Errorf("expected integer for generation number, got %v at offset %d", genTok.Type, genTok.Offset)
	}
	gen, err := p.toObjectNumber(genTok)
	if err != nil {
		return nil, err
	}

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

// endstreamFollowsAt reports whether the endstream keyword appears at offset
// off, allowing a single optional EOL marker of whitespace before it. Used to
// decide whether a declared stream Length can be trusted.
func endstreamFollowsAt(data []byte, off int64) bool {
	n := int64(len(data))
	if off < 0 || off > n {
		return false
	}
	i := off
	// Tolerate any trailing whitespace between the declared data end and the
	// keyword (a correct stream has exactly one EOL, but some writers add
	// more); a genuinely wrong Length lands on non-whitespace, non-keyword
	// bytes and is rejected.
	for i < n && isWhitespace(data[i]) {
		i++
	}
	marker := []byte("endstream")
	if i+int64(len(marker)) > n {
		return false
	}
	return bytes.Equal(data[i:i+int64(len(marker))], marker)
}
