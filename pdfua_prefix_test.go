package pdf0

import "testing"

func TestXMPElementPrefixes(t *testing.T) {
	xmp := `<pdfuaia:amd>A</pdfuaia:amd> <pdfuaid:part>1</pdfuaid:part> <x:amd/>`
	got := xmpElementPrefixes(xmp, "amd")
	want := map[string]bool{"pdfuaia": true, "x": true}
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 prefixes", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected prefix %q", p)
		}
	}
	if len(xmpElementPrefixes(xmp, "part")) != 1 || xmpElementPrefixes(xmp, "part")[0] != "pdfuaid" {
		t.Errorf("part prefixes = %v", xmpElementPrefixes(xmp, "part"))
	}
}

func TestUAIdentifierPrefix(t *testing.T) {
	d := &Document{}
	// Wrong prefix bound to the pdfua-id namespace -> flagged.
	bad := `xmlns:pdfuaia="http://www.aiim.org/pdfua/ns/id/" <pdfuaia:amd>A</pdfuaia:amd>`
	if len(d.checkUAIdentifierPrefix(bad)) == 0 {
		t.Error("wrong pdfuaid prefix not flagged")
	}
	// Correct prefix -> clean.
	good := `xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" <pdfuaid:amd>A</pdfuaid:amd>`
	if len(d.checkUAIdentifierPrefix(good)) != 0 {
		t.Error("correct pdfuaid prefix wrongly flagged")
	}
	// An unrelated 'amd' element not bound to the pdfua-id namespace -> ignored.
	unrelated := `<foo:amd>x</foo:amd>`
	if len(d.checkUAIdentifierPrefix(unrelated)) != 0 {
		t.Error("unrelated amd element wrongly flagged")
	}
}
