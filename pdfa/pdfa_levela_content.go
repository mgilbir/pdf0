package pdfa

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// This file supplies the two Level A rules that cannot be decided from the
// object graph alone, because what they constrain is written inside a content
// stream: a natural-language identifier carried on a marked-content property
// list (ISO 19005-1 6.8.4, -2/-3 6.7.4), and the requirement that a character
// mapped into a Unicode Private Use Area be covered by an ActualText entry
// (-2/-3 6.2.11.7.3).
//
// Both need the same facts at the same points of the same scan — which
// marked-content sequences are open, what their property lists say, and what
// text is shown inside them — so the scan runs once per validation run and both
// rules read what it recorded.
//
// The scan covers page content streams only. A form XObject can hold text too,
// but the marked content inside one is numbered against the page that paints it,
// and resolving that correctly is a different piece of work; excluding forms can
// only withhold a finding, never invent one, which is the direction a validator
// is allowed to be incomplete in.

// maxMarkedContentDepth bounds the open-sequence stack the page scan keeps.
const maxMarkedContentDepth = 1024

// mcFrame is one open marked-content sequence: whether its property list
// supplies replacement text, and the marked-content identifier that links it to
// a structure element (-1 when it has none).
type mcFrame struct {
	actualText bool
	mcid       int
}

// langSite is one /Lang value found on a marked-content property list, with the
// page it was found on.
type langSite struct {
	value  string
	objNum int
}

// puaSite is one shown character whose Unicode mapping lands in a Private Use
// Area with no ActualText covering it.
type puaSite struct {
	objNum int  // the page
	r      rune // the offending code point
}

// levelAContentFacts is everything the content-dependent Level A rules learned
// from one pass over the document's pages.
type levelAContentFacts struct {
	langs []langSite
	pua   []puaSite
	// untagged lists the pages that paint something outside every
	// marked-content sequence.
	untagged []int
}

type levelAContentSlot struct{}

type levelAContentMemo struct {
	facts levelAContentFacts
	valid bool
}

// levelAContent returns the content-derived Level A facts, computed once per
// validation run.
func levelAContent(doc core.View) *levelAContentFacts {
	c := core.Slot[levelAContentMemo](doc.Run, levelAContentSlot{})
	if c.valid {
		return &c.facts
	}
	c.facts = buildLevelAContentFacts(doc)
	c.valid = true
	return &c.facts
}

