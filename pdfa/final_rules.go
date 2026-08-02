package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
)

// This file collects the remaining low-frequency PDF/A rules: prohibited
// catalog/page entries (PDF/A-4), image interpolation and rendering-intent
// restrictions on inline and image XObjects, and file-trailer identifier
// validity. Grounded in ISO 32000-1 8.9.5 (images), 8.9.7 (inline images),
// 14.4 (file identifiers) and the ISO 19005 clause-6 prohibitions.
//
// It has since collected more rules of the same kind: PDF/A-4 trigger events
// (6.6.3), ActualText Private Use Area values (6.2.10.8), Type 5 halftone
// components (6.2.5), embedded PDF/A files (6.9, validated by re-reading the
// embedded bytes under an embeddedDepth guard) and the inherited-page-XObject
// rule (6.2.2). Several are scoped to executed content — only halftones
// reached through an applied ExtGState, only XObjects actually drawn — because
// the corpus passes conforming files that carry unused non-conforming ones.

// checkProhibitedCatalogEntries flags document-level features prohibited by
// PDF/A-4: alternate presentations, page presentation steps, and the
// Requirements dictionary (ISO 19005-4 6.11, 6.12).

// EmbeddedChecker reports whether embedded PDF bytes are a conforming PDF/A
// file, and whether the check ran to completion. Reading a whole document out
// of a byte slice needs the parser, which these checks deliberately do not
// depend on, so the caller hands one in per run.
type EmbeddedChecker func(cancel core.Canceler, data []byte, lim core.Limits) (compliant, complete bool)

type embeddedSlot struct{}

type embeddedHolder struct{ check EmbeddedChecker }

// SetEmbeddedChecker installs the recursive embedded-file check for this run.
// It is per run rather than a package-level variable so that nothing is shared
// between concurrent validations.
func SetEmbeddedChecker(v core.View, f EmbeddedChecker) {
	core.Slot[embeddedHolder](v.Run, embeddedSlot{}).check = f
}

// embeddedChecker returns the run's checker, or one that declines to answer.
// Declining is the safe default: "we could not tell" must not be reported as
// "the embedded file is not PDF/A".
func embeddedChecker(v core.View) EmbeddedChecker {
	if h := core.Slot[embeddedHolder](v.Run, embeddedSlot{}); h.check != nil {
		return h.check
	}
	return func(core.Canceler, []byte, core.Limits) (bool, bool) { return false, false }
}

func checkProhibitedCatalogEntries(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // 6.11 / 6.12 are clauses of ISO 19005 parts 2 and later
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	// 6.12 (embedded-file requirements) applies only to PDF/A-4.
	if level == PDFA4 && catalog.Get("Requirements") != nil {
		errs = append(errs, ValidationError{Rule: "6.12", Level: level,
			Message: "document catalog must not contain a /Requirements entry"})
	}
	// 6.11 forbids alternate presentations at PDF/A-2, -3, and -4 (ISO 19005-2/-3
	// 6.11, 19005-4 6.11), not only A-4.
	if names := doc.ResolveDict(catalog.Get("Names")); names != nil {
		if names.Get("AlternatePresentations") != nil {
			errs = append(errs, ValidationError{Rule: "6.11", Level: level,
				Message: "document name dictionary must not contain /AlternatePresentations"})
		}
	}
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		if page.Dict.Get("PresSteps") != nil {
			errs = append(errs, ValidationError{Rule: "6.11", Level: level,
				Message: "page dictionary must not contain /PresSteps (presentation steps)",
				Object:  page.ObjNum})
		}
	}
	return errs
}

