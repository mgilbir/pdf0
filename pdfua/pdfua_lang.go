package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// This file owns natural language (ISO 14289-1 7.2, ISO 14289-2 8.4.4): that
// every /Lang is a language identifier, and that the language of every piece
// of text can be determined.
//
// The two parts differ on the second. ISO 14289-2 8.4.4 requires the catalog
// /Lang. ISO 14289-1 7.2 requires only that the natural language be
// declared, which a catalog /Lang does for everything at once and /Lang
// entries on structure elements and marked content do piece by piece. Without
// a catalog /Lang, the veraPDF PDF/UA-1 profile asks of each text-bearing
// object whether its language can be determined (7.2-t02, t21..t25, t30..t34),
// and the corpus has pass files that declare it only on structure elements
// and marked content (PDF_UA-1 7.2-t21-pass-b ... 7.2-t34-pass-d), so
// requiring the catalog /Lang reported those 21 conforming files.
// checkUA1LangDeterminable is that per-object reading.

// uaLanguageTag reports whether a /Lang value is a language identifier as
// ISO 32000-1 14.9.2 defines it through RFC 3066: 1*8ALPHA *("-" 1*8alphanum).
// It is the veraPDF CosLang rule (PDF/UA-1 7.2-t29, PDF/UA-2 8.4.4-t02),
// /^[a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*$/, which a one-letter primary subtag
// passes (corpus 7.2-t29-pass-b, -d, -f) and the empty string does not
// (7.2-t29-fail-n, -o, -p): ISO 32000 lets an empty /Lang mean "unknown", and
// an unknown language is not a declared one.
func uaLanguageTag(tag string) bool { return core.ValidLanguageTag(tag, true) }

