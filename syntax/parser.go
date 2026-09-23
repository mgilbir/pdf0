package syntax

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/mgilbir/pdf0/object"
	"math"
	"strconv"
)

// This file implements the recursive-descent parser that turns lexer tokens
// into Object values, including the three-token look-ahead that separates
// "N G R" (indirect reference) from "N G obj" (object definition) and from a
// plain integer. Stream-body extraction (ISO 32000-1 7.3.8.1) is the delicate
// part: a declared /Length is trusted only when endstream really follows it, an
// indirect /Length is resolved through the cross-reference table when the caller
// supplies a resolver, and only failing both does the code fall back to
// searching for the endstream keyword — a search that over-reads pathologically
// on binary data.
//
// Input is untrusted: recursion is depth-capped (maxParseDepth), and duplicate
// keys are resolved through Dictionary.Set, which is O(1) amortised at any size.

// MaxGeneration is the largest generation number an indirect object can carry.
// ISO 32000-2 7.5.4 gives the generation a five-digit field in a
// cross-reference table and reserves 65535 as the generation after which an
// object number is never reused.
const MaxGeneration = 65535

// MaxObjectNumber is the largest object number pdf0 reads or writes.
//
// ISO 32000-2 sets no upper bound (PDF 1.7's Annex C suggested 8,388,607 as an
// implementation limit), so this is pdf0's own: 2^31 − 1, the largest value
// every platform's int holds. It keeps "start + count" in a cross-reference
// subsection from wrapping (audit 2026-09-22 C128), and it is far above any
// real file's numbering.
const MaxObjectNumber = 1<<31 - 1

// maxParseDepth bounds recursion through nested arrays and dictionaries so that
// adversarial input (e.g. millions of nested '[') cannot exhaust the goroutine
// stack, which would abort the process uncatchably. Real PDFs nest only a
// handful of levels deep.
const maxParseDepth = 1000

// Parser builds PDF Object values from a token stream.
type Parser struct {
	lexer *Lexer
	// buf is a ring of the tokens lexed ahead of the parse: head is the next
	// one to consume and n how many are held. The grammar never looks more
	// than maxLookahead tokens ahead ("N G R"), so it is a fixed array and
	// peeking allocates nothing. An entry can be an error: a malformed token
	// found while looking ahead is kept in its place and reported by whichever
	// parse reaches it, not by the one that peeked.
	buf     [maxLookahead]lookahead
	head, n int
	depth   int   // current nesting depth (arrays/dictionaries)
	end     int64 // offset just past the last consumed token; see Offset
	// streamKW is the offset of the stream keyword of the last stream parsed,
	// and -1 before one is; streamLen is the /Length that stream declared, as
	// the parse read it (-1 when it did not come to a non-negative integer).
	// See StreamKeyword.
	streamKW, streamLen int64

	// ResolveLength, when set, resolves an indirect stream /Length reference to
	// its integer value (typically via the cross-reference table). It lets
	// parseStream honour a forward-referenced /Length instead of falling back to
	// the endstream search, which can catastrophically over-read (see
	// parseStream). It returns false when the reference cannot be resolved to a
	// plain non-negative integer, in which case the search fallback is used.
	ResolveLength func(ref object.IndirectRef) (int64, bool)

	// Budget, when non-nil, meters the memory the parse materialises. Every
	// object ParseObject builds is charged MaterialCost for it (plus the bytes
	// of a string's or name's value) against *Budget, and once *Budget is
	// negative the parse stops with ErrBudget. It is for input whose size does
	// not bound its cost: an object stream is a few hundred kilobytes of
	// compressed "1 0 R " that becomes tens of millions of objects, each
	// several times larger in memory than in the file (audit 2026-09-22 C9).
	// Metering the decoded bytes instead let one container overshoot by five
	// times what it was charged.
	Budget *int64
}

// MaterialCost is what the parser charges Budget for one object: a
// conservative estimate, in bytes, of what one parsed value costs in memory.
// It covers the value's slot in its parent (an interface: 16 bytes), the
// boxed value itself (16 to 32 bytes: an IndirectRef, a String header), and
// the parent's growth slack (a slice grown by doubling holds up to twice what
// it uses, and while it grows the old array and the new one are both live).
// Measured on an array of fifteen million "1 0 R" references, the live heap
// after the parse is 32.5 bytes per element; 64 is twice that, which keeps the
// estimate above the cost through the growth.
const MaterialCost = 64

// ErrBudget is returned when a parse would materialise more than its Budget.
var ErrBudget = errors.New("parse budget exhausted: the input materialises more objects than the budget allows")

