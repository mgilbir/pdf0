package pdfvt

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/xmp"
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

// Violation reports a way in which a document departs from PDF/VT.
type Violation struct {
	// Rule is the short rule identifier. A finding of the PDF/X base is
	// prefixed with the base it came from — "pdfx-4/" for PDF/VT-1, "pdfx-5/"
	// for PDF/VT-2 — and a DPart finding with "dpart/".
	Rule    string
	Message string
	Object  int // object number the violation anchors to, 0 if N/A
	// Part is the PDF/VT part the finding is against: "1" or "2". An empty
	// Part reads as "1".
	Part string
}

// RuleID returns the PDF/VT rule identifier.
func (v Violation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v Violation) ObjectNum() int { return v.Object }

// Error prints the finding under the part it is against (audit 2026-09-22
// C145: a PDF/VT-2 finding printed "PDF/VT-1").
func (v Violation) Error() string {
	part := v.Part
	if part == "" {
		part = "1"
	}
	if v.Object != 0 {
		return fmt.Sprintf("PDF/VT-%s %s: %s (object %d)", part, v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/VT-%s %s: %s", part, v.Rule, v.Message)
}

// validateView runs the PDF/VT checks for a part, "1" or "2", over a view.
//
// The part decides three things: the pdfvtid:GTS_PDFVTVersion the file must
// declare ("PDF/VT-1" or "PDF/VT-2"); the PDF/X base, PDF/X-4 for PDF/VT-1
// and PDF/X-5 for PDF/VT-2 (ISO 16612-2 6.1), whose findings are prefixed
// "pdfx-4/" or "pdfx-5/"; and the part printed by Violation.Error. pdf0 has no
// PDF/X-5 validator: the PDF/X-5 base is the PDF/X-4 rules with the
// reference-XObject prohibition lifted, which is what PDF/X-5 permits, and
// its external-reference rules are not asserted.
func validateView(doc core.View, part string) []Violation {
	var out []Violation
	add := func(rule, msg string, obj int) {
		out = append(out, Violation{Rule: rule, Message: msg, Object: obj, Part: part})
	}
	if part != "1" && part != "2" {
		add(finding.LimitRule, fmt.Sprintf("not validated: PDF/VT has no part %q", part), 0)
		return out
	}
	versionPrefix := "PDF/VT-" + part
	allowRefXObjects := part == "2"
	basePrefix := "pdfx-4/"
	if part == "2" {
		basePrefix = "pdfx-5/"
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
	// prohibition (a PDF/X-4-only rule that PDF/X-5 lifts) is dropped, keyed
	// on the finding's Check, not its words.
	run(func() {
		for _, v := range pdfxValidateView(doc, pdfx.PDFX4) {
			if allowRefXObjects && v.Check == pdfx.CheckRefXObject {
				continue
			}
			if v.Rule == finding.LimitRule {
				// The nested run shares this run's recorder, so its guard
				// trips are reported once, by the flush below — not prefixed
				// as if they were a PDF/X conformance finding.
				continue
			}
			add(basePrefix+v.Rule, v.Message, v.Object)
		}
	})

	cat := doc.ResolveDict(doc.Trailer.Get("Root"))

	// Identification: the XMP pdfvtid:GTS_PDFVTVersion property shall be present
	// and identify the requested PDF/VT version (ISO 16612-2 6.2).
	run(func() {
		// Read through the XMP model, by namespace URI (audit C141). A packet
		// pdf0 declined to model has its trip on the run already; whether it
		// identifies the file is then unknown, not "no".
		claimed := ""
		packet, status := doc.DocumentXMPPacket()
		switch status {
		case core.XMPLimit:
			return
		case core.XMPParsed:
			claimed, _ = packet.Text(xmp.NSPDFVTID, "GTS_PDFVTVersion")
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
		for _, v := range dpartValidateView(doc) {
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