// checkImageIntentAndInterpolate flags Image XObjects and inline images that
// carry Interpolate/true or a non-standard rendering intent (ISO 19005-2
// 6.2.4/6.2.6, -4 6.2.7/6.2.9; ISO 32000-1 8.9.5.2, 8.9.5.4).
func checkImageIntentAndInterpolate(doc core.View, level PDFALevel) []ValidationError {
	interpRule := "6.2.7"
	intentRule := "6.2.9"
	switch level {
	case PDFA1b:
		interpRule, intentRule = "6.2.4", "6.2.4"
	case PDFA2b, PDFA3b:
		interpRule, intentRule = "6.2.8", "6.2.6"
	}
	// One example per distinct rule and message, attributed to the lowest object
	// number that produced it — both loops below iterate maps.
	var found exampleFindings
	add := func(rule, msg string, obj int) {
		found.add(ValidationError{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	// Image XObject /Intent (Interpolate on image XObjects is already
	// checked by checkInterpolate).
	for num, iobj := range doc.Objects {
		stream, ok := iobj.Value.(*object.Stream)
		if !ok {
			continue
		}
		if st, _ := stream.Dict.Get("Subtype").(object.Name); st != "Image" {
			continue
		}
		if intent, ok := doc.Resolve(stream.Dict.Get("Intent")).(object.Name); ok && !standardRenderingIntents[string(intent)] {
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
	return found.errs
}

// checkFileTrailerID validates the file identifier: when present, /ID shall
// be an array of exactly two non-empty byte strings (ISO 32000-1 14.4,
// ISO 19005-2 6.1.3).
func checkFileTrailerID(doc core.View, level PDFALevel) []ValidationError {
	rule := "6.1.3"
	idObj := doc.Trailer.Get("ID")
	if idObj == nil {
		return nil
	}
	arr, ok := doc.Resolve(idObj).(object.Array)
	valid := ok && len(arr) == 2
	if valid {
		for _, el := range arr {
			s, ok := el.(object.String)
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
			(i == 0 || core.IsContentWS(data[i-1]) || core.IsContentDelim(data[i-1])) &&
			(i+2 >= n || core.IsContentWS(data[i+2]) || core.IsContentDelim(data[i+2])) {
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
		for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
			i++
		}
		return string(data[start:i])
	}
	for i < n {
		switch b := data[i]; {
		case core.IsContentWS(b):
			i++
		case b == 'I' && i+1 < n && data[i+1] == 'D' &&
			(i+2 >= n || core.IsContentWS(data[i+2])):
			*pos = i + 2
			core.SkipInlineImage(data, pos)
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
			// object.Array value: record it opaquely and skip to ']'.
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
var forbiddenAAEvents = map[object.Name]bool{
	"WC": true, "WS": true, "DS": true, "WP": true, "DP": true,
	"O": true, "C": true, "PO": true, "PC": true, "PV": true,
}

// checkA4TriggerEvents flags AA dictionaries — on the catalog, pages, or
// annotations — that define a forbidden trigger event.
func checkA4TriggerEvents(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	report := func(aa *object.Dictionary, num int) {
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
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		report(doc.ResolveDict(page.Dict.Get("AA")), page.ObjNum)
	}
	for num, iobj := range doc.Objects {
		if d, ok := iobj.Value.(*object.Dictionary); ok && core.IsAnnotation(d) {
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
	for _, r := range core.DecodePDFTextString(b) {
		if isPUARune(r) {
			return true
		}
	}
	return false
}

// checkActualTextPUA enforces ISO 19005-4 6.2.10.8: an ActualText entry — in
// a structure element dictionary or a marked-content property list — must not
// contain Unicode Private Use Area values, which have no defined meaning.
func checkActualTextPUA(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA4 {
		return nil
	}
	// One example per distinct message, attributed to the lowest object number
	// that produced it — both loops below iterate maps.
	var found exampleFindings
	add := func(msg string, obj int) {
		found.add(ValidationError{Rule: "6.2.10.8", Level: level, Message: msg, Object: obj})
	}

	// Structure element (and any) dictionaries carrying /ActualText.
	for num, iobj := range doc.Objects {
		if d, ok := iobj.Value.(*object.Dictionary); ok {
			if s, ok := d.Get("ActualText").(object.String); ok && stringHasPUA(s.Value) {
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
	return found.errs
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
			for i < n && core.IsContentWS(data[i]) {
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
				str, next := core.DecodeContentLiteralString(data, i)
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
var primaryColorants = map[object.Name]bool{
	"Cyan": true, "Magenta": true, "Yellow": true, "Black": true, "Gray": true,
}

// halftoneReserved are the non-colorant keys of a Type 5 halftone dictionary.
var halftoneReserved = map[object.Name]bool{"Type": true, "HalftoneType": true, "HalftoneName": true}

// checkType5Halftones validates the TransferFunction usage in Type 5
// (multi-component) halftone dictionaries (ISO 19005-2/-4 6.2.5): a component
// for a process (primary) colorant must not contain a TransferFunction, and
// a component for a non-primary colorant must contain one.
func checkType5Halftones(doc core.View, level PDFALevel) []ValidationError {
	if level == PDFA1b {
		return nil // 1b forbids transparency/halftone features via other rules
	}
	var errs []ValidationError
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, ValidationError{Rule: "6.2.5", Level: level, Message: msg, Object: obj})
	}

	// Only halftones actually applied through a used ExtGState count (the
	// corpus passes an unused Type 5 halftone with RGB colorants and a
	// TransferFunction).
	for _, d := range collectAppliedHalftones(doc) {
		if ht, _ := doc.Resolve(d.Get("HalftoneType")).(object.Integer); ht != 5 {
			continue
		}
		num := objNumForDict(doc, d)
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
func collectAppliedHalftones(doc core.View) []*object.Dictionary {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var out []*object.Dictionary
	seenHT := map[*object.Dictionary]bool{}
	seenC := map[*object.Dictionary]bool{}
	var walk func(container *object.Dictionary, data []byte, key *object.Stream)
	walk = func(container *object.Dictionary, data []byte, key *object.Stream) {
		if container == nil || seenC[container] || data == nil {
			return
		}
		seenC[container] = true
		res := doc.Resources(container)
		if res == nil {
			return
		}
		used := doc.ContentUsedNamesCached(data, key)
		gsNames := scanContentColorUsage(doc.Cancel, data).gsNames
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
				if !used.XObjects[string(key)] {
					continue
				}
				if s, ok := doc.Resolve(xobj.Values[i]).(*object.Stream); ok {
					if st, _ := s.Dict.Get("Subtype").(object.Name); st == "Form" {
						walk(&s.Dict, doc.Content(s), s)
					}
				}
			}
		}
	}
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		data, key := doc.ContentBytesAndKey(page.Dict.Get("Contents"))
		walk(page.Dict, data, key)
	}
	return out
}

// checkEmbeddedPDFA enforces ISO 19005-4 6.9: an embedded file whose MIME
// subtype is application/pdf shall itself be a valid PDF/A document. Each
// such file is decoded and validated one level deep (a depth guard prevents
// unbounded recursion).
func checkEmbeddedPDFA(doc core.View, level PDFALevel) []ValidationError {
	if level != PDFA4 || doc.EmbeddedDepth > 0 {
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
		// One iteration can run a whole nested validation, so this is a
		// cancellation boundary in its own right (cancel.go).
		if doc.Cancel.Stopped() {
			break
		}
		dict, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		efDict := doc.ResolveDict(dict.Get("EF"))
		if efDict == nil {
			continue
		}
		for _, val := range efDict.Values {
			stream, ok := doc.Resolve(val).(*object.Stream)
			if !ok {
				continue
			}
			if !isPDFMIME(stream.Dict.Get("Subtype")) {
				errs = append(errs, ValidationError{Rule: "6.9", Level: level,
					Message: "an embedded file is not a PDF/A document (non-PDF type not permitted at PDF/A-4)", Object: num})
				continue
			}
			data, err := core.DecodeStreamData(doc.Cancel, stream, doc.Limits)
			if err != nil || len(data) == 0 {
				continue
			}
			compliant, complete := embeddedChecker(doc)(doc.Cancel, data, doc.Limits)
			if !complete {
				// The nested run reported a checker finding of its own — a guard
				// tripped inside it, a check panicked, or the shared context
				// ended. Counting that as "not compliant" would be the package's
				// central mistake: asserting a violation on the strength of an
				// incomplete result (limits_report.go). Decline, and report the
				// incompleteness under "limit" so it is attributable.
				doc.Note(core.GuardEmbeddedPDFA, "an embedded PDF file could not be validated to completion, so its PDF/A conformance (6.9) was neither confirmed nor denied", num)
				continue
			}
			if !compliant {
				errs = append(errs, ValidationError{Rule: "6.9", Level: level,
					Message: "an embedded PDF file is not compliant with PDF/A", Object: num})
			}
		}
	}
	return errs
}

// pdfaConformanceFlag returns the document's XMP pdfaid:conformance value
// ("F", "E", "B", "A", ...) or "" if absent.
func pdfaConformanceFlag(doc core.View) string {
	catalog := doc.Catalog()
	if catalog == nil {
		return ""
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*object.Stream)
	if !ok {
		return ""
	}
	xmp := doc.XMPText(stream)
	if v := core.ExtractXMPValue(xmp, "pdfaid:conformance"); v != "" {
		return strings.ToUpper(v)
	}
	return strings.ToUpper(ExtractXMPAttr(xmp, "pdfaid:conformance"))
}

// isPDFMIME reports whether a stream /Subtype names the application/pdf MIME
// type (stored as the name /application#2Fpdf).
func isPDFMIME(subtype object.Object) bool {
	n, ok := subtype.(object.Name)
	return ok && string(n) == "application/pdf"
}

// ExtractXMPAttr reads an attribute-form XMP value (key="value").
func ExtractXMPAttr(xmp, key string) string {
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
func checkInheritedPageXObject(doc core.View, level PDFALevel) []ValidationError {
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []ValidationError
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		data, key := doc.ContentBytesAndKey(page.Dict.Get("Contents"))
		if data == nil {
			continue
		}
		used := doc.ContentUsedNamesCached(data, key)
		if len(used.XObjects) == 0 {
			continue
		}
		var ownXObj *object.Dictionary
		if own := doc.ResolveDict(page.Dict.Get("Resources")); own != nil {
			ownXObj = doc.ResolveDict(own.Get("XObject"))
		}
		reported := false
		for name := range used.XObjects {
			if ownXObj == nil || ownXObj.Get(object.Name(name)) == nil {
				if !reported {
					reported = true
					errs = append(errs, ValidationError{Rule: "6.2.2", Level: level,
						Message: "page content draws an XObject that is inherited from a Pages node rather than present in the page's own resource dictionary",
						Object:  page.ObjNum})
				}
			}
		}
	}
	return errs
}