// checkUALang enforces that every /Lang present is a language identifier:
// the catalog's, each structure element's, and each marked-content property
// list's in the page and form XObject content (the CosLang objects of the
// veraPDF profiles). A value of a Locked document is ciphertext and is not
// judged (audit 2026-09-22 C63).
func checkUALang(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	bad := func(where string, o object.Object, num int) {
		// /Lang is a text string, and a UTF-16 "en-US" is as valid a language
		// tag as a PDFDocEncoded one: decode before judging it.
		if s, r := d.StringValue(o); r == core.ReasonOK && !uaLanguageTag(core.DecodePDFTextString(s.Value)) {
			v = append(v, Violation{Clause: "7.2", Message: where + " /Lang " + quote(core.DecodePDFTextString(s.Value)) + " is not a valid language identifier", Object: num})
		}
	}
	bad("catalog", cat.Get("Lang"), 0)
	walkStructElems(d, cat, func(elem *object.Dictionary, _ object.Name) {
		bad("structure element", elem.Get("Lang"), 0)
	})

	// Property lists: those written inline in a BDC, and those named from the
	// container's /Properties resources.
	content := func(data []byte, key *object.Stream, res *object.Dictionary, num int) {
		f := contentFacts(d, data, key)
		for _, l := range f.propLangs {
			bad("marked-content property list", object.String{Value: l}, num)
		}
		props := d.ResolveDict(d.ResolveDict(res).Get("Properties"))
		if props == nil {
			return
		}
		for _, name := range f.propNames {
			if pl := d.ResolveDict(props.Get(object.Name(name))); pl != nil {
				bad("marked-content property list", pl.Get("Lang"), num)
			}
		}
	}
	for _, pg := range d.Pages(d.CatalogPages()) {
		data, key, _ := d.ContentBytesAndKey(pg.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		content(data, key, d.Resources(pg.Dict), pg.ObjNum)
	}
	for _, r := range d.ReachableDicts() {
		if r.Stream == nil {
			continue
		}
		if st, _ := d.ResolveName(r.Dict.Get("Subtype")); st != "Form" {
			continue
		}
		data, _ := d.Content(r.Stream) // reason: presence-only; the producer recorded any declined trip
		content(data, r.Stream, d.ResolveDict(r.Dict.Get("Resources")), r.ObjNum)
	}
	return v
}

// checkUA2CatalogLang is PDF/UA-2's language rule: the catalog must carry a
// non-empty /Lang (ISO 14289-2 8.4.4; veraPDF 8.4.4-t01).
func checkUA2CatalogLang(d core.View, cat *object.Dictionary) []Violation {
	if !d.NonEmptyStringOrLocked(cat.Get("Lang")) {
		return []Violation{{Clause: "7.2", Message: "document does not specify a default language (catalog /Lang)", Object: 0}}
	}
	return nil
}

// checkUA1LangDeterminable reports, for a PDF/UA-1 document with no catalog
// /Lang, each text-bearing object whose natural language cannot be determined
// another way. It follows the veraPDF PDF/UA-1 profile rule by rule; a catalog
// /Lang that is a string at all (even an empty or malformed one, which
// checkUALang reports) determines everything, as it does there (the profile's
// variable over PDDocument.containsLang).
//
//   - 7.2-t02: outline entries, whose titles nothing else can qualify;
//   - 7.2-t21..t23: a structure element's /ActualText, /Alt or /E, unless it or
//     an ancestor has a /Lang;
//   - 7.2-t24: an annotation's /Contents, unless its enclosing structure
//     element has a /Lang;
//   - 7.2-t25: a form field's /TU, likewise;
//   - 7.2-t30..t32: a Span marked-content sequence's /ActualText, /Alt or /E,
//     unless a /Lang is in scope (see langScope);
//   - 7.2-t33: an XMP language alternative holding only an x-default item;
//   - 7.2-t34: text shown in page content, unless a /Lang is in scope.
func checkUA1LangDeterminable(d core.View, cat *object.Dictionary) []Violation {
	if _, r := d.StringValue(cat.Get("Lang")); r != core.ReasonAbsent {
		return nil
	}
	var v []Violation
	report := func(msg string, num int) {
		v = append(v, Violation{Clause: "7.2", Message: "natural language " + msg + " cannot be determined (no catalog /Lang)", Object: num})
	}
	hasLang := func(dict *object.Dictionary) bool {
		_, r := d.StringValue(dict.Get("Lang")) // a nil dict has no /Lang
		return r != core.ReasonAbsent
	}

	// 7.2-t02: an outline with entries.
	if ol := d.ResolveDict(cat.Get("Outlines")); ol != nil && d.ResolveDict(ol.Get("First")) != nil {
		report("of the outline entries", d.ObjNumOf(ol))
	}

	// 7.2-t21..t23: structure-element text attributes. The tree is in
	// pre-order, so a node's parent has been decided before it.
	nodes := structTree(d, cat)
	inScope := make([]bool, len(nodes)) // a /Lang on the node or an ancestor
	for i, n := range nodes {
		inScope[i] = hasLang(n.Elem) || (n.Parent >= 0 && inScope[n.Parent])
		if !n.HasS || inScope[i] {
			continue
		}
		for _, key := range []object.Name{"ActualText", "Alt", "E"} {
			if _, r := d.StringValue(n.Elem.Get(key)); r != core.ReasonAbsent {
				report("of a structure element's /"+string(key), d.ObjNumOf(n.Elem))
			}
		}
	}

	// 7.2-t24/t25: annotation /Contents and field /TU, through the enclosing
	// structure element's own /Lang (veraPDF does not climb here).
	ctx := newAnnotContext(d, cat)
	for _, annot := range ctx.annots {
		if _, r := d.StringValue(annot.Get("Contents")); r != core.ReasonAbsent && !hasLang(ctx.parent[annot]) {
			report("of an annotation's /Contents", d.ObjNumOf(annot))
		}
	}
	forEachField(d, cat, func(field *object.Dictionary) {
		if _, r := d.StringValue(field.Get("TU")); r != core.ReasonAbsent && !hasLang(ctx.parent[field]) {
			report("of a form field's /TU", d.ObjNumOf(field))
		}
	})

	// 7.2-t33: document metadata.
	if stream, ok := d.Resolve(cat.Get("Metadata")).(*object.Stream); ok {
		if packet, status := d.XMPPacketOf(stream, object.RefNum(cat.Get("Metadata"))); status == core.XMPParsed {
			for _, p := range packet.Properties() {
				if onlyXDefault(p.Value) {
					report("of the metadata property "+p.Prefix+":"+p.Name+", whose only language alternative is x-default,", object.RefNum(cat.Get("Metadata")))
				}
			}
		}
	}

	// 7.2-t30..t32, t34: content.
	scope := newLangScope(d, cat, nodes, inScope)
	for _, pg := range d.Pages(d.CatalogPages()) {
		data, key, _ := d.ContentBytesAndKey(pg.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		for _, msg := range scope.content(data, key, d.Resources(pg.Dict), pg.ObjNum, false) {
			report(msg, pg.ObjNum)
		}
	}
	for _, annot := range ctx.annots {
		if f, _ := d.ResolveInt(annot.Get("F")); int(f)&(0x2|0x20) != 0 {
			continue // hidden or no-view: its appearance is not shown
		}
		ap := annotAppearance(d, annot)
		if ap == nil {
			continue
		}
		data, _ := d.Content(ap) // reason: presence-only; the producer recorded any declined trip
		for _, msg := range scope.content(data, ap, d.ResolveDict(ap.Dict.Get("Resources")), d.StreamObjNum(ap), hasLang(ctx.parent[annot])) {
			report(msg, d.ObjNumOf(annot))
		}
	}
	return v
}

// onlyXDefault is veraPDF's XMPLangAlt.xDefault: a language alternative whose
// one item is the x-default one.
func onlyXDefault(val xmp.Value) bool {
	return val.Kind == xmp.Alt && len(val.Items) == 1 && val.Items[0].HasLang && val.Items[0].Lang == "x-default"
}

// annotAppearance is the appearance stream an annotation shows: /AP /N, or
// the /AS state of it when /N is a dictionary of states.
func annotAppearance(d core.View, annot *object.Dictionary) *object.Stream {
	ap := d.ResolveDict(annot.Get("AP"))
	if ap == nil {
		return nil
	}
	switch n := d.Resolve(ap.Get("N")).(type) {
	case *object.Stream:
		return n
	case *object.Dictionary:
		if as, ok := d.ResolveName(annot.Get("AS")); ok {
			s, _ := d.Resolve(n.Get(as)).(*object.Stream)
			return s
		}
	}
	return nil
}

// forEachField visits every dictionary in the AcroForm field tree once, in
// document order. The walk is iterative and charged to the work meter, as
// checkUAFieldDescription's is.
func forEachField(d core.View, cat *object.Dictionary, fn func(*object.Dictionary)) {
	form := d.ResolveDict(cat.Get("AcroForm"))
	if form == nil {
		return
	}
	var stack []object.Object
	if fields, ok := d.Resolve(form.Get("Fields")).(object.Array); ok {
		for i := len(fields) - 1; i >= 0; i-- {
			stack = append(stack, fields[i])
		}
	}
	seen := map[*object.Dictionary]bool{}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		d.Charge(1)
		fd := d.ResolveDict(node)
		if fd == nil || seen[fd] {
			continue
		}
		seen[fd] = true
		fn(fd)
		kids, _ := d.Resolve(fd.Get("Kids")).(object.Array)
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, kids[i])
		}
	}
}

