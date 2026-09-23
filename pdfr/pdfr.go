package pdfr

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
)

// This file validates the structural profile of PDF/R (PDF for Raster, ISO
// 23504-1) — a constrained profile for raster/scanned documents (a modern
// replacement for fax). A PDF/R file is a PDF 2.0 file whose every page is a
// raster image: the page content draws image XObjects only, with no text or
// vector graphics, using a limited set of image compression filters, and no
// encryption.
//
// ISO 23504 is not openly published, this repository does not hold it, and no
// conformance corpus is available. What is checked is what the author could
// state from knowledge of the standard, and each rule is written to be sure
// of what it reports:
//
//   - PDF 2.0, read from the header or a later catalog /Version; a version
//     that cannot be read is a finding, not a pass;
//   - no encryption;
//   - an XMP metadata stream identifying the file as PDF/R: pdfrid:part in the
//     namespace NSPDFRID, read through the XMP model. The namespace and
//     property follow the AIIM identification schemas of the sibling
//     standards (pdfaid, pdfuaid) and are from knowledge of ISO 23504-1, not
//     checked against its text;
//   - at least one page, each drawing raster images only: no text-showing,
//     path-painting or shading operators, image XObjects only, and image and
//     inline-image filters from the permitted set.
//
// Transparency is not checked. An earlier version of this comment listed "no
// transparency" among the covered requirements, with no check behind it
// (audit 2026-09-22 C146); what ISO 23504 says about transparency could not be
// confirmed here, so the claim is withdrawn rather than a rule invented.

// NSPDFRID is the PDF/R identification schema's namespace (see above).
const NSPDFRID = "http://www.aiim.org/pdfr/ns/id/"

// Violation reports a departure from the PDF/R structural profile.
type Violation struct {
	Rule    string
	Message string
	Object  int
}

// RuleID returns the PDF/R rule identifier.
func (v Violation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v Violation) ObjectNum() int { return v.Object }

func (v Violation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("PDF/R %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/R %s: %s", v.Rule, v.Message)
}

// ValidateView runs the PDF/R checks over a view. The caller starts the run
// and reports the guards that tripped while the file was read.
func ValidateView(v core.View) []Violation {
	var out []Violation
	add := func(rule, msg string, obj int) {
		out = append(out, Violation{Rule: rule, Message: msg, Object: obj})
	}
	// Every check runs under a recover boundary, so a panic on hostile input
	// becomes an "internal" finding instead of crashing the caller, and one
	// bad check (or one bad page) does not discard the others' findings
	// (audit C27). It is also the coarse cancellation boundary (cancel.go).
	run := func(check func()) {
		if v.Cancel.Stopped() {
			return
		}
		finding.Guarded(add, check)
	}

	run(func() {
		if v.Encrypted || v.Trailer.Get("Encrypt") != nil {
			add("encryption", "a PDF/R file shall not be encrypted", 0)
		}
		// Fail closed: a version that cannot be read is not PDF 2.0.
		declared := v.DeclaredVersion()
		if maj, _, ok := core.ParsePDFVersion(declared); !ok || maj != 2 {
			add("version", fmt.Sprintf("PDF/R is defined for PDF 2.0; file declares %q", declared), 0)
		}
	})

	cat := v.Catalog()
	if cat == nil {
		add("structure", "document has no catalog", 0)
		return out
	}
	run(func() { checkIdentification(v, add) })

	var pages []core.PageInfo
	run(func() {
		pages = v.Pages(cat.Get("Pages"))
		if len(pages) == 0 {
			add("structure", "a PDF/R file shall have at least one page", 0)
		}
	})
	for _, page := range pages {
		run(func() { CheckPage(v, page.Dict, page.ObjNum, add) })
	}
	return out
}

// checkIdentification requires an XMP packet declaring pdfrid:part. It is read
// through the XMP model, by namespace URI: the substring test it replaces took
// any "pdfr" or "pdf/r" anywhere in the packet, so a producer string "Acme
// PDF/Reader" identified the file (audit 2026-09-22 C146).
func checkIdentification(v core.View, add func(rule, msg string, obj int)) {
	packet, status := v.DocumentXMPPacket()
	switch status {
	case core.XMPLimit:
		return // not read; the trip is on the run
	case core.XMPAbsent:
		add("metadata", "a PDF/R file requires an XMP metadata stream", 0)
		return
	case core.XMPMalformed:
		add("identification", "the XMP metadata is not well-formed, so it does not identify the file as PDF/R", 0)
		return
	}
	part, ok := packet.Text(NSPDFRID, "part")
	switch {
	case !ok:
		add("identification", "the XMP metadata does not identify the file as PDF/R (no pdfrid:part)", 0)
	case part != "1":
		add("identification", fmt.Sprintf("pdfrid:part is %q; PDF/R has part 1", part), 0)
	}
}

