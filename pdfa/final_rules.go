package pdfa

import (
	"bytes"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
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

// checkProhibitedCatalogEntries flags document-level features PDF/A-2 and
// later prohibit: alternate presentations, page presentation steps, and the
// Requirements dictionary (ISO 19005-2/-3 6.10, 6.11; -4 6.11, 6.12).

// embeddedCheckFunc reports the PDF/A part embedded bytes declare (0 for none
// it could read), whether they are a conforming PDF/A file, and whether the
// check ran to completion. Reading a whole document out
// of a byte slice needs the parser, which these checks deliberately do not
// depend on, so the caller hands one in per run.
//
// depth is the embedded document's own depth: 1 for a file embedded in the
// top-level document, 2 for one embedded in that, and so on. The checker
// validates the nested document at that depth, so its own embedded files are
// checked in turn, down to MaxEmbeddedDepth.
type embeddedCheckFunc func(cancel core.Canceler, data []byte, lim core.Limits, depth int) (part int, compliant, complete bool)

// MaxEmbeddedDepth is how deep the embedded-PDF/A rule follows embedded files
// into embedded files. A document nested deeper is not validated: the rule
// notes that it stopped (a "limit" finding), rather than passing it — which is
// what it did at every depth below the first before (audit 2026-09-22 C137).
// The file-type requirement is not a matter of depth and holds at all of them.
const MaxEmbeddedDepth = 4

type embeddedSlot struct{}

type embeddedHolder struct{ check embeddedCheckFunc }

// setEmbeddedChecker installs the recursive embedded-file check for this run.
// It is per run rather than a package-level variable so that nothing is shared
// between concurrent validations.
func setEmbeddedChecker(v core.View, f embeddedCheckFunc) {
	core.Slot[embeddedHolder](v.Run, embeddedSlot{}).check = f
}

// embeddedChecker returns the run's checker, or one that declines to answer.
// Declining is the safe default: "we could not tell" must not be reported as
// "the embedded file is not PDF/A".
func embeddedChecker(v core.View) embeddedCheckFunc {
	if h := core.Slot[embeddedHolder](v.Run, embeddedSlot{}); h.check != nil {
		return h.check
	}
	return func(core.Canceler, []byte, core.Limits, int) (int, bool, bool) { return 0, false, false }
}

func checkProhibitedCatalogEntries(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil // 6.11 / 6.12 are clauses of ISO 19005 parts 2 and later
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []Violation
	// The clauses are numbered one apart by part: ISO 19005-2/-3 6.10
	// (alternate presentations, presentation steps) and 6.11 (requirements),
	// ISO 19005-4 6.11 and 6.12 (veraPDF 6.10-1/-2 and 6.11-1 at PDF/A-2).
	presRule, reqRule := "6.10", "6.11"
	if level.Part() == 4 {
		presRule, reqRule = "6.11", "6.12"
	}
	if catalog.Get("Requirements") != nil {
		errs = append(errs, Violation{Rule: reqRule, Level: level,
			Message: "document catalog must not contain a /Requirements entry"})
	}
	if names := doc.ResolveDict(catalog.Get("Names")); names != nil {
		if names.Get("AlternatePresentations") != nil {
			errs = append(errs, Violation{Rule: presRule, Level: level,
				Message: "document name dictionary must not contain /AlternatePresentations"})
		}
	}
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		if page.Dict.Get("PresSteps") != nil {
			errs = append(errs, Violation{Rule: presRule, Level: level,
				Message: "page dictionary must not contain /PresSteps (presentation steps)",
				Object:  page.ObjNum})
		}
	}
	return errs
}

// imageClauses is checkImageIntentAndInterpolate's two clauses per part.
func imageClauses(level Level) (interpolate, intent string) {
	switch level.Part() {
	case 1:
		return "6.2.4", "6.2.9"
	case 2, 3:
		return "6.2.8", "6.2.6"
	}
	return "6.2.7.1", ""
}