// langScope answers, for marked content, whether a natural language is in
// scope — veraPDF's SEMarkedContent.getLangValue and SETextItem.getLang. A
// sequence's language is its property list's /Lang, else that of the
// structure element its /MCID belongs to or of the nearest ancestor element
// with one, else the enclosing sequence's, and so on outwards; a form
// XObject's content inherits the scope it is painted in, and an annotation's
// appearance its enclosing structure element's /Lang.
type langScope struct {
	d core.View
	// mcidLang holds, by (content container, MCID), whether the structure
	// element the MCID belongs to has a /Lang in scope. The container is the
	// page an element's /Pg names, or the stream an MCR's /Stm names.
	mcidLang map[[2]int]bool
}

func newLangScope(d core.View, cat *object.Dictionary, nodes []structNode, inScope []bool) *langScope {
	s := &langScope{d: d, mcidLang: map[[2]int]bool{}}
	// pg is the page an element's content is on: its own /Pg, else the
	// nearest ancestor's.
	pg := make([]int, len(nodes))
	for i, n := range nodes {
		pg[i] = object.RefNum(n.Elem.Get("Pg"))
		if pg[i] == 0 && n.Parent >= 0 {
			pg[i] = pg[n.Parent]
		}
		switch {
		case n.HasS:
			for _, k := range structKids(d, n.Elem) {
				d.Charge(1)
				if mcid, ok := d.Resolve(k).(object.Integer); ok {
					s.mcidLang[[2]int{pg[i], int(mcid)}] = inScope[i]
				}
			}
		case n.Parent >= 0:
			if t, _ := d.ResolveName(n.Elem.Get("Type")); t != "MCR" {
				continue
			}
			mcid, ok := d.ResolveInt(n.Elem.Get("MCID"))
			if !ok {
				continue
			}
			container := pg[i]
			if stm := object.RefNum(n.Elem.Get("Stm")); stm != 0 {
				container = stm
			}
			s.mcidLang[[2]int{container, int(mcid)}] = inScope[n.Parent]
		}
	}
	return s
}