// charge debits the budget for one value of n payload bytes, and reports
// whether the parse may go on.
func (p *Parser) charge(n int) bool {
	if p.Budget == nil {
		return true
	}
	*p.Budget -= MaterialCost + int64(n)
	return *p.Budget >= 0
}

// maxLookahead is the most tokens the parser holds before consuming one.
const maxLookahead = 3

// lookahead is one look-ahead slot: a token, or the error lexing it gave.
type lookahead struct {
	tok Token
	err error
}

// NewParser creates a new Parser for the given data.
func NewParser(data []byte) *Parser {
	return NewParserFromLexer(NewLexer(data))
}

// NewParserFromLexer creates a new Parser using the given lexer, starting at
// the lexer's current position.
func NewParserFromLexer(lexer *Lexer) *Parser {
	return &Parser{
		lexer:     lexer,
		end:       lexer.pos,
		streamKW:  -1,
		streamLen: -1,
	}
}

// Lexer returns the underlying lexer. Its Position is where lexing has reached,
// which can be past tokens the parser has looked at but not consumed; Offset is
// the position of the parse. To move the parser, use SetOffset: moving the lexer
// directly leaves the look-ahead describing the old position.
func (p *Parser) Lexer() *Lexer {
	return p.lexer
}

// Offset returns the byte offset just past the last token the parser consumed:
// after ParseObject, the end of the object it returned. Look-ahead is not
// counted, so parsing "5" from "5 /Next 7" leaves Offset at 1.
func (p *Parser) Offset() int64 {
	return p.end
}

// StreamKeyword returns the offset of the stream keyword of the last stream
// the parser read, and false when it has read none. After ParseIndirectObject
// returns a stream, it is where that stream's data begins to be introduced:
// the keyword the parser actually took, which the byte-level conformance
// rules measure from rather than searching for the word again (a search
// cannot tell the keyword from the same letters in a string, and must be
// told that ">>stream" is a keyword too).
func (p *Parser) StreamKeyword() (int64, bool) {
	return p.streamKW, p.streamKW >= 0
}

// StreamLength returns the /Length the last stream the parser read declares,
// as the parse read it: a direct integer, or an indirect one ResolveLength
// resolved. It is false when the parser has read no stream, or the value did
// not come to a non-negative integer — absent, of another type, or a
// reference ResolveLength could not follow — in which case a caller that
// needs it resolves it itself. It is the declared value, whatever the data
// turned out to hold.
func (p *Parser) StreamLength() (int64, bool) {
	return p.streamLen, p.streamKW >= 0 && p.streamLen >= 0
}

// SetOffset moves the parser to offset, discarding any look-ahead.
func (p *Parser) SetOffset(offset int64) {
	p.head, p.n = 0, 0
	p.lexer.SetPosition(offset)
	p.end = offset
}

// peekToken returns the token n places ahead without consuming it. A lexing
// error at or before that place is returned instead; it stays buffered, so the
// parse that reaches it reports it.
func (p *Parser) peekToken(n int) (Token, error) {
	if n >= maxLookahead {
		return Token{}, fmt.Errorf("internal error: look-ahead of %d tokens exceeds %d", n+1, maxLookahead)
	}
	for p.n <= n {
		if p.n > 0 {
			if last := &p.buf[(p.head+p.n-1)%maxLookahead]; last.err != nil {
				return Token{}, last.err // nothing can be read past an error
			}
		}
		tok, err := p.lexer.NextToken()
		p.buf[(p.head+p.n)%maxLookahead] = lookahead{tok, err}
		p.n++
	}
	e := &p.buf[(p.head+n)%maxLookahead]
	return e.tok, e.err
}

// pop removes the next buffered token; the caller has checked there is one.
func (p *Parser) pop() lookahead {
	e := p.buf[p.head]
	p.buf[p.head] = lookahead{} // drop the reference to the token's bytes
	p.head = (p.head + 1) % maxLookahead
	p.n--
	return e
}

func (p *Parser) nextToken() (Token, error) {
	if p.n > 0 {
		if e := p.buf[p.head]; e.err != nil {
			return Token{}, e.err
		}
		e := p.pop()
		p.end = e.tok.End
		return e.tok, nil
	}
	tok, err := p.lexer.NextToken()
	if err == nil {
		p.end = tok.End
	}
	return tok, err
}

// consumeToken consumes the token a successful peekToken(0) returned.
func (p *Parser) consumeToken() {
	if p.n > 0 {
		p.end = p.pop().tok.End
	}
}

