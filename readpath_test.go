package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// C10: overflowing object numbers must error, not silently clamp.
func TestOverflowingObjectNumberRejected(t *testing.T) {
	p := syntax.NewParser([]byte("99999999999999999999999999 0 R"))
	if _, err := p.ParseObject(); err == nil {
		t.Error("expected error for overflowing object number in reference")
	}

	p = syntax.NewParser([]byte("99999999999999999999999999 0 obj\n42\nendobj"))
	if _, err := p.ParseIndirectObject(); err == nil {
		t.Error("expected error for overflowing object number in definition")
	}
}

// C16 (2026-07-26): a lexer error following an integer must surface, not be
// swallowed. Since audit 2026-09-22 C116 it surfaces from the parse that
// reaches the malformed token rather than failing the valid integer before
// it; inside an array the array still fails.
func TestLexerErrorAfterIntegerPropagates(t *testing.T) {
	p := syntax.NewParser([]byte("5 <zz>"))
	if obj, err := p.ParseObject(); err != nil || obj != object.Integer(5) {
		t.Fatalf("first object = %v, %v; want the integer 5", obj, err)
	}
	if _, err := p.ParseObject(); err == nil || !strings.Contains(err.Error(), "invalid hex") {
		t.Errorf("second object: err = %v; the invalid-hex error must surface", err)
	}
	if _, err := syntax.NewParser([]byte("[5 <zz>]")).ParseObject(); err == nil {
		t.Error("expected invalid-hex error to fail the array")
	}
}

// C17: "1.2.3" is one malformed number, not two reals.
func TestMalformedNumberMultipleDots(t *testing.T) {
	p := syntax.NewParser([]byte("[1.2.3]"))
	if _, err := p.ParseObject(); err == nil {
		t.Error("expected error for number with multiple dots")
	}
}

// C25: NUL smuggled into a name via #00 must be rejected.
func TestNameWithNULRejected(t *testing.T) {
	l := syntax.NewLexer([]byte("/A#00B"))
	if _, err := l.NextToken(); err == nil {
		t.Error("expected error for #00 in name")
	}
	// Other escapes still work.
	l = syntax.NewLexer([]byte("/A#20B"))
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
	if _, err := syntax.NewLexerFromReaderAt(shortReader{data: []byte("abc")}, 10); err == nil {
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
	p := syntax.NewParser([]byte(src))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := obj.(*object.Stream)
	if !ok {
		t.Fatalf("expected *Stream, got %T", obj)
	}
	if string(stream.Data) != "ABendstreamCD" {
		t.Errorf("stream data truncated: %q", stream.Data)
	}
}

// C11: an encrypted document is flagged, and one whose /Encrypt dictionary is
// unresolvable refuses to Write — its encryption state is unknown, so the
// content cannot be safely written back. (A document with a resolvable /Encrypt
// that pdf0 simply cannot decrypt is instead written back as a verbatim
// passthrough; see TestEncryptedPassthroughRoundTrip.)
func TestEncryptedDocumentDetectedAndNotWritten(t *testing.T) {
	base := buildMinimalPDF()
	// Rebuild the minimal PDF with an /Encrypt entry in the trailer that points
	// at a nonexistent object, so the /Encrypt dictionary does not resolve.
	s := string(base)
	s = strings.Replace(s, "<< /Size 4 /Root 1 0 R >>", "<< /Size 4 /Root 1 0 R /Encrypt 9 0 R >>", 1)
	if s == string(base) {
		// A security regression test that stops exercising its case must fail,
		// not skip: a skip reads as a pass (audit 2026-09-22 C167).
		t.Fatal("the minimal PDF's trailer changed, so the /Encrypt entry was not planted; update this test")
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
		t.Error("expected Write to refuse an encrypted document with an unresolvable /Encrypt")
	}
}

// C26: duplicate dictionary keys keep the last occurrence.
func TestDuplicateDictKeysLastWins(t *testing.T) {
	p := syntax.NewParser([]byte("<< /A 1 /A 2 >>"))
	obj, err := p.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	dict := obj.(*object.Dictionary)
	if v, ok := dict.Get("A").(object.Integer); !ok || v != 2 {
		t.Errorf("expected last-wins /A 2, got %v", dict.Get("A"))
	}
	if dict.Len() != 1 {
		t.Errorf("expected 1 key, got %d", dict.Len())
	}
}
