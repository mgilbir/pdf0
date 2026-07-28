package pdf0

import (
	"context"
	"fmt"
)

// This file validates PDF/UA-2 (ISO 14289-2:2024), the PDF 2.0 accessibility
// standard succeeding PDF/UA-1. PDF/UA-2 shares most of PDF/UA-1's requirements
// — a tagged logical structure, a default language, a shown document title,
// Unicode-mapped text, correctly used artifacts and headings — but is based on
// PDF 2.0 and identifies itself with pdfuaid:part 2 (and a namespaced structure
// model).
//
// This reuses the PDF/UA-1 structural checks (which carry over) and adds the
// PDF/UA-2 identification and version requirements. Known scope limits: the
// structure-type checks resolve against the ISO 32000-1 standard types and the
// classic /RoleMap only — a file using the PDF 2.0 namespaced structure model
// (/NS, /RoleMapNS, the 2.0 structure namespace) in ways PDF/UA-2 permits may
// be over-flagged — and no PDF/UA-2 conformance corpus is bundled, so this
// does not assert full ISO 14289-2 conformance.

// ValidatePDFUA2 checks a document against PDF/UA-2. Findings reuse the UAViolation
// type; clause identifiers follow ISO 14289-2.
func ValidatePDFUA2(d *Document) []UAViolation {
	return validatePDFUA2(canceler{}, d)
}

// ValidatePDFUA2Context is ValidatePDFUA2 with cancellation; see
// ValidatePDFUAContext for how a cancelled run reports itself.
func ValidatePDFUA2Context(ctx context.Context, d *Document) []UAViolation {
	return validatePDFUA2(newCanceler(ctx), d)
}

func validatePDFUA2(cancel canceler, d *Document) []UAViolation {
	// The shared checks (tagging, structure tree, default language, displayed
	// title, Unicode mapping, artifacts, headings), parameterized for part 2 so
	// the identification rule requires pdfuaid:part 2 and the UA-1 header rule
	// is not run at all.
	out := validatePDFUA(cancel, d, "2")

	// PDF/UA-2 is defined against PDF 2.0.
	out = append(out, runUACheck(func() []UAViolation {
		if maj, _, ok := parsePDFVersion(d.Version); ok && maj != 2 {
			return []UAViolation{{"4", fmt.Sprintf("PDF/UA-2 is defined for PDF 2.0; file declares %s", d.Version), 0}}
		}
		return nil
	})...)

	// validatePDFUA sorted its own findings; re-sort now that the UA-2 rule has
	// appended to them (audit C27).
	sortViolations(out)
	return out
}
