package pdf0

import (
	"context"
	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
)

// This file implements text extraction: the visible text of a whole document
// or of a single page, decoded through each font's ToUnicode CMap (ISO 32000-2
// clause 9.10.3) and recursing into invoked form XObjects. It carries its own
// lenient content-stream tokenizer, distinct from the validator's, because
// extraction must survive a malformed stream rather than diagnose it. There is
// no layout model, so the output is approximate rather than faithful.

// ExtractText returns the visible text of every page in reading order, pages
// separated by a form feed. Text is decoded through each font's ToUnicode CMap;
// glyphs without a ToUnicode mapping are dropped. Layout is approximate: line
// breaks follow the text-positioning operators and wide inter-glyph gaps become
// spaces.
func (d *Document) ExtractText() string {
	text, _ := d.extractText(core.Canceler{})
	return text
}

// ExtractTextContext is ExtractText with cancellation.
//
// It returns the text extracted before the cancellation *and* an error wrapping
// ctx.Err(). Both, because either alone would be a lie: discarding the text
// throws away work the caller paid for, and returning it bare would present a
// truncated document as a whole one. Extraction has no finding channel — the
// mechanism the validators use to say "this result is incomplete" (see
// cancel.go and docs/limits.md) — so the error is the only place that fact can
// live, and a caller who ignores it gets a silently short document.
//
// The error is nil exactly when the extraction ran to completion.
func (d *Document) ExtractTextContext(ctx context.Context) (string, error) {
	return d.extractText(core.NewCanceler(ctx))
}

func (d *Document) extractText(cancel core.Canceler) (string, error) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return "", cancel.StopErr("extracting text")
	}
	var b strings.Builder
	for i, pg := range d.view().Pages(catalog.Get("Pages")) {
		// Per page: the coarse boundary. Within a page the tokenizer stops every
		// cancelScanBytes, so a single enormous page is interruptible too.
		if err := cancel.StopErr("extracting text"); err != nil {
			return b.String(), err
		}
		if i > 0 {
			b.WriteByte('\f')
		}
		b.WriteString(d.extractPageText(cancel, pg.Dict))
	}
	return b.String(), cancel.StopErr("extracting text")
}

// ExtractPageText returns the visible text of a single page dictionary. It
// resolves the page's /Resources through the page-tree inheritance chain and
// recurses into invoked form XObjects, so text drawn via inherited fonts or
// inside a form is not dropped.
//
// There is deliberately no ExtractPageTextContext: one page is the unit of work,
// and a caller extracting several pages already has a loop of its own to check
// a context in. Adding a variant here would move that check inside a call that
// does one page's work either way.
func (d *Document) ExtractPageText(page *object.Dictionary) string {
	return d.extractPageText(core.Canceler{}, page)
}

func (d *Document) extractPageText(cancel core.Canceler, page *object.Dictionary) string {
	res := d.ResolveDict(d.view().InheritedPageAttr(page, "Resources"))
	content := core.ContentStreamData(d.view(), page.Get("Contents"))
	var out strings.Builder
	d.extractContentText(cancel, res, content, &out, map[*object.Stream]bool{}, 0)
	return out.String()
}

// maxTextFormDepth bounds recursion through nested form XObjects.
const maxTextFormDepth = 32

