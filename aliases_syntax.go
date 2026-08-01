package pdf0

import "github.com/mgilbir/pdf0/syntax"

// The tokenizer, the recursive-descent parser and the serializer live in the
// syntax subpackage. They sit directly on the object model and on nothing else
// here, which is what let them follow it out of the root package.
//
// As with the object types in aliases.go these are aliases, so pdf0.Lexer and
// syntax.Lexer are one type and existing callers keep compiling. The canonical
// documentation — every method on Lexer, Parser and Serializer — is in the
// syntax package; the one-line summaries here match its first lines so the two
// cannot say different things.

type (
	// TokenType identifies the kind of a lexical token.
	TokenType = syntax.TokenType
	// Token is a single lexical token with its type, value and offset.
	Token = syntax.Token
	// Lexer scans PDF syntax into tokens.
	Lexer = syntax.Lexer
	// Parser builds objects from a token stream.
	Parser = syntax.Parser
	// Serializer writes objects back to PDF syntax.
	Serializer = syntax.Serializer
)

// Token types, one per lexical form in ISO 32000-2 7.2.
const (
	TokenBoolean    = syntax.TokenBoolean
	TokenInteger    = syntax.TokenInteger
	TokenReal       = syntax.TokenReal
	TokenString     = syntax.TokenString
	TokenName       = syntax.TokenName
	TokenArrayStart = syntax.TokenArrayStart
	TokenArrayEnd   = syntax.TokenArrayEnd
	TokenDictStart  = syntax.TokenDictStart
	TokenDictEnd    = syntax.TokenDictEnd
	TokenStream     = syntax.TokenStream
	TokenEndStream  = syntax.TokenEndStream
	TokenObj        = syntax.TokenObj
	TokenEndObj     = syntax.TokenEndObj
	TokenRef        = syntax.TokenRef
	TokenXref       = syntax.TokenXref
	TokenTrailer    = syntax.TokenTrailer
	TokenStartXref  = syntax.TokenStartXref
	TokenNull       = syntax.TokenNull
	TokenEOF        = syntax.TokenEOF
)

// NewLexer creates a lexer over the given PDF bytes.
func NewLexer(data []byte) *Lexer { return syntax.NewLexer(data) }

// NewLexerFromReaderAt creates a lexer that reads through r.
func NewLexerFromReaderAt(r interface {
	ReadAt(p []byte, off int64) (int, error)
}, size int64) (*Lexer, error) {
	return syntax.NewLexerFromReaderAt(r, size)
}

// NewParser creates a parser over the given PDF bytes.
func NewParser(data []byte) *Parser { return syntax.NewParser(data) }

// NewParserFromLexer creates a parser that draws tokens from an existing lexer.
func NewParserFromLexer(l *Lexer) *Parser { return syntax.NewParserFromLexer(l) }

// NewSerializer creates a serializer writing to w.
func NewSerializer(w interface{ Write(p []byte) (int, error) }) *Serializer {
	return syntax.NewSerializer(w)
}
