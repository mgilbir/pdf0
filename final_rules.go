package pdf0

import (
	"bytes"
	"strings"
)

// This file collects the remaining low-frequency PDF/A rules: prohibited
// catalog/page entries (PDF/A-4), image interpolation and rendering-intent
// restrictions on inline and image XObjects, and file-trailer identifier
// validity. Grounded in ISO 32000-1 8.9.5 (images), 8.9.7 (inline images),
// 14.4 (file identifiers) and the ISO 19005 clause-6 prohibitions.

// checkProhibitedCatalogEntries flags document-level features prohibited by
// PDF/A-4: alternate presentations, page presentation steps, and the
// Requirements dictionary (ISO 19005-4 6.11, 6.12).
func checkProhibitedCatalogEntries(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	if catalog.Get("Requirements") != nil {
		errs = append(errs, ValidationError{Rule: "6.12", Level: level,
			Message: "document catalog must not contain a /Requirements entry"})
	}
	if names := doc.ResolveDict(catalog.Get("Names")); names != nil {
		if names.Get("AlternatePresentations") != nil {
			errs = append(errs, ValidationError{Rule: "6.11", Level: level,
				Message: "document name dictionary must not contain /AlternatePresentations"})
		}
	}
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		if page.dict.Get("PresSteps") != nil {
			errs = append(errs, ValidationError{Rule: "6.11", Level: level,
				Message: "page dictionary must not contain /PresSteps (presentation steps)",
				Object:  page.objNum})
		}
	}
	return errs
}

// checkImageIntentAndInterpolate flags Image XObjects and inline images that
// carry Interpolate/true or a non-standard rendering intent (ISO 19005-2
// 6.2.4/6.2.6, -4 6.2.7/6.2.9; ISO 32000-1 8.9.5.2, 8.9.5.4).
func checkImageIntentAndInterpolate(doc *Document, level PDFALevel) []ValidationError {
	interpRule := "6.2.7"
	intentRule := "6.2.9"
	switch level {
	case PDFA1b:
		interpRule, intentRule = "6.2.4", "6.2.4"
	case PDFA2b, PDFA3b:
		interpRule, intentRule = "6.2.8", "6.2.6"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(rule, msg string, obj int) {
		key := rule + msg
		if seen[key] {
			return
		}
		seen[key] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	// Image XObject /Intent (Interpolate on image XObjects is already
	// checked by checkInterpolate).
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if st, _ := stream.Dict.Get("Subtype").(Name); st != "Image" {
			continue
		}
		if intent, ok := doc.Resolve(stream.Dict.Get("Intent")).(Name); ok && !standardRenderingIntents[string(intent)] {
			add(intentRule, "an image dictionary uses a non-standard rendering intent", num)
		}
	}

	// Inline images: /I (Interpolate) must not be true; /Intent must be a
	// standard rendering intent.
	for num, data := range collectContentStreamData(doc) {
		for _, e := range inlineImageEntries(data) {
			if e["I"] == "true" || e["Interpolate"] == "true" {
				add(interpRule, "an inline image uses Interpolate true", num)
			}
			if v := e["Intent"]; v != "" && !standardRenderingIntents[v] {
				add(intentRule, "an inline image uses a non-standard rendering intent", num)
			}
		}
	}
	return errs
}

// checkFileTrailerID validates the file identifier: when present, /ID shall
// be an array of exactly two non-empty byte strings (ISO 32000-1 14.4,
// ISO 19005-2 6.1.3).
func checkFileTrailerID(doc *Document, level PDFALevel) []ValidationError {
	rule := "6.1.3"
	idObj := doc.Trailer.Get("ID")
	if idObj == nil {
		return nil
	}
	arr, ok := doc.Resolve(idObj).(Array)
	valid := ok && len(arr) == 2
	if valid {
		for _, el := range arr {
			s, ok := el.(String)
			if !ok || len(s.Value) == 0 {
				valid = false
			}
		}
	}
	if !valid {
		return []ValidationError{{Rule: rule, Level: level,
			Message: "trailer /ID must be an array of two non-empty file-identifier strings"}}
	}
	return nil
}

// inlineImageEntries returns the parameter dictionary of every inline image
// in a content stream as key -> first-value-token maps.
func inlineImageEntries(data []byte) []map[string]string {
	var out []map[string]string
	n := len(data)
	i := 0
	for i < n {
		if data[i] == 'B' && i+1 < n && data[i+1] == 'I' &&
			(i == 0 || isContentWS(data[i-1]) || isContentDelim(data[i-1])) &&
			(i+2 >= n || isContentWS(data[i+2]) || isContentDelim(data[i+2])) {
			i += 2
			out = append(out, parseInlineDictEntries(data, &i))
			continue
		}
		i++
	}
	return out
}

// parseInlineDictEntries reads an inline image parameter dictionary up to ID,
// returning each key mapped to its first value token (names without the
// leading slash, or barewords such as true/false/numbers).
func parseInlineDictEntries(data []byte, pos *int) map[string]string {
	entries := map[string]string{}
	n := len(data)
	i := *pos
	var pendingKey string
	readToken := func() string {
		start := i
		if i < n && data[i] == '/' {
			i++
			start = i
		}
		for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
			i++
		}
		return string(data[start:i])
	}
	for i < n {
		switch b := data[i]; {
		case isContentWS(b):
			i++
		case b == 'I' && i+1 < n && data[i+1] == 'D' &&
			(i+2 >= n || isContentWS(data[i+2])):
			*pos = i + 2
			skipInlineImage(data, pos)
			return entries
		case b == '/':
			name := readToken()
			if pendingKey == "" {
				pendingKey = name
			} else {
				entries[pendingKey] = name
				pendingKey = ""
			}
		case b == '[':
			// Array value: record it opaquely and skip to ']'.
			i++
			for i < n && data[i] != ']' {
				i++
			}
			if i < n {
				i++
			}
			if pendingKey != "" {
				entries[pendingKey] = "[array]"
				pendingKey = ""
			}
		default:
			tok := readToken()
			if tok == "" {
				i++
				continue
			}
			if pendingKey != "" {
				entries[pendingKey] = tok
				pendingKey = ""
			}
		}
	}
	*pos = i
	return entries
}

