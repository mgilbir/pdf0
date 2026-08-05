package pdfvt

import (
	"fmt"
	"github.com/mgilbir/pdf0/dpart"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfx"
	"strings"
)

// This file implements validation for PDF/VT-1 (ISO 16612-2), the self-contained
// variable-and-transactional print exchange format. A PDF/VT-1 file is a
// conforming PDF/X-4 file (ISO 15930-7) that additionally carries a document
// part (DPart) hierarchy describing its record boundaries and identifies itself
// as PDF/VT-1 in XMP metadata. This validator composes the PDF/X-4 and DPart
// checks with the PDF/VT-specific requirements; it is calibrated against the
// valid Cal Poly PDF/VT-1 test suite.

// Violation reports a way in which a document departs from PDF/VT-1.
type Violation struct {
	Rule    string // short rule identifier, base-profile violations prefixed "pdfx-4/" or "dpart/"
	Message string
	Object  int // object number the violation anchors to, 0 if N/A
}

// RuleID returns the PDF/VT rule identifier.
func (v Violation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v Violation) ObjectNum() int { return v.Object }

func (v Violation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("PDF/VT-1 %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/VT-1 %s: %s", v.Rule, v.Message)
}

// ValidateView runs the PDF/VT checks over a view. versionPrefix is the
// pdfvtid:GTS_PDFVTVersion the file must declare, and allowRefXObjects lifts
// the reference-XObject prohibition for PDF/VT-2.
func ValidateView(doc core.View, versionPrefix string, allowRefXObjects bool) []Violation {
	var out []Violation
	add := func(rule, msg string, obj int) {
		out = append(out, Violation{Rule: rule, Message: msg, Object: obj})
	}

	// Every check runs under a recover boundary, so a panic on hostile input
	// becomes an "internal" finding instead of crashing the caller, and one bad
	// check does not discard its siblings' findings (audit C27). It is also the
	// coarse cancellation boundary (cancel.go).
	run := func(check func()) {
		if doc.Cancel.Stopped() {
			return
		}
		finding.Guarded(add, check)
	}

	// A PDF/VT file shall be a conforming PDF/X file (ISO 16612-2 6.1): PDF/X-4
	// for PDF/VT-1, PDF/X-5 for PDF/VT-2. For PDF/VT-2 the reference-XObject
	// prohibition (a PDF/X-4-only rule that PDF/X-5 lifts) is dropped.
	run(func() {
		for _, v := range pdfx.ValidateView(doc, pdfx.PDFX4) {
			if allowRefXObjects && v.Rule == "forbidden" && strings.Contains(v.Message, "reference XObjects") {
				continue
			}
			if v.Rule == finding.LimitRule {
				// The nested run shares this run's recorder, so its guard
				// trips are reported once, by the flush below — not prefixed
				// as if they were a PDF/X conformance finding.
				continue
			}
			add("pdfx-4/"+v.Rule, v.Message, v.Object)
		}
	})

	cat := doc.ResolveDict(doc.Trailer.Get("Root"))

	// Identification: the XMP pdfvtid:GTS_PDFVTVersion property shall be present
	// and identify the requested PDF/VT version (ISO 16612-2 6.2).
	run(func() {
		claimed := ""
		if cat != nil {
			if ms, ok := doc.Resolve(cat.Get("Metadata")).(*object.Stream); ok {
				xmp := doc.XMPText(ms)
				claimed = strings.TrimSpace(core.ExtractXMPValue(xmp, "pdfvtid:GTS_PDFVTVersion"))
			}
		}
		switch {
		case claimed == "":
			add("identification", "file is not identified as PDF/VT (no XMP pdfvtid:GTS_PDFVTVersion)", 0)
		case !strings.HasPrefix(claimed, versionPrefix):
			add("identification", fmt.Sprintf("GTS_PDFVTVersion %q does not identify %s", claimed, versionPrefix), 0)
		}
	})

	// A document part hierarchy is required (ISO 16612-2 6.3): its leaves define
	// the record structure PDF/VT exists to convey.
	run(func() {
		if cat == nil || cat.Get("DPartRoot") == nil {
			add("dpart", "PDF/VT requires a document part hierarchy (catalog /DPartRoot)", 0)
		}
		for _, v := range dpart.ValidateView(doc) {
			if v.Rule == finding.LimitRule {
				continue // reported once by the flush below
			}
			add("dpart/"+v.Rule, v.Message, v.Object)
		}
	})

	// Guard trips are reported under their own rule, not as conformance
	// failures (see limits.go).

	// The checks iterate map-ordered doc.Objects, so their concatenated output
	// order is nondeterministic; sort for stable, diffable reports.
	finding.Sort(out)
	return out
}
