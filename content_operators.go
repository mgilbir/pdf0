package pdf0

import "fmt"

// contentOperators is the set of operators permitted in PDF content streams
// (ISO 32000-1 Annex A, Table A.1). PDF/A forbids any operator outside this
// set, even inside a BX/EX compatibility section. PDF 2.0 (ISO 32000-2)
// defines the same content-stream operator set.
var contentOperators = map[string]bool{
	// Graphics state
	"q": true, "Q": true, "cm": true, "w": true, "J": true, "j": true,
	"M": true, "d": true, "ri": true, "i": true, "gs": true,
	// Path construction
	"m": true, "l": true, "c": true, "v": true, "y": true, "h": true, "re": true,
	// Path painting
	"S": true, "s": true, "f": true, "F": true, "f*": true, "B": true,
	"B*": true, "b": true, "b*": true, "n": true,
	// Clipping
	"W": true, "W*": true,
	// Text objects
	"BT": true, "ET": true,
	// Text state
	"Tc": true, "Tw": true, "Tz": true, "TL": true, "Tf": true, "Tr": true, "Ts": true,
	// Text positioning
	"Td": true, "TD": true, "Tm": true, "T*": true,
	// Text showing
	"Tj": true, "TJ": true, "'": true, "\"": true,
	// Type 3 fonts
	"d0": true, "d1": true,
	// Color
	"CS": true, "cs": true, "SC": true, "SCN": true, "sc": true, "scn": true,
	"G": true, "g": true, "RG": true, "rg": true, "K": true, "k": true,
	// Shading patterns
	"sh": true,
	// Inline images
	"BI": true, "ID": true, "EI": true,
	// XObjects
	"Do": true,
	// Marked content
	"MP": true, "DP": true, "BMC": true, "BDC": true, "EMC": true,
	// Compatibility
	"BX": true, "EX": true,
}

// standardRenderingIntents are the four rendering intent names permitted as
// the operand of the ri operator and the /Intent key (ISO 32000-1 8.6.5.8,
// Table 70).
var standardRenderingIntents = map[string]bool{
	"AbsoluteColorimetric": true,
	"RelativeColorimetric": true,
	"Saturation":           true,
	"Perceptual":           true,
}

// checkContentStreamOperators verifies that every content stream uses only
// operators defined in ISO 32000 (6.2.2), that the ri operator's operand is
// a standard rendering intent, and that named XObject/resource references
// resolve within the associated resource dictionary.
func checkContentStreamOperators(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.2.2"
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	// Only EXECUTED content is validated: an operator inside a form XObject
	// that no content stream invokes does not appear on the page (the
	// corpus passes an UnknownOperator in an uninvoked form).
	seenContainer := map[*Dictionary]bool{}
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		data, key := doc.contentBytesAndKey(page.dict.Get("Contents"))
		walkExecutedContent(doc, page.dict, data, key, page.objNum, seenContainer, add)
	}

	// Annotation appearance streams (their /AP /N) are executed content too:
	// an operator not defined in the PDF imaging model is equally forbidden
	// there (ISO 19005-1 6.2.10; Isartor 6.2.10-t01-fail-c).
	for _, ap := range collectAppearanceStreams(doc) {
		if data := decodeContentStream(doc, ap.stream); data != nil {
			checkContentTokens(data, doc.ResolveDict(ap.stream.Dict.Get("Resources")), doc, ap.objNum, add)
		}
	}

	// Type 3 font glyph procedures are content streams whose named resources
	// must resolve in the Type 3 font's own /Resources — not inherited from
	// the page (ISO 19005 6.2.2; a glyph proc that references a colour space
	// present only in the page resources is invalid).
	for fontDict, u := range collectFontTextUsage(doc) {
		if st, _ := fontDict.Get("Subtype").(Name); st != "Type3" || !rendersVisibly(u) {
			continue
		}
		res := doc.ResolveDict(fontDict.Get("Resources"))
		cps := doc.ResolveDict(fontDict.Get("CharProcs"))
		if cps == nil {
			continue
		}
		for _, cpVal := range cps.Values {
			if cp, ok := doc.Resolve(cpVal).(*Stream); ok {
				if cpData := decodeContentStream(doc, cp); cpData != nil {
					checkContentTokens(cpData, res, doc, u.objNum, add)
				}
			}
		}
	}
	return errs
}

