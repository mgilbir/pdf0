package pdfa

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// docWithMetadata builds a minimal document whose catalog metadata stream holds
// xmp, optionally FlateDecode-compressed.
func docWithMetadata(t *testing.T, xmp string, compressed bool) core.View {
	t.Helper()
	d := mkViewVersion(map[int]*object.IndirectObject{}, object.Dictionary{}, "1.6")
	md := &object.Dictionary{}
	md.Set("Type", object.Name("Metadata"))
	md.Set("Subtype", object.Name("XML"))
	data := []byte(xmp)
	if compressed {
		md.Set("Filter", object.Name("FlateDecode"))
		data = core.FlateEncode([]byte(xmp))
	}
	d.Objects[2] = &object.IndirectObject{Number: 2, Value: &object.Stream{Dict: *md, Data: data}}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Metadata", object.IndirectRef{Number: 2})
	d.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	*d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

const pdfaXMP = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
<pdfaid:part>3</pdfaid:part><pdfaid:conformance>B</pdfaid:conformance>
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`

// TestMetadataFilterOnlyForbiddenInPDFA1 pins ISO 19005: a Filter on the catalog
// metadata stream is forbidden in PDF/A-1 (19005-1 6.7.2) but permitted in
// PDF/A-2 and PDF/A-3 (the restriction was removed). veraPDF likewise carries
// the PDMetadata Filter rule only in its PDF/A-1 profile.
func TestMetadataFilterOnlyForbiddenInPDFA1(t *testing.T) {
	doc := docWithMetadata(t, pdfaXMP, true)
	has := func(errs []Violation, substr string) bool {
		for _, e := range errs {
			if strings.Contains(e.Message, substr) {
				return true
			}
		}
		return false
	}
	if !has(checkMetadataStream(doc, PDFA1b), "must not have /Filter") {
		t.Error("PDF/A-1b: a compressed metadata stream must be flagged")
	}
	for _, lvl := range []Level{PDFA2b, PDFA3b, PDFA4} {
		if has(checkMetadataStream(doc, lvl), "must not have /Filter") {
			t.Errorf("%v: a compressed metadata stream must NOT be flagged (allowed since PDF/A-2)", lvl)
		}
	}
}

// TestCompressedMetadataIsDecoded confirms the XMP checks read a FlateDecode
// metadata stream through its filter: pdfaid:part is found rather than reported
// missing. Before the fix these read the raw compressed bytes.
func TestCompressedMetadataIsDecoded(t *testing.T) {
	doc := docWithMetadata(t, pdfaXMP, true)
	for _, e := range checkMetadataVersion(doc, PDFA3b) {
		if strings.Contains(e.Message, "must contain pdfaid:part") {
			t.Fatalf("compressed metadata was not decoded: %s", e.Message)
		}
	}
	// Sanity: the same XMP uncompressed yields identical results (raw == decoded).
	plain := docWithMetadata(t, pdfaXMP, false)
	c := checkMetadataVersion(doc, PDFA3b)
	p := checkMetadataVersion(plain, PDFA3b)
	if len(c) != len(p) {
		t.Errorf("compressed vs uncompressed metadata gave different results: %d vs %d", len(c), len(p))
	}
	_ = bytes.TrimSpace
}
