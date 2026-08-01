package pdf0

import (
	"context"
	"github.com/mgilbir/pdf0/internal/core"
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
	for i, pg := range collectPages(d, catalog.Get("Pages")) {
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
func (d *Document) ExtractPageText(page *Dictionary) string {
	return d.extractPageText(core.Canceler{}, page)
}

func (d *Document) extractPageText(cancel core.Canceler, page *Dictionary) string {
	res := d.ResolveDict(inheritedPageAttr(d, page, "Resources"))
	content := core.ContentStreamData(d.view(), page.Get("Contents"))
	var out strings.Builder
	d.extractContentText(cancel, res, content, &out, map[*Stream]bool{}, 0)
	return out.String()
}

// maxTextFormDepth bounds recursion through nested form XObjects.
const maxTextFormDepth = 32

// extractContentText appends the visible text of one content stream — a page or
// a form XObject — to out. Fonts are resolved from res; a Do that invokes a form
// XObject recurses into it with the form's own resources (audit C28). seen guards
// cyclic form references and depth bounds nesting.
func (d *Document) extractContentText(cancel core.Canceler, res *Dictionary, content []byte, out *strings.Builder, seen map[*Stream]bool, depth int) {
	if len(content) == 0 || depth > maxTextFormDepth {
		return
	}
	fonts := d.fontMapsFrom(res)
	var xobjs *Dictionary
	if res != nil {
		xobjs = d.ResolveDict(res.Get("XObject"))
	}

	var curMap map[int]rune
	curTwoByte := false
	var operands []core.ContentToken

	show := func(raw []byte) {
		for _, r := range decodeShown(raw, curMap, curTwoByte) {
			out.WriteRune(r)
		}
	}
	for tk := range core.TokenizeContent(cancel, content) {
		if tk.Kind != core.KindOp {
			operands = append(operands, tk)
			continue
		}
		switch tk.Op {
		case "Tf":
			if len(operands) >= 1 {
				if f, ok := fonts[operands[0].Name]; ok {
					curMap, curTwoByte = f.toUnicode, f.twoByte
				} else {
					curMap, curTwoByte = nil, false
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
				if st, ok := d.Resolve(xobjs.Get(Name(operands[len(operands)-1].Name))).(*Stream); ok {
					if sub, _ := st.Dict.Get("Subtype").(Name); sub == "Form" && !seen[st] {
						seen[st] = true
						formRes := d.ResolveDict(st.Dict.Get("Resources"))
						if formRes == nil {
							formRes = res // a form may draw with the calling context's resources
						}
						d.extractContentText(cancel, formRes, decodeContentStream(d, st), out, seen, depth+1)
					}
				}
			}
		}
		operands = operands[:0]
	}
}

type fontText struct {
	toUnicode map[int]rune
	twoByte   bool
}

// fontMapsFrom resolves a resource dictionary's /Font entries to their ToUnicode maps.
func (d *Document) fontMapsFrom(res *Dictionary) map[string]fontText {
	out := map[string]fontText{}
	if res == nil {
		return out
	}
	fontDict := d.ResolveDict(res.Get("Font"))
	if fontDict == nil {
		return out
	}
	for _, name := range fontDict.Keys {
		f := d.ResolveDict(fontDict.Get(name))
		if f == nil {
			continue
		}
		twoByte := false
		if st, _ := f.Get("Subtype").(Name); st == "Type0" {
			twoByte = true
		}
		out[string(name)] = fontText{toUnicode: parseToUnicodeMap(d, f), twoByte: twoByte}
	}
	return out
}

// decodeShown maps a shown byte string to runes. It prefers the font's
// ToUnicode CMap; for a simple (single-byte) font it falls back to the byte
// value as Latin-1 (a close approximation of WinAnsi for printable text), which
// recovers ASCII text from the standard fonts that carry no ToUnicode map.
func decodeShown(raw []byte, toUnicode map[int]rune, twoByte bool) []rune {
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
		if r, ok := toUnicode[code]; ok {
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