// content replays one content stream in the given container and returns what
// in it has no language in scope: "of text in page content" once, and once
// per /ActualText, /Alt or /E key of a Span sequence. outer is whether a
// language is in scope where the stream is painted.
func (s *langScope) content(data []byte, key *object.Stream, res *object.Dictionary, container int, outer bool) []string {
	return s.replay(data, key, res, container, outer, 0, map[*object.Stream]bool{})
}

// langFormKey keys the memo of a form's replay: what it reports depends on
// the form and on whether a language is in scope where it is painted.
type langFormKey struct {
	form  *object.Stream
	outer bool
}

// replay returns the distinct messages of one stream, in first-seen order.
// path holds the forms being entered, so a form that paints itself is entered
// once; a form with resources of its own is replayed once per outer scope for
// the run, however many times it is painted.
func (s *langScope) replay(data []byte, key *object.Stream, res *object.Dictionary, container int, outer bool, depth int, path map[*object.Stream]bool) []string {
	d := s.d
	if !d.Descend(depth) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	report := func(msg string) {
		if !seen[msg] {
			seen[msg] = true
			out = append(out, msg)
		}
	}
	events := langEvents(d, data, key)
	d.Charge(len(events) + 1)
	props := d.ResolveDict(res.Get("Properties"))
	xobjs := d.ResolveDict(res.Get("XObject"))
	// stack[i] is whether a language is in scope inside the i-th open
	// sequence; scope() is the innermost answer.
	var stack []bool
	scope := func() bool {
		if len(stack) > 0 {
			return stack[len(stack)-1]
		}
		return outer
	}
	for _, ev := range events {
		switch ev.kind {
		case evBMC:
			stack = append(stack, scope())
		case evBDC:
			mc := ev.mc
			if mc.propName != "" {
				mc = mcFromDict(d, d.ResolveDict(props.Get(object.Name(mc.propName))), mc.tag)
			}
			in := scope() || mc.lang || (mc.hasMCID && s.mcidLang[[2]int{container, mc.mcid}])
			stack = append(stack, in)
			if mc.tag == "Span" && !in {
				for _, k := range mc.textKeys {
					report("of a Span marked-content sequence's /" + k)
				}
			}
		case evEMC:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case evText:
			if !scope() {
				report("of text in page content")
			}
		case evDo:
			form, _ := d.Resolve(xobjs.Get(object.Name(ev.name))).(*object.Stream)
			if form == nil || path[form] {
				continue
			}
			if st, _ := d.ResolveName(form.Dict.Get("Subtype")); st != "Form" {
				continue
			}
			fres := d.ResolveDict(form.Dict.Get("Resources"))
			mk := langFormKey{form, scope()}
			memo := uaMemo(d)
			if m, ok := memo.langForm[mk]; ok && fres != nil {
				for _, msg := range m {
					report(msg)
				}
				continue
			}
			own := fres != nil
			if !own {
				fres = res
			}
			fdata, _ := d.Content(form) // reason: presence-only; the producer recorded any declined trip
			path[form] = true
			m := s.replay(fdata, form, fres, d.StreamObjNum(form), mk.outer, depth+1, path)
			delete(path, form)
			if own {
				if memo.langForm == nil {
					memo.langForm = map[langFormKey][]string{}
				}
				memo.langForm[mk] = m
			}
			for _, msg := range m {
				report(msg)
			}
		}
	}
	return out
}

// The events of a content stream that decide language scope.
const (
	evBDC = iota + 1
	evBMC
	evEMC
	evText // one or more text-showing operators in a row
	evDo
)

type langEvent struct {
	kind int
	mc   mcProps // evBDC
	name string  // evDo: the XObject name
}

// mcProps is what a BDC's property list says about language: its tag, whether
// it has a /Lang, its /MCID, and which of /ActualText, /Alt and /E it has.
// propName is set instead when the list is named from /Properties.
type mcProps struct {
	tag      string
	propName string
	lang     bool
	hasMCID  bool
	mcid     int
	textKeys []string
}

