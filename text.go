package pdf0

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/simplefont"
)

// This file implements text extraction: the visible text of a whole document
// or of a single page, decoded through each font's ToUnicode CMap (ISO 32000-2
// clause 9.10.3) and recursing into invoked form XObjects. It reads content
// through the shared lenient tokenizer, which survives a malformed stream
// rather than diagnosing it. There is no layout model, so the output is
// approximate rather than faithful.

// ExtractText returns the visible text of every page in reading order, pages
// separated by a form feed. Text is decoded through each font's ToUnicode CMap;
// glyphs without a ToUnicode mapping are dropped. Layout is approximate: line
// breaks follow the text-positioning operators and wide inter-glyph gaps become
// spaces.
//
// The error is nil exactly when every page's text is in the result. Otherwise
// it reports each page that is not, as a *PageTextError, and the result holds
// the text of the others, still separated by form feeds so that the n-th page's
// text stays after the (n-1)-th feed. Two things leave a page out: the content
// budget running out (the page and every one after it, see below), and an
// internal error — a panic in the extractor, recovered at the page so that one
// bad page does not take down the caller or the other pages. Neither is a
// statement about the file being wrong, and neither is silent.
//
// Extraction runs as one run, like a validation: it shares the run's memo of
// decoded streams, parsed ToUnicode and CMaps, and its work meter
// (WithMaxWork), which every content stream tokenized is charged to — each
// page's, and each form XObject's each time it is drawn. A form drawn N times
// is extracted N times, as it is drawn, so a document that draws a form which
// draws a form twice, thirty levels deep, asks for 2^30 extractions; the
// budget is what stops it. Pages that share one content stream and one
// resource dictionary have the same text, which is extracted once (audit
// 2026-09-22 C79: two hundred pages sharing a 50 MB stream re-decoded and
// re-read it per page, for a minute and a half).
func (d *Document) ExtractText() (string, error) {
	return d.extractText(core.Canceler{})
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
// The error is nil exactly when the extraction ran to completion, with every
// page's text in the result; it joins the cancellation with any
// *PageTextError, as ExtractText reports them.
func (d *Document) ExtractTextContext(ctx context.Context) (string, error) {
	return d.extractText(core.NewCanceler(ctx))
}

// PageTextError reports a page whose text an extraction left out, and why.
type PageTextError struct {
	// Page is the 1-based page number in document order, or 0 from
	// ExtractPageText, which is given the page rather than its number.
	Page int
	// Err says why: a resource limit (its message begins "resource limit
	// reached" and names the guard) or an internal error in the extractor.
	Err error
}

func (e *PageTextError) Error() string {
	if e.Page == 0 {
		return "text of the page not extracted: " + e.Err.Error()
	}
	return fmt.Sprintf("text of page %d not extracted: %v", e.Page, e.Err)
}

func (e *PageTextError) Unwrap() error { return e.Err }

func (d *Document) extractText(cancel core.Canceler) (string, error) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return "", cancel.StopErr("extracting text")
	}
	rd := beginRunCancel(d, cancel)
	// The run's canceler carries its work meter: a spent budget stops the
	// extraction as a cancelled context does, and StopErr says which.
	stop := rd.canceler()
	run := rd.newTextRun()
	var pages []core.PageInfo
	if core.Contain(func() { pages = rd.view().Pages(catalog.Get("Pages")) }) {
		return "", stop.StopErr("extracting text")
	}
	var b strings.Builder
	var errs []error
	for i, pg := range pages {
		// Per page: the coarse boundary. Within a page the tokenizer stops every
		// core.CancelScanBytes, so a single enormous page is interruptible too.
		if err := stop.StopErr("extracting text"); err != nil {
			return b.String(), errors.Join(append(errs, err)...)
		}
		if i > 0 {
			b.WriteByte('\f')
		}
		text, err := rd.pageText(run, pg.Dict)
		if err != nil {
			errs = append(errs, &PageTextError{Page: i + 1, Err: err})
			if serr := stop.StopErr("extracting text"); serr != nil && !errors.Is(serr, context.Canceled) && !errors.Is(serr, context.DeadlineExceeded) {
				// The work budget is the whole run's: every later page would
				// be refused too, and one error for the rest says so.
				errs = append(errs, fmt.Errorf("text of the pages after page %d not extracted: %w", i+1, serr))
				break
			}
			continue
		}
		b.WriteString(text)
	}
	if err := stop.StopErr("extracting text"); err != nil && !errors.Is(err, core.ErrWorkLimit) {
		errs = append(errs, err)
	}
	return b.String(), errors.Join(errs...)
}

