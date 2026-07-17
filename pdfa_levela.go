package pdf0

import (
	"fmt"
	"strings"
)

// This file implements PDF/A Level A (accessible) conformance. Level A is Level
// B plus the accessibility requirements: a tagged logical structure, a natural-
// language specification, Unicode character mapping, and a Level A conformance
// declaration. Validation runs the Level B checks and adds these families; the
// tagged-structure and language checks mirror the PDF/UA logic (ISO 14289),
// which pdf0 already validates.

// validatePDFALevelA validates a Level A conformance level (1a/2a/3a).
func validatePDFALevelA(doc *Document, level PDFALevel, rawData []byte) []ValidationError {
	// All Level B requirements apply, so run the Level B pipeline and adopt its
	// findings at this level. The Level B pipeline requires pdfaid:conformance
	// "B"; at Level A it must be "A", so that one Level B finding is dropped and
	// re-checked below.
	base := ValidatePDFABytes(doc, level.baseB(), rawData)
	errs := make([]ValidationError, 0, len(base))
	for _, e := range base {
		if strings.Contains(e.Message, "pdfaid:conformance must be B") {
			continue
		}
		e.Level = level
		errs = append(errs, e)
	}

	errs = append(errs, checkLevelAConformance(doc, level)...)
	errs = append(errs, checkLevelAStructure(doc, level)...)
	errs = append(errs, checkLevelALanguage(doc, level)...)
	return errs
}

// levelAClause returns the ISO 19005 clause identifier for a Level A concept,
// which is numbered differently in part 1 (1a) than in parts 2/3 (2a/3a).
func levelAClause(concept string, level PDFALevel) string {
	part1 := level == PDFA1a
	switch concept {
	case "structure": // Tagged PDF / logical structure
		if part1 {
			return "6.8.2.2"
		}
		return "6.7.2.2"
	case "language": // Natural language specification
		if part1 {
			return "6.8.4"
		}
		return "6.7.3.3"
	case "conformance": // Version and conformance identification
		if part1 {
			return "6.7.11"
		}
		return "6.6.4"
	}
	return "6.8"
}

// documentXMP returns the document's decoded XMP metadata packet, or "".
func documentXMP(doc *Document) string {
	cat := getCatalog(doc)
	if cat == nil {
		return ""
	}
	stream, ok := doc.Resolve(cat.Get("Metadata")).(*Stream)
	if !ok {
		return ""
	}
	return decodeXMPToUTF8(decodeContentStream(doc, stream))
}

// checkLevelAConformance verifies the XMP declares Level A conformance
// (pdfaid:conformance = "A").
func checkLevelAConformance(doc *Document, level PDFALevel) []ValidationError {
	xmp := documentXMP(doc)
	if xmp == "" {
		return nil // a missing metadata stream is reported by the Level B checks
	}
	if !xmpHasKey(xmp, "pdfaid:conformance") {
		return []ValidationError{{
			Rule:    levelAClause("conformance", level),
			Level:   level,
			Message: "metadata must declare pdfaid:conformance A for Level A",
		}}
	}
	if conf := extractXMPValue(xmp, "pdfaid:conformance"); conf != "A" {
		return []ValidationError{{
			Rule:    levelAClause("conformance", level),
			Level:   level,
			Message: fmt.Sprintf("pdfaid:conformance must be A, got %q", conf),
		}}
	}
	return nil
}

// checkLevelAStructure verifies the file is a Tagged PDF with a logical
// structure tree (ISO 19005-1 6.8.2 / -2/-3 6.7.2). It mirrors the PDF/UA
// tagged-PDF requirement.
func checkLevelAStructure(doc *Document, level PDFALevel) []ValidationError {
	cat := getCatalog(doc)
	if cat == nil {
		return nil // reported by the Level B checks
	}
	var errs []ValidationError
	mark := doc.ResolveDict(cat.Get("MarkInfo"))
	if mark == nil || !doc.isTrue(mark.Get("Marked")) {
		errs = append(errs, ValidationError{
			Rule:    levelAClause("structure", level),
			Level:   level,
			Message: "a Level A file shall be a Tagged PDF (catalog /MarkInfo << /Marked true >>)",
		})
	}
	if cat.Get("StructTreeRoot") == nil {
		errs = append(errs, ValidationError{
			Rule:    levelAClause("structure", level),
			Level:   level,
			Message: "a Level A file shall contain a logical structure tree (catalog /StructTreeRoot)",
		})
	}
	return errs
}

// checkLevelALanguage verifies that a natural language is specified for the
// document. A default language on the catalog (/Lang) satisfies it; when absent,
// the language must be given on the structure elements. Only a syntactically
// invalid catalog /Lang, or the absence of any language specification, is
// flagged — matching the Level B leniency (a valid /Lang is not otherwise
// mandatory in Level B, so requiring one at Level A must not false-positive on
// files that carry language on structure elements instead).
func checkLevelALanguage(doc *Document, level PDFALevel) []ValidationError {
	cat := getCatalog(doc)
	if cat == nil {
		return nil
	}
	if s, ok := doc.Resolve(cat.Get("Lang")).(String); ok && len(s.Value) > 0 {
		// /Lang is a PDF text string: it may be UTF-16BE (with a BOM) or
		// PDFDocEncoded, so decode it before checking the language-tag syntax.
		lang := decodePDFTextString(s.Value)
		if !validBCP47(lang) {
			return []ValidationError{{
				Rule:    levelAClause("language", level),
				Level:   level,
				Message: fmt.Sprintf("catalog /Lang %q is not a valid language identifier", lang),
			}}
		}
	}
	return nil
}