// ParseObject parses any PDF object from the token stream.
func (p *Parser) ParseObject() (object.Object, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, fmt.Errorf("maximum nesting depth %d exceeded", maxParseDepth)
	}

	tok, err := p.peekToken(0)
	if err != nil {
		return nil, err
	}
	if p.Budget != nil {
		n := 0
		if tok.Type == TokenString || tok.Type == TokenName {
			n = len(tok.Value)
		}
		if !p.charge(n) {
			return nil, ErrBudget
		}
	}

	switch tok.Type {
	case TokenBoolean:
		p.consumeToken()
		return object.Boolean(string(tok.Value) == "true"), nil

	case TokenInteger:
		// Look ahead for "N G R" (indirect ref) or "N G obj" (indirect obj)
		return p.parseIntegerOrRef(tok)

	case TokenReal:
		p.consumeToken()
		return p.toReal(tok)

	case TokenString:
		p.consumeToken()
		// Determine if hex based on original input.
		// The lexer already decoded the value; we need to check the raw source.
		// We check offset in the original data to determine if it was hex.
		isHex := false
		if tok.Offset >= 0 && tok.Offset < int64(len(p.lexer.data)) {
			isHex = p.lexer.data[tok.Offset] == '<'
		}
		return object.String{Value: tok.Value, IsHex: isHex}, nil

	case TokenName:
		p.consumeToken()
		return object.Name(tok.Value), nil

	case TokenArrayStart:
		return p.parseArray()

	case TokenDictStart:
		return p.parseDictOrStream()

	case TokenNull:
		p.consumeToken()
		return object.Null{}, nil

	case TokenEOF:
		return nil, fmt.Errorf("unexpected end of input")

	default:
		return nil, fmt.Errorf("unexpected token %v at offset %d", tok.Type, tok.Offset)
	}
}

// parseIntegerOrRef handles the ambiguity between integer, indirect ref, and indirect obj.
//
// The look-ahead only decides which of the three this is. A malformed token
// after the integer means "not a reference", so the integer is returned and
// the error stays buffered for the parse that reaches it — exactly as it would
// be after any other object. Failing the integer instead would drop a valid
// object because its neighbour is broken (an object-stream member followed by
// a malformed one, audit C116). A lexer error is never swallowed: clean end of
// input is TokenEOF, not an error, and the buffered error is still reported.
func (p *Parser) parseIntegerOrRef(tok Token) (object.Object, error) {
	// Try to look ahead for "N G R" or "N G obj"
	if tok2, err := p.peekToken(1); err == nil && tok2.Type == TokenInteger {
		tok3, err := p.peekToken(2)
		if err == nil && tok3.Type == TokenRef {
			// N G R → indirect reference
			p.consumeToken() // consume first int
			p.consumeToken() // consume second int
			p.consumeToken() // consume R

			num, err := p.toObjectNumber(tok, math.MaxInt, "object")
			if err != nil {
				return nil, err
			}
			gen, err := p.toObjectNumber(tok2, math.MaxInt, "generation")
			if err != nil {
				return nil, err
			}
			return object.IndirectRef{Number: num, Generation: gen}, nil
		}

		if err == nil && tok3.Type == TokenObj {
			// "N G obj" is an indirect object DEFINITION, valid only at the top
			// level, where ParseIndirectObject consumes it. Reaching it here means
			// it appeared as a nested value — an array element or dictionary value —
			// which is malformed; reject it rather than build a surprising
			// *IndirectObject into the object graph that Resolve, the serializer,
			// and the validators do not expect for a nested value (audit C31).
			return nil, fmt.Errorf("unexpected indirect object definition at offset %d (nested 'N G obj')", tok3.Offset)
		}
	}

	// Just an integer
	p.consumeToken()
	return p.toInteger(tok)
}

// toInteger converts an integer token. ISO 32000-2 Annex C treats the range of
// integers as an implementation limit, not a syntax rule, so an integer too
// large for int64 is read as the Real nearest to it (which is how its value
// survives; a conforming file never contains one) rather than failing the
// object around it (audit C117).
func (p *Parser) toInteger(tok Token) (object.Object, error) {
	val, err := strconv.ParseInt(string(tok.Value), 10, 64)
	if err == nil {
		return object.Integer(val), nil
	}
	if errors.Is(err, strconv.ErrRange) {
		return p.toReal(tok)
	}
	return nil, fmt.Errorf("invalid integer %q at offset %d: %w", tok.Value, tok.Offset, err)
}