// mcFromDict reads mcProps from a named property list.
func mcFromDict(d core.View, pl *object.Dictionary, tag string) mcProps {
	mc := mcProps{tag: tag}
	if pl == nil {
		return mc
	}
	if _, r := d.StringValue(pl.Get("Lang")); r != core.ReasonAbsent {
		mc.lang = true
	}
	if n, ok := d.ResolveInt(pl.Get("MCID")); ok {
		mc.hasMCID, mc.mcid = true, int(n)
	}
	for _, k := range []object.Name{"ActualText", "Alt", "E"} {
		if _, r := d.StringValue(pl.Get(k)); r != core.ReasonAbsent {
			mc.textKeys = append(mc.textKeys, string(k))
		}
	}
	return mc
}

// langEvents returns a content stream's language events, memoized per stream
// for the run (a stream shared by many pages is tokenized once).
func langEvents(d core.View, data []byte, key *object.Stream) []langEvent {
	c := uaMemo(d)
	if key != nil {
		if ev, ok := c.langEvents[key]; ok {
			return ev
		}
	}
	var events []langEvent
	var ops mcOperands
	for tk := range core.TokenizeContent(d.Cancel, data) {
		if tk.Kind != core.KindOp {
			ops.feed(tk)
			continue
		}
		switch tk.Op {
		case "BDC":
			events = append(events, langEvent{kind: evBDC, mc: ops.props()})
		case "BMC":
			events = append(events, langEvent{kind: evBMC})
		case "EMC":
			events = append(events, langEvent{kind: evEMC})
		case "Tj", "TJ", "'", "\"":
			if len(events) == 0 || events[len(events)-1].kind != evText {
				events = append(events, langEvent{kind: evText})
			}
		case "Do":
			events = append(events, langEvent{kind: evDo, name: ops.lastName})
		}
		ops.reset()
	}
	if key != nil {
		if c.langEvents == nil {
			c.langEvents = map[*object.Stream][]langEvent{}
		}
		c.langEvents[key] = events
	}
	return events
}

// mcOperands reads a marked-content operator's operands as core.TokenizeContent
// delivers them: an inline property list arrives as loose names and values
// (its << >> dropped), a named one as a second name operand. Only the entries
// language rules read are kept.
type mcOperands struct {
	n          int    // operands seen
	tag        string // the first operand, when a name
	second     string // the second operand, when a name
	twoNames   bool   // exactly a tag and a property-list name so far
	pendingKey string // the name just seen, awaiting its value
	lastName   string // the most recent name operand
	lang       []byte
	hasLang    bool
	mcid       int
	hasMCID    bool
	textKeys   []string
}

func (o *mcOperands) feed(tk core.ContentToken) {
	o.n++
	switch tk.Kind {
	case core.KindName:
		o.lastName = tk.Name
		switch o.n {
		case 1:
			o.tag = tk.Name
		case 2:
			o.second, o.twoNames = tk.Name, true
		default:
			o.twoNames = false
		}
		if o.n > 1 {
			o.pendingKey = tk.Name
		}
		return
	case core.KindString:
		switch o.pendingKey {
		case "Lang":
			o.lang, o.hasLang = tk.Str, true
		case "ActualText", "Alt", "E":
			o.textKeys = append(o.textKeys, o.pendingKey)
		}
	case core.KindNumber:
		if o.pendingKey == "MCID" {
			if n, ok := numberInt(tk); ok {
				o.mcid, o.hasMCID = n, true
			}
		}
	}
	if o.n > 1 {
		o.twoNames = false
	}
	o.pendingKey = ""
}

// props is the property list the operands describe.
func (o *mcOperands) props() mcProps {
	if o.twoNames && o.n == 2 {
		return mcProps{tag: o.tag, propName: o.second}
	}
	return mcProps{tag: o.tag, lang: o.hasLang, hasMCID: o.hasMCID, mcid: o.mcid, textKeys: o.textKeys}
}

func (o *mcOperands) reset() {
	*o = mcOperands{lastName: o.lastName}
}

// numberInt reads a number token as an int, when it is a whole number.
func numberInt(tk core.ContentToken) (int, bool) {
	f := tk.Number()
	if f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}
