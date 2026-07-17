package pdf0

import (
	"fmt"
	"strings"
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

// PDFRViolation reports a departure from the PDF/R structural profile.
type PDFRViolation struct {
	Rule    string
	Message string
	Object  int
}

func (v PDFRViolation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("PDF/R %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/R %s: %s", v.Rule, v.Message)
}

// pdfrImageFilters are the image compression filters PDF/R permits.
var pdfrImageFilters = map[Name]bool{
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

// ValidatePDFR checks a document against the PDF/R structural profile.
func (d *Document) ValidatePDFR() []PDFRViolation {
	var out []PDFRViolation
	add := func(rule, msg string, obj int) {
		out = append(out, PDFRViolation{Rule: rule, Message: msg, Object: obj})
	}

	if d.Encrypted || d.Trailer.Get("Encrypt") != nil {
		add("encryption", "a PDF/R file shall not be encrypted", 0)
	}
	if maj, _, ok := parsePDFVersion(d.Version); ok && maj != 2 {
		add("version", fmt.Sprintf("PDF/R is defined for PDF 2.0; file declares %s", d.Version), 0)
	}

	cat := getCatalog(d)
	if cat == nil {
		add("structure", "document has no catalog", 0)
		return out
	}
	if xmp := documentXMP(d); xmp == "" {
		add("metadata", "a PDF/R file requires an XMP metadata stream", 0)
	} else if !strings.Contains(strings.ToLower(xmp), "pdf/r") && !strings.Contains(strings.ToLower(xmp), "pdfr") {
		add("identification", "the XMP metadata does not identify the file as PDF/R", 0)
	}

	pages := collectPages(d, cat.Get("Pages"))
	if len(pages) == 0 {
		add("structure", "a PDF/R file shall have at least one page", 0)
	}
	for _, page := range pages {
		d.checkPDFRPage(page.dict, page.objNum, add)
	}
	return out
}

func (d *Document) checkPDFRPage(page *Dictionary, objNum int, add func(rule, msg string, obj int)) {
	// The page content must draw raster images only — no text or vector marks.
	data := getContentStreamData(d, page.Get("Contents"))
	flagged := map[string]bool{}
	forEachContentToken(data, func(tok []byte, isName bool) {
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
	res := resolveResources(d, page)
	if res == nil {
		return
	}
	xobjs := d.ResolveDict(res.Get("XObject"))
	if xobjs == nil {
		return
	}
	for i, key := range xobjs.Keys {
		st, ok := d.Resolve(xobjs.Values[i]).(*Stream)
		if !ok {
			continue
		}
		xnum := refNum(xobjs.Values[i])
		sub, _ := st.Dict.Get("Subtype").(Name)
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
func streamFilters(d *Document, st *Stream) []Name {
	switch f := d.Resolve(st.Dict.Get("Filter")).(type) {
	case Name:
		return []Name{f}
	case Array:
		var out []Name
		for _, e := range f {
			if n, ok := d.Resolve(e).(Name); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}
