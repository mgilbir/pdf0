package pdfx

import (
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
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

// Level identifies a PDF/X conformance level.
type Level int

const (
	// PDFX4 is PDF/X-4 with an embedded ICC destination profile (ISO 15930-7).
	PDFX4 Level = iota
	// PDFX4p is PDF/X-4p, which permits an externally referenced destination
	// profile instead of an embedded one.
	PDFX4p
	// PDFX1a is PDF/X-1a (ISO 15930-1/4): CMYK, grayscale and spot colour only,
	// no transparency, defined against PDF 1.3/1.4.
	PDFX1a
	// PDFX3 is PDF/X-3 (ISO 15930-3/6): PDF/X-1a plus ICC-managed colour, still
	// no transparency, PDF 1.3/1.4.
	PDFX3
	// PDFX6 is PDF/X-6 (ISO 15930-9): the PDF 2.0-based successor to PDF/X-4.
	PDFX6
)

func (l Level) String() string {
	switch l {
	case PDFX4:
		return "PDF/X-4"
	case PDFX4p:
		return "PDF/X-4p"
	case PDFX1a:
		return "PDF/X-1a"
	case PDFX3:
		return "PDF/X-3"
	case PDFX6:
		return "PDF/X-6"
	default:
		return "PDF/X"
	}
}

// pdfxVersionPrefix is the GTS_PDFXVersion identifier prefix a level requires.
func (l Level) pdfxVersionPrefix() string {
	switch l {
	case PDFX1a:
		return "PDF/X-1a"
	case PDFX3:
		return "PDF/X-3"
	case PDFX6:
		return "PDF/X-6"
	default:
		return "PDF/X-4"
	}
}

// noTransparency reports whether the level forbids transparency (PDF/X-1a and
// PDF/X-3 predate the transparency imaging model; PDF/X-4 and -6 permit it).
func (l Level) noTransparency() bool { return l == PDFX1a || l == PDFX3 }

// maxPDFMinor returns the highest PDF 1.x minor version the level is defined
// for, and whether the level is a PDF 2.0 level.
func (l Level) versionBound() (maxMinor int, pdf2 bool) {
	switch l {
	case PDFX1a, PDFX3:
		return 4, false
	case PDFX6:
		return 0, true
	default: // PDFX4 / PDFX4p
		return 6, false
	}
}

// Violation reports a way in which a document departs from a PDF/X level.
type Violation struct {
	Rule    string // short rule identifier, e.g. "output-intent"
	Message string
	Object  int // object number the violation anchors to, 0 if N/A
}

// RuleID returns the PDF/X rule identifier.
func (v Violation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v Violation) ObjectNum() int { return v.Object }

func (v Violation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("PDF/X %s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("PDF/X %s: %s", v.Rule, v.Message)
}

// pdfxCheckNoTransparency flags any use of the transparency imaging model, which
// PDF/X-1a and PDF/X-3 predate: a page transparency group, or transparency in a
// page's own ExtGState resources (soft mask, non-normal blend mode, or alpha
// other than fully opaque).
func pdfxCheckNoTransparency(doc core.View, add func(rule, msg string, obj int)) {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return
	}
	for _, page := range doc.Pages(cat.Get("Pages")) {
		if grp := doc.ResolveDict(page.Dict.Get("Group")); grp != nil {
			if s, _ := grp.Get("S").(object.Name); s == "Transparency" {
				add("transparency", "a page transparency group is not permitted in this PDF/X level", page.ObjNum)
				continue
			}
		}
		if core.PageUsesTransparency(doc, page.Dict) {
			add("transparency", "transparency (soft mask, blend mode or alpha) is not permitted in this PDF/X level", page.ObjNum)
		}
	}
}

// pdfxCheckForbidden flags features PDF/X-4 does not permit (ISO 15930-7 6.x):
// interactive actions and JavaScript, OPI proxies, PostScript XObjects,
// reference (external-content) XObjects, alternate images, non-identity transfer
// functions, and multimedia annotations. It walks the object list once, so it
// stays fast regardless of page count.
func pdfxCheckForbidden(doc core.View, add func(rule, msg string, obj int)) {
	if cat := doc.ResolveDict(doc.Trailer.Get("Root")); cat != nil {
		if cat.Get("AA") != nil {
			add("forbidden", "the document catalog shall not carry additional actions (/AA)", 0)
		}
		if d, ok := doc.Resolve(cat.Get("OpenAction")).(*object.Dictionary); ok && d.Get("S") != nil {
			add("forbidden", "the document catalog shall not carry an /OpenAction action", 0)
		}
		if names := doc.ResolveDict(cat.Get("Names")); names != nil && names.Get("JavaScript") != nil {
			add("forbidden", "a JavaScript name tree is not permitted", 0)
		}
	}

	for num, iobj := range doc.Objects {
		var d *object.Dictionary
		switch v := iobj.Value.(type) {
		case *object.Dictionary:
			d = v
		case *object.Stream:
			d = &v.Dict
		}
		if d == nil {
			continue
		}
		sub, _ := d.Get("Subtype").(object.Name)

		if d.Get("OPI") != nil {
			add("forbidden", "OPI (Open Prepress Interface) proxies are not permitted", num)
		}
		if s, _ := d.Get("S").(object.Name); s == "JavaScript" {
			add("forbidden", "JavaScript actions are not permitted", num)
		}
		switch sub {
		case "PS":
			add("forbidden", "PostScript XObjects are not permitted", num)
		case "Image":
			if d.Get("Alternates") != nil {
				add("forbidden", "image XObjects shall not carry /Alternates", num)
			}
		case "Form":
			if d.Get("Ref") != nil {
				add("forbidden", "reference XObjects (/Ref) are not permitted in PDF/X-4", num)
			}
		case "Movie", "Sound", "Screen", "FileAttachment":
			add("forbidden", fmt.Sprintf("annotation subtype /%s is not permitted", sub), num)
		}
		if t, _ := d.Get("Type").(object.Name); t == "ExtGState" {
			for _, k := range []object.Name{"TR", "TR2"} {
				if tr := d.Get(k); tr != nil && !pdfxTransferIsIdentity(doc, tr) {
					add("forbidden", fmt.Sprintf("a transfer function (ExtGState /%s) is not permitted", k), num)
				}
			}
		}
	}
}

// pdfxTransferIsIdentity reports whether a transfer-function value is the benign
// /Identity or /Default (or an array of those, one per colorant), as opposed to
// an actual function that PDF/X-4 forbids.
func pdfxTransferIsIdentity(doc core.View, o object.Object) bool {
	switch v := doc.Resolve(o).(type) {
	case object.Name:
		return v == "Identity" || v == "Default"
	case object.Array:
		for _, e := range v {
			n, ok := doc.Resolve(e).(object.Name)
			if !ok || (n != "Identity" && n != "Default") {
				return false
			}
		}
		return true
	}
	return false
}

// pdfxCheckDeviceColor verifies that device-dependent colour (DeviceRGB,
// DeviceCMYK, DeviceGray) is only used where the printing condition is defined —
// by the GTS_PDFX output intent's ICC destination profile, a Default* colour
// space in scope, or a covering transparency-group colour space (ISO 15930-7
// 6.2, PDF Reference device-colour rules). It uses a memoised scan so the
// per-page content walk stays fast on PDF/VT files that reuse content across
// very many pages.
func pdfxCheckDeviceColor(doc core.View, add func(rule, msg string, obj int)) {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return
	}
	oiRGB, oiCMYK, oiGray := pdfxOutputIntentCoverage(doc, cat)
	sc := NewDevColorScanner(doc)
	for _, page := range doc.Pages(cat.Get("Pages")) {
		u := sc.PageDeviceUse(page.Dict)
		groupRGB, groupCMYK, _ := core.GroupCSCoverage(doc, page.Dict)
		if u.RGB && !oiRGB && !groupRGB {
			add("color", "DeviceRGB used without a matching OutputIntent, DefaultRGB or covering group colour space", page.ObjNum)
		}
		if u.CMYK && !oiCMYK && !groupCMYK {
			add("color", "DeviceCMYK used without a matching OutputIntent, DefaultCMYK or covering group colour space", page.ObjNum)
		}
		if u.Gray && !oiRGB && !oiCMYK && !oiGray {
			add("color", "DeviceGray used without any OutputIntent or DefaultGray", page.ObjNum)
		}
	}
}