// ExtractPageText returns the visible text of a single page dictionary. It
// resolves the page's /Resources through the page-tree inheritance chain and
// recurses into invoked form XObjects, so text drawn via inherited fonts or
// inside a form is not dropped.
//
// The error is a *PageTextError, with Page 0, when the page's text could not
// be extracted — the content budget ran out, or the extractor failed
// internally — and the text is then empty. See ExtractText.
//
// There is deliberately no ExtractPageTextContext: one page is the unit of work,
// and a caller extracting several pages already has a loop of its own to check
// a context in. Adding a variant here would move that check inside a call that
// does one page's work either way.
func (d *Document) ExtractPageText(page *object.Dictionary) (string, error) {
	rd := beginRun(d)
	text, err := rd.pageText(rd.newTextRun(), page)
	if err != nil {
		return "", &PageTextError{Err: err}
	}
	return text, nil
}

// textRun is one extraction's state across its pages, beyond what the run
// (core.Run, on the Document the run was begun on) memoises for everyone.
type textRun struct {
	// fonts memoizes fontMapsFrom per resource dictionary. A form drawn many
	// times would otherwise parse its fonts' ToUnicode CMaps each time, a
	// cost the content budget does not see: eleven bytes of "/X Do /X Do"
	// can stand for a ToUnicode stream of megabytes.
	fonts map[*object.Dictionary]map[string]fontText
	// pages memoizes a page's text by its content stream and resources, which
	// are all the text depends on. Pages commonly carry their own copy of
	// the same resource dictionary, so resources are matched by value
	// (core.ResMemo).
	pages core.ResMemo[string]
}

// fontMaps is fontMapsFrom(res), once per resource dictionary per run.
func (r *textRun) fontMaps(d *Document, res *object.Dictionary) map[string]fontText {
	if m, ok := r.fonts[res]; ok {
		return m
	}
	m := d.fontMapsFrom(res)
	if r.fonts == nil {
		r.fonts = map[*object.Dictionary]map[string]fontText{}
	}
	r.fonts[res] = m
	return m
}

// newTextRun starts the text state of an extraction on d, a Document a run has
// been begun on (beginRunCancel).
func (d *Document) newTextRun() *textRun {
	return &textRun{}
}

// textPageHook, when set, runs at the start of each page's extraction. It is
// how a test plants the fault the per-page recover exists for.
var textPageHook func(page *object.Dictionary)

// pageText extracts one page. It is the boundary a panic stops at: the page's
// text is discarded and the panic returned as an error, so that the caller and
// the other pages are unaffected. The boundary is defence in depth — every
// crash the extractor has had is also fixed where it happened — and the error
// keeps it from being silent.
//
// It is also where the run's work meter unwinds to: a page the budget or the
// context stopped is left out, with the error saying which, and its partial
// text is discarded.
func (d *Document) pageText(run *textRun, page *object.Dictionary) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			if core.IsAbort(r) {
				text, err = "", d.canceler().Cause()
				return
			}
			text, err = "", fmt.Errorf("internal error in text extraction: %v", r)
		}
	}()
	if textPageHook != nil {
		textPageHook(page)
	}
	v := d.view()
	res := d.ResolveDict(v.InheritedPageAttr(page, "Resources"))
	content, key, _ := v.ContentBytesAndKey(page.Get("Contents")) // reason: extraction returns the text it could decode
	if key != nil {
		if t, ok := run.pages.Get(v, key, res); ok {
			v.ChargeCopy(len(t)) // the copy the caller is handed
			return t, nil
		}
	}
	var out strings.Builder
	d.extractContentText(run, res, content, &out, map[*object.Stream]bool{}, 0)
	if key != nil {
		run.pages.Put(key, res, out.String())
	}
	return out.String(), nil
}

// maxTextFormDepth bounds recursion through nested form XObjects.
const maxTextFormDepth = 32

// minContentCharge is what entering one content stream costs the run's work
// meter, on top of the bytes its tokenizer charges. Entering a stream —
// resolving its resources, decoding it, starting the tokenizer, recursing —
// was measured at some 570 ns, which is a few tens of the meter's units
// (core.Meter). A stream of "/X Do /X Do" charged for its
// eleven bytes alone let a fan-out of forms run for half a minute inside the
// budget; charged this as well, the budget is the same bound in time whatever
// the streams are made of.
const minContentCharge = 64

