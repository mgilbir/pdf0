package pdf0

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestStreamDataDecodesTheFilterChain pins the point of the method: what comes
// back is content, not the stored bytes. A stream with no filter decodes to
// itself, one with a filter decodes through it, and both go through the same
// call so a caller never has to ask which case it has.
func TestStreamDataDecodesTheFilterChain(t *testing.T) {
	doc := &Document{Objects: map[int]*object.IndirectObject{}}
	plain := []byte("q 1 0 0 rg Q")

	bare := &object.Stream{Dict: object.Dictionary{}, Data: plain}
	bare.Dict.Set("Length", object.Integer(len(plain)))
	got, err := doc.StreamData(bare)
	if err != nil {
		t.Fatalf("unfiltered: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("unfiltered stream decoded to %q, want %q", got, plain)
	}

	encoded := core.FlateEncode(plain)
	filtered := &object.Stream{Dict: object.Dictionary{}, Data: encoded}
	filtered.Dict.Set("Filter", object.Name("FlateDecode"))
	filtered.Dict.Set("Length", object.Integer(len(encoded)))
	got, err = doc.StreamData(filtered)
	if err != nil {
		t.Fatalf("flate: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("flate stream decoded to %q, want %q", got, plain)
	}
	// And the stored bytes are untouched: Data holds the encoded form, which is
	// what writing the file back out depends on.
	if !bytes.Equal(filtered.Data, encoded) {
		t.Error("decoding altered the stream's stored bytes")
	}
}

// TestStreamDataHonoursTheDecodedSizeLimit pins that the configured ceiling
// reaches this entry point. A caller decoding a stream out of an untrusted file
// is exactly who needs the bomb guard, and an entry point that skipped it would
// be the way around every other one.
func TestStreamDataHonoursTheDecodedSizeLimit(t *testing.T) {
	bomb := core.FlateEncode(bytes.Repeat([]byte{0}, 1<<20))
	s := &object.Stream{Dict: object.Dictionary{}, Data: bomb}
	s.Dict.Set("Filter", object.Name("FlateDecode"))
	s.Dict.Set("Length", object.Integer(len(bomb)))

	doc := &Document{Objects: map[int]*object.IndirectObject{}}
	if _, err := doc.StreamData(s); err != nil {
		t.Fatalf("a megabyte is within the default ceiling: %v", err)
	}

	raw := minimalPDF()
	tight, err := Read(bytes.NewReader(raw), int64(len(raw)), WithMaxDecodedStreamBytes(1024))
	if err != nil {
		t.Fatalf("reading with a lowered ceiling: %v", err)
	}
	if _, err := tight.StreamData(s); err == nil {
		t.Error("a stream decoding past the configured ceiling was expanded anyway")
	}
}

// TestStreamDataRefusesALockedDocument pins that ciphertext is never handed
// back as if it were content. A caller cannot tell the difference by looking,
// so the difference has to be reported.
func TestStreamDataRefusesALockedDocument(t *testing.T) {
	doc := &Document{Objects: map[int]*object.IndirectObject{}, Encrypted: true}
	s := &object.Stream{Dict: object.Dictionary{}, Data: []byte("ciphertext")}
	_, err := doc.StreamData(s)
	if err == nil {
		t.Fatal("a locked document handed back its ciphertext")
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("error %q does not say the document is encrypted", err)
	}
}

// TestStreamDataContextCancels pins the cancellable variant.
func TestStreamDataContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doc := &Document{Objects: map[int]*object.IndirectObject{}}
	encoded := core.FlateEncode(bytes.Repeat([]byte("x"), 4096))
	s := &object.Stream{Dict: object.Dictionary{}, Data: encoded}
	s.Dict.Set("Filter", object.Name("FlateDecode"))
	if _, err := doc.StreamDataContext(ctx, s); err == nil {
		t.Error("decoding proceeded under a cancelled context")
	}
}

// TestStreamDataOnANilStream pins that the obvious mistake is an error rather
// than a panic.
func TestStreamDataOnANilStream(t *testing.T) {
	doc := &Document{Objects: map[int]*object.IndirectObject{}}
	if _, err := doc.StreamData(nil); err == nil {
		t.Error("a nil stream was accepted")
	}
}

// minimalPDF is the smallest document Read accepts, for tests that need a
// Document carrying options rather than one built by hand.
func minimalPDF() []byte {
	doc := NewPDFADocument(pdfa.PDFA2b)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
