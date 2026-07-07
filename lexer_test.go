package pdf0

import (
	"testing"
)

func TestLexerWhitespaceAndComments(t *testing.T) {
	l := NewLexer([]byte("  \t\n\r  % this is a comment\n  true"))
	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenBoolean || string(tok.Value) != "true" {
		t.Errorf("expected Boolean true, got %v", tok)
	}
}

func TestLexerBoolean(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"true", "true"},
		{"false", "false"},
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != TokenBoolean || string(tok.Value) != tt.want {
			t.Errorf("input %q: expected Boolean %q, got %v", tt.input, tt.want, tok)
		}
	}
}

func TestLexerInteger(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"123", "123"},
		{"-98", "-98"},
		{"+17", "+17"},
		{"0", "0"},
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != TokenInteger || string(tok.Value) != tt.want {
			t.Errorf("input %q: expected Integer %q, got %v", tt.input, tt.want, tok)
		}
	}
}

func TestLexerReal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3.14", "3.14"},
		{"-.002", "-.002"},
		{"+.5", "+.5"},
		{"0.0", "0.0"},
		{"34.", "34."},
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != TokenReal || string(tok.Value) != tt.want {
			t.Errorf("input %q: expected Real %q, got %v", tt.input, tt.want, tok)
		}
	}
}

func TestLexerLiteralString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"(hello)", "hello"},
		{"(hello world)", "hello world"},
		{"(nested (parens) ok)", "nested (parens) ok"},
		{"()", ""},
		{"(\\n)", "\n"},
		{"(\\r)", "\r"},
		{"(\\t)", "\t"},
		{"(\\b)", "\b"},
		{"(\\f)", "\f"},
		{"(\\()", "("},
		{"(\\))", ")"},
		{"(\\\\)", "\\"},
		{"(\\053)", "+"},                     // octal escape
		{"(\\53)", "+"},                      // octal 2 digits
		{"(\\0533)", "+3"},                   // octal 3 digits + non-octal
		{"(line1\rline2)", "line1\nline2"},   // \r → \n
		{"(line1\r\nline2)", "line1\nline2"}, // \r\n → \n
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != TokenString || string(tok.Value) != tt.want {
			t.Errorf("input %q: expected String %q, got %v (value=%q)", tt.input, tt.want, tok, tok.Value)
		}
	}
}

func TestLexerLineContinuation(t *testing.T) {
	// Backslash followed by newline is a line continuation (ignored)
	input := "(hello\\\nworld)"
	l := NewLexer([]byte(input))
	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenString || string(tok.Value) != "helloworld" {
		t.Errorf("expected String 'helloworld', got %v (value=%q)", tok, tok.Value)
	}

	// Backslash followed by \r\n
	input2 := "(hello\\\r\nworld)"
	l2 := NewLexer([]byte(input2))
	tok2, err := l2.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok2.Type != TokenString || string(tok2.Value) != "helloworld" {
		t.Errorf("expected String 'helloworld', got %v (value=%q)", tok2, tok2.Value)
	}
}

func TestLexerHexString(t *testing.T) {
	tests := []struct {
		input string
		want  []byte
	}{
		{"<48656C6C6F>", []byte("Hello")},
		{"<48 65 6C 6C 6F>", []byte("Hello")},
		{"<>", []byte{}},
		{"<4>", []byte{0x40}},                 // odd digits: pad with 0
		{"<901FA>", []byte{0x90, 0x1F, 0xA0}}, // odd digits
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != TokenString {
			t.Errorf("input %q: expected String, got %v", tt.input, tok.Type)
			continue
		}
		if string(tok.Value) != string(tt.want) {
			t.Errorf("input %q: expected %v, got %v", tt.input, tt.want, tok.Value)
		}
	}
}

func TestLexerName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Type", "Type"},
		{"/Name1", "Name1"},
		{"/ASomewhatLongerName", "ASomewhatLongerName"},
		{"/A;Name_With-Various***Characters?", "A;Name_With-Various***Characters?"},
		{"/.notdef", ".notdef"},
		{"/", ""},
		{"/Adobe#20Green", "Adobe Green"}, // hex escape
		{"/PANTONE#205757#20EC", "PANTONE 5757 EC"},
		{"/paired#28#29parentheses", "paired()parentheses"},
		{"/The_Key_of_F#23_Minor", "The_Key_of_F#_Minor"},
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != TokenName || string(tok.Value) != tt.want {
			t.Errorf("input %q: expected Name %q, got %v (value=%q)", tt.input, tt.want, tok, tok.Value)
		}
	}
}

