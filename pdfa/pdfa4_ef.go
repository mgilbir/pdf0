package pdfa

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The rules that exist only at a PDF/A-4 variant.
//
// ISO 19005-4 has two named variants on top of the base part. A PDF/A-4f file
// carries attachments, and declares that with pdfaid:conformance F; a PDF/A-4e
// file carries 3D artwork, and declares E. Each relaxes something the base part
// forbids — arbitrary embedded files for F, 3D and RichMedia annotations for E
// — and each adds requirements in exchange. What follows is the other half of
// the bargain.
//
// Both halves are gated on the target, never on the document's declaration:
// validating at PDFA4F applies 4f whatever the file says, and a file that
// declares F but is validated as plain PDF/A-4 gets neither the relaxations
// nor these requirements — it gets the identification finding, which says its
// declaration is not the one the target accepts. (Before, the gates read the
// declaration after the run had flattened PDFA4F to PDFA4, so a PDFA4F run on
// a file declaring nothing never reached them: audit 2026-09-22 C64.) To
// validate a file against whatever it declares, ask for LevelDeclared.

// checkA4FEmbeddedFilesPresent: a PDF/A-4f file shall contain an EmbeddedFiles
// key in the name dictionary of the document catalog (ISO 19005-4 6.9).
//
// The f is for files. Declaring the variant and attaching nothing is a
// contradiction rather than a harmless overstatement: a reader that honours the
// declaration goes looking for attachments that are not there.
func checkA4FEmbeddedFilesPresent(doc core.View, level Level) []Violation {
	if level.variant() != "F" {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	names := doc.ResolveDict(catalog.Get("Names"))
	if names == nil {
		return []Violation{{Rule: "6.9", Level: level,
			Message: "a PDF/A-4f file must contain an /EmbeddedFiles key in the " +
				"document catalog's name dictionary, and there is no name dictionary"}}
	}
	if names.Get("EmbeddedFiles") == nil {
		return []Violation{{Rule: "6.9", Level: level,
			Message: "a PDF/A-4f file must contain an /EmbeddedFiles key in the " +
				"document catalog's name dictionary"}}
	}
	return nil
}

// checkA4E3DStreamSubtype: the Subtype of a 3D stream shall be U3D or PRC
// (ISO 19005-4 6.1.6.1, Annex B).
//
// Only at PDF/A-4e, because only there is a 3D annotation permitted at all.
// Elsewhere the annotation carrying it is the violation, and reporting the
// artwork format as well would be answering a question nobody reached.
func checkA4E3DStreamSubtype(doc core.View, level Level) []Violation {
	if level.variant() != "E" {
		return nil
	}
	var errs []Violation
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		if t, _ := doc.ResolveName(stream.Dict.Get("Type")); t != "3D" {
			continue
		}
		st, ok := doc.ResolveName(stream.Dict.Get("Subtype"))
		if !ok {
			errs = append(errs, Violation{Rule: "6.1.6.1", Level: level,
				Message: "3D stream has no /Subtype; it must be /U3D or /PRC", Object: num})
			continue
		}
		if st != "U3D" && st != "PRC" {
			errs = append(errs, Violation{Rule: "6.1.6.1", Level: level,
				Message: fmt.Sprintf("3D stream /Subtype is /%s; it must be /U3D or /PRC", string(st)),
				Object:  num})
		}
	}
	return errs
}
