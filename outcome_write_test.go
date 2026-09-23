package pdf0

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
)

// xrefStreamPDF lays out bodies as objects 1..n behind a cross-reference
// stream (object n+1), with /Root 1 0 R.
func xrefStreamPDF(bodies []string) []byte {
	head, offs := numberedPDF(bodies)
	xoff := len(head)
	n := len(bodies)
	var ent []byte
	ent = append(ent, 0, 0, 0, 0, 255)
	for i := 1; i <= n; i++ {
		ent = append(ent, 1)
		ent = append(ent, be(offs[i], 3)...)
		ent = append(ent, 0)
	}
	ent = append(ent, 1)
	ent = append(ent, be(xoff, 3)...)
	ent = append(ent, 0)
	var b bytes.Buffer
	b.Write(head)
	fmt.Fprintf(&b, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 3 1] /Root 1 0 R /Length %d >>\nstream\n", n+1, n+2, len(ent))
	b.Write(ent)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xoff)
	return b.Bytes()
}

// onePageXRefStream is a one-page document whose cross-reference section is a
// stream, so Write packs its objects into an object stream.
func onePageXRefStream(t *testing.T) *Document {
	t.Helper()
	doc := readRaw(t, xrefStreamPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>",
		"<< /Length 3 >>\nstream\nq Q\nendstream",
	}))
	if !doc.usedXRefStream {
		t.Fatal("fixture: the document was not read from a cross-reference stream")
	}
	return doc
}

// TestObjectStreamPackingPropagatesSerialisationErrors is audit 2026-09-22
// C100: the traditional-table path refuses a NaN ("cannot serialize non-finite
// real"), but packing an object stream dropped the serializer's error, so Write
// returned nil and the file it wrote could not be read back ("unterminated
// array").
func TestObjectStreamPackingPropagatesSerialisationErrors(t *testing.T) {
	doc := onePageXRefStream(t)
	ref := doc.Add(object.Array{object.Integer(1), object.Real(math.NaN())})
	doc.ResolveDict(doc.Trailer.Get("Root")).Set("Extra", ref)
	var buf bytes.Buffer
	err := doc.Write(&buf)
	if err == nil {
		t.Fatal("Write accepted a NaN inside a packed object")
	}
	if !strings.Contains(err.Error(), "non-finite") {
		t.Errorf("Write error %q does not say what was wrong", err)
	}
}

// TestSigningAnXRefStreamDocument is audit 2026-09-22 C21: WriteSigned, with
// and without a signature time-stamp, packed the placeholder signature dictionary into a
// compressed object stream, where its /ByteRange never appears in the output,
// and failed on every document read from an xref-stream file with "/ByteRange
// placeholder not found". The signature — and anything holding one — now
// stays a plain indirect object.
func TestSigningAnXRefStreamDocument(t *testing.T) {
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	for name, write := range map[string]func(*Document, *bytes.Buffer) error{
		"WriteSigned": func(d *Document, w *bytes.Buffer) error { return d.WriteSigned(w, cert, key) },
		"WriteSigned with a signature time-stamp": func(d *Document, w *bytes.Buffer) error {
			return d.WriteSigned(w, cert, key, WithSignatureTimestamp(tsaCert, tsaKey))
		},
	} {
		var buf bytes.Buffer
		if err := write(onePageXRefStream(t), &buf); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out := buf.Bytes()
		signed := readRaw(t, out)
		if !signed.usedXRefStream {
			t.Errorf("%s: the signed file is not an xref-stream file; the test did not exercise packing", name)
		}
		results, err := signed.VerifySignatures(sign.VerifyOptions{})
		if err != nil {
			t.Errorf("%s: VerifySignatures: %v", name, err)
			continue
		}
		ours := ourSignature(results, cert)
		if ours == nil || !ours.DocumentUnmodified() {
			t.Errorf("%s: the signature does not verify over the whole document: %+v", name, ours)
		}
	}
	// The signature dictionary must not have been packed, nor any object
	// holding one directly.
	if !holdsSignatureDict(object.NewDictionary(object.Entry{Key: "V", Value: object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Sig")},
		object.Entry{Key: "ByteRange", Value: object.Array{object.Integer(0)}},
		object.Entry{Key: "Contents", Value: object.String{Value: []byte{0}, IsHex: true}},
	)})) {
		t.Error("a field holding its signature dictionary directly is not recognised")
	}
}

// TestCorpusWriteSignedOfXRefStreamFiles signs every corpus file that ends in a
// cross-reference stream with a full rewrite (WriteSigned), which is the path
// C21 broke for all of them, and verifies the signature over the result.
func TestCorpusWriteSignedOfXRefStreamFiles(t *testing.T) {
	cert, key := signtest.CertKey(t)
	root := corpusRoot(t)
	var eligible, signed int
	reasons := map[string]int{}
	var failures []string
	for _, path := range testfiles.VeraPDFCorpus.Files(t, "", testfiles.IsPDF) {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil || !doc.usedXRefStream {
			continue
		}
		eligible++
		var out bytes.Buffer
		if err := doc.WriteSigned(&out, cert, key); err != nil {
			if isSigningPrecondition(err) || errors.Is(err, ErrAlreadySigned) || doc.Encrypted || doc.missingObjectsErr("") != nil {
				reasons[err.Error()]++
				continue
			}
			failures = append(failures, rel+": WriteSigned: "+err.Error())
			continue
		}
		b := out.Bytes()
		sd, err := Read(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			failures = append(failures, rel+": re-read: "+err.Error())
			continue
		}
		results, err := sd.VerifySignatures(sign.VerifyOptions{})
		if err != nil {
			failures = append(failures, rel+": VerifySignatures: "+err.Error())
			continue
		}
		if ours := ourSignature(results, cert); ours == nil || !ours.DocumentUnmodified() {
			failures = append(failures, fmt.Sprintf("%s: the signature does not verify: %+v", rel, ours))
			continue
		}
		signed++
	}
	t.Logf("%d xref-stream files, %d signed and verified; not signable on purpose: %v", eligible, signed, reasons)
	if eligible < 100 {
		t.Errorf("only %d xref-stream files found", eligible)
	}
	if len(failures) > 0 {
		t.Errorf("%d failures:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
}
