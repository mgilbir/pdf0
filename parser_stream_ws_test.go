package pdf0

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// TestParseStreamKeywordTrailingWhitespace verifies that a spurious space
// between the stream keyword and its EOL ("stream \r\n") is not absorbed into
// the stream data: the data must be exactly the declared length, decode
// correctly through its filter, and survive a round-trip unchanged. A producer
// emitting "stream \r\n" is non-conformant but common; absorbing the whitespace
// corrupts the data (bytes before the FlateDecode header) and destabilizes the
// round-trip (the recovered length no longer matches the declared /Length).
func TestParseStreamKeywordTrailingWhitespace(t *testing.T) {
	payload := []byte("hello flate world, this is the stream content")
	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(payload)
	zw.Close()
	flate := zb.Bytes()

	// Build a PDF whose stream object uses "stream \r\n" (note the space).
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	catOff := b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	pagesOff := b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	pageOff := b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")
	strOff := b.Len()
	// The space after "stream" is the case under test.
	b.WriteString("4 0 obj\n<< /Length ")
	b.WriteString(itoa(len(flate)))
	b.WriteString(" /Filter /FlateDecode >>\nstream \r\n")
	b.Write(flate)
	b.WriteString("\nendstream\nendobj\n")
	xref := b.Len()
	b.WriteString("xref\n0 5\n0000000000 65535 f \n")
	for _, off := range []int{catOff, pagesOff, pageOff, strOff} {
		b.WriteString(pad10(off) + " 00000 n \n")
	}
	b.WriteString("trailer\n<< /Root 1 0 R /Size 5 >>\nstartxref\n")
	b.WriteString(itoa(xref))
	b.WriteString("\n%%EOF\n")

	doc, err := Read(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	st, ok := doc.Objects[4].Value.(*Stream)
	if !ok {
		t.Fatalf("object 4 is %T, want *Stream", doc.Objects[4].Value)
	}
	// Data must be exactly the flate bytes — no leading whitespace absorbed.
	if !bytes.Equal(st.Data, flate) {
		t.Fatalf("stream data length %d, want %d; first bytes %x (whitespace absorbed?)", len(st.Data), len(flate), st.Data[:min(6, len(st.Data))])
	}
	// It must decode through its filter.
	dec, err := decodeStreamData(st)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(dec, payload) {
		t.Fatalf("decoded %q, want %q", dec, payload)
	}
	// /Length must be preserved and the round-trip stable.
	if got, _ := st.Dict.Get("Length").(Integer); int(got) != len(flate) {
		t.Errorf("/Length = %v, want %d", got, len(flate))
	}
	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		t.Fatalf("write: %v", err)
	}
	doc2, err := Read(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !DocumentEqual(doc, doc2) {
		t.Error("round-trip changed the document (stream length instability)")
	}
}

func itoa(n int) string  { return string(appendInt(nil, n)) }
func pad10(n int) string { s := itoa(n); for len(s) < 10 { s = "0" + s }; return s }
func appendInt(b []byte, n int) []byte {
	if n == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, tmp[i:]...)
}
