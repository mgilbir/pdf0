package pdf0

import (
	"bytes"
	"compress/zlib"
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

	pkt := &Stream{Dict: Dictionary{}, Data: zbuf.Bytes()}
	pkt.Dict.Set("Filter", Name("FlateDecode"))
	doc := &Document{Objects: map[int]*IndirectObject{}}
	form := &Dictionary{}
	form.Set("XFA", Array{String{Value: []byte("config")}, pkt})
	cat := &Dictionary{}
	cat.Set("AcroForm", form)

	if len(doc.checkUAXFA(cat)) == 0 {
		t.Error("dynamicRender in a compressed XFA packet not detected")
	}

	// A static XFA (no dynamicRender) is clean.
	var zbuf2 bytes.Buffer
	zw2 := zlib.NewWriter(&zbuf2)
	zw2.Write([]byte(`<config><staticRender>1</staticRender></config>`))
	zw2.Close()
	pkt2 := &Stream{Dict: Dictionary{}, Data: zbuf2.Bytes()}
	pkt2.Dict.Set("Filter", Name("FlateDecode"))
	form.Set("XFA", Array{String{Value: []byte("config")}, pkt2})
	if len(doc.checkUAXFA(cat)) != 0 {
		t.Error("static XFA wrongly flagged")
	}
}
