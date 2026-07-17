package pdf0

import (
	"fmt"
	"strings"
)

// This file implements validation for PDF/VT-1 (ISO 16612-2), the self-contained
// variable-and-transactional print exchange format. A PDF/VT-1 file is a
// conforming PDF/X-4 file (ISO 15930-7) that additionally carries a document
// part (DPart) hierarchy describing its record boundaries and identifies itself
// as PDF/VT-1 in XMP metadata. This validator composes the PDF/X-4 and DPart
// checks with the PDF/VT-specific requirements; it is calibrated against the
// valid Cal Poly PDF/VT-1 test suite.

// PDFVTViolation reports a way in which a document departs from PDF/VT-1.
type PDFVTViolation struct {
	Rule    string // short rule identifier, base-profile violations prefixed "pdfx-4/" or "dpart/"
	Message string
	Object  int // object number the violation anchors to, 0 if N/A
}

func (v PDFVTViolation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("PDF/VT-1 %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/VT-1 %s: %s", v.Rule, v.Message)
}

// ValidatePDFVT checks whether doc conforms to PDF/VT-1 (ISO 16612-2). It
// requires conformance to the PDF/X-4 base profile, a valid document part
// hierarchy, and PDF/VT-1 identification in XMP. An empty result means no
// violations were found.
// ValidatePDFVT checks whether doc conforms to PDF/VT-1 (ISO 16612-2): a
// conforming PDF/X-4 file, identified as PDF/VT-1, with a document part
// hierarchy.
func ValidatePDFVT(doc *Document) []PDFVTViolation {
	return validatePDFVTImpl(doc, "PDF/VT-1", false)
}

// ValidatePDFVT2 checks whether doc conforms to PDF/VT-2 (ISO 16612-2). PDF/VT-2
// is based on PDF/X-5 rather than PDF/X-4, so it additionally permits externally
// referenced content (reference XObjects); it is otherwise validated like
// PDF/VT-1. pdf0 has no PDF/X-5 validator, so the PDF/X-4 base is used with the
// reference-XObject prohibition relaxed — the PDF/X-5-specific external-reference
// rules are not asserted.
func ValidatePDFVT2(doc *Document) []PDFVTViolation {
	return validatePDFVTImpl(doc, "PDF/VT-2", true)
}

func validatePDFVTImpl(doc *Document, versionPrefix string, allowRefXObjects bool) []PDFVTViolation {
	var out []PDFVTViolation
	add := func(rule, msg string, obj int) {
		out = append(out, PDFVTViolation{Rule: rule, Message: msg, Object: obj})
	}

	// A PDF/VT file shall be a conforming PDF/X file (ISO 16612-2 6.1): PDF/X-4
	// for PDF/VT-1, PDF/X-5 for PDF/VT-2. For PDF/VT-2 the reference-XObject
	// prohibition (a PDF/X-4-only rule that PDF/X-5 lifts) is dropped.
	for _, v := range ValidatePDFX(doc, PDFX4) {
		if allowRefXObjects && v.Rule == "forbidden" && strings.Contains(v.Message, "reference XObjects") {
			continue
		}
		add("pdfx-4/"+v.Rule, v.Message, v.Object)
	}

	// Identification: the XMP pdfvtid:GTS_PDFVTVersion property shall be present
	// and identify the requested PDF/VT version (ISO 16612-2 6.2).
	claimed := ""
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat != nil {
		if ms, ok := doc.Resolve(cat.Get("Metadata")).(*Stream); ok {
			xmp := decodeXMPToUTF8(decodeContentStream(doc, ms))
			claimed = strings.TrimSpace(extractXMPValue(xmp, "pdfvtid:GTS_PDFVTVersion"))
		}
	}
	switch {
	case claimed == "":
		add("identification", "file is not identified as PDF/VT (no XMP pdfvtid:GTS_PDFVTVersion)", 0)
	case !strings.HasPrefix(claimed, versionPrefix):
		add("identification", fmt.Sprintf("GTS_PDFVTVersion %q does not identify %s", claimed, versionPrefix), 0)
	}

	// A document part hierarchy is required (ISO 16612-2 6.3): its leaves define
	// the record structure PDF/VT exists to convey.
	if cat == nil || cat.Get("DPartRoot") == nil {
		add("dpart", "PDF/VT requires a document part hierarchy (catalog /DPartRoot)", 0)
	}
	for _, v := range ValidateDParts(doc) {
		add("dpart/"+v.Rule, v.Message, v.Object)
	}

	return out
}
