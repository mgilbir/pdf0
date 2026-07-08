package pdf0

import (
	"bytes"
	"compress/lzw"
	"testing"
)

// TestLZWDecodeRoundTrip checks the decoder against Go's standard MSB LZW
// encoder. Go's encoder does not apply the PDF "early change" quirk, so it
// matches lzwDecode with earlyChange=0. This exercises the core code-table and
// variable-width-code logic (the KwKwK case, width growth at 512/1024/2048).
func TestLZWDecodeRoundTrip(t *testing.T) {
	inputs := [][]byte{
		[]byte(""),
		[]byte("A"),
		[]byte("TOBEORNOTTOBEORTOBEORNOT"),
		bytes.Repeat([]byte("ABCABCABCABC"), 500), // forces width growth past 9 bits
		func() []byte {
			b := make([]byte, 5000)
			for i := range b {
				b[i] = byte(i * 7)
			}
			return b
		}(),
	}
	for i, in := range inputs {
		var buf bytes.Buffer
		w := lzw.NewWriter(&buf, lzw.MSB, 8)
		if _, err := w.Write(in); err != nil {
			t.Fatalf("case %d: encode: %v", i, err)
		}
		w.Close()

		got, err := lzwDecode(buf.Bytes(), 0)
		if err != nil {
			t.Fatalf("case %d: decode: %v", i, err)
		}
		if !bytes.Equal(got, in) {
			t.Errorf("case %d: round-trip mismatch (len got=%d want=%d)", i, len(got), len(in))
		}
	}
}

// TestLZWSpecVector validates the earlyChange=1 (PDF default) path against the
// canonical ISO 32000-1 7.4.4.2 LZW example.
func TestLZWSpecVector(t *testing.T) {
	encoded := []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}
	want := []byte{0x2D, 0x2D, 0x2D, 0x2D, 0x2D, 0x41, 0x2D, 0x2D, 0x2D, 0x42}
	got, err := lzwDecode(encoded, 1)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X, want %X", got, want)
	}
}

// TestLZWStreamDecoded ensures a stream carrying /Filter /LZWDecode is decoded
// through the normal filter path (audit C18: such streams previously aborted).
func TestLZWStreamDecoded(t *testing.T) {
	payload := []byte("Hello LZW world, the quick brown fox jumps over the lazy dog.")
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.MSB, 8)
	w.Write(payload)
	w.Close()

	s := &Stream{Dict: Dictionary{}, Data: buf.Bytes()}
	s.Dict.Set("Filter", Name("LZWDecode"))
	// EarlyChange 0 to match Go's encoder.
	parms := &Dictionary{}
	parms.Set("EarlyChange", Integer(0))
	s.Dict.Set("DecodeParms", parms)

	got, err := decodeStreamData(s)
	if err != nil {
		t.Fatalf("decodeStreamData: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("decoded %q, want %q", got, payload)
	}
	if !isSupportedFilter("LZWDecode") {
		t.Errorf("LZWDecode should report as supported")
	}
}

// TestLZWInvalidCode ensures a garbage LZW stream errors instead of panicking.
func TestLZWInvalidCode(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("lzwDecode panicked: %v", r)
		}
	}()
	if _, err := lzwDecode([]byte{0xFF, 0xFF, 0xFF, 0xFF}, 1); err == nil {
		t.Logf("no error (acceptable); must not panic")
	}
}
