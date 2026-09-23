package pdfua

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/xmp"
)

func TestUAIdentifierPrefix(t *testing.T) {
	parse := func(s string) *xmp.Packet {
		t.Helper()
		p, err := xmp.Parse([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Wrong prefix bound to the pdfua-id namespace -> flagged.
	bad := uaPacket(` xmlns:pdfuaia="http://www.aiim.org/pdfua/ns/id/"`, `<pdfuaia:amd>A</pdfuaia:amd>`)
	if len(checkUAIdentifierPrefix(parse(bad))) == 0 {
		t.Error("wrong pdfuaid prefix not flagged")
	}
	// The same, in attribute form -> flagged too.
	badAttr := uaPacket(` xmlns:pdfuaia="http://www.aiim.org/pdfua/ns/id/" pdfuaia:corr="1"`, ``)
	if len(checkUAIdentifierPrefix(parse(badAttr))) == 0 {
		t.Error("wrong pdfuaid prefix in attribute form not flagged")
	}
	// Correct prefix -> clean.
	good := uaPacket(` xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/"`, `<pdfuaid:amd>A</pdfuaid:amd>`)
	if len(checkUAIdentifierPrefix(parse(good))) != 0 {
		t.Error("correct pdfuaid prefix wrongly flagged")
	}
	// An unrelated 'amd' property not in the pdfua-id namespace -> ignored.
	unrelated := uaPacket(` xmlns:foo="urn:foo"`, `<foo:amd>x</foo:amd>`)
	if len(checkUAIdentifierPrefix(parse(unrelated))) != 0 {
		t.Error("unrelated amd element wrongly flagged")
	}
}