// extractContentText appends the visible text of one content stream — a page or
// a form XObject — to out. Fonts are resolved from res; a Do that invokes a form
// XObject recurses into it with the form's own resources (audit C28). seen guards
// cyclic form references and depth bounds nesting.
func (d *Document) extractContentText(cancel core.Canceler, res *object.Dictionary, content []byte, out *strings.Builder, seen map[*object.Stream]bool, depth int) {
	if len(content) == 0 || depth > maxTextFormDepth {
		return
	}
	fonts := d.fontMapsFrom(res)
	var xobjs *object.Dictionary
	if res != nil {
		xobjs = d.ResolveDict(res.Get("XObject"))
	}

	var curMap map[int][]rune
	var curEncoding map[int]rune
	curTwoByte := false
	var operands []core.ContentToken

	// marked is the stack of open marked-content sequences, true for one
	// carrying an /ActualText, and replaced counts those. Inside one, what
	// the glyphs map to is not the text: the /ActualText is (ISO 32000-2
	// 14.9.4), and it has already been written when the sequence opened.
	var marked []bool
	replaced := 0

	show := func(raw []byte) {
		if replaced > 0 {
			return
		}
		for _, r := range decodeShown(raw, curMap, curEncoding, curTwoByte) {
			out.WriteRune(r)
		}
	}
	for tk := range core.TokenizeContent(cancel, content) {
		if tk.Kind != core.KindOp {
			operands = append(operands, tk)
			continue
		}
		if replaced > 0 {
			// Everything inside a replaced sequence is covered by its text:
			// the line breaks and spacing the operators would add as well
			// as the glyphs.
			switch tk.Op {
			case "BDC", "BMC", "EMC":
			default:
				operands = operands[:0]
				continue
			}
		}
		switch tk.Op {
		case "BMC":
			marked = append(marked, false)
		case "BDC":
			actual, ok := d.actualText(res, operands)
			if ok && replaced == 0 {
				out.WriteString(actual)
			}
			marked = append(marked, ok)
			if ok {
				replaced++
			}
		case "EMC":
			// An EMC with nothing open is malformed and changes nothing.
			if n := len(marked); n > 0 {
				if marked[n-1] {
					replaced--
				}
				marked = marked[:n-1]
			}
		case "Tf":
			if len(operands) >= 1 {
				if f, ok := fonts[operands[0].Name]; ok {
					curMap, curEncoding, curTwoByte = f.toUnicode, f.encoding, f.twoByte
				} else {
					curMap, curEncoding, curTwoByte = nil, nil, false
				}
			}
		case "Tj", "'", "\"":
			if tk.Op != "Tj" {
				out.WriteByte('\n')
			}
			if len(operands) >= 1 {
				show(operands[len(operands)-1].Str)
			}
		case "TJ":
			for _, el := range operands {
				switch el.Kind {
				case core.KindString:
					show(el.Str)
				case core.KindNumber:
					if el.Number() < -100 { // wide negative adjustment ≈ a space
						out.WriteByte(' ')
					}
				}
			}
		case "Td", "TD", "T*":
			out.WriteByte('\n')
		case "Do":
			if xobjs != nil && len(operands) >= 1 {
				if st, ok := d.Resolve(xobjs.Get(object.Name(operands[len(operands)-1].Name))).(*object.Stream); ok {
					if sub, _ := d.view().ResolveName(st.Dict.Get("Subtype")); sub == "Form" && !seen[st] {
						seen[st] = true
						formRes := d.ResolveDict(st.Dict.Get("Resources"))
						if formRes == nil {
							formRes = res // a form may draw with the calling context's resources
						}
						d.extractContentText(cancel, formRes, d.view().Content(st), out, seen, depth+1)
					}
				}
			}
		}
		operands = operands[:0]
	}
}

// actualText is the /ActualText of the marked-content sequence a BDC opens,
// from the property list its operands carry inline or from the named entry in
// the resources' /Properties.
//
// The tokenizer steps over dictionary delimiters, so an inline list arrives as
// its keys and values in a row: the tag, then /ActualText followed by its
// string. A named list arrives as the tag and one more name.
func (d *Document) actualText(res *object.Dictionary, operands []core.ContentToken) (string, bool) {
	for i := 1; i+1 < len(operands); i++ {
		if operands[i].Kind == core.KindName && operands[i].Name == "ActualText" &&
			operands[i+1].Kind == core.KindString {
			return core.DecodePDFTextString(operands[i+1].Str), true
		}
	}
	if len(operands) == 2 && operands[1].Kind == core.KindName && res != nil {
		props := d.ResolveDict(res.Get("Properties"))
		if props == nil {
			return "", false
		}
		list := d.ResolveDict(props.Get(object.Name(operands[1].Name)))
		if list == nil {
			return "", false
		}
		if s, ok := d.Resolve(list.Get("ActualText")).(object.String); ok {
			return core.DecodePDFTextString(s.Value), true
		}
	}
	return "", false
}

type fontText struct {
	// toUnicode maps a code to every character its ToUnicode entry names — a
	// ligature's are several.
	toUnicode map[int][]rune
	// encoding maps a character code to the character it stands for, built
	// from the font's /Encoding. It is consulted when the font carries no
	// ToUnicode entry for a code, which is the ordinary case for a simple font
	// naming one of the standard encodings.
	encoding map[int]rune
	twoByte  bool
}

