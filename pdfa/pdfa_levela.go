package pdfa

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"strings"
)

// This file implements PDF/A Level A (accessible) conformance. Level A is Level
// B plus the accessibility requirements: a tagged logical structure, a natural-
// language specification, Unicode character mapping, and a Level A conformance
// declaration. Validation runs the Level B checks and adds these families; the
// tagged-structure and language checks mirror the PDF/UA logic (ISO 14289),
// which pdf0 already validates.

// ValidateLevelAView validates a Level A conformance level (1a/2a/3a).
func ValidateLevelAView(doc core.View, level Level, rawData []byte) []Violation {
	// All Level B requirements apply, so run the Level B pipeline and adopt its
	// findings at this level. The Level B pipeline requires pdfaid:conformance
	// "B"; at Level A it must be "A", so that one Level B finding is dropped and
	// re-checked below.
	base := ValidateView(doc, level.BaseB(), rawData)
	errs := make([]Violation, 0, len(base))
	for _, e := range base {
		if strings.Contains(e.Message, "pdfaid:conformance must be B") {
			continue
		}
		e.Level = level
		errs = append(errs, e)
	}

	// Run the Level A checks through runCheck like every Level B check, so a
	// panic on hostile input becomes a reported "internal" finding rather than
	// crashing the caller — the asymmetry runCheck exists to prevent (audit C27).
	// A cancelled run abandons the ones it has not started; the finding that
	// says so is already in base, carried over by the loop above.
	for _, check := range []func(core.View, Level) []Violation{
		checkLevelAConformance, checkLevelAStructure, checkLevelAArtifacts,
		checkLevelAStructTypes, checkLevelALanguage, checkLevelAToUnicode,
		checkLevelAActualText,
	} {
		if doc.Cancel.Stopped() {
			break
		}
		errs = append(errs, runCheck(doc, level, check)...)
	}

	// validatePDFABytes sorted the Level B findings; re-sort now that the Level A
	// families have appended to them, so Level A returns findings in the same
	// "by rule, then object, then message" order every other validator promises
	// (validatePDFUA2 does the same for its extra rule).
	finding.Sort(errs)
	return errs
}

// levelAClause returns the ISO 19005 clause identifier for a Level A concept,
// which is numbered differently in part 1 (1a) than in parts 2/3 (2a/3a).
func levelAClause(concept string, level Level) string {
	part1 := level == PDFA1a
	switch concept {
	case "structure": // Tagged PDF / logical structure
		if part1 {
			return "6.8.2.2"
		}
		return "6.7.2.2"
	case "toUnicode": // A rendered font must map its codes to Unicode
		if part1 {
			return "6.3.8"
		}
		return "6.2.11.7.2"
	case "actualText": // Private Use Area code points need replacement text
		return "6.2.11.7.3" // parts 2/3 only; ISO 19005-1 has no equivalent
	case "artifacts": // Content is either tagged or marked as an artifact
		if part1 {
			return "6.8.3.2"
		}
		return "6.7.3.2"
	case "structTypes": // Structure types and the role map
		if part1 {
			return "6.8.3.4"
		}
		return "6.7.3.4"
	case "language": // Natural language specification
		if part1 {
			return "6.8.4"
		}
		return "6.7.4"
	case "conformance": // Version and conformance identification
		if part1 {
			return "6.7.11"
		}
		return "6.6.4"
	}
	return "6.8"
}