// forbiddenAAEvents are the additional-action trigger events prohibited by
// PDF/A-4 (ISO 19005-4 6.6.3): document lifecycle events (WC/WS/DS/WP/DP),
// page navigation (O/C), and page-triggered annotation events (PO/PC/PV).
// User-interaction events (E, X, D, U, Fo, Bl, PI) remain permitted.
var forbiddenAAEvents = map[Name]bool{
	"WC": true, "WS": true, "DS": true, "WP": true, "DP": true,
	"O": true, "C": true, "PO": true, "PC": true, "PV": true,
}

// checkA4TriggerEvents flags AA dictionaries — on the catalog, pages, or
// annotations — that define a forbidden trigger event.
func checkA4TriggerEvents(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	report := func(aa *Dictionary, num int) {
		if aa == nil {
			return
		}
		for _, k := range aa.Keys {
			if forbiddenAAEvents[k] {
				errs = append(errs, ValidationError{Rule: "6.6.3", Level: level,
					Message: "an /AA dictionary must not contain the forbidden trigger event /" + string(k),
					Object:  num})
			}
		}
	}
	report(doc.ResolveDict(catalog.Get("AA")), 0)
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		report(doc.ResolveDict(page.dict.Get("AA")), page.objNum)
	}
	for num, iobj := range doc.Objects {
		if d, ok := iobj.Value.(*Dictionary); ok && isAnnotation(d) {
			report(doc.ResolveDict(d.Get("AA")), num)
		}
	}
	for _, a := range collectDirectAnnotations(doc) {
		report(doc.ResolveDict(a.dict.Get("AA")), a.num)
	}
	return errs
}

