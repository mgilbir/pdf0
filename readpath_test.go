package pdf0

import (
	"bytes"
	"strings"
	"testing"
)

// C10: overflowing object numbers must error, not silently clamp.
func TestOverflowingObjectNumberRejected(t *testing.T) {
	p := NewParser([]byte("99999999999999999999999999 0 R"))
	if _, err := p.ParseObject(); err == nil {
		t.Error("expected error for overflowing object number in reference")
	}

	p = NewParser([]byte("99999999999999999999999999 0 obj\n42\nendobj"))
	if _, err := p.ParseIndirectObject(); err == nil {
		t.Error("expected error for overflowing object number in definition")
	}
}

// C16: a lexer error following an integer must surface, not be swallowed.
func TestLexerErrorAfterIntegerPropagates(t *testing.T) {
	p := NewParser([]byte("5 <zz>"))
	if _, err := p.ParseObject(); err == nil {
		t.Error("expected invalid-hex error to propagate through look-ahead")
	}
}

// C17: "1.2.3" is one malformed number, not two reals.
func TestMalformedNumberMultipleDots(t *testing.T) {
	p := NewParser([]byte("[1.2.3]"))
	if _, err := p.ParseObject(); err == nil {
		t.Error("expected error for number with multiple dots")
	}
}

// C25: NUL smuggled into a name via #00 must be rejected.
func TestNameWithNULRejected(t *testing.T) {
	l := NewLexer([]byte("/A#00B"))
	if _, err := l.NextToken(); err == nil {
		t.Error("expected error for #00 in name")
	}
	// Other escapes still work.
	l = NewLexer([]byte("/A#20B"))
	tok, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if string(tok.Value) != "A B" {
		t.Errorf("expected 'A B', got %q", tok.Value)
	}
}

// C27: a ReaderAt that returns fewer bytes than promised must error rather
// than leave NUL padding that lexes as whitespace.
type shortReader struct{ data []byte }

func (r shortReader) ReadAt(p []byte, off int64) (int, error) {
	n := copy(p, r.data[off:])
	if n < len(p) {
		return n, nil // deliberately no error
	}
	return n, nil
}

func TestShortReadRejected(t *testing.T) {
	data := buildMinimalPDF()
	if _, err := Read(shortReader{data: data[:len(data)/2]}, int64(len(data))); err == nil {
		t.Error("expected error for short read in Read")
	}
	if _, err := NewLexerFromReaderAt(shortReader{data: []byte("abc")}, 10); err == nil {
		t.Error("expected error for short read in NewLexerFromReaderAt")
	}
}

// C28: a startxref offset pointing outside the file must error cleanly.
func TestStartXrefOffsetOutOfBounds(t *testing.T) {
	pdf := []byte("%PDF-1.7\nstartxref\n99999\n%%EOF\n")
	if _, err := Read(bytes.NewReader(pdf), int64(len(pdf))); err == nil {
		t.Error("expected error for out-of-bounds startxref offset")
	}
}

// C12: binary stream data containing the bytes "endstream" must not
// truncate a stream whose /Length is indirect or absent.
func TestStreamEndstreamInBinaryData(t *testing.T) {
	// No /Length: the body contains a raw (non-delimited) "endstream"
	// before the real, whitespace-delimited one.
	src := "<< >>\nstream\nABendstreamCD\nendstream"
	p := NewParser([]byte(src))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := obj.(*Stream)
	if !ok {
		t.Fatalf("expected *Stream, got %T", obj)
	}
	if string(stream.Data) != "ABendstreamCD" {
		t.Errorf("stream data truncated: %q", stream.Data)
	}
}

// C11: encrypted documents are flagged and refuse to Write.
func TestEncryptedDocumentDetectedAndNotWritten(t *testing.T) {
	base := buildMinimalPDF()
	// Rebuild the minimal PDF with an /Encrypt entry in the trailer.
	s := string(base)
	s = strings.Replace(s, "<< /Size 4 /Root 1 0 R >>", "<< /Size 4 /Root 1 0 R /Encrypt 9 0 R >>", 1)
	if s == string(base) {
		t.Skip("minimal PDF trailer changed; update this test")
	}
	doc, err := Read(bytes.NewReader([]byte(s)), int64(len(s)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !doc.Encrypted {
		t.Error("Encrypted flag not set")
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err == nil {
		t.Error("expected Write to refuse an encrypted document")
	}
}

// C26: duplicate dictionary keys keep the last occurrence.
func TestDuplicateDictKeysLastWins(t *testing.T) {
	p := NewParser([]byte("<< /A 1 /A 2 >>"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	dict := obj.(*Dictionary)
	if v, ok := dict.Get("A").(Integer); !ok || v != 2 {
		t.Errorf("expected last-wins /A 2, got %v", dict.Get("A"))
	}
	if dict.Len() != 1 {
		t.Errorf("expected 1 key, got %d", dict.Len())
	}
}
