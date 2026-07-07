package pdf0

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