// isPUARune reports whether a code point is in a Unicode Private Use Area.
func isPUARune(r rune) bool {
	return r >= 0xE000 && r <= 0xF8FF ||
		r >= 0xF0000 && r <= 0xFFFFD ||
		r >= 0x100000 && r <= 0x10FFFD
}

// stringHasPUA reports whether a decoded PDF text string contains any Private
// Use Area code point.
func stringHasPUA(b []byte) bool {
	for _, r := range decodePDFTextString(b) {
		if isPUARune(r) {
			return true
		}
	}
	return false
}

// checkActualTextPUA enforces ISO 19005-4 6.2.10.8: an ActualText entry — in
// a structure element dictionary or a marked-content property list — must not
// contain Unicode Private Use Area values, which have no defined meaning.
func checkActualTextPUA(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: "6.2.10.8", Level: level, Message: msg, Object: obj})
	}

	// Structure element (and any) dictionaries carrying /ActualText.
	for num, iobj := range doc.Objects {
		if d, ok := iobj.Value.(*Dictionary); ok {
			if s, ok := d.Get("ActualText").(String); ok && stringHasPUA(s.Value) {
				add("an ActualText entry in a dictionary contains a Unicode Private Use Area value", num)
			}
		}
	}

	// Marked-content property lists inside content streams
	// (/Tag << /ActualText <...> >> BDC).
	for num, data := range collectContentStreamData(doc) {
		for _, v := range contentActualTexts(data) {
			if stringHasPUA(v) {
				add("an ActualText entry in a marked-content property list contains a Unicode Private Use Area value", num)
			}
		}
	}
	return errs
}

// contentActualTexts extracts the (decoded) value of every /ActualText entry
// appearing in a content stream's inline marked-content property lists.
func contentActualTexts(data []byte) [][]byte {
	var out [][]byte
	n := len(data)
	i := 0
	for i < n {
		// Find "/ActualText" as a name token.
		if data[i] == '/' && i+11 <= n && string(data[i+1:i+11]) == "ActualText" {
			i += 11
			for i < n && isContentWS(data[i]) {
				i++
			}
			if i < n && data[i] == '<' {
				j := i + 1
				for j < n && data[j] != '>' {
					j++
				}
				out = append(out, decodeHexBytes(data[i+1:j]))
				i = j + 1
				continue
			}
			if i < n && data[i] == '(' {
				str, next := decodeContentLiteralString(data, i)
				out = append(out, str)
				i = next
				continue
			}
		}
		i++
	}
	return out
}

// primaryColorants are the process colorants whose Type 5 halftone
// component must NOT carry a TransferFunction. A component for any other
// (non-primary) colorant must carry one, so its output can be mapped.
var primaryColorants = map[Name]bool{
	"Cyan": true, "Magenta": true, "Yellow": true, "Black": true, "Gray": true,
}

// halftoneReserved are the non-colorant keys of a Type 5 halftone dictionary.
var halftoneReserved = map[Name]bool{"Type": true, "HalftoneType": true, "HalftoneName": true}

// checkType5Halftones validates the TransferFunction usage in Type 5
// (multi-component) halftone dictionaries (ISO 19005-2/-4 6.2.5): a component
// for a process (primary) colorant must not contain a TransferFunction, and
// a component for a non-primary colorant must contain one.
func checkType5Halftones(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // 1b forbids transparency/halftone features via other rules
	}
	rule := "6.2.5"
	if level == PDFA4 {
		rule = "6.2.5"
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	// Only halftones actually applied through a used ExtGState count (the
	// corpus passes an unused Type 5 halftone with RGB colorants and a
	// TransferFunction).
	for _, d := range collectAppliedHalftones(doc) {
		if ht, _ := doc.Resolve(d.Get("HalftoneType")).(Integer); ht != 5 {
			continue
		}
		num := 0
		for _, key := range d.Keys {
			if halftoneReserved[key] || key == "Default" {
				continue
			}
			comp := doc.ResolveDict(d.Get(key))
			if comp == nil {
				continue
			}
			hasTF := comp.Get("TransferFunction") != nil
			if primaryColorants[key] {
				if hasTF {
					add("a Type 5 halftone component for a primary colorant must not contain a TransferFunction", num)
				}
			} else if !hasTF {
				add("a Type 5 halftone component for a non-primary colorant must contain a TransferFunction", num)
			}
		}
	}
	return errs
}

