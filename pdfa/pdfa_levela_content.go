package pdfa

import (
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
	toUni := map[*object.Dictionary]map[int][]rune{}
	for _, pg := range doc.Pages(cat.Get("Pages")) {
		if doc.Cancel.Stopped() {
			return f
		}
		scanLevelAPage(doc, pg, covered, toUni, &f)
	}
	return f
}

// scanLevelAPage walks one page's content stream, recording the /Lang values its
// marked-content property lists carry and the Private Use Area characters it
// shows without replacement text.
func scanLevelAPage(doc core.View, pg core.PageInfo, covered map[mcKey]bool, toUni map[*object.Dictionary]map[int][]rune, f *levelAContentFacts) {
	data := core.ContentStreamData(doc, pg.Dict.Get("Contents"))
	if len(data) == 0 {
		return
	}
	res := doc.Resources(pg.Dict)
	var fontRes, propRes *object.Dictionary
	if res != nil {
		fontRes = doc.ResolveDict(res.Get("Font"))
		propRes = doc.ResolveDict(res.Get("Properties"))
	}

	var stack []mcFrame
	var untagged bool
	var font *object.Dictionary
	var lastName string
	var lastDict *object.Dictionary
	var dictIsLatest bool
	var pending [][]byte
	reported := map[rune]bool{}

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
			if s, ok := doc.Resolve(props.Get("ActualText")).(object.String); ok && len(s.Value) > 0 {
				fr.actualText = true
			}
			if n, ok := doc.Resolve(props.Get("MCID")).(object.Integer); ok {
				fr.mcid = int(n)
			}
			if s, ok := doc.Resolve(props.Get("Lang")).(object.String); ok && len(s.Value) > 0 {
				f.langs = append(f.langs, langSite{value: core.DecodePDFTextString(s.Value), objNum: pg.ObjNum})
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
		for _, fr := range stack {
			if fr.actualText || (fr.mcid >= 0 && covered[mcKey{pg.ObjNum, fr.mcid}]) {
				pending = nil
				return
			}
		}
		m, ok := toUni[font]
		if !ok {
			m = core.ParseToUnicodeRunes(doc, font)
			toUni[font] = m
		}
		if m == nil {
			pending = nil
			return
		}
		width := 1
		if st, _ := font.Get("Subtype").(object.Name); st == "Type0" {
			width = 2
		}
		for _, s := range pending {
			for i := 0; i+width <= len(s); i += width {
				code := int(s[i])
				if width == 2 {
					code = code<<8 | int(s[i+1])
				}
				for _, r := range m[code] {
					if privateUseArea(r) && !reported[r] {
						reported[r] = true
						f.pua = append(f.pua, puaSite{objNum: pg.ObjNum, r: r})
					}
				}
			}
		}
		pending = nil
	}

	core.ForEachContentItem(doc.Cancel, data, func(kind core.ContentItemKind, payload []byte) {
		switch kind {
		case core.ItemName:
			lastName = string(payload)
			dictIsLatest = false
		case core.ItemDict:
			lastDict = parseContentDict(payload)
			dictIsLatest = true
		case core.ItemString:
			pending = append(pending, payload)
		case core.ItemOperator:
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
	})
	if untagged {
		f.untagged = append(f.untagged, pg.ObjNum)
	}
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
		if s, ok := doc.Resolve(n.Elem.Get("ActualText")).(object.String); ok && len(s.Value) > 0 {
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
				if t, _ := k.Get("Type").(object.Name); t != "MCR" {
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
