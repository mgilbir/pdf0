package pdf0

import (
	"fmt"
	"strings"
)

// This file implements validation for PDF/X-4 (ISO 15930-7), the print-exchange
// profile that PDF/VT-1 (ISO 16612-2) builds on. The checks are conservative and
// structural: identification, the PDF/X output intent with an embedded ICC
// destination profile, the /Trapped flag, page geometry boxes, font embedding,
// and the prohibition on encryption. They are grounded in the requirements of
// ISO 15930-7 and calibrated against the valid Cal Poly PDF/VT-1 test suite,
// whose files are conforming PDF/X-4.
//
// Colour-space output-intent coverage (device colour requiring the destination
// profile) and the full forbidden-feature list are deliberately left to later
// work; the pieces here are the ones that can be verified false-positive-free
// against the valid corpus today.

// PDFXLevel identifies a PDF/X conformance level.
type PDFXLevel int

const (
	// PDFX4 is PDF/X-4 with an embedded ICC destination profile (ISO 15930-7).
	PDFX4 PDFXLevel = iota
	// PDFX4p is PDF/X-4p, which permits an externally referenced destination
	// profile instead of an embedded one.
	PDFX4p
)

func (l PDFXLevel) String() string {
	switch l {
	case PDFX4:
		return "PDF/X-4"
	case PDFX4p:
		return "PDF/X-4p"
	default:
		return "PDF/X"
	}
}

// PDFXViolation reports a way in which a document departs from a PDF/X level.
type PDFXViolation struct {
	Rule    string // short rule identifier, e.g. "output-intent"
	Message string
	Object  int // object number the violation anchors to, 0 if N/A
}

func (v PDFXViolation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("PDF/X %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/X %s: %s", v.Rule, v.Message)
}

// ValidatePDFX checks whether doc conforms to the given PDF/X level. An empty
// result means no violations were found.
func ValidatePDFX(doc *Document, level PDFXLevel) []PDFXViolation {
	var out []PDFXViolation
	add := func(rule, msg string, obj int) {
		out = append(out, PDFXViolation{Rule: rule, Message: msg, Object: obj})
	}

	// Encryption is forbidden (ISO 15930-7 6.1): a PDF/X file must be readable
	// without a decryption key.
	if doc.Encrypted || doc.Trailer.Get("Encrypt") != nil {
		add("encryption", "a PDF/X file shall not be encrypted", 0)
	}

	// Version: PDF/X-4 is defined against PDF 1.6; 1.7+/2.0 features are out of
	// scope. Older minor versions are tolerated.
	if maj, min, ok := parsePDFVersion(doc.Version); ok && (maj != 1 || min > 6) {
		add("version", fmt.Sprintf("PDF/X-4 is defined for PDF 1.6; file declares %s", doc.Version), 0)
	}

	pdfxCheckIdentification(doc, level, add)
	pdfxCheckOutputIntent(doc, level, add)
	pdfxCheckTrapped(doc, add)
	pdfxCheckPageBoxes(doc, add)
	pdfxCheckFontsEmbedded(doc, add)

	return out
}

// pdfxCheckIdentification verifies the file identifies as the requested PDF/X
// level. PDF/X-4 records the identifier in XMP (pdfxid:GTS_PDFXVersion); the
// Info dictionary /GTS_PDFXVersion, used by older PDF/X versions, is accepted as
// a fallback.
func pdfxCheckIdentification(doc *Document, level PDFXLevel, add func(rule, msg string, obj int)) {
	claimed := ""
	if cat := doc.ResolveDict(doc.Trailer.Get("Root")); cat != nil {
		if ms, ok := doc.Resolve(cat.Get("Metadata")).(*Stream); ok {
			xmp := decodeXMPToUTF8(decodeContentStream(doc, ms))
			claimed = strings.TrimSpace(extractXMPValue(xmp, "pdfxid:GTS_PDFXVersion"))
			if claimed == "" {
				claimed = strings.TrimSpace(extractXMPValue(xmp, "GTS_PDFXVersion"))
			}
		}
	}
	if claimed == "" {
		if info := doc.ResolveDict(doc.Trailer.Get("Info")); info != nil {
			if s, ok := info.Get("GTS_PDFXVersion").(String); ok {
				claimed = strings.TrimSpace(string(s.Value))
			}
		}
	}
	if claimed == "" {
		add("identification", "file is not identified as PDF/X (no pdfxid:GTS_PDFXVersion or Info /GTS_PDFXVersion)", 0)
		return
	}
	// The identifier for both PDF/X-4 and PDF/X-4p begins "PDF/X-4".
	if !strings.HasPrefix(claimed, "PDF/X-4") {
		add("identification", fmt.Sprintf("GTS_PDFXVersion %q does not identify %s", claimed, level), 0)
	}
}