// collectAppliedHalftones returns every halftone dictionary referenced by the
// /HT entry of an ExtGState that is applied (via gs) in executed content.
func collectAppliedHalftones(doc *Document) []*Dictionary {
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	var out []*Dictionary
	seenHT := map[*Dictionary]bool{}
	seenC := map[*Dictionary]bool{}
	var walk func(container *Dictionary, data []byte)
	walk = func(container *Dictionary, data []byte) {
		if container == nil || seenC[container] || data == nil {
			return
		}
		seenC[container] = true
		res := resolveResources(doc, container)
		if res == nil {
			return
		}
		used := contentUsedNames(data)
		gsNames := scanContentColorUsage(data).gsNames
		if gsDict := doc.ResolveDict(res.Get("ExtGState")); gsDict != nil {
			for i, key := range gsDict.Keys {
				if !gsNames[string(key)] {
					continue
				}
				gs := doc.ResolveDict(gsDict.Values[i])
				if gs == nil {
					continue
				}
				if ht := doc.ResolveDict(gs.Get("HT")); ht != nil && !seenHT[ht] {
					seenHT[ht] = true
					out = append(out, ht)
				}
			}
		}
		if xobj := doc.ResolveDict(res.Get("XObject")); xobj != nil {
			for i, key := range xobj.Keys {
				if !used.xobjects[string(key)] {
					continue
				}
				if s, ok := doc.Resolve(xobj.Values[i]).(*Stream); ok {
					if st, _ := s.Dict.Get("Subtype").(Name); st == "Form" {
						walk(&s.Dict, decodeContentStream(doc, s))
					}
				}
			}
		}
	}
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		walk(page.dict, getContentStreamData(doc, page.dict.Get("Contents")))
	}
	return out
}

// checkEmbeddedPDFA enforces ISO 19005-4 6.9: an embedded file whose MIME
// subtype is application/pdf shall itself be a valid PDF/A document. Each
// such file is decoded and validated one level deep (a depth guard prevents
// unbounded recursion).
func checkEmbeddedPDFA(doc *Document, level PDFALevel) []ValidationError {
	if level != PDFA4 || doc.embeddedDepth > 0 {
		return nil
	}
	// PDF/A-4f and PDF/A-4e permit arbitrary embedded files; plain PDF/A-4
	// requires every embedded file to itself be a compliant PDF/A document
	// (ISO 19005-4 6.9).
	if c := pdfaConformanceFlag(doc); c == "F" || c == "E" {
		return nil
	}
	var errs []ValidationError
	for num, iobj := range doc.Objects {
		dict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		efDict := doc.ResolveDict(dict.Get("EF"))
		if efDict == nil {
			continue
		}
		for _, val := range efDict.Values {
			stream, ok := doc.Resolve(val).(*Stream)
			if !ok {
				continue
			}
			if !isPDFMIME(stream.Dict.Get("Subtype")) {
				errs = append(errs, ValidationError{Rule: "6.9", Level: level,
					Message: "an embedded file is not a PDF/A document (non-PDF type not permitted at PDF/A-4)", Object: num})
				continue
			}
			data, err := decodeStreamData(stream)
			if err != nil || len(data) == 0 {
				continue
			}
			if !embeddedPDFACompliant(data) {
				errs = append(errs, ValidationError{Rule: "6.9", Level: level,
					Message: "an embedded PDF file is not compliant with PDF/A", Object: num})
			}
		}
	}
	return errs
}

