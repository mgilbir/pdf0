package pdfa

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
)

// The target profile: what a validation run checks a document against.
//
// A PDF/A level is three independent facts — the part of ISO 19005 (1 to 4),
// the conformance level within it (a, b or u for parts 1-3; none for part 4)
// and, for part 4 only, a variant (e or f, Annexes B and A) — plus the revision
// part 4 is identified by. Every rule gates on one of those facts, and before
// this file they were spread out: some gates read the requested Level after it
// had been flattened to the part's b level, so a PDFA4F run never reached the
// 4f gates; others read the document's own declaration, so the same file gave
// different answers depending on what it claimed; and the declaration was
// mapped to a level twice, lossily (audit 2026-09-22 C33, C34, C64, C139).
//
// Now a Level names one target profile and nothing else, the profile is
// carried unflattened to every check, and each check asks it the question it
// means — Part, Conformance, IsA, variant, requiresUnicode. The document's own
// declaration is read in exactly one place, the identification rule, which
// compares it with the target (checkIdentification), and in exactly one
// mapping, LevelFor, which turns a declaration into the level it names.
// internal/lint's TestPDFAChecksAskTheProfile keeps checks from comparing a
// Level with a constant again.

// Level names a PDF/A target profile: the part, conformance level and variant
// a document is validated against or built to.
//
// The zero value is LevelDeclared, which is not a profile but an instruction:
// validate against the level the document itself declares.
type Level int

const (
	// LevelDeclared asks the validator to take the target from the document's
	// own pdfaid identification (see LevelFor). It is the zero value, so a
	// configuration that never set a level validates against the claim the
	// file makes rather than, silently, against PDF/A-1b. A document that
	// declares no level pdf0 can target is reported with one checker finding,
	// not validated. It names no profile, so a builder refuses it.
	LevelDeclared Level = iota

	PDFA1b
	PDFA2b
	PDFA3b
	PDFA4
	// Level A (accessible) conformance: Level B plus tagged logical structure,
	// natural-language specification and Unicode character mapping. PDF/A-4 has
	// no Level A — accessibility there is expressed via PDF/UA-2.
	PDFA1a
	PDFA2a
	PDFA3a

	// The PDF/A-4 variants, ISO 19005-4 Annexes A and B. Each relaxes something
	// the base part forbids — arbitrary embedded files for 4f, 3D and RichMedia
	// annotations for 4e — and takes on requirements in exchange.
	PDFA4E
	PDFA4F

	// Level U conformance (ISO 19005-2/-3): Level B plus the requirement that
	// every character shown maps to Unicode — the Level A Unicode rule without
	// the structure, language and ActualText requirements.
	PDFA2u
	PDFA3u
)

// profile is the decomposition of a Level.
type profile struct {
	part int
	// conformance is the pdfaid:conformance letter the level is identified by:
	// "A", "B" or "U" at parts 1-3, "E" or "F" for a part-4 variant, and ""
	// for plain PDF/A-4, which has none.
	conformance string
	name        string
}

var profiles = map[Level]profile{
	PDFA1b: {1, "B", "PDF/A-1b"},
	PDFA1a: {1, "A", "PDF/A-1a"},
	PDFA2b: {2, "B", "PDF/A-2b"},
	PDFA2u: {2, "U", "PDF/A-2u"},
	PDFA2a: {2, "A", "PDF/A-2a"},
	PDFA3b: {3, "B", "PDF/A-3b"},
	PDFA3u: {3, "U", "PDF/A-3u"},
	PDFA3a: {3, "A", "PDF/A-3a"},
	PDFA4:  {4, "", "PDF/A-4"},
	PDFA4E: {4, "E", "PDF/A-4e"},
	PDFA4F: {4, "F", "PDF/A-4f"},
}

// Levels returns every level that names a profile, in a stable order: parts
// 1 to 4, and within a part b, u, a (or plain, e, f).
func Levels() []Level {
	return []Level{PDFA1b, PDFA1a, PDFA2b, PDFA2u, PDFA2a, PDFA3b, PDFA3u, PDFA3a, PDFA4, PDFA4E, PDFA4F}
}

func (l Level) String() string {
	if p, ok := profiles[l]; ok {
		return p.name
	}
	if l == LevelDeclared {
		return "PDF/A (as declared)"
	}
	return fmt.Sprintf("PDFALevel(%d)", int(l))
}

// Valid reports whether l names a profile. LevelDeclared does not: it is
// resolved to one before anything is checked.
func (l Level) Valid() bool {
	_, ok := profiles[l]
	return ok
}

// Part is the part of ISO 19005 the level belongs to (1-4), or 0 for a level
// that names no profile.
func (l Level) Part() int { return profiles[l].part }

// Conformance is the pdfaid:conformance value that identifies the level: "A",
// "B" or "U" at parts 1-3, "E" or "F" for PDF/A-4e and -4f, and "" for plain
// PDF/A-4 (which declares none) or a level that names no profile.
func (l Level) Conformance() string { return profiles[l].conformance }

// IsA reports whether l is a Level A (accessible) conformance level.
func (l Level) IsA() bool { return l.Part() <= 3 && l.Conformance() == "A" }

// Is4Variant reports whether l is one of the PDF/A-4 variants (4e, 4f).
func (l Level) Is4Variant() bool { return l.Part() == 4 && l.Conformance() != "" }

// variant is the PDF/A-4 variant letter, "E" or "F", or "" for every level
// that is not a variant. The relaxations and requirements the variants carry
// are gated on it — on the target, never on what the document says.
func (l Level) variant() string {
	if l.Part() == 4 {
		return l.Conformance()
	}
	return ""
}

