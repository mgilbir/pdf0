package pdf0

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// onePage is a catalog, a page tree and one page whose /Contents is object 4,
// which the caller supplies.
func onePage(content rawObj, resources string, more ...rawObj) []rawObj {
	if resources == "" {
		resources = "<<>>"
	}
	return append([]rawObj{
		{dict: "<</Type/Catalog/Pages 2 0 R>>"},
		{dict: "<</Type/Pages/Kids[3 0 R]/Count 1>>"},
		{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]/Contents 4 0 R/Resources " + resources + ">>"},
		content,
	}, more...)
}

// minimalContentPDF is a one-page file whose content stream holds content,
// compressed with FlateDecode when flate is set.
func minimalContentPDF(t *testing.T, content []byte, flate bool) []byte {
	t.Helper()
	if flate {
		return buildRawPDF(onePage(rawObj{dict: "<</Filter/FlateDecode>>", stream: zlibBytes(content)}, ""))
	}
	return buildRawPDF(onePage(rawObj{dict: "<<>>", stream: content}, ""))
}

// contentStreamOf is the content stream of a onePage file (object 4).
func contentStreamOf(t *testing.T, doc *Document) *object.Stream {
	t.Helper()
	iobj, ok := doc.Objects[4]
	if !ok {
		t.Fatal("object 4 (the content stream) is missing")
	}
	st, ok := iobj.Value.(*object.Stream)
	if !ok {
		t.Fatalf("object 4 is a %T, not a stream", iobj.Value)
	}
	return st
}

// readRaw reads a hand-assembled file, failing the test on error.
func readRaw(t *testing.T, file []byte, opts ...Option) *Document {
	t.Helper()
	doc, err := Read(bytes.NewReader(file), int64(len(file)), opts...)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return doc
}

// be writes v big-endian in w bytes.
func be(v, w int) []byte {
	out := make([]byte, w)
	for i := w - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out
}

// numberedPDF lays out bodies as objects 1..n (a body is the text between
// "obj" and "endobj") and returns the bytes and each object's offset.
func numberedPDF(bodies []string) ([]byte, []int) {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	offs := make([]int, len(bodies)+1)
	for i, body := range bodies {
		offs[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	return b.Bytes(), offs
}
