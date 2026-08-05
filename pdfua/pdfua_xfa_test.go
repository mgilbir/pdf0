package pdfua

import (
	"bytes"
	"compress/zlib"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAXFACompressed verifies that a dynamicRender directive in a
// FlateDecode-compressed XFA packet is detected (regression: the check used to
// scan raw, still-encoded stream bytes).
func TestUAXFACompressed(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write([]byte(`<config><dynamicRender >required</dynamicRender></config>`))
	zw.Close()

	pkt := &object.Stream{Dict: object.Dictionary{}, Data: zbuf.Bytes()}
	pkt.Dict.Set("Filter", object.Name("FlateDecode"))
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	form := &object.Dictionary{}
	form.Set("XFA", object.Array{object.String{Value: []byte("config")}, pkt})
	cat := &object.Dictionary{}
	cat.Set("AcroForm", form)

	if len(checkUAXFA(doc, cat)) == 0 {
		t.Error("dynamicRender in a compressed XFA packet not detected")
	}

	// A static XFA (no dynamicRender) is clean.
	var zbuf2 bytes.Buffer
	zw2 := zlib.NewWriter(&zbuf2)
	zw2.Write([]byte(`<config><staticRender>1</staticRender></config>`))
	zw2.Close()
	pkt2 := &object.Stream{Dict: object.Dictionary{}, Data: zbuf2.Bytes()}
	pkt2.Dict.Set("Filter", object.Name("FlateDecode"))
	form.Set("XFA", object.Array{object.String{Value: []byte("config")}, pkt2})
	if len(checkUAXFA(doc, cat)) != 0 {
		t.Error("static XFA wrongly flagged")
	}
}