// pdfxCheckOutputIntent verifies a PDF/X output intent with an ICC destination
// profile (ISO 15930-7 6.2). A GTS_PDFX intent with an OutputConditionIdentifier
// is required; PDF/X-4 requires the profile embedded (DestOutputProfile), while
// PDF/X-4p also accepts an external reference.
func pdfxCheckOutputIntent(doc *Document, level PDFXLevel, add func(rule, msg string, obj int)) {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return
	}
	arr, ok := doc.Resolve(cat.Get("OutputIntents")).(Array)
	if !ok || len(arr) == 0 {
		add("output-intent", "a PDF/X file requires a catalog /OutputIntents array with a GTS_PDFX intent", 0)
		return
	}
	var profiles []Object
	found := false
	for _, e := range arr {
		oi := doc.ResolveDict(e)
		if oi == nil {
			continue
		}
		if s, _ := oi.Get("S").(Name); s != "GTS_PDFX" {
			continue
		}
		found = true
		if oci, ok := oi.Get("OutputConditionIdentifier").(String); !ok || len(oci.Value) == 0 {
			add("output-intent", "GTS_PDFX output intent lacks a non-empty /OutputConditionIdentifier", refNum(e))
		}
		prof := oi.Get("DestOutputProfile")
		if _, ok := doc.Resolve(prof).(*Stream); ok {
			profiles = append(profiles, prof)
		} else if level == PDFX4 {
			add("output-intent", "PDF/X-4 requires an embedded ICC /DestOutputProfile in the GTS_PDFX output intent", refNum(e))
		} else if oi.Get("DestOutputProfileRef") == nil {
			add("output-intent", "PDF/X-4p output intent has neither an embedded /DestOutputProfile nor a /DestOutputProfileRef", refNum(e))
		}
	}
	if !found {
		add("output-intent", "no output intent with /S /GTS_PDFX is present", 0)
	}
	// ISO 15930-7 6.2: all GTS_PDFX intents shall reference the same profile.
	for i := 1; i < len(profiles); i++ {
		if refNum(profiles[i]) != refNum(profiles[0]) {
			add("output-intent", "multiple GTS_PDFX output intents reference different destination profiles", 0)
			break
		}
	}
}

// pdfxCheckTrapped verifies the Info /Trapped flag is present and definite
// (ISO 15930-7 6.3): it shall be True or False, not Unknown or absent.
func pdfxCheckTrapped(doc *Document, add func(rule, msg string, obj int)) {
	info := doc.ResolveDict(doc.Trailer.Get("Info"))
	if info == nil {
		add("trapped", "Info dictionary with a definite /Trapped value is required", 0)
		return
	}
	switch t, _ := info.Get("Trapped").(Name); t {
	case "True", "False":
		// definite, as required
	default:
		add("trapped", "Info /Trapped shall be True or False, not Unknown or absent", 0)
	}
}

// pdfxCheckPageBoxes verifies page geometry (ISO 15930-7 6.4): every page has a
// MediaBox; exactly one of TrimBox or ArtBox defines the finished-page area and
// lies within the MediaBox; a BleedBox, if present, contains that area and lies
// within the MediaBox.
func pdfxCheckPageBoxes(doc *Document, add func(rule, msg string, obj int)) {
	for _, pg := range collectPages(doc, doc.catalogPages()) {
		media, hasMedia := pdfxRect(doc, inheritedPageAttr(doc, pg.dict, "MediaBox"))
		if !hasMedia {
			add("page-box", "page has no MediaBox", pg.objNum)
			continue
		}
		trim, hasTrim := pdfxRect(doc, inheritedPageAttr(doc, pg.dict, "TrimBox"))
		art, hasArt := pdfxRect(doc, inheritedPageAttr(doc, pg.dict, "ArtBox"))
		switch {
		case hasTrim && hasArt:
			add("page-box", "page has both TrimBox and ArtBox; exactly one is permitted", pg.objNum)
		case !hasTrim && !hasArt:
			add("page-box", "page has neither TrimBox nor ArtBox", pg.objNum)
		}
		finished, hasFinished := trim, hasTrim
		if hasArt {
			finished, hasFinished = art, true
		}
		if hasFinished && !rectContains(media, finished) {
			add("page-box", "page TrimBox/ArtBox is not within the MediaBox", pg.objNum)
		}
		if bleed, ok := pdfxRect(doc, inheritedPageAttr(doc, pg.dict, "BleedBox")); ok {
			if !rectContains(media, bleed) {
				add("page-box", "page BleedBox is not within the MediaBox", pg.objNum)
			}
			if hasFinished && !rectContains(bleed, finished) {
				add("page-box", "page BleedBox does not contain the TrimBox/ArtBox", pg.objNum)
			}
		}
	}
}