// checkLevelAConformance verifies the XMP declares Level A conformance
// (pdfaid:conformance = "A").
func checkLevelAConformance(doc core.View, level Level) []Violation {
	xmp := doc.DocumentXMP()
	if xmp == "" {
		return nil // a missing metadata stream is reported by the Level B checks
	}
	if !xmpHasKey(xmp, "pdfaid:conformance") {
		return []Violation{{
			Rule:    levelAClause("conformance", level),
			Level:   level,
			Message: "metadata must declare pdfaid:conformance A for Level A",
		}}
	}
	if conf := core.ExtractXMPValue(xmp, "pdfaid:conformance"); conf != "A" {
		return []Violation{{
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
func checkLevelAStructure(doc core.View, level Level) []Violation {
	cat := doc.Catalog()
	if cat == nil {
		return nil // reported by the Level B checks
	}
	var errs []Violation
	mark := doc.ResolveDict(cat.Get("MarkInfo"))
	if mark == nil || !doc.IsTrue(mark.Get("Marked")) {
		errs = append(errs, Violation{
			Rule:    levelAClause("structure", level),
			Level:   level,
			Message: "a Level A file shall be a Tagged PDF (catalog /MarkInfo << /Marked true >>)",
		})
	}
	if cat.Get("StructTreeRoot") == nil {
		errs = append(errs, Violation{
			Rule:    levelAClause("structure", level),
			Level:   level,
			Message: "a Level A file shall contain a logical structure tree (catalog /StructTreeRoot)",
		})
	}
	return errs
}

// checkLevelAArtifacts enforces the requirement that makes the tagging mean
// something (ISO 19005-1 6.8.3.2, -2/-3 6.7.3.2): everything a page paints is
// either part of the logical structure or marked as an artifact.
//
// Without it, Level A is three presence checks. A document may set /MarkInfo
// /Marked true, carry a /StructTreeRoot describing nothing at all, paint a page
// of graphics and text outside every marked-content sequence, and be declared
// conforming — which is exactly the file the level exists to rule out, since a
// reader following the structure tree finds nothing on it.
func checkLevelAArtifacts(doc core.View, level Level) []Violation {
	var errs []Violation
	for _, page := range levelAContent(doc).untagged {
		errs = append(errs, Violation{
			Rule:    levelAClause("artifacts", level),
			Level:   level,
			Message: "page content is painted outside any marked-content sequence; a Level A file shall tag its real content or mark it as an artifact",
			Object:  page,
		})
	}
	return errs
}

// checkLevelAActualText enforces ISO 19005-2/-3 6.2.11.7.3: a character mapped
// to a Private Use Area code point carries no meaning of its own, so replacement
// text must be supplied for it — an /ActualText entry on an enclosing
// marked-content sequence, or on the structure element that sequence belongs to.
//
// The rule holds regardless of text rendering mode. Invisible text is exactly
// what a scanned page's OCR layer is made of, and it is the layer a reader
// extracts from, so exempting mode 3 here would exempt the case the rule exists
// for. ISO 19005-1 states no equivalent requirement, so this runs at parts 2
// and 3 only.
func checkLevelAActualText(doc core.View, level Level) []Violation {
	if level == PDFA1a {
		return nil
	}
	var errs []Violation
	for _, p := range levelAContent(doc).pua {
		errs = append(errs, Violation{
			Rule:    levelAClause("actualText", level),
			Level:   level,
			Message: fmt.Sprintf("text shows U+%04X, a Private Use Area code point, with no ActualText to replace it", p.r),
			Object:  p.objNum,
		})
	}
	return errs
}

// checkLevelAStructTypes enforces the structure-type rules (ISO 19005-1 6.8.3.4,
// -2/-3 6.7.3.4): a structure element whose type is not one of the standard ones
// shall be mapped, through the structure tree's /RoleMap, to a standard type,
// and that mapping shall not be circular.
//
// Both rules are scoped to the non-standard types the document actually uses,
// which is what the veraPDF profile scopes them to as well (its object is the
// non-standard structure element, not the role map). The distinction is not
// academic: a role map that maps a standard type to itself is a cycle in the
// abstract, and files carrying one — /Document -> /Document alongside the real
// offender — sit in the corpus as *conforming*. Judging the map as a whole would
// reject them.
func checkLevelAStructTypes(doc core.View, level Level) []Violation {
	cat := doc.Catalog()
	if cat == nil {
		return nil
	}
	root := doc.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil // the missing structure tree is reported by checkLevelAStructure
	}
	roleMap := doc.ResolveDict(root.Get("RoleMap"))
	rule := levelAClause("structTypes", level)

	var errs []Violation
	// One verdict per distinct type: each answer costs a walk of that type's
	// role-map chain, and a document repeats a handful of types over all its
	// elements.
	decided := map[object.Name]bool{}
	for _, n := range core.StructTree(doc, cat) {
		st := n.RawS
		if st == "" || core.StandardStructTypes[st] || decided[st] {
			continue
		}
		decided[st] = true
		// A budget trip leaves the mapping unknown, and unknown is not evidence
		// of a violation — the rule the whole package follows for a walk that
		// did not finish.
		if _, mapped, complete := core.ResolveRoleMapChain(doc, st, roleMap); !mapped && complete {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: fmt.Sprintf("non-standard structure type /%s is not mapped to a standard type by /RoleMap", st),
			})
		}
		if cyclic, complete := core.RoleMapChainCycles(doc, st, roleMap); cyclic && complete {
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: fmt.Sprintf("/RoleMap contains a circular mapping for structure type /%s", st),
			})
		}
	}
	return errs
}