// fontMapsFrom resolves a resource dictionary's /Font entries to their ToUnicode maps.
func (d *Document) fontMapsFrom(res *object.Dictionary) map[string]fontText {
	out := map[string]fontText{}
	if res == nil {
		return out
	}
	fontDict := d.ResolveDict(res.Get("Font"))
	if fontDict == nil {
		return out
	}
	for name := range fontDict.Keys() {
		f := d.ResolveDict(fontDict.Get(name))
		if f == nil {
			continue
		}
		twoByte := false
		if st, _ := d.view().ResolveName(f.Get("Subtype")); st == "Type0" {
			twoByte = true
		}
		out[string(name)] = fontText{
			toUnicode: core.ParseToUnicodeRunes(d.view(), f),
			encoding:  d.simpleEncoding(f, twoByte),
			twoByte:   twoByte,
		}
	}
	return out
}

// simpleEncoding builds the code-to-character map a simple font's /Encoding
// describes: a named base encoding, a /Differences list, or both.
//
// It matters most where a byte's value is least informative. WinAnsiEncoding
// and Latin-1 agree everywhere except 0x80 to 0x9F, and that band is where the
// curly quotes, the dashes, the bullet, the ellipsis and the euro live — so a
// document setting a quotation mark is exactly the document the byte value gets
// wrong.
func (d *Document) simpleEncoding(f *object.Dictionary, twoByte bool) map[int]rune {
	if twoByte {
		return nil // a composite font is decoded by its CMap, not by an encoding
	}
	base := font.StandardEncodingNames
	var differences object.Array
	switch enc := d.Resolve(f.Get("Encoding")).(type) {
	case object.Name:
		base = baseEncodingNames(enc, base)
	case *object.Dictionary:
		if n, ok := d.Resolve(enc.Get("BaseEncoding")).(object.Name); ok {
			base = baseEncodingNames(n, base)
		}
		differences, _ = d.Resolve(enc.Get("Differences")).(object.Array)
	case nil:
		// No /Encoding: the font's built-in encoding governs, which for a
		// standard Latin face is close enough to Standard for extraction.
	default:
		return nil
	}

	out := make(map[int]rune, len(base)+len(differences))
	for code, name := range base {
		if r, ok := font.GlyphNameToRune(name, code); ok {
			out[int(code)] = r
		}
	}
	// /Differences overrides the base: a run of names beginning at each code it
	// introduces (ISO 32000-2 9.6.5.1).
	code := 0
	for _, item := range differences {
		switch v := d.Resolve(item).(type) {
		case object.Integer, object.Real:
			// object.Int saturates an out-of-range Real; a raw int(v) is
			// implementation-defined for one (audit 2026-09-22 C118).
			code = object.Int(v)
		case object.Name:
			if code >= 0 && code < 256 {
				if r, ok := font.GlyphNameToRune(string(v), byte(code)); ok {
					out[code] = r
				} else {
					delete(out, code) // named something unresolvable: say nothing
				}
			}
			code++
		}
	}
	return out
}

// baseEncodingNames resolves a base encoding name to its table, keeping the
// current one for a name this package does not know.
func baseEncodingNames(n object.Name, current map[byte]string) map[byte]string {
	switch n {
	case "WinAnsiEncoding":
		return font.WinAnsiEncodingNames
	case "MacRomanEncoding":
		return font.MacRomanEncodingNames
	case "StandardEncoding":
		return font.StandardEncodingNames
	}
	return current
}

// decodeShown maps a shown byte string to runes. It prefers the font's
// ToUnicode CMap, then the font's own /Encoding, and only then the byte value
// as Latin-1 — which is right for ASCII and wrong exactly where an encoding
// would have said so.
func decodeShown(raw []byte, toUnicode map[int][]rune, encoding map[int]rune, twoByte bool) []rune {
	var runes []rune
	step := 1
	if twoByte {
		step = 2
	}
	for i := 0; i+step <= len(raw); i += step {
		code := int(raw[i])
		if twoByte {
			code = int(raw[i])<<8 | int(raw[i+1])
		}
		if rs, ok := toUnicode[code]; ok {
			runes = append(runes, rs...)
			continue
		}
		if r, ok := encoding[code]; ok {
			runes = append(runes, r)
			continue
		}
		if !twoByte && code >= 32 && code < 256 {
			runes = append(runes, rune(code))
		}
	}
	return runes
}

// --- content-stream tokenizer ---