func buildLevelAContentFacts(doc core.View) levelAContentFacts {
	var f levelAContentFacts
	cat := doc.Catalog()
	if cat == nil {
		return f
	}
	covered := structActualTextMCIDs(doc, cat)
	toUni := map[*object.Dictionary]*core.ToUnicode{}
	var traces core.ResMemo[*levelATrace]
	for _, pg := range doc.Pages(cat.Get("Pages")) {
		if doc.Cancel.Stopped() {
			return f
		}
		data, key, _ := doc.ContentBytesAndKey(pg.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		if len(data) == 0 {
			continue
		}
		res := doc.Resources(pg.Dict)
		tr, ok := traces.Get(doc, key, res)
		if !ok {
			tr = scanLevelATrace(doc, data, res, toUni)
			if !doc.Cancel.Stopped() {
				traces.Put(key, res, tr)
			}
		}
		tr.replay(doc, pg.ObjNum, covered, &f)
	}
	return f
}

// levelATrace is what one page content stream, drawn with one set of
// resources, says for the Level A content rules — everything but what depends
// on the page it is drawn on, which is the page's object number and which of
// its marked-content identifiers a structure element covers with replacement
// text. Pages commonly share their content (a template, a letterhead), and the
// scan is kept per (content, resources) so that such content is tokenised
// once rather than once per page.
type levelATrace struct {
	// langs is each distinct /Lang value a property list carries.
	langs []string
	// shows is each distinct (open marked-content identifiers, Private Use
	// Area character) pair shown with no ActualText on an enclosing property
	// list, in the order first shown.
	shows []puaShow
	// untagged is whether anything is painted outside every marked-content
	// sequence.
	untagged bool
}

// puaShow is a Private Use Area character shown inside the marked-content
// sequences whose identifiers are mcids (the open ones that have one).
type puaShow struct {
	mcids []int
	r     rune
}

// replay records the trace's facts for the page objNum: its /Lang values, the
// Private Use Area characters it shows that no structure element covers with
// replacement text on this page, each once, and whether it is untagged.
func (tr *levelATrace) replay(doc core.View, objNum int, covered map[mcKey]bool, f *levelAContentFacts) {
	for _, l := range tr.langs {
		doc.Charge(1)
		f.langs = append(f.langs, langSite{value: l, objNum: objNum})
	}
	reported := map[rune]bool{}
shows:
	for _, s := range tr.shows {
		doc.Charge(1 + len(s.mcids))
		if reported[s.r] {
			continue
		}
		for _, id := range s.mcids {
			if covered[mcKey{objNum, id}] {
				continue shows
			}
		}
		reported[s.r] = true
		f.pua = append(f.pua, puaSite{objNum: objNum, r: s.r})
	}
	if tr.untagged {
		f.untagged = append(f.untagged, objNum)
	}
}

// scanLevelATrace walks one page content stream drawn with res, recording the
// /Lang values its marked-content property lists carry and the Private Use
// Area characters it shows without replacement text on a property list.
func scanLevelATrace(doc core.View, data []byte, res *object.Dictionary, toUni map[*object.Dictionary]*core.ToUnicode) *levelATrace {
	tr := &levelATrace{}
	var fontRes, propRes *object.Dictionary
	if res != nil {
		fontRes = doc.ResolveDict(res.Get("Font"))
		propRes = doc.ResolveDict(res.Get("Properties"))
	}
	fontCodes := map[*object.Dictionary]*core.FontCodes{}
	seenLang := map[string]bool{}
	seenShow := map[string]bool{}

	var stack []mcFrame
	var untagged bool
	var font *object.Dictionary
	var lastName string
	var lastDict *object.Dictionary
	var dictIsLatest bool
	var pending [][]byte

	// propsFor returns the property list an operator's operands name: an inline
	// dictionary when one was the last operand, otherwise the named entry of the
	// page's /Properties resource.
	propsFor := func() *object.Dictionary {
		if dictIsLatest {
			return lastDict
		}
		if propRes == nil || lastName == "" {
			return nil
		}
		return doc.ResolveDict(propRes.Get(object.Name(lastName)))
	}

	push := func(props *object.Dictionary) {
		fr := mcFrame{mcid: -1}
		if props != nil {
			// Content is read only when it decoded, so a Locked document never
			// reaches here; a string that is ciphertext would still read as
			// "present", which only exempts.
			if s, r := doc.StringValue(props.Get("ActualText")); r == core.ReasonLocked || (r == core.ReasonOK && len(s.Value) > 0) {
				fr.actualText = true
			}
			if n, ok := doc.Resolve(props.Get("MCID")).(object.Integer); ok {
				fr.mcid = int(n)
			}
			if s, r := doc.StringValue(props.Get("Lang")); r == core.ReasonOK && len(s.Value) > 0 {
				// Each distinct value once: a property list repeated on
				// every line would otherwise repeat the same finding.
				if l := core.DecodePDFTextString(s.Value); !seenLang[l] {
					seenLang[l] = true
					tr.langs = append(tr.langs, l)
				}
			}
		}
		// A crafted stream of nothing but BDC must not grow the stack without
		// bound. Past the cap the sequence is dropped rather than pushed, which
		// costs at most a missed finding on a file whose marked content is
		// nested thousands deep — not a shape any real document has.
		if len(stack) < maxMarkedContentDepth {
			stack = append(stack, fr)
		}
	}

	show := func() {
		if font == nil || len(pending) == 0 {
			pending = nil
			return
		}
		// Replacement text on an enclosing property list covers the show
		// wherever it is drawn; a structure element's covers it on the pages
		// where the element's identifiers are, which replay decides.
		var mcids []int
		for _, fr := range stack {
			if fr.actualText {
				pending = nil
				return
			}
			if fr.mcid >= 0 {
				mcids = append(mcids, fr.mcid)
			}
		}
		m, ok := toUni[font]
		if !ok {
			m, _ = core.ParseToUnicode(doc, font) // reason: a nil map skips the text below; the producer recorded any declined trip
			toUni[font] = m
		}
		if m == nil {
			pending = nil
			return
		}
		// A composite font's codes are cut by its CMap, one to four bytes each
		// (audit 2026-09-22 C88: a fixed two-byte cut misread every mixed-width
		// CMap); a simple font's codes are its bytes.
		codes, ok := fontCodes[font]
		if !ok {
			codes = pendingFontCodes(doc, font)
			fontCodes[font] = codes
		}
		for _, s := range pending {
			var cut []core.Code
			if codes != nil {
				cut = codes.Codes(s)
			} else {
				cut = make([]core.Code, len(s))
				for i, b := range s {
					cut[i] = core.Code{Value: uint32(b), Bytes: 1}
				}
			}
			for _, c := range cut {
				rs, _ := m.Runes(int(c.Value))
				for _, r := range rs {
					if privateUseArea(r) {
						k := fmt.Sprint(r, mcids)
						if !seenShow[k] {
							seenShow[k] = true
							tr.shows = append(tr.shows, puaShow{mcids: mcids, r: r})
						}
					}
				}
			}
		}
		pending = nil
	}

	lx := core.NewContentLexer(doc.Cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentName:
			lastName = t.Name()
			dictIsLatest = false
		case core.ContentDictStart:
			lastDict = parseContentDict(lx.SkipDict(&t))
			dictIsLatest = true
		case core.ContentString, core.ContentHexString:
			pending = append(pending, t.Bytes())
		case core.ContentOperator:
			payload := t.Raw
			switch string(payload) {
			case "Tf":
				font = nil
				if fontRes != nil {
					font = doc.ResolveDict(fontRes.Get(object.Name(lastName)))
				}
				pending = nil
			case "BDC", "BMC":
				if string(payload) == "BMC" {
					push(nil)
				} else {
					push(propsFor())
				}
				pending = nil
			case "EMC":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				pending = nil
			case "Tj", "TJ", "'", "\"":
				if len(stack) == 0 {
					untagged = true
				}
				show()
			default:
				if len(stack) == 0 && paintingOperators[string(payload)] {
					untagged = true
				}
				pending = nil
			}
			dictIsLatest = false
		}
	}
	tr.untagged = untagged
	return tr
}