// pdfxCheckFontsEmbedded verifies every font reachable from page content
// resources is embedded (ISO 15930-7 6.5). It scans the /Font entries of each
// page's resource dictionary and, recursively, of the form XObjects and tiling
// patterns those resources reference — deduplicating shared resource and font
// objects so the walk is proportional to the distinct resources, not the page
// count (a PDF/VT file may reuse one resource set across hundreds of thousands
// of pages). Fonts reachable only from an AcroForm's default resources are not
// page content and are correctly excluded.
func pdfxCheckFontsEmbedded(doc *Document, add func(rule, msg string, obj int)) {
	seenRes := map[*Dictionary]bool{}
	seenFont := map[*Dictionary]bool{}
	var scan func(res *Dictionary, depth int)
	scan = func(res *Dictionary, depth int) {
		if res == nil || depth > 32 || seenRes[res] {
			return
		}
		seenRes[res] = true
		if fonts := doc.ResolveDict(res.Get("Font")); fonts != nil {
			for i, ref := range fonts.Values {
				fd := doc.ResolveDict(ref)
				if fd == nil || seenFont[fd] {
					continue
				}
				seenFont[fd] = true
				if !fontIsEmbedded(doc, fd) {
					name, _ := fd.Get("BaseFont").(Name)
					add("font-embedding", fmt.Sprintf("font /%s (resource /%s) is not embedded", name, fonts.Keys[i]), refNum(ref))
				}
			}
		}
		for _, key := range []Name{"XObject", "Pattern"} {
			sub := doc.ResolveDict(res.Get(key))
			if sub == nil {
				continue
			}
			for _, ref := range sub.Values {
				switch v := doc.Resolve(ref).(type) {
				case *Stream:
					scan(doc.ResolveDict(v.Dict.Get("Resources")), depth+1)
				case *Dictionary:
					scan(doc.ResolveDict(v.Get("Resources")), depth+1)
				}
			}
		}
	}
	for _, pg := range collectPages(doc, doc.catalogPages()) {
		scan(resolveResources(doc, pg.dict), 0)
	}
}

// fontIsEmbedded reports whether a font's program is embedded. A Type 0
// composite font carries its program on the descendant CIDFont; a Type 3 font
// defines glyphs with content streams and has no program to embed.
func fontIsEmbedded(doc *Document, font *Dictionary) bool {
	switch sub, _ := font.Get("Subtype").(Name); sub {
	case "Type3":
		return true
	case "Type0":
		df, ok := doc.Resolve(font.Get("DescendantFonts")).(Array)
		if !ok || len(df) == 0 {
			return false
		}
		cid := doc.ResolveDict(df[0])
		if cid == nil {
			return false
		}
		return fontDescriptorEmbedded(doc, doc.ResolveDict(cid.Get("FontDescriptor")))
	default:
		return fontDescriptorEmbedded(doc, doc.ResolveDict(font.Get("FontDescriptor")))
	}
}

// fontDescriptorEmbedded reports whether a font descriptor carries an embedded
// font program.
func fontDescriptorEmbedded(doc *Document, fd *Dictionary) bool {
	if fd == nil {
		return false
	}
	for _, key := range []Name{"FontFile", "FontFile2", "FontFile3"} {
		if _, ok := doc.Resolve(fd.Get(key)).(*Stream); ok {
			return true
		}
	}
	return false
}

// pdfxRect parses a PDF rectangle (an array of four numbers) into normalised
// [llx, lly, urx, ury] coordinates.
func pdfxRect(doc *Document, o Object) ([4]float64, bool) {
	arr, ok := doc.Resolve(o).(Array)
	if !ok || len(arr) != 4 {
		return [4]float64{}, false
	}
	var r [4]float64
	for i, e := range arr {
		f, ok := pdfxNum(doc.Resolve(e))
		if !ok {
			return [4]float64{}, false
		}
		r[i] = f
	}
	if r[0] > r[2] {
		r[0], r[2] = r[2], r[0]
	}
	if r[1] > r[3] {
		r[1], r[3] = r[3], r[1]
	}
	return r, true
}

func pdfxNum(o Object) (float64, bool) {
	switch v := o.(type) {
	case Integer:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

// rectContains reports whether inner lies within outer, tolerating small
// rounding differences at the edges.
func rectContains(outer, inner [4]float64) bool {
	const eps = 1e-3
	return inner[0] >= outer[0]-eps && inner[1] >= outer[1]-eps &&
		inner[2] <= outer[2]+eps && inner[3] <= outer[3]+eps
}

// parsePDFVersion splits a "1.6"-style version string into major and minor.
func parsePDFVersion(v string) (major, minor int, ok bool) {
	dot := strings.IndexByte(v, '.')
	if dot <= 0 || dot == len(v)-1 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(v, "%d.%d", &major, &minor); err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
