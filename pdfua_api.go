package pdf0

import (
	"context"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfua"
)

// The PDF/UA API. The checks live in the pdfua package and read the document
// through a core.View; the functions here are the boundary that starts the run,
// builds that view, and appends the findings that can only be made from the
// Document — the resource guards that tripped while the file was read.

// ValidatePDFUA checks a document against PDF/UA-1 (ISO 14289-1). The checks
// cover the catalog and identification (tagged, a structure tree, a displayed
// title, the pdfuaid:part declaration and a 1.x header), the natural language
// of every piece of text being determinable (from the catalog /Lang or a /Lang
// nearer the text — PDF/UA-1 does not require the catalog entry itself),
// the structure tree (standard or role-mapped types, nesting, tables, lists,
// headings, notes, figure alternate text, real content tagged or marked as an
// artifact), fonts and Unicode mapping, annotations, form fields and tab
// order, security, XFA, optional content, embedded files, media clips and
// reference XObjects; docs/pdfua.md lists the rule families and the clause
// each reports under. A finding names its clause in pdfua.Violation.Clause
// (where the other validators' findings have Rule).
//
// It is a partial validator — a clean result means the implemented checks
// passed, not full PDF/UA conformance — and the result may include checker
// findings under the clauses "limit" and "internal", which say pdf0 could not
// finish rather than that the document is wrong (see IsCheckerFinding).
func ValidatePDFUA(doc *Document) []pdfua.Violation {
	return validatePDFUA(core.Canceler{}, doc, "1")
}

// ValidatePDFUAContext is ValidatePDFUA with cancellation. When ctx ends the run
// stops and returns the findings gathered so far plus one under the clause
// "limit" recording the cancellation, which IsCheckerFinding reports as a
// checker finding — so a cancelled run cannot be mistaken for a clean one. See
// cancel.go.
func ValidatePDFUAContext(ctx context.Context, doc *Document) []pdfua.Violation {
	return validatePDFUA(core.NewCanceler(ctx), doc, "1")
}

// validatePDFUA runs the checks shared by PDF/UA-1 and PDF/UA-2, parameterized
// by the pdfuaid:part the file must declare. The two UA-1-only requirements —
// part 1 and a PDF 1.x header — are selected here by part rather than filtered
// out of the result by message text afterwards (audit C39).
func validatePDFUA(cancel core.Canceler, doc *Document, part string) []pdfua.Violation {
	if doc == nil {
		return []pdfua.Violation{{Clause: finding.LimitRule, Message: nilDocumentMessage, Part: part}}
	}
	// Install a per-run cache (page tree, decoded content, font-usage map) on a
	// shallow copy so the original document is never mutated. Many checks walk
	// the same structures — core.CollectFontTextUsage alone runs in nine font
	// checks — and without the cache a large document's content was decoded and
	// tokenized dozens of times, making validation quadratic in practice.
	//
	// This is the boundary: everything below reads the document through a view.
	rd := beginRunCancel(doc, cancel)
	// Contain is the work meter's boundary outside the checks' own.
	var v []pdfua.Violation
	core.Contain(func() { v = pdfuaValidateView(rd.view(), part) })

	// Resource guards that tripped during the run (or while the file was read)
	// are reported under the "limit" clause, so a caller can tell "a check could
	// not be completed" from "the file is non-conforming". Read-time trips live
	// on the Document, which is why this is here and not below.
	v = append(v, limitUAViolations(rd)...)
	finding.Sort(v)
	return pdfuaWithPart(v, part)
}

// ValidatePDFUA2 checks a document against PDF/UA-2 (ISO 14289-2). It runs the
// checks ValidatePDFUA does, with the identification rule requiring
// pdfuaid:part 2 and without the 1.x-header rule, and adds the rules only part
// 2 has: the declared PDF version must be 2.0 (clause "4"), pdfuaid:rev must
// be 2024 ("5"), a type in an explicit namespace must not be role-mapped back
// into that namespace ("8.2.4"), the structure tree root holds one Document
// element in the PDF 2.0 namespace ("8.2.5.2"), H is not used ("8.2.5.12"),
// Link annotations with different targets sit in different elements
// ("8.2.5.20"), a labelled list has a ListNumbering ("8.2.5.25"), MathML's
// math sits in a Formula ("8.2.5.29"), destinations within the document are
// structure destinations ("8.8"), invisible annotations are artifacts
// ("8.9.2.2"), a file attachment's file specification has an AFRelationship
// ("8.9.2.4.10"), a Form element encloses at most one widget ("8.10.1"), a
// widget has a label or /Contents ("8.10.2.3"), and an embedded file has a
// /Desc ("8.14.1").
//
// Findings reuse the pdfua.Violation type, with Part "2", and Error prints
// "[PDF/UA-2 …]". Only those rules carry ISO 14289-2 clause numbers: the
// shared checks keep their ISO 14289-1 numbers, so read a clause against the
// standard that numbers it (docs/pdfua.md, "PDF/UA-2"). The caveats of
// ValidatePDFUA apply: it is a partial validator, and the result may include
// checker findings.
func ValidatePDFUA2(doc *Document) []pdfua.Violation {
	return validatePDFUA2(core.Canceler{}, doc)
}

// ValidatePDFUA2Context is ValidatePDFUA2 with cancellation; see
// ValidatePDFUAContext for how a cancelled run reports itself.
func ValidatePDFUA2Context(ctx context.Context, doc *Document) []pdfua.Violation {
	return validatePDFUA2(core.NewCanceler(ctx), doc)
}
func validatePDFUA2(cancel core.Canceler, d *Document) []pdfua.Violation {
	if d == nil {
		return validatePDFUA(cancel, d, "2")
	}
	// The shared checks (tagging, structure tree, displayed title, Unicode
	// mapping, artifacts, headings), parameterized for part 2 so the
	// identification rule requires pdfuaid:part 2, the language rule requires the
	// catalog /Lang (ISO 14289-2 8.4.4) rather than UA-1's determinable language,
	// and the UA-1 header rule is not run at all.
	out := validatePDFUA(cancel, d, "2")

	// PDF/UA-2 is defined against PDF 2.0: the declared version (the header,
	// or a later catalog /Version), and one that cannot be read is not 2.0 —
	// it used to pass (audit 2026-09-22 C146).
	out = append(out, pdfuaRunCheck(func() []pdfua.Violation {
		declared := d.view().DeclaredVersion()
		if maj, _, ok := core.ParsePDFVersion(declared); !ok || maj != 2 {
			return []pdfua.Violation{{Clause: "4", Message: fmt.Sprintf("PDF/UA-2 is defined for PDF 2.0; file declares %q", declared), Part: "2"}}
		}
		return nil
	})...)

	// validatePDFUA sorted its own findings; re-sort now that the UA-2 rule has
	// appended to them (audit C27).
	finding.Sort(out)
	return out
}
