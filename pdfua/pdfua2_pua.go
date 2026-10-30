package pdfua

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// checkUA2ContentPUA is ISO 14289-2 8.4.3 for content: "where real content
// maps to Unicode PUA values, an ActualText or Alt entry shall be present"
// (veraPDF 8.4.3-t01, its Glyph object). A character shown outside every
// Artifact sequence whose ToUnicode mapping lands in a Private Use Area is
// covered by an /ActualText or /Alt string on an enclosing marked-content
// property list, or on the structure element its marked content belongs to
// or any ancestor of it: ISO 32000-2 14.9.3 and 14.9.4 apply both entries to
// everything the element encloses. veraPDF asks the owning element only, so
// where an ancestor carries the entry pdf0 passes what it fails.
//
// Page content is read, and the form XObjects it paints, in the scope they
// are painted in. Characters are mapped by the font's ToUnicode CMap, as the
// PDF/A Level A rule maps them (6.2.11.7.3); a character without one is not
// judged.
func checkUA2ContentPUA(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	covered := make([]bool, len(nodes))
	for i, n := range nodes {
		covered[i] = n.Parent >= 0 && covered[n.Parent]
		if !n.HasS {
			continue
		}
		for _, k := range []object.Name{"ActualText", "Alt"} {
			if _, r := d.StringValue(n.Elem.Get(k)); r != core.ReasonAbsent {
				covered[i] = true
			}
		}
	}
	s := &puaScan{
		d:       d,
		covered: mcidFlags(d, nodes, covered),
		toUni:   map[*object.Dictionary]*core.ToUnicode{},
		codes:   map[*object.Dictionary]*core.FontCodes{},
		forms:   map[puaFormKey][]rune{},
	}
	var v []Violation
	for _, pg := range d.Pages(d.CatalogPages()) {
		data, _, _ := d.ContentBytesAndKey(pg.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		for _, r := range s.replay(data, d.Resources(pg.Dict), pg.ObjNum, puaFrame{}, 0, map[*object.Stream]bool{}) {
			v = append(v, Violation{Clause: "8.4.3", Message: fmt.Sprintf("real content shows U+%04X, a Private Use Area code point, with no ActualText or Alt to replace it", r), Object: pg.ObjNum})
		}
	}
	return v
}

// puaFrame is the scope of one open marked-content sequence: whether
// replacement text covers what it encloses, and whether it is an artifact.
type puaFrame struct {
	covered, artifact bool
}

// puaFormKey keys the memo of a form's replay: what it reports depends on the
// form and on the scope it is painted in.
type puaFormKey struct {
	form  *object.Stream
	scope puaFrame
}

type puaScan struct {
	d core.View
	// covered holds, by (content container, MCID), whether the structure
	// element the MCID belongs to, or an ancestor, has replacement text.
	covered map[[2]int]bool
	toUni   map[*object.Dictionary]*core.ToUnicode
	codes   map[*object.Dictionary]*core.FontCodes
	forms   map[puaFormKey][]rune
}

// replay returns the distinct Private Use Area characters one stream shows
// as real content with no replacement text, in first-seen order. container
// is the page, or the form, the stream's MCIDs are numbered in; outer is the
// scope the stream is painted in. path holds the forms being entered, so a
// form that paints itself is entered once.
func (s *puaScan) replay(data []byte, res *object.Dictionary, container int, outer puaFrame, depth int, path map[*object.Stream]bool) []rune {
	d := s.d
	if !d.Descend(depth) {
		return nil
	}
	var fonts, props, xobjs *object.Dictionary
	if res != nil {
		fonts = d.ResolveDict(res.Get("Font"))
		props = d.ResolveDict(res.Get("Properties"))
		xobjs = d.ResolveDict(res.Get("XObject"))
	}
	var out []rune
	seen := map[rune]bool{}
	report := func(r rune) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	var stack []puaFrame
	scope := func() puaFrame {
		if len(stack) > 0 {
			return stack[len(stack)-1]
		}
		return outer
	}
	var font *object.Dictionary
	var names []string // the name operands since the last operator
	var dict *object.Dictionary
	var dictIsLatest bool
	var shown [][]byte

	lx := core.NewContentLexer(d.Cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		d.Charge(1)
		switch t.Kind {
		case core.ContentName:
			names = append(names, t.Name())
			dictIsLatest = false
			continue
		case core.ContentDictStart:
			dict = core.ParsePropertyList(lx.SkipDict(&t))
			dictIsLatest = true
			continue
		case core.ContentString, core.ContentHexString:
			shown = append(shown, t.Bytes())
			continue
		case core.ContentOperator:
		default:
			continue
		}
		switch string(t.Raw) {
		case "Tf":
			font = nil
			if len(names) > 0 {
				font = d.ResolveDict(fonts.Get(object.Name(names[len(names)-1])))
			}
		case "BMC", "BDC":
			f := scope()
			if len(names) > 0 && names[0] == "Artifact" {
				f.artifact = true
			}
			if string(t.Raw) == "BDC" {
				pl := dict
				if !dictIsLatest {
					pl = nil
					if len(names) > 1 {
						pl = d.ResolveDict(props.Get(object.Name(names[1])))
					}
				}
				if pl != nil {
					for _, k := range []object.Name{"ActualText", "Alt"} {
						if _, r := d.StringValue(pl.Get(k)); r != core.ReasonAbsent {
							f.covered = true
						}
					}
					if mcid, ok := d.ResolveInt(pl.Get("MCID")); ok && s.covered[[2]int{container, int(mcid)}] {
						f.covered = true
					}
				}
			}
			// A stream of nothing but BDC grows the stack no further than
			// the cap; past it a sequence is dropped, as the PDF/A scan
			// drops it.
			if len(stack) < maxMarkedContentDepth {
				stack = append(stack, f)
			}
		case "EMC":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case "Tj", "TJ", "'", "\"":
			if f := scope(); !f.covered && !f.artifact {
				for _, r := range s.pua(font, shown) {
					report(r)
				}
			}
		case "Do":
			if len(names) == 0 {
				break
			}
			form, _ := d.Resolve(xobjs.Get(object.Name(names[len(names)-1]))).(*object.Stream)
			if form == nil || path[form] {
				break
			}
			if st, _ := d.ResolveName(form.Dict.Get("Subtype")); st != "Form" {
				break
			}
			key := puaFormKey{form, scope()}
			m, ok := s.forms[key]
			if !ok {
				fres := d.ResolveDict(form.Dict.Get("Resources"))
				if fres == nil {
					fres = res
				}
				fdata, _ := d.Content(form) // reason: presence-only; the producer recorded any declined trip
				path[form] = true
				m = s.replay(fdata, fres, d.StreamObjNum(form), key.scope, depth+1, path)
				delete(path, form)
				s.forms[key] = m
			}
			for _, r := range m {
				report(r)
			}
		}
		names, shown, dictIsLatest = names[:0], shown[:0], false
	}
	return out
}

// maxMarkedContentDepth bounds the open-sequence stack a scan keeps.
const maxMarkedContentDepth = 1024

// pua returns the Private Use Area characters the strings shown in font map
// to through its ToUnicode CMap.
func (s *puaScan) pua(font *object.Dictionary, shown [][]byte) []rune {
	if font == nil {
		return nil
	}
	d := s.d
	m, ok := s.toUni[font]
	if !ok {
		m, _ = core.ParseToUnicode(d, font) // reason: a nil map judges nothing; the producer recorded any declined trip
		s.toUni[font] = m
	}
	if m == nil {
		return nil
	}
	codes, ok := s.codes[font]
	if !ok {
		codes = core.ShownCodes(d, font)
		s.codes[font] = codes
	}
	var out []rune
	for _, str := range shown {
		var cut []core.Code
		if codes != nil {
			cut = codes.Codes(str)
		} else {
			cut = make([]core.Code, len(str))
			for i, b := range str {
				cut[i] = core.Code{Value: uint32(b), Bytes: 1}
			}
		}
		d.Charge(len(cut))
		for _, c := range cut {
			rs, _ := m.Runes(int(c.Value))
			for _, r := range rs {
				if core.PrivateUseArea(r) {
					out = append(out, r)
				}
			}
		}
	}
	return out
}
