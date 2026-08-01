package pdf0

import (
	"context"
	"github.com/mgilbir/pdf0/internal/core"
	"iter"
	"strconv"
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
	content := getContentStreamData(d, page.Get("Contents"))
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
	var operands []contentToken

	show := func(raw []byte) {
		for _, r := range decodeShown(raw, curMap, curTwoByte) {
			out.WriteRune(r)
		}
	}
	for tk := range tokenizeContent(cancel, content) {
		if tk.kind != ctOp {
			operands = append(operands, tk)
			continue
		}
		switch tk.op {
		case "Tf":
			if len(operands) >= 1 {
				if f, ok := fonts[operands[0].name]; ok {
					curMap, curTwoByte = f.toUnicode, f.twoByte
				} else {
					curMap, curTwoByte = nil, false
				}
			}
		case "Tj", "'", "\"":
			if tk.op != "Tj" {
				out.WriteByte('\n')
			}
			if len(operands) >= 1 {
				show(operands[len(operands)-1].str)
			}
		case "TJ":
			for _, el := range operands {
				switch el.kind {
				case ctString:
					show(el.str)
				case ctNumber:
					if el.number() < -100 { // wide negative adjustment ≈ a space
						out.WriteByte(' ')
					}
				}
			}
		case "Td", "TD", "T*":
			out.WriteByte('\n')
		case "Do":
			if xobjs != nil && len(operands) >= 1 {
				if st, ok := d.Resolve(xobjs.Get(Name(operands[len(operands)-1].name))).(*Stream); ok {
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

type ctKind int

const (
	ctOp ctKind = iota
	ctNumber
	ctString
	ctName
	ctArrayStart
	ctArrayEnd
)

type contentToken struct {
	kind ctKind
	op   string
	name string
	str  []byte
	raw  []byte // ctNumber: the unparsed digits, sub-sliced from the content
}

// number parses a ctNumber token's value. Parsing is deferred to the consumer
// because most consumers never look at a number: the PDF/UA content pass reads
// only operators, names and strings, yet numbers are the most common token in a
// content stream, so parsing every one eagerly was pure waste.
func (t contentToken) number() float64 {
	f, _ := strconv.ParseFloat(string(t.raw), 64)
	return f
}

// tokenizeContent iterates the operand/operator tokens of a content stream. It
// is lenient: unrecognized bytes are skipped. Array and dictionary delimiters are
// surfaced so TJ arrays can be read; inline images (BI…ID…EI) are stepped over.
//
// Tokens are yielded one at a time rather than collected into a slice. Every
// caller consumes them in a single forward pass, and a content stream of a real
// document can hold tens of millions of tokens: materializing them dominated
// PDF/UA validation, where the token slice alone accounted for ~94% of the run's
// allocated bytes (the repeated grow-and-copy of a multi-gigabyte slice, not the
// scan itself). Streaming makes the tokenizer allocation-free apart from the
// string operands it must decode.
//
// The scan stops when cancel fires, checked every cancelScanBytes of input; see
// cancel.go for why the check is gated on the scan position rather than run per
// token.
func tokenizeContent(cancel core.Canceler, data []byte) iter.Seq[contentToken] {
	return func(yield func(contentToken) bool) {
		i := 0
		nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
		for i < len(data) {
			if i >= nextCancelCheck {
				if cancel.Stopped() {
					return
				}
				nextCancelCheck = i + core.CancelScanBytes
			}
			c := data[i]
			switch {
			case isContentWS(c):
				i++
			case c == '%':
				for i < len(data) && data[i] != '\n' && data[i] != '\r' {
					i++
				}
			case c == '(':
				s, ni := scanContentLiteral(data, i)
				if !yield(contentToken{kind: ctString, str: s}) {
					return
				}
				i = ni
			case c == '<' && i+1 < len(data) && data[i+1] == '<':
				i += 2 // dictionary start — skip; not needed for text
			case c == '>' && i+1 < len(data) && data[i+1] == '>':
				i += 2
			case c == '<':
				s, ni := scanContentHex(data, i)
				if !yield(contentToken{kind: ctString, str: s}) {
					return
				}
				i = ni
			case c == '/':
				n, ni := scanContentName(data, i)
				if !yield(contentToken{kind: ctName, name: n}) {
					return
				}
				i = ni
			case c == '[':
				if !yield(contentToken{kind: ctArrayStart}) {
					return
				}
				i++
			case c == ']':
				if !yield(contentToken{kind: ctArrayEnd}) {
					return
				}
				i++
			case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
				raw, ni := scanContentNumberBytes(data, i)
				if !yield(contentToken{kind: ctNumber, raw: raw}) {
					return
				}
				i = ni
			default:
				word, ni := scanContentWord(data, i)
				i = ni
				if word == "" {
					i++
					continue
				}
				if word == "BI" {
					i = skipContentInlineImage(data, i)
					continue
				}
				if !yield(contentToken{kind: ctOp, op: word}) {
					return
				}
			}
		}
	}
}

func scanContentLiteral(data []byte, i int) ([]byte, int) {
	i++ // '('
	var out []byte
	depth := 1
	for i < len(data) {
		c := data[i]
		switch c {
		case '\\':
			i++
			if i >= len(data) {
				return out, i
			}
			switch e := data[i]; e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			default:
				if e >= '0' && e <= '7' {
					v := 0
					for k := 0; k < 3 && i < len(data) && data[i] >= '0' && data[i] <= '7'; k++ {
						v = v*8 + int(data[i]-'0')
						i++
					}
					out = append(out, byte(v))
					continue
				}
				out = append(out, e)
			}
			i++
		case '(':
			depth++
			out = append(out, c)
			i++
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, c)
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

func scanContentHex(data []byte, i int) ([]byte, int) {
	i++ // '<'
	var digits []byte
	for i < len(data) && data[i] != '>' {
		if !isContentWS(data[i]) {
			digits = append(digits, data[i])
		}
		i++
	}
	if i < len(data) {
		i++ // '>'
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for k := 0; k < len(out); k++ {
		out[k] = hexNibble(digits[2*k])<<4 | hexNibble(digits[2*k+1])
	}
	return out, i
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func scanContentName(data []byte, i int) (string, int) {
	i++ // '/'
	start := i
	for i < len(data) && !isContentWS(data[i]) && !isContentDelim(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

// scanContentNumberBytes returns the numeric literal starting at i as a
// sub-slice of data, leaving the parse to contentToken.number.
func scanContentNumberBytes(data []byte, i int) ([]byte, int) {
	start := i
	if data[i] == '-' || data[i] == '+' {
		i++
	}
	for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == '.') {
		i++
	}
	return data[start:i], i
}

func scanContentWord(data []byte, i int) (string, int) {
	start := i
	for i < len(data) && !isContentWS(data[i]) && !isContentDelim(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

// skipContentInlineImage steps past a BI…ID…EI inline image, given i positioned
// just after the BI operator. It delegates to skipInlineImage — the single,
// robust skipper — which parses the parameter dictionary and honors a declared
// /L (or /Length) so binary sample data that happens to contain the bytes "EI"
// does not truncate the image early and spew the rest as bogus tokens (audit
// C35; the previous whitespace-delimited-EI search ignored /L).
func skipContentInlineImage(data []byte, i int) int {
	pos := i
	skipInlineImage(data, &pos)
	return pos
}