// requiresUnicode reports whether the level carries the Unicode character-map
// requirement (ISO 19005-1 6.3.8, -2/-3 6.2.11.7.2): Level A everywhere, and
// Level U, which is that requirement alone.
func (l Level) requiresUnicode() bool {
	c := l.Conformance()
	return l.Part() <= 3 && (c == "A" || c == "U")
}

// revision is the pdfaid:rev a file of this level declares: "2020" for part 4
// (ISO 19005-4:2020), which requires it, and "" for parts 1-3, which do not.
func (l Level) revision() string {
	if l.Part() == 4 {
		return "2020"
	}
	return ""
}

// acceptsConformance reports whether a document declaring conformance c
// satisfies the identification requirement of target l, and is the one place
// the conformance hierarchy is written down.
//
// ISO 19005-2 and -3 define Level U as Level B plus Unicode mapping and Level
// A as Level U plus logical structure, so a file that conforms to a higher
// level conforms to every level below it, and the veraPDF profiles — the rule
// source this package is calibrated against — encode exactly that in their
// identification test (6.6.4 t03): a 2b target accepts "B", "U" or "A", a 2u
// target "U" or "A", a 2a target only "A". ISO 19005-1 has no Level U, so a 1b
// target accepts "B" or "A" and a 1a target only "A" (6.7.11 t03). Part 4 has
// no hierarchy: the variants are not supersets of each other, and plain
// PDF/A-4 is identified by the absence of a conformance entry (6.7.3 t03), so
// each part-4 target accepts exactly its own declaration.
//
// The comparison is case-sensitive: pdfaid:conformance is a closed choice and
// "e" is not "E" (the corpus's 6-7-3-t01-fail-c).
func (l Level) acceptsConformance(c string, present bool) bool {
	switch l.Part() {
	case 4:
		if l.Conformance() == "" {
			return !present
		}
		return present && c == l.Conformance()
	case 1, 2, 3:
		if !present {
			return false
		}
		rank := map[string]int{"B": 1, "U": 2, "A": 3}
		if l.Part() == 1 {
			delete(rank, "U")
		}
		got, ok := rank[c]
		return ok && got >= rank[l.Conformance()]
	}
	return false
}

// acceptedConformance describes, for a message, what acceptsConformance
// accepts at l.
func (l Level) acceptedConformance() string {
	switch {
	case l.Part() == 4 && l.Conformance() == "":
		return "no pdfaid:conformance"
	case l.Part() == 4:
		return fmt.Sprintf("%q", l.Conformance())
	case l.Conformance() == "A":
		return `"A"`
	case l.Conformance() == "U":
		return `"U" or "A"`
	case l.Part() == 1:
		return `"B" or "A"`
	default:
		return `"B", "U" or "A"`
	}
}

// LevelFor maps a pdfaid identification — the part and conformance a document's
// metadata declares — onto the level it names. It is the one mapping from a
// declaration to a level: Document.Conformance, Save, the embedded-file rule
// and a LevelDeclared run all go through it.
//
// The letter is matched without regard to case, so "e" names PDF/A-4e: a file
// that writes it is evidently trying to be one, and is held to that level, where
// the identification rule then reports the letter's case. ok is false for a
// part and letter that name no level: an unknown part, a part-1 "U" (ISO
// 19005-1 has no Level U), a part-4 "A", "B" or "U", or no letter at parts 1-3,
// which require one.
func LevelFor(part, conformance string) (Level, bool) {
	c := strings.ToUpper(conformance)
	for _, l := range Levels() {
		if fmt.Sprint(l.Part()) == part && l.Conformance() == c {
			return l, true
		}
	}
	return LevelDeclared, false
}

// ResolveTarget turns the level a caller asked for into the profile a run
// checks against, or into the one finding that says there is none.
//
// LevelDeclared becomes the level the document's own pdfaid identification
// names (LevelFor). A document whose identification cannot be read — no
// metadata, a packet that is not well-formed, one over the XMP packet limit —
// or that names no level pdf0 can target, is reported with a single finding
// under the "limit" rule and not validated: pdf0 was asked to check a claim it
// could not find, which is the checker stopping short (IsCheckerFinding), not
// a verdict on the file. A level that names no profile at all — an arbitrary
// integer converted to Level — is refused the same way, rather than running
// the part-agnostic subset of the rules and reporting it as a PDF/A result
// (audit 2026-09-22 C139).
func ResolveTarget(doc core.View, level Level) (Level, []Violation) {
	if level == LevelDeclared {
		id := readPDFAIdentification(doc)
		var why string
		switch id.status {
		case core.XMPParsed:
			if l, ok := LevelFor(id.part, id.conformance); ok {
				return l, nil
			}
			switch {
			case !id.hasPart:
				why = "its metadata carries no pdfaid:part"
			case !id.hasConformance:
				why = fmt.Sprintf("its metadata declares pdfaid:part %q with no pdfaid:conformance, which names no level", id.part)
			default:
				why = fmt.Sprintf("its metadata declares pdfaid:part %q and pdfaid:conformance %q, which name no PDF/A level", id.part, id.conformance)
			}
		case core.XMPMalformed:
			why = "its XMP metadata is not well-formed"
		case core.XMPLimit:
			why = "its XMP metadata is over the XMP packet limit and was not read"
		default:
			why = "it has no XMP metadata"
		}
		return level, []Violation{{Rule: finding.LimitRule, Level: level,
			Message: "not validated: the level to validate against was to be taken from the document's own declaration, and " + why}}
	}
	if !level.Valid() {
		return level, []Violation{{Rule: finding.LimitRule, Level: level,
			Message: fmt.Sprintf("not validated: %s names no PDF/A level", level)}}
	}
	return level, nil
}