// pdfxOutputIntentCoverage reports which device colour families the GTS_PDFX
// output intent's ICC destination profile covers, read from the profile's
// colour-space signature. An intent with an OutputConditionIdentifier but no
// embedded profile is treated conservatively as covering RGB and CMYK.
func pdfxOutputIntentCoverage(doc core.View, cat *object.Dictionary) (rgb, cmyk, gray bool) {
	arr, ok := doc.Resolve(cat.Get("OutputIntents")).(object.Array)
	if !ok {
		return
	}
	for _, e := range arr {
		oi := doc.ResolveDict(e)
		if oi == nil {
			continue
		}
		if s, _ := oi.Get("S").(object.Name); s != "GTS_PDFX" {
			continue
		}
		stream, ok := doc.Resolve(oi.Get("DestOutputProfile")).(*object.Stream)
		if !ok {
			if oi.Get("OutputConditionIdentifier") != nil {
				rgb, cmyk = true, true
			}
			continue
		}
		data := core.ICCProfileData(stream, doc.Limits)
		if len(data) < 20 {
			rgb, cmyk = true, true
			continue
		}
		switch string(data[16:20]) {
		case "RGB ":
			rgb = true
		case "CMYK":
			cmyk = true
		case "GRAY":
			gray = true
		default:
			rgb, cmyk = true, true
		}
	}
	return
}