// checkLevelALanguage verifies the syntax of every natural-language identifier
// the document declares. /Lang may be written in three places — the document
// catalogue, a structure element dictionary, and the property list of a
// marked-content sequence — and the rule (ISO 19005-1 6.8.4, -2/-3 6.7.4)
// applies to all three alike: wherever it appears, the value must be the empty
// string or a Language-Tag.
//
// The rule constrains the value, not the presence. A file that declares no
// language at all is not flagged here, matching the Level B leniency: language
// may legitimately be carried per structure element rather than on the
// catalogue, and demanding a catalogue /Lang would reject conforming files.
func checkLevelALanguage(doc core.View, level Level) []Violation {
	cat := doc.Catalog()
	if cat == nil {
		return nil
	}
	digits := languageSubtagsMayHaveDigits(level)
	var errs []Violation
	bad := func(where, lang string, obj int) {
		errs = append(errs, Violation{
			Rule:    levelAClause("language", level),
			Level:   level,
			Message: fmt.Sprintf("%s /Lang %q is not a valid language identifier", where, lang),
			Object:  obj,
		})
	}
	// A /Lang is a PDF text string: it may be UTF-16BE (with a BOM) or
	// PDFDocEncoded, so it is decoded before the tag syntax is judged. The
	// Cyrillic tag the corpus offers is written as UTF-16BE precisely so that a
	// checker reading raw bytes sees eight ASCII-looking ones.
	check := func(where string, o object.Object, obj int) {
		s, ok := doc.Resolve(o).(object.String)
		if !ok || len(s.Value) == 0 {
			return
		}
		if lang := core.DecodePDFTextString(s.Value); !core.ValidLanguageTag(lang, digits) {
			bad(where, lang, obj)
		}
	}

	check("catalog", cat.Get("Lang"), 0)
	for _, n := range core.StructTree(doc, cat) {
		obj := n.ObjNum
		if obj < 0 {
			obj = 0
		}
		check("structure element", n.Elem.Get("Lang"), obj)
	}
	for _, l := range levelAContent(doc).langs {
		if !core.ValidLanguageTag(l.value, digits) {
			bad("marked-content property list", l.value, l.objNum)
		}
	}
	return errs
}

// languageSubtagsMayHaveDigits reports whether a subtag of a /Lang value may
// contain digits at this conformance level. ISO 19005-1 is written against
// RFC 1766 (subtags are letters only) and ISO 19005-2/-3 against RFC 3066
// (alphanumeric), and the corpus asserts the difference from both sides: "en-12"
// is a failing file at 1a, "ru-petr1708" a passing one at 2a.
func languageSubtagsMayHaveDigits(level Level) bool {
	return level != PDFA1a
}
