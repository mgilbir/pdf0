package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the half of PDF/UA-2's version identification (ISO 14289-2
// clause 5, Table 1) that PDF/UA-1 does not have: the pdfuaid:rev property.
// checkUAIdentifier (pdfua.go) owns pdfuaid:part for both parts.

// ua2Revision is the value ISO 14289-2:2024 gives pdfuaid:rev: "the four
// digits of the year of publication or revision", which for this edition is
// 2024. The veraPDF PDF/UA-2 profile tests exactly this string (5-t05).
const ua2Revision = "2024"

// checkUA2Revision is ISO 14289-2 clause 5 for pdfuaid:rev, as veraPDF reads
// it (5-t04, 5-t05): the property is required, is "2024", and is written with
// the prefix pdfuaid — a property in the PDF/UA identification namespace
// bound to another prefix (corpus 5-t04-fail-a writes pdfuadd:rev) is the
// property, and is reported for its prefix only.
//
// It judges a packet that declares a PDF/UA part. With no pdfuaid:part, or no
// metadata, or metadata that is not XML, checkUAIdentifier has already said
// the file declares no PDF/UA identification at all, and the revision of an
// identification that is not there is no second finding.
func checkUA2Revision(d core.View, cat *object.Dictionary) []Violation {
	stream, ok := d.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return nil
	}
	packet, status := d.XMPPacketOf(stream, object.RefNum(cat.Get("Metadata")))
	if status != core.XMPParsed {
		return nil
	}
	if _, has := packet.Text(xmp.NSPDFUAID, "part"); !has {
		return nil
	}
	rev, has := packet.Text(xmp.NSPDFUAID, "rev")
	if !has {
		return []Violation{{Clause: "5", Message: "XMP metadata does not declare the PDF/UA revision (pdfuaid:rev)"}}
	}
	var v []Violation
	if rev != ua2Revision {
		v = append(v, Violation{Clause: "5", Message: "pdfuaid:rev must be " + ua2Revision + " for PDF/UA-2, got " + quote(rev)})
	}
	for _, p := range packet.Lookup(xmp.NSPDFUAID, "rev") {
		if p.Prefix != "pdfuaid" && p.Prefix != "" {
			v = append(v, Violation{Clause: "5", Message: "PDF/UA identification property 'rev' uses namespace prefix '" + p.Prefix + "', must be 'pdfuaid'"})
			break
		}
	}
	return v
}
