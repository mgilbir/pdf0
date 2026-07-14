package pdf0

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// docWithXMP builds a minimal document carrying the given XMP packet as the
// catalog's /Metadata stream (stored raw, no filter).
func docWithXMP(xmp []byte) *Document {
	ms := &Stream{Dict: Dictionary{}, Data: xmp}
	ms.Dict.Set("Type", Name("Metadata"))
	ms.Dict.Set("Subtype", Name("XML"))
	ms.Dict.Set("Length", Integer(len(xmp)))
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.7"}
	d.Objects[2] = &IndirectObject{Number: 2, Value: ms}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Metadata", IndirectRef{Number: 2})
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

// validXMP wraps property XML in a well-formed XMP packet with a properly
// namespaced rdf:RDF element.
func validXMP(body string) string {
	return `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>` +
		`<x:xmpmeta xmlns:x="adobe:ns:meta/">` +
		`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">` +
		body +
		`</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
}

func TestXMPWellFormedStreaming(t *testing.T) {
	cases := []struct {
		name            string
		xmp             string
		wantWF, wantRDF bool
	}{
		{"well-formed with rdf", validXMP(`<dc:title>x</dc:title>`), true, true},
		{"well-formed no rdf", `<x:xmpmeta xmlns:x="adobe:ns:meta/"><foo/></x:xmpmeta>`, true, false},
		{"malformed mismatched tag", `<a><b></a>`, false, false},
		{"empty", ``, false, false},
		{"text only, no element", `just some text`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, rdf := xmpWellFormed([]byte(tc.xmp))
			if wf != tc.wantWF || rdf != tc.wantRDF {
				t.Errorf("xmpWellFormed = (%v,%v), want (%v,%v)", wf, rdf, tc.wantWF, tc.wantRDF)
			}
		})
	}
}

// TestXMPStreamingMatchesTree confirms the streaming well-formedness check
// agrees with the previous parseXMLTree + findRDF result across representative
// packets — the streaming path must not change any validation outcome.
func TestXMPStreamingMatchesTree(t *testing.T) {
	packets := []string{
		validXMP(`<dc:title>hello</dc:title>`),
		`<x:xmpmeta xmlns:x="adobe:ns:meta/"><foo/></x:xmpmeta>`,
		`<RDF:RDF xmlns:x="adobe:ns:meta/"><a/></RDF:RDF>`, // undeclared RDF prefix: well-formed, no proper rdf:RDF
		`<a><b></a>`,
		``,
		`   `,
	}
	for i, p := range packets {
		wf, rdf := xmpWellFormed([]byte(p))
		tree, err := parseXMLTree([]byte(p))
		treeWF := err == nil
		treeRDF := treeWF && findRDF(tree) != nil
		if wf != treeWF || rdf != treeRDF {
			t.Errorf("packet %d: streaming (%v,%v) != tree (%v,%v)", i, wf, rdf, treeWF, treeRDF)
		}
	}
}

// TestXMPLargePacketBounded is the DoS regression: a large but perfectly
// well-formed XMP packet must not build a node tree (an O(n²) blow-up), yet its
// well-formedness must still be validated and no false positive raised. With the
// property-parse cap lowered, the property extraction is skipped while the
// streaming well-formedness check still passes.
func TestXMPLargePacketBounded(t *testing.T) {
	orig := xmpPropertyMaxBytes
	xmpPropertyMaxBytes = 4 << 10 // 4 KiB, for the test
	defer func() { xmpPropertyMaxBytes = orig }()

	var b strings.Builder
	for i := 0; b.Len() < 64<<10; i++ { // 64 KiB of valid properties, well over the cap
		fmt.Fprintf(&b, `<dc:item%d>value %d</dc:item%d>`, i, i, i)
	}
	xmp := validXMP(b.String())
	doc := docWithXMP([]byte(xmp))

	// Property extraction is skipped (capped), reported as an error the caller
	// turns into "no properties to check" — never a violation.
	if _, err := parseXMPProperties([]byte(xmp)); err == nil {
		t.Error("expected parseXMPProperties to refuse the oversized packet")
	}
	// Well-formedness still validated by streaming, with no false positive.
	for _, e := range checkXMPWellFormed(doc, PDFA1b) {
		t.Errorf("unexpected well-formedness violation on a valid large packet: %s", e.Message)
	}
	// The property check must not flag anything on the capped packet.
	if errs := checkXMPProperties(doc, PDFA1b); len(errs) != 0 {
		t.Errorf("unexpected property violations on a capped packet: %v", errs)
	}
}

// TestXMPManyElementsFast guards the quadratic-GC blow-up: validating a document
// whose XMP has a very large number of elements must stay fast. Before the fix,
// building the tree for a packet with hundreds of thousands of elements took
// tens of seconds; here even a large element count completes well under a second
// because the tree is never built.
func TestXMPManyElementsFast(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200000; i++ {
		fmt.Fprintf(&b, `<dc:i%d>v</dc:i%d>`, i, i)
	}
	xmp := validXMP(b.String())
	doc := docWithXMP([]byte(xmp))
	start := time.Now()
	_ = checkXMPWellFormed(doc, PDFA1b)
	_ = checkXMPProperties(doc, PDFA1b)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("XMP checks on a many-element packet took %v; expected sub-second", d)
	}
}