// appearanceStream pairs an annotation appearance stream with the object number
// to attribute its violations to.
type appearanceStream struct {
	stream *Stream
	objNum int
}

// collectAppearanceStreams gathers the normal-appearance (/AP /N) streams of
// every annotation, following the button-widget form where /N is a
// sub-dictionary of appearance-state streams.
func collectAppearanceStreams(doc *Document) []appearanceStream {
	var out []appearanceStream
	add := func(n Object, objNum int) {
		switch v := doc.Resolve(n).(type) {
		case *Stream:
			out = append(out, appearanceStream{stream: v, objNum: objNum})
		case *Dictionary:
			for _, sv := range v.Values {
				if s, ok := doc.Resolve(sv).(*Stream); ok {
					out = append(out, appearanceStream{stream: s, objNum: objNum})
				}
			}
		}
	}
	visit := func(annot *Dictionary, num int) {
		if ap := doc.ResolveDict(annot.Get("AP")); ap != nil {
			add(ap.Get("N"), num)
		}
	}
	for num, iobj := range doc.Objects {
		if dict, ok := iobj.Value.(*Dictionary); ok && isAnnotation(dict) {
			visit(dict, num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		visit(a.dict, a.num)
	}
	return out
}

// walkExecutedContent validates a content stream and recurses into the form
// XObjects and tiling patterns it actually invokes.
func walkExecutedContent(doc *Document, container *Dictionary, data []byte, key *Stream, objNum int, seen map[*Dictionary]bool, add func(string, int)) {
	if container == nil || seen[container] {
		return
	}
	seen[container] = true
	res := resolveResources(doc, container)
	if data != nil {
		checkContentTokens(data, res, doc, objNum, add)
	}
	if res == nil {
		return
	}
	used := doc.contentUsedNamesCached(data, key)
	if xobj := doc.ResolveDict(res.Get("XObject")); xobj != nil {
		for i, key := range xobj.Keys {
			if !used.xobjects[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(xobj.Values[i]).(*Stream); ok {
				st, _ := s.Dict.Get("Subtype").(Name)
				xnum := resolveObjNum(doc, xobj.Values[i])
				// A PostScript XObject that is actually drawn is prohibited
				// (ISO 19005-1 6.2.5, -2/-3/-4 6.2.9).
				if st == "PS" {
					add("a drawn PostScript XObject is not permitted", xnum)
				} else if st == "Form" {
					if s2, _ := s.Dict.Get("Subtype2").(Name); s2 == "PS" {
						add("a drawn form XObject has /Subtype2 /PS (PostScript)", xnum)
					}
					if s.Dict.Get("PS") != nil {
						add("a drawn form XObject dictionary contains a /PS entry", xnum)
					}
					walkExecutedContent(doc, &s.Dict, decodeContentStream(doc, s), s, xnum, seen, add)
				}
			}
		}
	}
	if pat := doc.ResolveDict(res.Get("Pattern")); pat != nil {
		for i, key := range pat.Keys {
			if !used.patterns[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(pat.Values[i]).(*Stream); ok {
				walkExecutedContent(doc, &s.Dict, decodeContentStream(doc, s), s, i, seen, add)
			}
		}
	}
}

// checkContentTokens scans one content stream for undefined operators, custom
// rendering intents, and unresolved named resource references.
func checkContentTokens(data []byte, res *Dictionary, doc *Document, objNum int, add func(string, int)) {
	var lastName string
	forEachContentToken(data, func(tok []byte, isName bool) {
		if isName {
			lastName = string(tok)
			return
		}
		s := string(tok)
		if isContentOperand(s) {
			return
		}
		if !contentOperators[s] {
			add(fmt.Sprintf("content stream contains an operator %q not defined in ISO 32000", s), objNum)
			return
		}
		switch s {
		case "ri":
			if lastName != "" && !standardRenderingIntents[lastName] {
				add(fmt.Sprintf("rendering intent operator (ri) uses a non-standard value /%s", lastName), objNum)
			}
		case "Do":
			if !namedResourcePresent(doc, res, "XObject", lastName) {
				add("content stream references an XObject that is absent from the resource dictionary", objNum)
			}
		case "sh":
			if !namedResourcePresent(doc, res, "Shading", lastName) {
				add("content stream references a shading that is absent from the resource dictionary", objNum)
			}
		case "gs":
			if !namedResourcePresent(doc, res, "ExtGState", lastName) {
				add("content stream references an ExtGState that is absent from the resource dictionary", objNum)
			}
		case "cs", "CS":
			// The colour-space operand is either a built-in device space or
			// a name defined in the Resources /ColorSpace dictionary.
			if !builtinColorSpaceName[lastName] && !namedResourcePresent(doc, res, "ColorSpace", lastName) {
				add("content stream references a colour space that is absent from the resource dictionary", objNum)
			}
		case "Tf":
			if !namedResourcePresent(doc, res, "Font", lastName) {
				add("content stream references a font that is absent from the resource dictionary", objNum)
			}
		}
	})
}

// isContentOperand reports whether a content token is an operand (number,
// boolean, or null) rather than an operator.
func isContentOperand(s string) bool {
	if s == "true" || s == "false" || s == "null" {
		return true
	}
	if s == "" {
		return true
	}
	c := s[0]
	return c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// namedResourcePresent reports whether a named resource of the given category
// exists in the resource dictionary.
func namedResourcePresent(doc *Document, res *Dictionary, category, name string) bool {
	if name == "" {
		return true // no name captured; do not flag
	}
	if res == nil {
		return false
	}
	sub := doc.ResolveDict(res.Get(Name(category)))
	if sub == nil {
		return false
	}
	return sub.Get(Name(name)) != nil
}

// builtinColorSpaceName lists the colour-space names selectable with cs/CS
// without a Resources entry (ISO 32000-1 8.6.3).
var builtinColorSpaceName = map[string]bool{
	"DeviceGray": true, "DeviceRGB": true, "DeviceCMYK": true, "Pattern": true,
}

// resolveObjNum returns the object number of an indirect reference, or 0.
func resolveObjNum(doc *Document, o Object) int {
	if ref, ok := o.(IndirectRef); ok {
		return ref.Number
	}
	return 0
}

// checkContentStreamLimits enforces the Annex C architectural limits on
// numeric and string operands within content streams (ISO 19005-1 6.1.12,
// -2/-3 6.1.13). The real magnitude and string-length limits differ by
// part; the integer limit (2^31-1) is universal.
func checkContentStreamLimits(doc *Document, level PDFALevel, lim implLimits, errs *[]ValidationError) {
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		*errs = append(*errs, ValidationError{Rule: lim.rule, Level: level, Message: msg, Object: obj})
	}
	for num, data := range collectContentStreamData(doc) {
		forEachContentItem(data, func(kind contentItemKind, payload []byte) {
			switch kind {
			case itemNumber:
				checkContentNumberLimit(string(payload), lim, num, add)
			case itemString:
				if len(payload) > lim.stringLen {
					add(fmt.Sprintf("a content-stream string of %d bytes exceeds the maximum length %d", len(payload), lim.stringLen), num)
				}
			}
		})
	}
}

// checkContentNumberLimit validates a numeric content operand against the
// integer or real architectural limit.
func checkContentNumberLimit(s string, lim implLimits, objNum int, add func(string, int)) {
	isReal := false
	for i := 0; i < len(s); i++ {
		if s[i] == '.' || s[i] == 'e' || s[i] == 'E' {
			isReal = true
			break
		}
	}
	if isReal {
		v := numVal(parseNumberToken([]byte(s)))
		if absf(v) > lim.realLimit {
			add(fmt.Sprintf("a content-stream real value %s exceeds the magnitude limit %g", s, lim.realLimit), objNum)
		}
		return
	}
	// Integer: parse with overflow guard against the 2^31-1 architectural
	// limit (ISO 32000-1 Annex C, Table C.1).
	neg := false
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var v int64
	overflow := false
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return
		}
		v = v*10 + int64(s[i]-'0')
		if v > 1<<40 {
			overflow = true
			break
		}
	}
	if neg {
		v = -v
	}
	if overflow || v > 2147483647 || v < -2147483648 {
		add(fmt.Sprintf("a content-stream integer value %s is outside [-2^31, 2^31-1]", s), objNum)
	}
}

// checkICCProfileIdentity implements the PDF/A-4 rule (ISO 19005-4 6.2.4.2)
// that an ICCBased CMYK colour space used for rendering must not embed the
// same ICC profile as the PDF/A output intent or the current transparency
// blending colour space. Content is followed through invoked form XObjects,
// carrying the enclosing group's blending profile.
func checkICCProfileIdentity(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	catalogOI := pdfaOutputIntentProfile(doc, catalog)

	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: "6.2.4.2", Level: level, Message: msg, Object: obj})
	}
	seenC := map[*Dictionary]bool{}
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		// PDF/A-4 permits page-level output intents; prefer the page's own.
		oiProfile := catalogOI
		if p := pdfaOutputIntentProfile(doc, page.dict); p != nil {
			oiProfile = p
		}
		data, key := doc.contentBytesAndKey(page.dict.Get("Contents"))
		blend := groupBlendProfile(doc, page.dict)
		walkICCIdentity(doc, page.dict, data, key, page.objNum, oiProfile, blend, seenC, add)
	}
	return errs
}