func TestLexerDelimiters(t *testing.T) {
	input := "[ ] << >>"
	l := NewLexer([]byte(input))

	expected := []TokenType{TokenArrayStart, TokenArrayEnd, TokenDictStart, TokenDictEnd, TokenEOF}
	for _, want := range expected {
		tok, err := l.NextToken()
		if err != nil {
			t.Fatal(err)
		}
		if tok.Type != want {
			t.Errorf("expected %v, got %v", want, tok)
		}
	}
}

func TestLexerKeywords(t *testing.T) {
	tests := []struct {
		input string
		want  TokenType
	}{
		{"null", TokenNull},
		{"obj", TokenObj},
		{"endobj", TokenEndObj},
		{"stream", TokenStream},
		{"endstream", TokenEndStream},
		{"R", TokenRef},
		{"xref", TokenXref},
		{"trailer", TokenTrailer},
		{"startxref", TokenStartXref},
	}
	for _, tt := range tests {
		l := NewLexer([]byte(tt.input))
		tok, err := l.NextToken()
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if tok.Type != tt.want {
			t.Errorf("input %q: expected %v, got %v", tt.input, tt.want, tok)
		}
	}
}

func TestLexerMultipleTokens(t *testing.T) {
	input := "/Type /Catalog /Pages 3 0 R"
	l := NewLexer([]byte(input))

	expected := []struct {
		typ TokenType
		val string
	}{
		{TokenName, "Type"},
		{TokenName, "Catalog"},
		{TokenName, "Pages"},
		{TokenInteger, "3"},
		{TokenInteger, "0"},
		{TokenRef, "R"},
		{TokenEOF, ""},
	}

	for _, want := range expected {
		tok, err := l.NextToken()
		if err != nil {
			t.Fatal(err)
		}
		if tok.Type != want.typ {
			t.Errorf("expected type %v, got %v", want.typ, tok.Type)
		}
		if want.val != "" && string(tok.Value) != want.val {
			t.Errorf("expected value %q, got %q", want.val, tok.Value)
		}
	}
}

func TestLexerDictStartVsHexString(t *testing.T) {
	// << should be DictStart, <48> should be hex string
	input := "<< /Key <48> >>"
	l := NewLexer([]byte(input))

	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenDictStart {
		t.Errorf("expected DictStart, got %v", tok)
	}

	tok, err = l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenName {
		t.Errorf("expected Name, got %v", tok)
	}

	tok, err = l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenString || string(tok.Value) != "H" {
		t.Errorf("expected String 'H', got %v (value=%q)", tok, tok.Value)
	}

	tok, err = l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenDictEnd {
		t.Errorf("expected DictEnd, got %v", tok)
	}
}

func TestLexerOffset(t *testing.T) {
	input := "  123  456"
	l := NewLexer([]byte(input))

	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Offset != 2 {
		t.Errorf("expected offset 2, got %d", tok.Offset)
	}

	tok, err = l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Offset != 7 {
		t.Errorf("expected offset 7, got %d", tok.Offset)
	}
}

func TestLexerSetPosition(t *testing.T) {
	input := "true false null"
	l := NewLexer([]byte(input))

	// Seek to "false"
	l.SetPosition(5)
	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenBoolean || string(tok.Value) != "false" {
		t.Errorf("expected Boolean false, got %v", tok)
	}
}

func TestLexerEmptyInput(t *testing.T) {
	l := NewLexer([]byte{})
	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenEOF {
		t.Errorf("expected EOF, got %v", tok)
	}
}

func TestLexerNameAtDelimiter(t *testing.T) {
	// Name followed immediately by delimiter
	input := "/Type/Catalog"
	l := NewLexer([]byte(input))

	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenName || string(tok.Value) != "Type" {
		t.Errorf("expected Name 'Type', got %v", tok)
	}

	tok, err = l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Type != TokenName || string(tok.Value) != "Catalog" {
		t.Errorf("expected Name 'Catalog', got %v", tok)
	}
}
