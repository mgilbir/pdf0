package pdfr

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file validates the structural profile of PDF/R (PDF for Raster, ISO
// 23504) — a constrained profile for raster/scanned documents (a modern
// replacement for fax). PDF/R is a PDF 2.0 file whose every page is a raster
// image: the page content draws image XObjects only, with no text or vector
// graphics, using a limited set of image compression filters, and no encryption
// or transparency.
//
// ISO 23504 is not an openly published specification and no conformance test
// corpus is available, so this validator covers the well-defined structural
// requirements (raster-only content, allowed image filters, PDF 2.0, no
// encryption, no transparency) conservatively and does not assert full ISO 23504
// conformance. The XMP PDF/R identification is checked leniently.

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

// pdfrTextOrVectorOps are content operators that produce non-raster marks: text
// showing, text objects, path painting, and shading. Their presence means a
// page carries more than a raster image.
var pdfrTextOrVectorOps = map[string]bool{
	"BT": true, "Tj": true, "TJ": true, "'": true, "\"": true, // text
	"S": true, "s": true, "f": true, "F": true, "f*": true, // path painting
	"B": true, "B*": true, "b": true, "b*": true,
	"sh": true, // shading
}

func CheckPage(d core.View, page *object.Dictionary, objNum int, add func(rule, msg string, obj int)) {
	// The page content must draw raster images only — no text or vector marks.
	data := core.ContentStreamData(d, page.Get("Contents"))
	flagged := map[string]bool{}
	core.ForEachContentToken(d.Cancel, data, func(tok []byte, isName bool) {
		if isName {
			return
		}
		op := string(tok)
		if pdfrTextOrVectorOps[op] && !flagged[op] {
			flagged[op] = true
			add("raster-only", fmt.Sprintf("page content uses a non-raster operator %q; a PDF/R page shall contain only raster images", op), objNum)
		}
	})

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
	for i, key := range xobjs.Keys {
		st, ok := d.Resolve(xobjs.Values[i]).(*object.Stream)
		if !ok {
			continue
		}
		xnum := object.RefNum(xobjs.Values[i])
		sub, _ := st.Dict.Get("Subtype").(object.Name)
		if sub != "Image" {
			add("raster-only", fmt.Sprintf("XObject /%s is not an image (/Subtype %q); a PDF/R page shall use image XObjects only", key, sub), xnum)
			continue
		}
		for _, f := range streamFilters(d, st) {
			if !pdfrImageFilters[f] {
				add("image-filter", fmt.Sprintf("image XObject /%s uses filter /%s, which PDF/R does not permit", key, f), xnum)
			}
		}
	}
}

// streamFilters returns a stream's filter names (/Filter may be a single name or
// an array).
func streamFilters(d core.View, st *object.Stream) []object.Name {
	return d.StreamFilters(st)
}
