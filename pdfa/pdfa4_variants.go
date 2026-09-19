package pdfa

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
)

// PDF/A-4e and PDF/A-4f, ISO 19005-4 Annexes B and A.
//
// Each is the base part plus a bargain: 4f may carry arbitrary embedded files
// and must actually carry some, 4e may carry 3D and RichMedia annotations and
// must keep their artwork to the formats the annex names. pdf0 already honoured
// the relaxations and the requirements, reading the variant out of the file's
// own pdfaid:conformance, which is enough for every rule but one.
//
// The one is what makes these levels. A part-4 file with no conformance is a
// valid *plain* PDF/A-4 file — base rule 6.7.3-3 says a file conforming to
// neither variant shall not provide one — so a file that ought to have declared
// E is wrong only relative to a caller who asked for PDF/A-4e. Nothing readable
// from the document can answer that; the target has to.
//
// Validation follows the Level A pattern: run the base pipeline, adopt its
// findings at this level, and add what the variant asks for.

// ValidateVariant4View validates a PDF/A-4 variant level (4e or 4f).
func ValidateVariant4View(doc core.View, level Level, rawData []byte) []Violation {
	// Every base PDF/A-4 requirement applies. The base pipeline permits an
	// absent, "F" or "E" conformance, because at that level all three are
	// legal; here exactly one is, so its finding is dropped and re-checked
	// below rather than reported alongside a more specific one.
	base := ValidateView(doc, level.BaseB(), rawData)
	errs := make([]Violation, 0, len(base))
	for _, e := range base {
		if strings.Contains(e.Message, "pdfaid:conformance must be absent, F, or E") {
			continue
		}
		e.Level = level
		errs = append(errs, e)
	}

	for _, check := range []func(core.View, Level) []Violation{
		checkVariant4Conformance,
	} {
		if doc.Cancel.Stopped() {
			break
		}
		errs = append(errs, runCheck(doc, level, check)...)
	}

	// Re-sort now that the variant families have appended, so this level
	// returns findings in the same order every other validator promises.
	finding.Sort(errs)
	return errs
}

// effectiveVariant is which PDF/A-4 variant's requirements apply: the level's,
// when the caller named one, and otherwise the one the document declares for
// itself.
//
// Both matter. Validating at PDFA4 reads the declaration, which is how a
// document that says it is a 4f is held to 4f without the caller having to
// know. Validating at PDFA4F applies 4f whatever the document says — including
// to a document that says nothing, which is the case the level exists for.
func effectiveVariant(doc core.View, level Level) string {
	if v := level.variantConformance(); v != "" {
		return v
	}
	if level.BaseB() != PDFA4 {
		return ""
	}
	switch c := pdfaConformanceFlag(doc); c {
	case "E", "F":
		return c
	}
	return ""
}

// conformanceAsWritten is pdfaid:conformance exactly as the document spells it,
// and whether the property is there at all.
//
// pdfaConformanceFlag uppercases, which is what the relaxations want — a file
// saying "e" is trying to be a 4e and is treated as one, so the stricter rules
// apply to it rather than being skipped. The value rule is the opposite: the
// property is case-sensitive and "e" is not "E", which is exactly what
// 6-7-3-t01-fail-c is built to catch.
func conformanceAsWritten(doc core.View) (string, bool) {
	catalog := doc.Catalog()
	if catalog == nil {
		return "", false
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*object.Stream)
	if !ok {
		return "", false
	}
	xmp := doc.XMPText(stream)
	if !xmpHasKey(xmp, "pdfaid:conformance") {
		return "", false
	}
	if v := core.ExtractXMPValue(xmp, "pdfaid:conformance"); v != "" {
		return v, true
	}
	return ExtractXMPAttr(xmp, "pdfaid:conformance"), true
}

// checkVariant4Conformance: a PDF/A-4e file shall declare pdfaid:conformance E,
// and a PDF/A-4f file shall declare F (ISO 19005-4 6.7.3).
//
// This is the rule that cannot be read out of the document, and the reason the
// variants are levels. A file with no conformance at all is the interesting
// case: it is a conforming plain PDF/A-4 file, and it is not the 4e file the
// caller asked to validate.
func checkVariant4Conformance(doc core.View, level Level) []Violation {
	want := level.variantConformance()
	if want == "" {
		return nil
	}
	got, present := conformanceAsWritten(doc)
	if got == want {
		return nil
	}
	rule := metadataClause("version", level.BaseB())
	switch {
	case !present:
		return []Violation{{Rule: rule, Level: level,
			Message: fmt.Sprintf("the document declares no pdfaid:conformance, so it "+
				"identifies itself as plain PDF/A-4 rather than %s, which must declare %q",
				level, want)}}
	case got == "":
		return []Violation{{Rule: rule, Level: level,
			Message: fmt.Sprintf("pdfaid:conformance is present but empty; %s must declare %q",
				level, want)}}
	default:
		// Including the right letter in the wrong case, which is the whole of
		// the difference for a case-sensitive property.
		return []Violation{{Rule: rule, Level: level,
			Message: fmt.Sprintf("pdfaid:conformance is %q; %s must declare %q", got, level, want)}}
	}
}
