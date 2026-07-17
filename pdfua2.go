package pdf0

import (
	"fmt"
	"strings"
)

// This file validates PDF/UA-2 (ISO 14289-2:2024), the PDF 2.0 accessibility
// standard succeeding PDF/UA-1. PDF/UA-2 shares most of PDF/UA-1's requirements
// — a tagged logical structure, a default language, a shown document title,
// Unicode-mapped text, correctly used artifacts and headings — but is based on
// PDF 2.0 and identifies itself with pdfuaid:part 2 (and a namespaced structure
// model).
//
// This reuses the PDF/UA-1 structural checks (which carry over) and adds the
// PDF/UA-2 identification and version requirements. The PDF 2.0 namespaced
// structure model is not yet checked in depth, and no PDF/UA-2 conformance
// corpus is bundled, so this does not assert full ISO 14289-2 conformance.

// ValidatePDFUA2 checks a document against PDF/UA-2. Findings reuse the UAViolation
// type; clause identifiers follow ISO 14289-2.
func (d *Document) ValidatePDFUA2() []UAViolation {
	// The PDF/UA-1 checks that carry over to PDF/UA-2: tagging, structure tree,
	// default language, displayed title, Unicode mapping, artifacts, headings.
	var out []UAViolation
	for _, e := range ValidatePDFUA(d) {
		// PDF/UA-1 requires pdfuaid:part 1 and a PDF 1.x header; PDF/UA-2 requires
		// part 2 and PDF 2.0, so drop those two PDF/UA-1-specific findings and
		// re-check them for PDF/UA-2 below.
		if strings.Contains(e.Message, "pdfuaid:part must be 1") ||
			strings.Contains(e.Message, "requires a PDF 1.x header") {
			continue
		}
		out = append(out, e)
	}

	// PDF/UA-2 is defined against PDF 2.0.
	if maj, _, ok := parsePDFVersion(d.Version); ok && maj != 2 {
		out = append(out, UAViolation{"4", fmt.Sprintf("PDF/UA-2 is defined for PDF 2.0; file declares %s", d.Version), 0})
	}

	// Identification: the XMP must declare pdfuaid:part 2. Absence and a wrong
	// namespace prefix are already reported by the reused PDF/UA-1 checks.
	if part := xmpPDFUAPart(documentXMP(d)); part != "" && part != "2" {
		out = append(out, UAViolation{"5", "pdfuaid:part must be 2 for PDF/UA-2, got " + part, 0})
	}
	return out
}