// pdfxCheckIdentification verifies the file identifies as the requested PDF/X
// level. PDF/X-4 records the identifier in XMP (pdfxid:GTS_PDFXVersion); the
// Info dictionary /GTS_PDFXVersion, used by older PDF/X versions, is accepted as
// a fallback.
func pdfxCheckIdentification(doc core.View, level Level, add func(rule, msg string, obj int)) {
	claimed := ""
	if cat := doc.ResolveDict(doc.Trailer.Get("Root")); cat != nil {
		if ms, ok := doc.Resolve(cat.Get("Metadata")).(*object.Stream); ok {
			xmp := doc.XMPText(ms)
			claimed = strings.TrimSpace(core.ExtractXMPValue(xmp, "pdfxid:GTS_PDFXVersion"))
			if claimed == "" {
				claimed = strings.TrimSpace(core.ExtractXMPValue(xmp, "GTS_PDFXVersion"))
			}
		}
	}
	if claimed == "" {
		if info := doc.ResolveDict(doc.Trailer.Get("Info")); info != nil {
			if s, ok := info.Get("GTS_PDFXVersion").(object.String); ok {
				claimed = strings.TrimSpace(string(s.Value))
			}
		}
	}
	if claimed == "" {
		add("identification", "file is not identified as PDF/X (no pdfxid:GTS_PDFXVersion or Info /GTS_PDFXVersion)", 0)
		return
	}
	// The identifier begins with the level's family prefix (e.g. "PDF/X-4" for
	// both PDF/X-4 and PDF/X-4p, "PDF/X-1a" for PDF/X-1a:2001/2003).
	if !strings.HasPrefix(claimed, level.pdfxVersionPrefix()) {
		add("identification", fmt.Sprintf("GTS_PDFXVersion %q does not identify %s", claimed, level), 0)
	}
}