// toReal converts a real (or out-of-range integer) token. A value beyond the
// range of float64 is clamped to the largest finite value of its sign — an
// infinity is not a PDF number and could not be written back — and one below
// its precision rounds toward zero, as strconv does (audit C117).
func (p *Parser) toReal(tok Token) (object.Object, error) {
	val, err := strconv.ParseFloat(string(tok.Value), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, fmt.Errorf("invalid real %q at offset %d: %w", tok.Value, tok.Offset, err)
	}
	switch {
	case math.IsInf(val, 1):
		val = math.MaxFloat64
	case math.IsInf(val, -1):
		val = -math.MaxFloat64
	}
	return object.Real(val), nil
}

// toObjectNumber parses an object or generation number, rejecting values that
// overflow, are negative, or exceed max. Overflow was previously swallowed
// (strconv.Atoi error dropped), yielding garbage references like
// Number=MaxInt64.
//
// A definition ("N G obj") passes MaxObjectNumber and MaxGeneration: a
// generation above 65535 (ISO 32000-2 7.5.4: the field is five digits and
// 65535 is the largest value it holds) cannot be written back in a
// cross-reference table, and the scan that rebuilds a damaged table rejects
// the same header, so a definition carrying one is refused here rather than
// accepted by one path and dropped by the other (audit 2026-09-22 C125). A
// reference ("N G R") passes math.MaxInt: a reference to an object that cannot
// exist is harmless, and refusing it would fail the whole containing object.
func (p *Parser) toObjectNumber(tok Token, max int, what string) (int, error) {
	val, err := strconv.Atoi(string(tok.Value))
	if err != nil {
		return 0, fmt.Errorf("invalid %s number %q at offset %d: %w", what, tok.Value, tok.Offset, err)
	}
	if val < 0 {
		return 0, fmt.Errorf("negative %s number %d at offset %d", what, val, tok.Offset)
	}
	if val > max {
		return 0, fmt.Errorf("%s number %d at offset %d exceeds %d", what, val, tok.Offset, max)
	}
	return val, nil
}

// IntegerObjectValue reads an indirect object of the exact shape
// "N G obj <integer>" and returns its non-negative integer value. It is used to
// resolve an indirect stream /Length without parsing an arbitrarily large
// value: it reads only four tokens and never recurses, so an adversarial
// /Length pointing at a huge composite object costs nothing. It returns false
// unless the four tokens are exactly integer, integer, 'obj', integer with a
// non-negative value.
func (p *Parser) IntegerObjectValue() (int64, bool) {
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
func (p *Parser) parseArray() (object.Object, error) {
	p.consumeToken() // consume '['
	var items object.Array

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
func (p *Parser) parseDictOrStream() (object.Object, error) {
	p.consumeToken() // consume '<<'
	dict := &object.Dictionary{}

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
		key := object.Name(tok.Value)
		// A key is an entry of its own: the key, the value slot and the
		// dictionary's index entry for it.
		if p.Budget != nil && !p.charge(len(tok.Value)) {
			return nil, ErrBudget
		}

		// Value
		val, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("parsing dictionary value for key %s: %w", key, err)
		}

		// A duplicated key is undefined by the spec; keep the last value at the
		// key's first position, which is Dictionary.Set's rule and common reader
		// behaviour. Set is O(1) amortised, so a crafted dictionary with
		// hundreds of thousands of keys still parses in linear time.
		dict.Set(key, val)
	}

	// Check if followed by 'stream'. As in parseIntegerOrRef, a malformed
	// token here only means "not a stream": the dictionary is complete, and
	// the error stays buffered for the parse that reaches it (audit C116).
	if tok, err := p.peekToken(0); err == nil && tok.Type == TokenStream {
		return p.parseStream(dict, tok)
	}

	return dict, nil
}

