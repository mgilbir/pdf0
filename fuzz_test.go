package pdf0

import (
	"bytes"
	"testing"
)

// FuzzRead exercises the parser and the paths that consume a parsed document on
// arbitrary input. Read must never panic (it recovers internally); and any
// document it returns must survive Write and validation without panicking —
// these process untrusted input and must fail with errors, not crashes.
func FuzzRead(f *testing.F) {
	f.Add(buildMinimalPDF())
	f.Add(buildXRefStreamPDF())
	f.Add([]byte("%PDF-2.0\n%\x80\x80\x80\x80\nstartxref\n0\n%%EOF"))
	f.Add([]byte("%PDF-2.0\n"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		if doc == nil {
			t.Fatal("Read returned nil document and nil error")
		}
		var buf bytes.Buffer
		_ = doc.Write(&buf)
		_ = ValidatePDFABytes(doc, PDFA2b, data)
	})
}
