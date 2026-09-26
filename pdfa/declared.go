package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
)

// DeclaredLevel reads the PDF/A conformance level a document claims via
// its XMP pdfaid:part / pdfaid:conformance identifiers.
//
// The part is read through the XMP model, like every other identification
// reader (readPDFAIdentification). A packet pdf0 declined to model — over the
// size limit, say — answers false: nothing was declared that pdf0 could read,
// and the trip is on the run for the caller's report.
func DeclaredLevel(doc core.View) (Level, bool) {
	switch readPDFAIdentification(doc).part {
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
