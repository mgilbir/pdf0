package pdf0

import (
	"github.com/mgilbir/pdf0/syntax"
)

// NewLexer creates a lexer over the given PDF bytes.
func NewLexer(data []byte) *syntax.Lexer { return syntax.NewLexer(data) }

// NewLexerFromReaderAt creates a lexer that reads through r.
func NewLexerFromReaderAt(r interface {
	ReadAt(p []byte, off int64) (int, error)
}, size int64) (*syntax.Lexer, error) {
	return syntax.NewLexerFromReaderAt(r, size)
}

// NewParser creates a parser over the given PDF bytes.
func NewParser(data []byte) *syntax.Parser { return syntax.NewParser(data) }

// NewParserFromLexer creates a parser that draws tokens from an existing lexer.
func NewParserFromLexer(l *syntax.Lexer) *syntax.Parser { return syntax.NewParserFromLexer(l) }

// NewSerializer creates a serializer writing to w.
func NewSerializer(w interface{ Write(p []byte) (int, error) }) *syntax.Serializer {
	return syntax.NewSerializer(w)
}