// checkImageIntentAndInterpolate flags Image XObjects and inline images that
// carry Interpolate/true or a non-standard rendering intent (ISO 32000-1
// 8.9.5.2, 8.9.5.4). The clauses are the veraPDF profiles': Interpolate is
// 6.2.4 at PDF/A-1, 6.2.8 at -2 and -3, and 6.2.7.1 at -4; a rendering intent
// is 6.2.9 at PDF/A-1 (any rendering intent, not only an image's) and 6.2.6 at
// -2 and -3. PDF/A-4's profile has no rendering-intent rule, and veraPDF
// passes a PDF/A-4 file with a non-standard one, so none is reported there.
func checkImageIntentAndInterpolate(doc core.View, level Level) []Violation {
	interpRule, intentRule := imageClauses(level)
	// One example per distinct rule and message, attributed to the lowest object
	// number that produced it — both loops below iterate maps.
	var found exampleFindings
	add := func(rule, msg string, obj int) {
		if rule != "" {
			found.add(Violation{Rule: rule, Level: level, Message: msg, Object: obj})
		}
	}

	// Image XObject /Intent (Interpolate on image XObjects is already
	// checked by checkInterpolate).
	for _, r := range doc.ReachableDicts() {
		stream, num := r.Stream, r.ObjNum
		if stream == nil {
			continue
		}
		if st, _ := doc.ResolveName(stream.Dict.Get("Subtype")); st != "Image" {
			continue
		}
		if intent, ok := doc.Resolve(stream.Dict.Get("Intent")).(object.Name); ok && !standardRenderingIntents[string(intent)] {
			add(intentRule, "an image dictionary uses a non-standard rendering intent", num)
		}
	}

	// Inline images: /I (Interpolate) must not be true; /Intent must be a
	// standard rendering intent.
	for num, f := range contentBytesFactsOf(doc) {
		for _, e := range f.inlineEntries {
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

// checkImageBitsPerComponent flags an image whose BitsPerComponent is not
// one the part allows: 1, 2, 4 or 8 at PDF/A-1 (veraPDF 6.2.4-4), and 16 too
// at -2, -3 (6.2.8-4) and -4 (6.2.7.1-4). A stencil mask (/ImageMask true) is
// exempt, as is an image that leaves the key out (a JPEG 2000 image may).
// Inline images are images too: /BPC and /IM are their abbreviations.
// An explicit mask an image's /Mask names is 1 bit per sample at every part.
func checkImageBitsPerComponent(doc core.View, level Level) []Violation {
	rule := imageClause("image", level)
	allowed := func(bpc string) bool {
		switch bpc {
		case "1", "2", "4", "8":
			return true
		case "16":
			return level.Part() != 1
		}
		return false
	}
	var found exampleFindings
	for _, r := range doc.ReachableDicts() {
		stream, num := r.Stream, r.ObjNum
		if stream == nil {
			continue
		}
		if st, _ := doc.ResolveName(stream.Dict.Get("Subtype")); st != "Image" {
			continue
		}
		// An explicit mask, the stencil an image's /Mask names, is 1 bit
		// per sample (ISO 32000-1 8.9.6.3; veraPDF 6.2.4-5, 6.2.8-5,
		// 6.2.7.1-5).
		if mask, ok := doc.Resolve(stream.Dict.Get("Mask")).(*object.Stream); ok {
			if n, ok := doc.ResolveInt(mask.Dict.Get("BitsPerComponent")); ok && n != 1 {
				found.add(Violation{Rule: rule, Level: level, Object: num,
					Message: fmt.Sprintf("an image's explicit /Mask has BitsPerComponent %d; a mask's is 1", n)})
			}
		}
		if m, ok := doc.ResolveBool(stream.Dict.Get("ImageMask")); ok && bool(m) {
			continue
		}
		bpcObj := stream.Dict.Get("BitsPerComponent")
		if bpcObj == nil {
			continue
		}
		bpc := "?"
		if n, ok := doc.Resolve(bpcObj).(object.Integer); ok {
			bpc = fmt.Sprint(int64(n))
		}
		if !allowed(bpc) {
			found.add(Violation{Rule: rule, Level: level, Object: num,
				Message: fmt.Sprintf("an image dictionary's BitsPerComponent %s is not one PDF/A-%d allows", bpc, level.Part())})
		}
	}
	for num, f := range contentBytesFactsOf(doc) {
		for _, e := range f.inlineEntries {
			if e["IM"] == "true" || e["ImageMask"] == "true" {
				continue
			}
			bpc, ok := e["BPC"]
			if !ok {
				bpc, ok = e["BitsPerComponent"]
			}
			if ok && !allowed(bpc) {
				found.add(Violation{Rule: rule, Level: level, Object: num,
					Message: fmt.Sprintf("an inline image's BitsPerComponent %s is not one PDF/A-%d allows", bpc, level.Part())})
			}
		}
	}
	return found.errs
}

// checkFileTrailerID validates the file identifier: when present, /ID shall
// be an array of exactly two non-empty byte strings (ISO 32000-1 14.4,
// ISO 19005-2 6.1.3).
func checkFileTrailerID(doc core.View, level Level) []Violation {
	rule := "6.1.3"
	idObj := doc.Trailer.Get("ID")
	if idObj == nil {
		return nil
	}
	arr, ok := doc.Resolve(idObj).(object.Array)
	valid := ok && len(arr) == 2
	if valid {
		for _, el := range arr {
			s, ok := doc.Resolve(el).(object.String) // string: the file identifier is never encrypted (ISO 32000-2 7.6.2), so its bytes are its value even in a Locked document
			if !ok || len(s.Value) == 0 {
				valid = false
			}
		}
	}
	if !valid {
		return []Violation{{Rule: rule, Level: level,
			Message: "trailer /ID must be an array of two non-empty file-identifier strings"}}
	}
	return nil
}

// inlineImageEntries returns the parameter dictionary of every inline image
// in a content stream as key -> first-value-token maps: a name without its
// slash, a bareword or number as written, and "[array]" for an array.
func inlineImageEntries(cancel core.Canceler, data []byte) []map[string]string {
	return scanContentBytes(cancel, data).inlineEntries
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
func checkA4TriggerEvents(doc core.View, level Level) []Violation {
	if level.Part() != 4 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []Violation
	report := func(aa *object.Dictionary, num int) {
		if aa == nil {
			return
		}
		for k := range aa.Keys() {
			if forbiddenAAEvents[k] {
				errs = append(errs, Violation{Rule: "6.6.3", Level: level,
					Message: "an /AA dictionary must not contain the forbidden trigger event /" + string(k),
					Object:  num})
			}
		}
	}
	report(doc.ResolveDict(catalog.Get("AA")), 0)
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		report(doc.ResolveDict(page.Dict.Get("AA")), page.ObjNum)
	}
	for _, a := range reachableAnnotations(doc) {
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
func checkActualTextPUA(doc core.View, level Level) []Violation {
	if level.Part() != 4 {
		return nil
	}
	// One example per distinct message, attributed to the lowest object number
	// that produced it — both loops below iterate maps.
	var found exampleFindings
	add := func(msg string, obj int) {
		found.add(Violation{Rule: "6.2.10.8", Level: level, Message: msg, Object: obj})
	}

	// Structure element (and any) dictionaries carrying /ActualText.
	for _, r := range doc.ReachableDicts() {
		if r.Stream != nil {
			continue
		}
		if s, reason := doc.StringValue(r.Dict.Get("ActualText")); reason == core.ReasonOK && stringHasPUA(s.Value) {
			add("an ActualText entry in a dictionary contains a Unicode Private Use Area value", r.ObjNum)
		}
	}

	// Marked-content property lists inside content streams
	// (/Tag << /ActualText <...> >> BDC).
	for num, f := range contentBytesFactsOf(doc) {
		for _, v := range f.actualTexts {
			if stringHasPUA(v) {
				add("an ActualText entry in a marked-content property list contains a Unicode Private Use Area value", num)
			}
		}
	}
	return found.errs
}

// contentActualTexts extracts the (decoded) value of every /ActualText entry
// appearing in a content stream's inline marked-content property lists: a
// name token /ActualText followed by a string. Dictionaries are read token by
// token, not skipped, because that is where the entry lives.
func contentActualTexts(cancel core.Canceler, data []byte) [][]byte {
	return scanContentBytes(cancel, data).actualTexts
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
func checkType5Halftones(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil // 1b forbids transparency/halftone features via other rules
	}
	var errs []Violation
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, Violation{Rule: "6.2.5", Level: level, Message: msg, Object: obj})
	}

	// Only halftones actually applied through a used ExtGState count (the
	// corpus passes an unused Type 5 halftone with RGB colorants and a
	// TransferFunction).
	for _, d := range collectAppliedHalftones(doc) {
		if ht, _ := doc.Resolve(d.Get("HalftoneType")).(object.Integer); ht != 5 {
			continue
		}
		num := objNumForDict(doc, d)
		for key := range d.Keys() {
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
	var walk func(container *object.Dictionary, data []byte, key *object.Stream, depth int)
	walk = func(container *object.Dictionary, data []byte, key *object.Stream, depth int) {
		if !doc.Descend(depth) {
			return
		}
		doc.Charge(1)
		if container == nil || seenC[container] || data == nil {
			return
		}
		seenC[container] = true
		res := doc.Resources(container)
		if res == nil {
			return
		}
		used := doc.ContentUsedNamesCached(data, key)
		gsNames := contentColorUsageOf(doc, data, key).gsNames
		if gsDict := doc.ResolveDict(res.Get("ExtGState")); gsDict != nil {
			for key, gref := range gsDict.All() {
				if !gsNames[string(key)] {
					continue
				}
				gs := doc.ResolveDict(gref)
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
			for key, xref := range xobj.All() {
				if !used.XObjects[string(key)] {
					continue
				}
				if s, ok := doc.Resolve(xref).(*object.Stream); ok {
					if st, _ := doc.ResolveName(s.Dict.Get("Subtype")); st == "Form" {
						data, _ := doc.Content(s) // reason: presence-only; the producer recorded any declined trip
						walk(&s.Dict, data, s, depth+1)
					}
				}
			}
		}
	}
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		data, key, _ := doc.ContentBytesAndKey(page.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		walk(page.Dict, data, key, 0)
	}
	return out
}

// checkEmbeddedPDFA enforces that an embedded file is itself a PDF/A
// document: one compliant with PDF/A-1 or -2 at PDF/A-2 (ISO 19005-2 6.8;
// veraPDF 6.8-5), and with whatever level it declares at plain
// PDF/A-4 (ISO 19005-4 6.9). PDF/A-3, -4e and -4f permit any embedded file.
//
// The file is judged by what it holds, as veraPDF judges it, not by the MIME
// type its stream claims: a PDF/A document labelled text/plain is one, and
// bytes that are not a PDF at all are not, whatever their label. Each PDF is
// decoded and validated in turn, which applies this rule to its own embedded
// files, down to MaxEmbeddedDepth levels of nesting; below that a PDF is noted
// as not validated. Whether the bytes are a PDF at all is checked at every
// depth.
func checkEmbeddedPDFA(doc core.View, level Level) []Violation {
	rule := "6.9"
	switch {
	case level.Part() == 2:
		rule = "6.8"
	case level.Part() != 4, level.variant() != "":
		return nil
	}
	var errs []Violation
	seen := map[*object.Stream]bool{}
	for _, r := range doc.ReachableDicts() {
		// One iteration can run a whole nested validation, so this is a
		// cancellation boundary in its own right (cancel.go).
		if doc.Cancel.Stopped() {
			break
		}
		dict, num := r.Dict, r.ObjNum
		if r.Stream != nil {
			continue
		}
		efDict := doc.ResolveDict(dict.Get("EF"))
		if efDict == nil {
			continue
		}
		for val := range efDict.Values() {
			stream, ok := doc.Resolve(val).(*object.Stream)
			if !ok || seen[stream] {
				continue
			}
			seen[stream] = true
			data, r := doc.Decode(stream)
			if r.Declined() {
				continue // not read: the producer recorded the trip
			}
			if r == core.ReasonMalformed {
				// The embedded file's data does not decode, so whatever it was
				// meant to be, it is not a PDF/A document anyone can open. It
				// used to be skipped, which reported nothing at all.
				errs = append(errs, Violation{Rule: rule, Level: level,
					Message: "an embedded file's stream data does not decode, so it is not a valid PDF/A document", Object: num})
				continue
			}
			if !looksLikePDF(data) {
				errs = append(errs, Violation{Rule: rule, Level: level,
					Message: fmt.Sprintf("an embedded file is not a PDF/A document (non-PDF type not permitted at %s)", level), Object: num})
				continue
			}
			if doc.EmbeddedDepth >= MaxEmbeddedDepth {
				doc.Note(core.GuardEmbeddedPDFA, fmt.Sprintf("an embedded PDF file is nested more than %d levels deep, so its PDF/A conformance (%s) was not checked", MaxEmbeddedDepth, rule), num)
				continue
			}
			part, compliant, complete := embeddedChecker(doc)(doc.Cancel, data, doc.Limits, doc.EmbeddedDepth+1)
			// The nested validation shares this run's work meter. If it
			// spent the budget, or the context ended, its verdict is a
			// partial one — its reading of the file may have been refused
			// for that reason — so this check is unwound rather than allowed
			// to draw a conclusion from it (core.Meter).
			doc.CheckStopped()
			if !complete {
				// The nested run reported a checker finding of its own — a guard
				// tripped inside it, a check panicked, or the shared context
				// ended. Counting that as "not compliant" would be the package's
				// central mistake: asserting a violation on the strength of an
				// incomplete result (limits_report.go). Decline, and report the
				// incompleteness under "limit" so it is attributable.
				doc.Note(core.GuardEmbeddedPDFA, fmt.Sprintf("an embedded PDF file could not be validated to completion, so its PDF/A conformance (%s) was neither confirmed nor denied", rule), num)
				continue
			}
			switch {
			case !compliant:
				errs = append(errs, Violation{Rule: rule, Level: level,
					Message: "an embedded PDF file is not compliant with PDF/A", Object: num})
			case level.Part() == 2 && part != 1 && part != 2:
				errs = append(errs, Violation{Rule: rule, Level: level,
					Message: fmt.Sprintf("an embedded file is a PDF/A-%d document; PDF/A-2 permits only PDF/A-1 and PDF/A-2 ones", part), Object: num})
			}
		}
	}
	return errs
}

// looksLikePDF reports whether data has a PDF header within its first 1024
// bytes, where ISO 32000-1 7.5.2 lets a reader find one.
func looksLikePDF(data []byte) bool {
	return bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-"))
}

// checkInheritedPageXObject enforces that an XObject drawn (Do) by a page's
// content stream is present in the page's own resource dictionary rather than
// inherited from a /Pages tree node (ISO 19005-2 6.2.2, -4 6.2.2; veraPDF
// 6.2.2-2, its inherited resource names). Resource inheritance in general remains
// permitted; only a rendered XObject that is resolved through inheritance is
// rejected. A name the page tree does not define either is not inherited but
// undefined, contentClause's to judge; and PDF/A-1 has neither rule.
func checkInheritedPageXObject(doc core.View, level Level) []Violation {
	if level.Part() == 1 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []Violation
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		data, key, _ := doc.ContentBytesAndKey(page.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		if data == nil {
			continue
		}
		used := doc.ContentUsedNamesCached(data, key)
		if len(used.XObjects) == 0 {
			continue
		}
		// A page with resources of its own inherits none (ISO 32000-1 7.7.3.4:
		// the attribute is inherited whole, not merged).
		if page.Dict.Get("Resources") != nil {
			continue
		}
		var inherited *object.Dictionary
		if res := doc.Resources(page.Dict); res != nil {
			inherited = doc.ResolveDict(res.Get("XObject"))
		}
		reported := false
		for name := range used.XObjects {
			if inherited != nil && inherited.Get(object.Name(name)) != nil {
				if !reported {
					reported = true
					errs = append(errs, Violation{Rule: "6.2.2", Level: level,
						Message: "page content draws an XObject that is inherited from a Pages node rather than present in the page's own resource dictionary",
						Object:  page.ObjNum})
				}
			}
		}
	}
	return errs
}