// pdfaOutputIntentProfile returns the DestOutputProfile of a dictionary's
// GTS_PDFA1 output intent, or nil.
func pdfaOutputIntentProfile(doc *Document, container *Dictionary) *Stream {
	arr, ok := doc.Resolve(container.Get("OutputIntents")).(Array)
	if !ok {
		return nil
	}
	for _, el := range arr {
		d := doc.ResolveDict(el)
		if d == nil {
			continue
		}
		if s, _ := d.Get("S").(Name); s == "GTS_PDFA1" {
			if p, ok := doc.Resolve(d.Get("DestOutputProfile")).(*Stream); ok {
				return p
			}
		}
	}
	return nil
}

func walkICCIdentity(doc *Document, container *Dictionary, data []byte, key *Stream, objNum int, oi, blend *Stream, seen map[*Dictionary]bool, add func(string, int)) {
	if container == nil || seen[container] || data == nil {
		return
	}
	seen[container] = true
	res := resolveResources(doc, container)
	if res == nil {
		return
	}
	usage := scanContentColorUsage(data)
	csDict := doc.ResolveDict(res.Get("ColorSpace"))
	checkName := func(name string) {
		if csDict == nil {
			return
		}
		prof := renderedICCCMYKProfile(doc, csDict.Get(Name(name)))
		if prof == nil {
			return
		}
		if sameICCProfile(doc, prof, oi) {
			add("ICCBased CMYK colour space must not embed the same profile as the PDF/A output intent", objNum)
		}
		if sameICCProfile(doc, prof, blend) {
			add("ICCBased CMYK colour space must not embed the same profile as the transparency blending colour space", objNum)
		}
	}
	for name := range usage.fillCS {
		checkName(name)
	}
	for name := range usage.strokeCS {
		checkName(name)
	}

	// Recurse into invoked form XObjects, updating the blending profile when
	// the form is an isolated transparency group.
	used := doc.contentUsedNamesCached(data, key)
	if xobj := doc.ResolveDict(res.Get("XObject")); xobj != nil {
		for i, xkey := range xobj.Keys {
			if !used.xobjects[string(xkey)] {
				continue
			}
			s, ok := doc.Resolve(xobj.Values[i]).(*Stream)
			if !ok {
				continue
			}
			if st, _ := s.Dict.Get("Subtype").(Name); st != "Form" {
				continue
			}
			childBlend := blend
			if gp := groupBlendProfile(doc, &s.Dict); gp != nil {
				childBlend = gp
			}
			walkICCIdentity(doc, &s.Dict, decodeContentStream(doc, s), s, resolveObjNum(doc, xobj.Values[i]), oi, childBlend, seen, add)
		}
	}
}

// groupBlendProfile returns the ICC profile of a container's transparency
// group blending colour space, or nil.
func groupBlendProfile(doc *Document, container *Dictionary) *Stream {
	if g := doc.ResolveDict(container.Get("Group")); g != nil {
		return iccProfileStream(doc, g.Get("CS"))
	}
	return nil
}

// renderedICCCMYKProfile returns the ICCBased CMYK profile a colour space
// renders through: the space itself, or the ICCBased CMYK alternate of a
// Separation or DeviceN space.
func renderedICCCMYKProfile(doc *Document, csVal Object) *Stream {
	if p := iccCMYKProfile(doc, csVal); p != nil {
		return p
	}
	arr, ok := doc.Resolve(csVal).(Array)
	if !ok || len(arr) < 3 {
		return nil
	}
	if n, _ := arr[0].(Name); n == "Separation" || n == "DeviceN" {
		return iccCMYKProfile(doc, arr[2])
	}
	return nil
}
