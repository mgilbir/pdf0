package pdfa

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The rules that only exist because a PDF/A-4 file said it was an E or an F.
//
// ISO 19005-4 has two named variants on top of the base part. A PDF/A-4f file
// carries attachments, and declares that with pdfaid:conformance F; a PDF/A-4e
// file carries 3D artwork, and declares E. Each relaxes something the base part
// forbids — arbitrary embedded files for F, 3D and RichMedia annotations for E
// — and each adds requirements in exchange. pdf0 already honours the
// relaxations; what follows is the other half of the bargain.
//
// The conformance is read from the file's own XMP rather than chosen by the
// caller, which is the same way the relaxations are already gated. That works
// for everything here and does not work for one thing: whether a file *should*
// have said E or F. A file with pdfaid:part 4 and no conformance is a valid
// plain PDF/A-4 file — base rule 6.7.3-3 says a file conforming to neither
// variant shall not provide one — so "this ought to have been an E" is a
// question only a caller who asked for PDF/A-4e can answer. That needs
// PDFA4E/PDFA4F as levels of their own.

// checkA4FEmbeddedFilesPresent: a PDF/A-4f file shall contain an EmbeddedFiles
// key in the name dictionary of the document catalog (ISO 19005-4 6.9).
//
// The f is for files. Declaring the variant and attaching nothing is a
// contradiction rather than a harmless overstatement: a reader that honours the
// declaration goes looking for attachments that are not there.
func checkA4FEmbeddedFilesPresent(doc core.View, level Level) []Violation {
	if level != PDFA4 || pdfaConformanceFlag(doc) != "F" {
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
	if level != PDFA4 || pdfaConformanceFlag(doc) != "E" {
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