// parseStream parses stream data after the dictionary has been parsed.
func (p *Parser) parseStream(dict *object.Dictionary, streamTok Token) (object.Object, error) {
	p.consumeToken() // consume 'stream'
	p.streamKW = streamTok.Offset
	// The data is read from the bytes after the keyword, not through the
	// lexer, so nothing may be buffered past it. Look-ahead never reaches past
	// "stream" (it follows ">>", which ends any look-ahead), but a stale token
	// here would be data misread as syntax, so drop it rather than trust that.
	p.head, p.n = 0, 0
	p.lexer.pos = streamTok.End

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
	case object.Integer:
		length = int64(l)
	case object.IndirectRef:
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
		if p.ResolveLength != nil {
			if n, ok := p.ResolveLength(l); ok {
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

	p.streamLen = length
	var data []byte
	// A declared Length is authoritative only when the endstream keyword
	// actually follows the indicated data (allowing one EOL). If it does not
	// — an incorrect Length, which PDF/A forbids but which a conforming
	// reader must recover from (ISO 32000-1 7.3.8.1, NOTE 2) — fall back to
	// locating endstream by search. The resulting Stream.Data then reflects
	// the true byte count, letting the validator flag the mismatch.
	kwAt := int64(-1) // offset of the endstream keyword
	if length >= 0 {
		kwAt = endstreamAfterWhitespace(p.lexer.data, pos+length)
	}
	if kwAt >= 0 {
		endPos := pos + length
		data = make([]byte, length)
		copy(data, p.lexer.data[pos:endPos])
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
		endPos := findEndstream(p.lexer.data, pos)
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
		kwAt = endPos
	}

	// Consume exactly the keyword. It is located by the byte search above, not
	// lexed, because the lexer reads a keyword to the next delimiter: in
	// "endstreamendobj", which real writers emit, it would see one unknown
	// keyword (audit C120).
	p.lexer.pos = kwAt + int64(len(endstreamKeyword))
	p.end = p.lexer.pos
	return object.NewStream(dict, data), nil
}

// FindDelimitedKeyword returns the offset of the first occurrence of keyword
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
func FindDelimitedKeyword(data []byte, start int64, keyword string, requireLeadingWS bool) int64 {
	marker := []byte(keyword)
	for from := start; from < int64(len(data)); {
		idx := bytes.Index(data[from:], marker)
		if idx < 0 {
			return -1
		}
		at := from + int64(idx)
		end := at + int64(len(marker))
		beforeOK := !requireLeadingWS || at == start || IsWhitespace(data[at-1])
		afterOK := end >= int64(len(data)) || !IsRegular(data[end])
		if beforeOK && afterOK {
			return at
		}
		from = at + 1
	}
	return -1
}

// ParseIndirectObject parses an indirect object definition: N G obj ... endobj
func (p *Parser) ParseIndirectObject() (*object.IndirectObject, error) {
	numTok, err := p.nextToken()
	if err != nil {
		return nil, err
	}
	if numTok.Type != TokenInteger {
		return nil, fmt.Errorf("expected integer for object number, got %v at offset %d", numTok.Type, numTok.Offset)
	}
	num, err := p.toObjectNumber(numTok, MaxObjectNumber, "object")
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
	gen, err := p.toObjectNumber(genTok, MaxGeneration, "generation")
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

	return &object.IndirectObject{
		Number:     num,
		Generation: gen,
		Value:      value,
	}, nil
}

// EndstreamFollowsAt reports whether the endstream keyword appears at offset
// off, allowing whitespace before it. Used to decide whether a declared stream
// Length can be trusted.
func EndstreamFollowsAt(data []byte, off int64) bool {
	return endstreamAfterWhitespace(data, off) >= 0
}

const endstreamKeyword = "endstream"

// endstreamAfterWhitespace returns the offset of an endstream keyword at off
// or after whitespace following off, or -1. Any amount of whitespace is
// tolerated between the declared data end and the keyword (a correct stream
// has exactly one EOL, but some writers add more); a genuinely wrong Length
// lands on non-whitespace, non-keyword bytes and is rejected.
func endstreamAfterWhitespace(data []byte, off int64) int64 {
	n := int64(len(data))
	if off < 0 || off > n {
		return -1
	}
	i := off
	for i < n && IsWhitespace(data[i]) {
		i++
	}
	if isEndstreamAt(data, i) {
		return i
	}
	return -1
}

// isEndstreamAt reports whether the endstream keyword stands at off as a
// token: followed by the end of the input, a non-regular byte, or directly by
// the endobj keyword ("endstreamendobj" is common enough in real files to
// read; audit C120). "endstreamX" is not the keyword.
func isEndstreamAt(data []byte, off int64) bool {
	end := off + int64(len(endstreamKeyword))
	if off < 0 || end > int64(len(data)) || string(data[off:end]) != endstreamKeyword {
		return false
	}
	return end == int64(len(data)) || !IsRegular(data[end]) || bytes.HasPrefix(data[end:], []byte("endobj"))
}

// findEndstream returns the offset of the first endstream keyword at or after
// start that isEndstreamAt accepts, or -1. Like FindDelimitedKeyword with no
// leading-whitespace requirement — a stream's raw data may end in any byte —
// and it also accepts endobj directly after the keyword.
func findEndstream(data []byte, start int64) int64 {
	marker := []byte(endstreamKeyword)
	for from := start; from >= 0 && from < int64(len(data)); {
		idx := bytes.Index(data[from:], marker)
		if idx < 0 {
			return -1
		}
		at := from + int64(idx)
		if isEndstreamAt(data, at) {
			return at
		}
		from = at + 1
	}
	return -1
}