// pdfrImageFilters are the image compression filters PDF/R permits.
var pdfrImageFilters = map[object.Name]bool{
	"CCITTFaxDecode":  true,
	"JBIG2Decode":     true,
	"DCTDecode":       true,
	"JPXDecode":       true,
	"FlateDecode":     true,
	"RunLengthDecode": true,
	"LZWDecode":       true, // permitted in PDF 2.0 raster
}

// inlineFilterNames expands the abbreviated filter names an inline image may
// use (ISO 32000-2 Table 92).
var inlineFilterNames = map[string]object.Name{
	"AHx": "ASCIIHexDecode", "A85": "ASCII85Decode", "LZW": "LZWDecode",
	"Fl": "FlateDecode", "RL": "RunLengthDecode", "CCF": "CCITTFaxDecode",
	"DCT": "DCTDecode",
}

// pdfrTextOrVectorOps are content operators that produce non-raster marks: text
// showing, text objects, path painting, and shading. Their presence means a
// page carries more than a raster image.
var pdfrTextOrVectorOps = map[string]bool{
	"BT": true, "Tj": true, "TJ": true, "'": true, "\"": true, // text
	"S": true, "s": true, "f": true, "F": true, "f*": true, // path painting
	"B": true, "B*": true, "b": true, "b*": true,
	"sh": true, // shading
}

// CheckPage checks one page: raster-only content, image XObjects only, and
// permitted image and inline-image filters.
func CheckPage(d core.View, page *object.Dictionary, objNum int, add func(rule, msg string, obj int)) {
	// The page content must draw raster images only — no text or vector marks.
	data, _ := core.ContentStreamData(d, page.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
	flagged := map[string]bool{}
	lx := core.NewContentLexer(d.Cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentDictStart:
			lx.SkipDict(&t)
		case core.ContentOperator:
			op := string(t.Raw)
			if pdfrTextOrVectorOps[op] && !flagged[op] {
				flagged[op] = true
				add("raster-only", fmt.Sprintf("page content uses a non-raster operator %q; a PDF/R page shall contain only raster images", op), objNum)
			}
		}
	}

	// An inline image is an image the content draws like any other, and its
	// filters are held to the same list; they were not read at all before
	// (audit 2026-09-22 C146).
	reportedInline := map[object.Name]bool{}
	for _, filters := range core.InlineImageFilters(d.Cancel, data) {
		for _, f := range filters {
			name := object.Name(f)
			if full, ok := inlineFilterNames[f]; ok {
				name = full
			}
			if !pdfrImageFilters[name] && !reportedInline[name] {
				reportedInline[name] = true
				add("image-filter", fmt.Sprintf("an inline image uses filter %s, which PDF/R does not permit", name), objNum)
			}
		}
	}

	// Every XObject the page carries must be an image using a permitted filter;
	// a form XObject (vector container) is not allowed.
	res := d.Resources(page)
	if res == nil {
		return
	}
	xobjs := d.ResolveDict(res.Get("XObject"))
	if xobjs == nil {
		return
	}
	for key, xval := range xobjs.All() {
		st, ok := d.Resolve(xval).(*object.Stream)
		if !ok {
			continue
		}
		xnum := object.RefNum(xval)
		sub, _ := d.ResolveName(st.Dict.Get("Subtype"))
		if sub != "Image" {
			add("raster-only", fmt.Sprintf("XObject %s is not an image (/Subtype %q); a PDF/R page shall use image XObjects only", key, sub), xnum)
			continue
		}
		for _, f := range d.StreamFilters(st) {
			if !pdfrImageFilters[f] {
				add("image-filter", fmt.Sprintf("image XObject %s uses filter %s, which PDF/R does not permit", key, f), xnum)
			}
		}
	}
}
