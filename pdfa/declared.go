package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// DeclaredLevel reads the PDF/A conformance level a document claims via
// its XMP pdfaid:part / pdfaid:conformance identifiers.
func DeclaredLevel(doc core.View) (PDFALevel, bool) {
	catalog := doc.Catalog()
	if catalog == nil {
		return 0, false
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*object.Stream)
	if !ok {
		return 0, false
	}
	xmp := doc.XMPText(stream)
	part := core.ExtractXMPValue(xmp, "pdfaid:part")
	if part == "" {
		part = ExtractXMPAttr(xmp, "pdfaid:part")
	}
	switch part {
	case "1":
		return PDFA1b, true
	case "2":
		return PDFA2b, true
	case "3":
		return PDFA3b, true
	case "4":
		return PDFA4, true
	}
	return 0, false
}