// extractContentText appends the visible text of one content stream — a page or
// a form XObject — to out. Fonts are resolved from res; a Do that invokes a form
// XObject recurses into it with the form's own resources (audit C28).
//
// A form is extracted each time it is drawn (audit 2026-09-22 C87: the guard
// against a form that draws itself was a visited set that was never cleared,
// so a form drawn three times extracted once). onPath holds the forms being
// extracted on the way down to this one and is cleared on the way back, so it
// stops only a form that draws itself, directly or through others; depth
// bounds nesting; and the run's content budget bounds the total, which a
// fan-out of forms drawing forms would otherwise make exponential.
func (d *Document) extractContentText(run *textRun, res *object.Dictionary, content []byte, out *strings.Builder, onPath map[*object.Stream]bool, depth int) {
	if len(content) == 0 || depth > maxTextFormDepth {
		return
	}
	v := d.view()
	v.Charge(minContentCharge)
	fonts := run.fontMaps(d, res)
	var xobjs *object.Dictionary
	if res != nil {
		xobjs = d.ResolveDict(res.Get("XObject"))
	}

	var cur fontText
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
		for _, r := range cur.decode(raw) {
			out.WriteRune(r)
		}
	}
	for tk := range core.TokenizeContent(v.Cancel, content) {
		if tk.Kind != core.KindOp {
			operands = append(operands, tk)
			continue
		}
		if replaced > 0 {
			// Everything inside a replaced sequence is covered by its text:
			// the line breaks and spacing the operators would add as well
			// as the glyphs, and a form it invokes. What it changes is not:
			// a font selected inside the sequence is the font after it.
			switch tk.Op {
			case "BDC", "BMC", "EMC", "Tf":
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
				cur = fonts[operands[0].Name] // the zero fontText for a font not in the resources
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
					if sub, _ := d.view().ResolveName(st.Dict.Get("Subtype")); sub == "Form" && !onPath[st] {
						onPath[st] = true
						formRes := d.ResolveDict(st.Dict.Get("Resources"))
						if formRes == nil {
							formRes = res // a form may draw with the calling context's resources
						}
						formData, _ := d.view().Content(st) // reason: extraction returns the text it could decode
						d.extractContentText(run, formRes, formData, out, onPath, depth+1)
						delete(onPath, st)
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
	toUnicode *core.ToUnicode
	// encoding maps a character code to the character it stands for, built
	// from the font's /Encoding. It is consulted when the font carries no
	// ToUnicode entry for a code, which is the ordinary case for a simple font
	// naming one of the standard encodings.
	encoding map[int]rune
	// composite is a Type 0 font, whose strings codes cuts into codes of one
	// to four bytes by its CMap's codespace. A simple font's codes are bytes.
	composite bool
	codes     core.FontCodes
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
		toUnicode, _ := core.ParseToUnicode(d.view(), f) // reason: extraction falls back to the font's encoding without a ToUnicode map
		ft := fontText{toUnicode: toUnicode}
		if st, _ := d.view().ResolveName(f.Get("Subtype")); st == "Type0" {
			ft.composite = true
			var ok bool
			if ft.codes, ok = core.LoadFontCodes(d.view(), f); !ok {
				ft.codes = core.TwoByteFontCodes()
			}
		} else {
			ft.encoding = d.simpleEncoding(f)
		}
		out[string(name)] = ft
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
func (d *Document) simpleEncoding(f *object.Dictionary) map[int]rune {
	base := simplefont.StandardEncoding
	var differences object.Array
	switch enc := d.Resolve(f.Get("Encoding")).(type) {
	case object.Name:
		base = baseEncoding(enc, base)
	case *object.Dictionary:
		if n, ok := d.Resolve(enc.Get("BaseEncoding")).(object.Name); ok {
			base = baseEncoding(n, base)
		}
		differences, _ = d.Resolve(enc.Get("Differences")).(object.Array)
	case nil:
		// No /Encoding: the font's built-in encoding governs, which for a
		// standard Latin face is close enough to Standard for extraction.
	default:
		return nil
	}

	out := make(map[int]rune, base.Len()+len(differences))
	for code, name := range base.Codes() {
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

// baseEncoding resolves a base encoding name, keeping the current encoding for
// a name this package does not know.
func baseEncoding(n object.Name, current simplefont.Encoding) simplefont.Encoding {
	if e, ok := simplefont.EncodingNamed(string(n)); ok {
		return e
	}
	return current
}

// decode maps a shown byte string to runes.
//
// A simple font's codes are its bytes. It prefers the font's ToUnicode CMap,
// then the font's own /Encoding, and only then the byte value as Latin-1 —
// which is right for ASCII and wrong exactly where an encoding would have said
// so.
//
// A composite font's codes are cut by its CMap (ISO 32000-2 9.7.6.2), one to
// four bytes each, and looked up in its ToUnicode CMap; a code it has no entry
// for is Unicode only when the CMap is one of the predefined Uni* CMaps, whose
// codes are UTF-16 (9.10.2). Otherwise it is dropped: the CID-to-Unicode data
// for the other predefined CMaps is not carried.
func (f fontText) decode(raw []byte) []rune {
	var runes []rune
	if f.composite {
		for _, c := range f.codes.Codes(raw) {
			if rs, ok := f.toUnicode.Runes(int(c.Value)); ok {
				runes = append(runes, rs...)
			} else if rs, ok := f.codes.Unicode(c); ok {
				runes = append(runes, rs...)
			}
		}
		return runes
	}
	for _, b := range raw {
		code := int(b)
		if rs, ok := f.toUnicode.Runes(code); ok {
			runes = append(runes, rs...)
			continue
		}
		if r, ok := f.encoding[code]; ok {
			runes = append(runes, r)
			continue
		}
		if code >= 32 {
			runes = append(runes, rune(code))
		}
	}
	return runes
}

// --- content-stream tokenizer ---
