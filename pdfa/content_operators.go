package pdfa

import "github.com/mgilbir/pdf0/object"

import "github.com/mgilbir/pdf0/internal/core"

import "fmt"

import "github.com/mgilbir/pdf0/syntax"

// This file implements the PDF/A rules that are decided by reading content
// streams: the operator whitelist and rendering-intent operand (ISO 19005
// 6.2.2 over ISO 32000-1 Annex A Table A.1), resolution of named resources
// invoked by Do/sh/gs/cs/CS/Tf, the prohibition on drawn PostScript XObjects
// (6.2.5 at PDF/A-1, 6.2.9 later), the Annex C operand limits (6.1.12 /
// 6.1.13) and the PDF/A-4 ICC profile-identity rule (6.2.4.2).
//
// All of it follows the executed-content model: only content that actually
// reaches the page is scanned — page /Contents, annotation appearance streams,
// visibly rendered Type 3 glyph procedures, and the form XObjects and tiling
// patterns those invoke by name. A stream that nothing invokes cannot violate
// a rendering rule, and the corpus contains conforming files that rely on
// this (an undefined operator inside an uninvoked form must not be reported).

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
func checkContentStreamOperators(doc core.View, level Level) []Violation {
	rule := "6.2.2"
	var errs []Violation
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, Violation{Rule: rule, Level: level, Message: msg, Object: obj})
	}

	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	// Only EXECUTED content is validated: an operator inside a form XObject
	// that no content stream invokes does not appear on the page (the
	// corpus passes an UnknownOperator in an uninvoked form).
	w := &contentWalk{containers: map[*object.Dictionary]bool{}, facts: map[*object.Stream]*contentTokenFacts{}}
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		data, key, _ := doc.ContentBytesAndKey(page.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		walkExecutedContent(doc, page.Dict, data, key, page.ObjNum, w, add, 0)
	}

	// Annotation appearance streams (their /AP /N) are executed content too:
	// an operator not defined in the PDF imaging model is equally forbidden
	// there (ISO 19005-1 6.2.10; Isartor 6.2.10-t01-fail-c).
	for _, ap := range collectAppearanceStreams(doc) {
		if data, _ := doc.Content(ap.stream); data != nil { // reason: presence-only; the producer recorded any declined trip
			w.tokens(doc, data, ap.stream, doc.ResolveDict(ap.stream.Dict.Get("Resources")), ap.objNum, add)
		}
	}

	// Type 3 font glyph procedures are content streams whose named resources
	// must resolve in the Type 3 font's own /Resources — not inherited from
	// the page (ISO 19005 6.2.2; a glyph proc that references a colour space
	// present only in the page resources is invalid).
	for fontDict, u := range core.CollectFontTextUsage(doc) {
		if st, _ := doc.ResolveName(fontDict.Get("Subtype")); st != "Type3" || !rendersVisibly(u) {
			continue
		}
		res := doc.ResolveDict(fontDict.Get("Resources"))
		cps := doc.ResolveDict(fontDict.Get("CharProcs"))
		if cps == nil {
			continue
		}
		for cpVal := range cps.Values() {
			if cp, ok := doc.Resolve(cpVal).(*object.Stream); ok {
				if cpData, _ := doc.Content(cp); cpData != nil { // reason: presence-only; the producer recorded any declined trip
					w.tokens(doc, cpData, cp, res, u.ObjNum, add)
				}
			}
		}
	}
	return errs
}

// appearanceStream pairs an annotation appearance stream with the object number
// to attribute its violations to.
type appearanceStream struct {
	stream *object.Stream
	objNum int
}

// collectAppearanceStreams gathers the normal-appearance (/AP /N) streams of
// every annotation, following the button-widget form where /N is a
// sub-dictionary of appearance-state streams.
func collectAppearanceStreams(doc core.View) []appearanceStream {
	var out []appearanceStream
	add := func(n object.Object, objNum int) {
		switch v := doc.Resolve(n).(type) {
		case *object.Stream:
			out = append(out, appearanceStream{stream: v, objNum: objNum})
		case *object.Dictionary:
			for sv := range v.Values() {
				if s, ok := doc.Resolve(sv).(*object.Stream); ok {
					out = append(out, appearanceStream{stream: s, objNum: objNum})
				}
			}
		}
	}
	visit := func(annot *object.Dictionary, num int) {
		if ap := doc.ResolveDict(annot.Get("AP")); ap != nil {
			add(ap.Get("N"), num)
		}
	}
	for _, a := range reachableAnnotations(doc) {
		visit(a.dict, a.num)
	}
	return out
}