// paintingOperators are the operators that put marks on the page, other than
// the text-showing ones the scan handles alongside the Unicode rules.
//
// Only marks count. Setting a colour, a line width or a clip changes what a
// later operator will draw and is not itself content, so a page that sets its
// state outside a marked-content sequence and paints inside one — which is what
// every tagged document does — is not flagged. "n" is deliberately absent: it
// ends a path *without* painting it, and is how a clipping path is set.
//
// Do is absent for a different reason. An XObject invocation paints, but a form
// XObject carries its own content stream and may do its tagging inside; naming
// the Do as untagged content would be wrong for exactly the documents that took
// the most care. Reaching into the form is a larger piece of work than this
// rule needs.
var paintingOperators = map[string]bool{
	"S": true, "s": true,
	"f": true, "F": true, "f*": true,
	"B": true, "B*": true, "b": true, "b*": true,
	"sh": true,
}

// parseContentDict parses the raw << … >> bytes of a property list. A property
// list is a PDF object, so it is read with the object parser rather than picked
// apart by the content tokenizer: only the parser can tell /Lang's value from
// the next key.
func parseContentDict(raw []byte) *object.Dictionary {
	obj, err := syntax.NewParser(raw).ParseObject()
	if err != nil {
		return nil
	}
	d, _ := obj.(*object.Dictionary)
	return d
}

// pendingFontCodes is how the Level A scan cuts a font's shown strings into
// codes: nil for a simple font, whose codes are its bytes, and the font's CMap
// codespace for a composite one — or, when its /Encoding gives none, the
// two-byte guess, which is what the scan always assumed.
func pendingFontCodes(doc core.View, font *object.Dictionary) *core.FontCodes {
	if st, _ := doc.ResolveName(font.Get("Subtype")); st != "Type0" {
		return nil
	}
	codes, ok := core.LoadFontCodes(doc, font)
	if !ok {
		codes = core.TwoByteFontCodes()
	}
	return &codes
}

// privateUseArea reports whether r lies in one of Unicode's three Private Use
// Areas: the BMP area and the two supplementary planes.
func privateUseArea(r rune) bool {
	return r >= 0xE000 && r <= 0xF8FF ||
		r >= 0xF0000 && r <= 0xFFFFD ||
		r >= 0x100000 && r <= 0x10FFFD
}

// mcKey identifies a marked-content sequence: an identifier is unique only
// within the page it is used on (ISO 32000-1 14.7.4.2).
type mcKey struct {
	page int
	mcid int
}

// structActualTextMCIDs returns the marked-content sequences that a structure
// element with replacement text covers — the element's own /ActualText or any
// ancestor's, since ISO 32000-1 14.9.4 makes the entry apply to the whole
// subtree beneath the element that carries it.
func structActualTextMCIDs(doc core.View, cat *object.Dictionary) map[mcKey]bool {
	nodes := core.StructTree(doc, cat)
	out := map[mcKey]bool{}
	// Pre-order guarantees a node's parent precedes it, so inheritance of both
	// /ActualText and the /Pg default resolves in one forward pass.
	actual := make([]bool, len(nodes))
	page := make([]int, len(nodes))
	for i, n := range nodes {
		if n.Parent >= 0 {
			actual[i] = actual[n.Parent]
			page[i] = page[n.Parent]
		}
		// A Locked document's ActualText is ciphertext: it is there, and that
		// only exempts.
		if s, r := doc.StringValue(n.Elem.Get("ActualText")); r == core.ReasonLocked || (r == core.ReasonOK && len(s.Value) > 0) {
			actual[i] = true
		}
		if ref, ok := n.Elem.Get("Pg").(object.IndirectRef); ok {
			page[i] = ref.Number
		}
		if !actual[i] {
			continue
		}
		for _, kid := range core.StructKids(doc, n.Elem) {
			switch k := doc.Resolve(kid).(type) {
			case object.Integer:
				out[mcKey{page[i], int(k)}] = true
			case *object.Dictionary:
				if t, _ := doc.ResolveName(k.Get("Type")); t != "MCR" {
					continue
				}
				pg := page[i]
				if ref, ok := k.Get("Pg").(object.IndirectRef); ok {
					pg = ref.Number
				}
				if n, ok := doc.Resolve(k.Get("MCID")).(object.Integer); ok {
					out[mcKey{pg, int(n)}] = true
				}
			}
		}
	}
	return out
}