// pdfaConformanceFlag returns the document's XMP pdfaid:conformance value
// ("F", "E", "B", "A", ...) or "" if absent.
func pdfaConformanceFlag(doc *Document) string {
	catalog := getCatalog(doc)
	if catalog == nil {
		return ""
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*Stream)
	if !ok {
		return ""
	}
	xmp := decodeXMPToUTF8(stream.Data)
	if v := extractXMPValue(xmp, "pdfaid:conformance"); v != "" {
		return strings.ToUpper(v)
	}
	return strings.ToUpper(extractXMPAttr(xmp, "pdfaid:conformance"))
}

// isPDFMIME reports whether a stream /Subtype names the application/pdf MIME
// type (stored as the name /application#2Fpdf).
func isPDFMIME(subtype Object) bool {
	n, ok := subtype.(Name)
	return ok && string(n) == "application/pdf"
}

// embeddedPDFACompliant reports whether embedded PDF bytes parse as a PDF/A
// document and validate against their own declared conformance level.
func embeddedPDFACompliant(data []byte) bool {
	edoc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	elevel, ok := declaredPDFALevel(edoc)
	if !ok {
		return false // an embedded PDF that is not PDF/A at all
	}
	edoc.embeddedDepth = 1
	return len(ValidatePDFABytes(edoc, elevel, data)) == 0
}

// declaredPDFALevel reads the PDF/A conformance level a document claims via
// its XMP pdfaid:part / pdfaid:conformance identifiers.
func declaredPDFALevel(doc *Document) (PDFALevel, bool) {
	catalog := getCatalog(doc)
	if catalog == nil {
		return 0, false
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*Stream)
	if !ok {
		return 0, false
	}
	xmp := decodeXMPToUTF8(stream.Data)
	part := extractXMPValue(xmp, "pdfaid:part")
	if part == "" {
		part = extractXMPAttr(xmp, "pdfaid:part")
	}
	switch part {
	case "1":
		return PDFA1b, true
	case "2":
		return PDFA2b, true
	case "3":
		return PDFA3b, true
	case "4":
		return PDFA4, true
	}
	return 0, false
}

// extractXMPAttr reads an attribute-form XMP value (key="value").
func extractXMPAttr(xmp, key string) string {
	i := strings.Index(xmp, key+"=")
	if i < 0 {
		return ""
	}
	rest := xmp[i+len(key)+1:]
	if len(rest) == 0 {
		return ""
	}
	q := rest[0]
	if q != '"' && q != '\'' {
		return ""
	}
	end := strings.IndexByte(rest[1:], q)
	if end < 0 {
		return ""
	}
	return rest[1 : 1+end]
}

// checkInheritedPageXObject enforces that an XObject drawn (Do) by a page's
// content stream is present in the page's own resource dictionary rather than
// inherited from a /Pages tree node (ISO 19005-2 6.2.2, -4 6.2.2). Resource
// inheritance in general remains permitted; only a rendered XObject that is
// resolved solely through inheritance is rejected.
func checkInheritedPageXObject(doc *Document, level PDFALevel) []ValidationError {
	catalog := getCatalog(doc)
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		data := getContentStreamData(doc, page.dict.Get("Contents"))
		if data == nil {
			continue
		}
		used := contentUsedNames(data)
		if len(used.xobjects) == 0 {
			continue
		}
		var ownXObj *Dictionary
		if own := doc.ResolveDict(page.dict.Get("Resources")); own != nil {
			ownXObj = doc.ResolveDict(own.Get("XObject"))
		}
		reported := false
		for name := range used.xobjects {
			if ownXObj == nil || ownXObj.Get(Name(name)) == nil {
				if !reported {
					reported = true
					errs = append(errs, ValidationError{Rule: "6.2.2", Level: level,
						Message: "page content draws an XObject that is inherited from a Pages node rather than present in the page's own resource dictionary",
						Object:  page.objNum})
				}
			}
		}
	}
	return errs
}