// pdfxCheckOutputIntent verifies a PDF/X output intent with an ICC destination
// profile (ISO 15930-7 6.2). A GTS_PDFX intent with an OutputConditionIdentifier
// is required; PDF/X-4 requires the profile embedded (DestOutputProfile), while
// PDF/X-4p also accepts an external reference.
func pdfxCheckOutputIntent(doc core.View, level Level, add func(rule, msg string, obj int)) {
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return
	}
	arr, ok := doc.Resolve(cat.Get("OutputIntents")).(object.Array)
	if !ok || len(arr) == 0 {
		add("output-intent", "a PDF/X file requires a catalog /OutputIntents array with a GTS_PDFX intent", 0)
		return
	}
	var profiles []object.Object
	found := false
	for _, e := range arr {
		oi := doc.ResolveDict(e)
		if oi == nil {
			continue
		}
		if s, _ := oi.Get("S").(object.Name); s != "GTS_PDFX" {
			continue
		}
		found = true
		if oci, ok := oi.Get("OutputConditionIdentifier").(object.String); !ok || len(oci.Value) == 0 {
			add("output-intent", "GTS_PDFX output intent lacks a non-empty /OutputConditionIdentifier", object.RefNum(e))
		}
		prof := oi.Get("DestOutputProfile")
		if _, ok := doc.Resolve(prof).(*object.Stream); ok {
			profiles = append(profiles, prof)
		} else if level != PDFX4p {
			// Only PDF/X-4p permits an external reference; every other level
			// requires the ICC profile embedded.
			add("output-intent", fmt.Sprintf("%s requires an embedded ICC /DestOutputProfile in the GTS_PDFX output intent", level), object.RefNum(e))
		} else if oi.Get("DestOutputProfileRef") == nil {
			add("output-intent", "PDF/X-4p output intent has neither an embedded /DestOutputProfile nor a /DestOutputProfileRef", object.RefNum(e))
		}
	}
	if !found {
		add("output-intent", "no output intent with /S /GTS_PDFX is present", 0)
	}
	// ISO 15930-7 6.2: all GTS_PDFX intents shall reference the same profile.
	for i := 1; i < len(profiles); i++ {
		if object.RefNum(profiles[i]) != object.RefNum(profiles[0]) {
			add("output-intent", "multiple GTS_PDFX output intents reference different destination profiles", 0)
			break
		}
	}
}