// contentWalk is the state of one checkContentStreamOperators run: the
// containers already walked, what each stream's tokens say (contentTokenFacts),
// and what each (stream, resources) pair was found to lack.
//
// A scan's findings depend on nothing but the stream's tokens and the
// resource dictionary its names resolve in, and every finding is reported
// once per message. So a stream is tokenised once, whoever draws it, and what
// its names need of a resource dictionary is judged once per distinct
// dictionary. It used to be tokenised once per referrer: one 16 MB appearance
// stream named by a hundred annotations was tokenised a hundred times,
// eighteen seconds for a 37 KB file (audit 2026-09-22 C39), and pages sharing
// a content stream each with its own copy of the same resources were
// tokenised once per page.
type contentWalk struct {
	containers map[*object.Dictionary]bool
	facts      map[*object.Stream]*contentTokenFacts
	judged     core.ResMemo[[]string]
}

// tokens reports a content stream's undefined operators, custom rendering
// intents and unresolved named resource references for (data, res), tokenising
// each stream once (key; a nil key is tokenised every time) and judging its
// resource references once per distinct resource dictionary.
func (w *contentWalk) tokens(doc core.View, data []byte, key *object.Stream, res *object.Dictionary, objNum int, add func(string, int)) {
	if msgs, ok := w.judged.Get(doc, key, res); ok {
		for _, m := range msgs {
			add(m, objNum)
		}
		return
	}
	f := w.facts[key]
	if f == nil {
		f = scanContentTokens(doc.Cancel, data)
		if key != nil && !doc.Cancel.Stopped() {
			w.facts[key] = f
		}
	}
	msgs := f.judge(doc, res)
	for _, m := range msgs {
		add(m, objNum)
	}
	if !doc.Cancel.Stopped() {
		w.judged.Put(key, res, msgs)
	}
}

