package pdf0

import "testing"

// TestSkipWhitespaceGapBounded guards against a parser DoS: an xref offset that
// points into binary stream data makes skipWhitespaceAndComments scan huge runs
// of "whitespace" and no-newline "%" comments. Both the whitespace and the
// comment paths must stop within the gap bound, so a single lex never scans the
// file end-to-end (which, repeated per object, was quadratic).
func TestSkipWhitespaceGapBounded(t *testing.T) {
	// Case 1: a giant no-newline "%" comment far larger than the gap bound.
	comment := make([]byte, maxTokenGap*3)
	comment[0] = '%'
	for i := 1; i < len(comment); i++ {
		comment[i] = 'x' // no EOL anywhere
	}
	l := NewLexer(comment)
	l.skipWhitespaceAndComments()
	if l.pos > maxTokenGap+16 {
		t.Errorf("comment scan advanced %d bytes, expected <= ~%d", l.pos, maxTokenGap)
	}

	// Case 2: a giant run of whitespace-classified bytes.
	ws := make([]byte, maxTokenGap*3)
	for i := range ws {
		ws[i] = ' '
	}
	l2 := NewLexer(ws)
	l2.skipWhitespaceAndComments()
	if l2.pos > maxTokenGap+16 {
		t.Errorf("whitespace scan advanced %d bytes, expected <= ~%d", l2.pos, maxTokenGap)
	}

	// A short comment followed by a token still lexes normally.
	l3 := NewLexer([]byte("% a normal comment\n42"))
	tok, err := l3.NextToken()
	if err != nil || tok.Type != TokenInteger || string(tok.Value) != "42" {
		t.Errorf("short comment broke normal lexing: tok=%v err=%v", tok, err)
	}
}