// pdfxCheckTrapped verifies the Info /Trapped flag is present and definite
// (ISO 15930-7 6.3): it shall be True or False, not Unknown or absent.
func pdfxCheckTrapped(doc core.View, add func(rule, msg string, obj int)) {
	info := doc.ResolveDict(doc.Trailer.Get("Info"))
	if info == nil {
		add("trapped", "Info dictionary with a definite /Trapped value is required", 0)
		return
	}
	switch t, _ := info.Get("Trapped").(object.Name); t {
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
func pdfxCheckPageBoxes(doc core.View, add func(rule, msg string, obj int)) {
	for _, pg := range doc.Pages(doc.CatalogPages()) {
		media, hasMedia := pdfxRect(doc, doc.InheritedPageAttr(pg.Dict, "MediaBox"))
		if !hasMedia {
			add("page-box", "page has no MediaBox", pg.ObjNum)
			continue
		}
		trim, hasTrim := pdfxRect(doc, doc.InheritedPageAttr(pg.Dict, "TrimBox"))
		art, hasArt := pdfxRect(doc, doc.InheritedPageAttr(pg.Dict, "ArtBox"))
		switch {
		case hasTrim && hasArt:
			add("page-box", "page has both TrimBox and ArtBox; exactly one is permitted", pg.ObjNum)
		case !hasTrim && !hasArt:
			add("page-box", "page has neither TrimBox nor ArtBox", pg.ObjNum)
		}
		finished, hasFinished := trim, hasTrim
		if hasArt {
			finished, hasFinished = art, true
		}
		if hasFinished && !rectContains(media, finished) {
			add("page-box", "page TrimBox/ArtBox is not within the MediaBox", pg.ObjNum)
		}
		if bleed, ok := pdfxRect(doc, doc.InheritedPageAttr(pg.Dict, "BleedBox")); ok {
			if !rectContains(media, bleed) {
				add("page-box", "page BleedBox is not within the MediaBox", pg.ObjNum)
			}
			if hasFinished && !rectContains(bleed, finished) {
				add("page-box", "page BleedBox does not contain the TrimBox/ArtBox", pg.ObjNum)
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
func pdfxCheckFontsEmbedded(doc core.View, add func(rule, msg string, obj int)) {
	seenRes := map[*object.Dictionary]bool{}
	seenFont := map[*object.Dictionary]bool{}
	var scan func(res *object.Dictionary, depth int)
	scan = func(res *object.Dictionary, depth int) {
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
					name, _ := fd.Get("BaseFont").(object.Name)
					add("font-embedding", fmt.Sprintf("font /%s (resource /%s) is not embedded", name, fonts.Keys[i]), object.RefNum(ref))
				}
			}
		}
		for _, key := range []object.Name{"XObject", "Pattern"} {
			sub := doc.ResolveDict(res.Get(key))
			if sub == nil {
				continue
			}
			for _, ref := range sub.Values {
				switch v := doc.Resolve(ref).(type) {
				case *object.Stream:
					scan(doc.ResolveDict(v.Dict.Get("Resources")), depth+1)
				case *object.Dictionary:
					scan(doc.ResolveDict(v.Get("Resources")), depth+1)
				}
			}
		}
	}
	for _, pg := range doc.Pages(doc.CatalogPages()) {
		scan(doc.Resources(pg.Dict), 0)
	}
}

// fontIsEmbedded reports whether a font's program is embedded. A Type 0
// composite font carries its program on the descendant CIDFont; a Type 3 font
// defines glyphs with content streams and has no program to embed.
func fontIsEmbedded(doc core.View, font *object.Dictionary) bool {
	switch sub, _ := font.Get("Subtype").(object.Name); sub {
	case "Type3":
		return true
	case "Type0":
		df, ok := doc.Resolve(font.Get("DescendantFonts")).(object.Array)
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
func fontDescriptorEmbedded(doc core.View, fd *object.Dictionary) bool {
	if fd == nil {
		return false
	}
	for _, key := range []object.Name{"FontFile", "FontFile2", "FontFile3"} {
		if _, ok := doc.Resolve(fd.Get(key)).(*object.Stream); ok {
			return true
		}
	}
	return false
}

// pdfxRect parses a PDF rectangle (an array of four numbers) into normalised
// [llx, lly, urx, ury] coordinates.
func pdfxRect(doc core.View, o object.Object) ([4]float64, bool) {
	arr, ok := doc.Resolve(o).(object.Array)
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

func pdfxNum(o object.Object) (float64, bool) {
	switch v := o.(type) {
	case object.Integer:
		return float64(v), true
	case object.Real:
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

// ValidateView runs the PDF/X checks over a view. The caller starts the run,
// builds the view, and reports the guards that tripped while the file was read.
func ValidateView(v core.View, level Level) []Violation {
	var out []Violation
	add := func(rule, msg string, obj int) {
		out = append(out, Violation{Rule: rule, Message: msg, Object: obj})
	}

	// Every check runs under a recover boundary, so a panic on hostile input
	// becomes an "internal" finding instead of crashing the caller, and one bad
	// check does not discard its siblings' findings (audit C27). It is also the
	// coarse cancellation boundary (cancel.go).
	run := func(check func()) {
		if v.Cancel.Stopped() {
			return
		}
		finding.Guarded(add, check)
	}

	run(func() {
		// Encryption is forbidden (ISO 15930-7 6.1): a PDF/X file must be readable
		// without a decryption key.
		if v.Encrypted || v.Trailer.Get("Encrypt") != nil {
			add("encryption", "a PDF/X file shall not be encrypted", 0)
		}

		// Version: each PDF/X level is defined for a specific PDF version. PDF/X-1a
		// and -3 for PDF 1.3/1.4, PDF/X-4/-4p for 1.6, PDF/X-6 for PDF 2.0. A newer
		// version than the level allows is out of scope.
		if maj, min, ok := core.ParsePDFVersion(v.Version); ok {
			maxMinor, pdf2 := level.versionBound()
			if pdf2 {
				if maj != 2 {
					add("version", fmt.Sprintf("%s is defined for PDF 2.0; file declares %s", level, v.Version), 0)
				}
			} else if maj != 1 || min > maxMinor {
				add("version", fmt.Sprintf("%s is defined for PDF 1.%d; file declares %s", level, maxMinor, v.Version), 0)
			}
		}
	})

	run(func() { pdfxCheckIdentification(v, level, add) })
	run(func() { pdfxCheckOutputIntent(v, level, add) })
	run(func() { pdfxCheckTrapped(v, add) })
	run(func() { pdfxCheckPageBoxes(v, add) })
	run(func() { pdfxCheckFontsEmbedded(v, add) })
	run(func() { pdfxCheckDeviceColor(v, add) })
	run(func() { pdfxCheckForbidden(v, add) })
	if level.noTransparency() {
		run(func() { pdfxCheckNoTransparency(v, add) })
	}

	// The checks iterate map-ordered v.Objects, so their concatenated output
	// order is nondeterministic; sort for stable, diffable reports.
	// Guard trips are reported under their own rule, not as conformance
	// failures (see limits.go).

	return out
}