// walkExecutedContent validates a content stream and recurses into the form
// XObjects and tiling patterns it actually invokes.
func walkExecutedContent(doc core.View, container *object.Dictionary, data []byte, key *object.Stream, objNum int, w *contentWalk, add func(string, int), depth int) {
	if !doc.Descend(depth) {
		return
	}
	doc.Charge(1)
	// One invocation scans one content stream and recurses into the forms and
	// patterns it draws, so this is the per-stream cancellation boundary of the
	// executed-content model (cancel.go).
	if container == nil || w.containers[container] || doc.Cancel.Stopped() {
		return
	}
	w.containers[container] = true
	res := doc.Resources(container)
	if data != nil {
		w.tokens(doc, data, key, res, objNum, add)
	}
	if res == nil {
		return
	}
	used := doc.ContentUsedNamesCached(data, key)
	if xobj := doc.ResolveDict(res.Get("XObject")); xobj != nil {
		for key, xref := range xobj.All() {
			if !used.XObjects[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(xref).(*object.Stream); ok {
				st, _ := doc.ResolveName(s.Dict.Get("Subtype"))
				xnum := resolveObjNum(doc, xref)
				// A PostScript XObject that is actually drawn is prohibited
				// (ISO 19005-1 6.2.5, -2/-3/-4 6.2.9).
				if st == "PS" {
					add("a drawn PostScript XObject is not permitted", xnum)
				} else if st == "Form" {
					if s2, _ := doc.ResolveName(s.Dict.Get("Subtype2")); s2 == "PS" {
						add("a drawn form XObject has /Subtype2 /PS (PostScript)", xnum)
					}
					if s.Dict.Get("PS") != nil {
						add("a drawn form XObject dictionary contains a /PS entry", xnum)
					}
					data, _ := doc.Content(s) // reason: presence-only; the producer recorded any declined trip
					walkExecutedContent(doc, &s.Dict, data, s, xnum, w, add, depth+1)
				}
			}
		}
	}
	if pat := doc.ResolveDict(res.Get("Pattern")); pat != nil {
		// A tiling pattern's findings anchor to the pattern's own object, as a
		// form XObject's do above. It used to be the entry's position in the
		// /Pattern dictionary, so a report named an object that had nothing to
		// do with the pattern (audit 2026-09-22 C143).
		for key, pref := range pat.All() {
			if !used.Patterns[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(pref).(*object.Stream); ok {
				data, _ := doc.Content(s) // reason: presence-only; the producer recorded any declined trip
				walkExecutedContent(doc, &s.Dict, data, s, resolveObjNum(doc, pref), w, add, depth+1)
			}
		}
	}
}

// contentTokenFacts is what one content stream's tokens say for rule 6.2.2,
// in the order the stream says it: a finding that holds whatever the
// resources (an undefined operator, a non-standard rendering intent), or a
// named resource the stream uses, whose presence depends on the resources it
// is drawn with. Each distinct fact is kept once, at its first occurrence.
type contentTokenFacts struct {
	events []contentTokenEvent
}

type contentTokenEvent struct {
	msg      string // a finding; empty for a resource reference
	category string // the resource category a reference names (XObject, Font, …)
	name     string
}

// resourceAbsentMsg is the finding for a named resource absent from its
// category's dictionary.
var resourceAbsentMsg = map[string]string{
	"XObject":    "content stream references an XObject that is absent from the resource dictionary",
	"Shading":    "content stream references a shading that is absent from the resource dictionary",
	"ExtGState":  "content stream references an ExtGState that is absent from the resource dictionary",
	"ColorSpace": "content stream references a colour space that is absent from the resource dictionary",
	"Font":       "content stream references a font that is absent from the resource dictionary",
}

// judge is the findings of the stream drawn with res, in the order a scan
// would have reported them.
func (f *contentTokenFacts) judge(doc core.View, res *object.Dictionary) []string {
	var out []string
	for _, e := range f.events {
		doc.Charge(1)
		if e.msg != "" {
			out = append(out, e.msg)
		} else if !namedResourcePresent(doc, res, e.category, e.name) {
			out = append(out, resourceAbsentMsg[e.category])
		}
	}
	return out
}

// scanContentTokens tokenises one content stream into its contentTokenFacts.
func scanContentTokens(cancel core.Canceler, data []byte) *contentTokenFacts {
	f := &contentTokenFacts{}
	seen := map[contentTokenEvent]bool{}
	add := func(e contentTokenEvent) {
		if !seen[e] {
			seen[e] = true
			f.events = append(f.events, e)
		}
	}
	ref := func(category, name string) {
		// No name captured: nothing to look up, and nothing to flag.
		if name != "" {
			add(contentTokenEvent{category: category, name: name})
		}
	}
	var lastName string
	lx := core.NewContentLexer(cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentName:
			lastName = t.Name()
			continue
		case core.ContentDictStart:
			// A property list is one operand; what is inside it is not an
			// operator.
			lx.SkipDict(&t)
			continue
		case core.ContentOperator:
		default:
			continue
		}
		s := string(t.Raw)
		if isContentOperand(s) {
			continue
		}
		if !contentOperators[s] {
			add(contentTokenEvent{msg: fmt.Sprintf("content stream contains an operator %q not defined in ISO 32000", s)})
			continue
		}
		switch s {
		case "ri":
			if lastName != "" && !standardRenderingIntents[lastName] {
				add(contentTokenEvent{msg: fmt.Sprintf("rendering intent operator (ri) uses a non-standard value /%s", lastName)})
			}
		case "Do":
			ref("XObject", lastName)
		case "sh":
			ref("Shading", lastName)
		case "gs":
			ref("ExtGState", lastName)
		case "cs", "CS":
			// The colour-space operand is either a built-in device space or
			// a name defined in the Resources /ColorSpace dictionary.
			if !builtinColorSpaceName[lastName] {
				ref("ColorSpace", lastName)
			}
		case "Tf":
			ref("Font", lastName)
		}
	}
	return f
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
func namedResourcePresent(doc core.View, res *object.Dictionary, category, name string) bool {
	if name == "" {
		return true // no name captured; do not flag
	}
	if res == nil {
		return false
	}
	sub := doc.ResolveDict(res.Get(object.Name(category)))
	if sub == nil {
		return false
	}
	return sub.Get(object.Name(name)) != nil
}

// builtinColorSpaceName lists the colour-space names selectable with cs/CS
// without a Resources entry (ISO 32000-1 8.6.3).
var builtinColorSpaceName = map[string]bool{
	"DeviceGray": true, "DeviceRGB": true, "DeviceCMYK": true, "Pattern": true,
}

// resolveObjNum returns the object number carried by an indirect reference, or 0
// for a direct object (which has no indirect identity). This is reference-number
// extraction, distinct from (*Document).dictObjNum / objNumForDict, which scan
// the object table for a dictionary's identity; callers that already hold the
// reference use this to avoid the scan.
func resolveObjNum(doc core.View, o object.Object) int {
	if ref, ok := o.(object.IndirectRef); ok {
		return ref.Number
	}
	return 0
}

// checkContentStreamLimits enforces the Annex C architectural limits on
// numeric and string operands within content streams (ISO 19005-1 6.1.12,
// -2/-3 6.1.13). The real magnitude and string-length limits differ by
// part; the integer limit (2^31-1) is universal.
func checkContentStreamLimits(doc core.View, level Level, lim implLimits, errs *[]Violation) {
	// One example per distinct message, attributed to the lowest object number
	// that produced it — collectContentStreamData returns a map, so the first
	// stream to breach a limit varies from run to run.
	// Flushed from a defer so that findings made before a panic still reach the
	// caller, as they did when add appended to *errs directly.
	var found exampleFindings
	defer func() { *errs = append(*errs, found.errs...) }()
	add := func(msg string, obj int) {
		found.add(Violation{Rule: lim.rule, Level: level, Message: msg, Object: obj})
	}
	for num, f := range contentBytesFactsOf(doc) {
		for _, s := range f.bigNumbers {
			checkContentNumberLimit([]byte(s), lim, num, add)
		}
		for _, n := range f.longStrings {
			if n > lim.stringLen {
				add(fmt.Sprintf("a content-stream string of %d bytes exceeds the maximum length %d", n, lim.stringLen), num)
			}
		}
	}
}

// checkContentNumberLimit validates a numeric content operand against the
// integer or real architectural limit. A token that is not a PDF number
// (syntax.ParseNumber: "1e40" has an exponent, which PDF numbers do not) has
// no magnitude for a limit to judge.
func checkContentNumberLimit(s []byte, lim implLimits, objNum int, add func(string, int)) {
	v, isInt, ok := syntax.ParseNumber(s)
	if !ok {
		return
	}
	if !isInt {
		if absf(v) > lim.realLimit {
			add(fmt.Sprintf("a content-stream real value %s exceeds the magnitude limit %g", s, lim.realLimit), objNum)
		}
		return
	}
	// Integer: exact, against the 2^31-1 architectural limit (ISO 32000-1
	// Annex C, Table C.1). v is exact to 2^53, far past the limit, so the
	// comparison is exact where it matters.
	if v > 2147483647 || v < -2147483648 {
		add(fmt.Sprintf("a content-stream integer value %s is outside [-2^31, 2^31-1]", s), objNum)
	}
}

// checkICCProfileIdentity implements the PDF/A-4 rule (ISO 19005-4 6.2.4.2)
// that an ICCBased CMYK colour space used for rendering must not embed the
// same ICC profile as the PDF/A output intent or the current transparency
// blending colour space. Content is followed through invoked form XObjects,
// carrying the enclosing group's blending profile.
func checkICCProfileIdentity(doc core.View, level Level) []Violation {
	if level.Part() != 4 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	catalogOI := pdfaOutputIntentProfile(doc, catalog)

	var errs []Violation
	seen := map[string]bool{}
	add := func(msg string, obj int) {
		if seen[msg] {
			return
		}
		seen[msg] = true
		errs = append(errs, Violation{Rule: "6.2.4.2", Level: level, Message: msg, Object: obj})
	}
	seenC := map[*object.Dictionary]bool{}
	for _, page := range doc.Pages(catalog.Get("Pages")) {
		// PDF/A-4 permits page-level output intents; prefer the page's own.
		oiProfile := catalogOI
		if p := pdfaOutputIntentProfile(doc, page.Dict); p != nil {
			oiProfile = p
		}
		data, key, _ := doc.ContentBytesAndKey(page.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		blend := groupBlendProfile(doc, page.Dict)
		walkICCIdentity(doc, page.Dict, data, key, page.ObjNum, oiProfile, blend, seenC, add, 0)
	}
	return errs
}

// pdfaOutputIntentProfile returns the DestOutputProfile of a dictionary's
// GTS_PDFA1 output intent, or nil.
func pdfaOutputIntentProfile(doc core.View, container *object.Dictionary) *object.Stream {
	arr, ok := doc.Resolve(container.Get("OutputIntents")).(object.Array)
	if !ok {
		return nil
	}
	for _, el := range arr {
		d := doc.ResolveDict(el)
		if d == nil {
			continue
		}
		if s, _ := doc.ResolveName(d.Get("S")); s == "GTS_PDFA1" {
			if p, ok := doc.Resolve(d.Get("DestOutputProfile")).(*object.Stream); ok {
				return p
			}
		}
	}
	return nil
}

func walkICCIdentity(doc core.View, container *object.Dictionary, data []byte, key *object.Stream, objNum int, oi, blend *object.Stream, seen map[*object.Dictionary]bool, add func(string, int), depth int) {
	if !doc.Descend(depth) {
		return
	}
	doc.Charge(1)
	if container == nil || seen[container] || data == nil {
		return
	}
	seen[container] = true
	res := doc.Resources(container)
	if res == nil {
		return
	}
	usage := contentColorUsageOf(doc, data, key)
	csDict := doc.ResolveDict(res.Get("ColorSpace"))
	checkName := func(name string) {
		if csDict == nil {
			return
		}
		prof := renderedICCCMYKProfile(doc, csDict.Get(object.Name(name)))
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
	used := doc.ContentUsedNamesCached(data, key)
	if xobj := doc.ResolveDict(res.Get("XObject")); xobj != nil {
		for xkey, xref := range xobj.All() {
			if !used.XObjects[string(xkey)] {
				continue
			}
			s, ok := doc.Resolve(xref).(*object.Stream)
			if !ok {
				continue
			}
			if st, _ := doc.ResolveName(s.Dict.Get("Subtype")); st != "Form" {
				continue
			}
			childBlend := blend
			if gp := groupBlendProfile(doc, &s.Dict); gp != nil {
				childBlend = gp
			}
			data, _ := doc.Content(s) // reason: presence-only; the producer recorded any declined trip
			walkICCIdentity(doc, &s.Dict, data, s, resolveObjNum(doc, xref), oi, childBlend, seen, add, depth+1)
		}
	}
}

// groupBlendProfile returns the ICC profile of a container's transparency
// group blending colour space, or nil.
func groupBlendProfile(doc core.View, container *object.Dictionary) *object.Stream {
	if g := doc.ResolveDict(container.Get("Group")); g != nil {
		return iccProfileStream(doc, g.Get("CS"))
	}
	return nil
}

// renderedICCCMYKProfile returns the ICCBased CMYK profile a colour space
// renders through: the space itself, or the ICCBased CMYK alternate of a
// Separation or DeviceN space.
func renderedICCCMYKProfile(doc core.View, csVal object.Object) *object.Stream {
	if p := iccCMYKProfile(doc, csVal); p != nil {
		return p
	}
	arr, ok := doc.Resolve(csVal).(object.Array)
	if !ok || len(arr) < 3 {
		return nil
	}
	if n, _ := doc.ResolveName(arr[0]); n == "Separation" || n == "DeviceN" {
		return iccCMYKProfile(doc, arr[2])
	}
	return nil
}
