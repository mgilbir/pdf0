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
// clause 9.10.3) or, failing that, its encoding, and recursing into invoked
// form XObjects. It reads content
// through the shared lenient tokenizer, which survives a malformed stream
// rather than diagnosing it. There is no layout model, so the output is
// approximate rather than faithful.

// ExtractText returns the visible text of every page in reading order, pages
// separated by a form feed. Layout is approximate: line breaks follow the
// text-positioning operators and wide inter-glyph gaps become spaces.
//
// Each character code is decoded through its font's ToUnicode CMap first. A
// code the CMap does not map falls back:
//
//   - in a simple font, to the character its /Encoding names (a base encoding
//     and its /Differences; StandardEncoding when there is no /Encoding), and
//     failing that to the code read as Latin-1, except that a code below 32 is
//     dropped;
//   - in a composite (Type 0) font, to the code read as UTF-16 when the
//     font's /Encoding names a predefined Unicode CMap (Uni…-UCS2-… or
//     Uni…-UTF16-…); otherwise the code is dropped.
//
// A marked-content sequence carrying /ActualText contributes that text in
// place of everything shown inside it.
//
// The error is nil exactly when every page's text is in the result. Otherwise
// it reports each page that is not, as a *PageTextError, and the result holds
// the text of the others, still separated by form feeds so that the n-th page's
// text stays after the (n-1)-th feed. Three things leave a page out:
//
//   - content the page's text depends on that pdf0 declined to read: its own
//     content stream or a form XObject's, over a size limit
//     (WithMaxDecodedStreamBytes, WithMaxContentStreamBytes,
//     WithMaxDecodedContentBytes), in a filter pdf0 does not decode, or one of
//     DecryptFailures; or the ToUnicode or encoding CMap of a font it shows
//     text in, declined for the same reasons;
//   - the work budget running out (the page and every one after it, see
//     below);
//   - an internal error — a panic in the extractor, recovered at the page so
//     that one bad page does not take down the caller or the other pages.
//
// Each is reported, none is silent, and a limit's error matches
// ErrResourceLimit. Corrupt content is different: a stream whose data does not
// decode carries no text to extract, so the page contributes what could be
// read, with no error.
//
// A Locked document returns "" and one error wrapping LockReason: every page's
// content is ciphertext.
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
// mechanism the validators use to say "this result is incomplete", a finding
// under the "limit" rule (see IsCheckerFinding and docs/limits.md) — so the
// error is the only place that fact can live, and a caller who ignores it gets
// a silently short document.
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
	// Err says why: a resource limit, matching ErrResourceLimit, whose
	// message names the guard; content pdf0 declined to read for another
	// reason — a filter it does not implement, a stream that did not
	// decrypt — whose message says which; from ExtractPageText on a Locked
	// document, an error wrapping LockReason; or an internal error in the
	// extractor. A message about declined content names the stream's object
	// when there is one.
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
	if d.Locked() {
		return "", d.lockedError("extracting text")
	}
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
		return "", markLimit(stop.StopErr("extracting text"))
	}
	var b strings.Builder
	var errs []error
	for i, pg := range pages {
		// Per page: the coarse boundary. Within a page the tokenizer stops every
		// core.CancelScanBytes, so a single enormous page is interruptible too.
		if err := stop.StopErr("extracting text"); err != nil {
			return b.String(), errors.Join(append(errs, markLimit(err))...)
		}
		if i > 0 {
			b.WriteByte('\f')
		}
		text, err := rd.pageText(run, pg.Dict)
		if err != nil {
			errs = append(errs, &PageTextError{Page: i + 1, Err: markLimit(err)})
			if serr := stop.StopErr("extracting text"); serr != nil && !errors.Is(serr, context.Canceled) && !errors.Is(serr, context.DeadlineExceeded) {
				// The work budget is the whole run's: every later page would
				// be refused too, and one error for the rest says so.
				errs = append(errs, markLimit(fmt.Errorf("text of the pages after page %d not extracted: %w", i+1, serr)))
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
// be extracted — for the reasons ExtractText leaves a page out, or because the
// document is Locked — and the text is then empty. See ExtractText.
//
// There is deliberately no ExtractPageTextContext: one page is the unit of work,
// and a caller extracting several pages already has a loop of its own to check
// a context in. Adding a variant here would move that check inside a call that
// does one page's work either way.
func (d *Document) ExtractPageText(page *object.Dictionary) (string, error) {
	if d.Locked() {
		return "", &PageTextError{Err: d.lockedError("extracting the page's text")}
	}
	rd := beginRun(d)
	text, err := rd.pageText(rd.newTextRun(), page)
	if err != nil {
		return "", &PageTextError{Err: markLimit(err)}
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
	// (core.ResMemo). A page whose text is incomplete is not memoized.
	pages core.ResMemo[string]
	// declined is the first content the page being extracted depends on that
	// pdf0 declined to read, or nil. pageText resets it per page.
	declined *declinedContent
}

// declinedContent is content a page's text depends on that pdf0 declined to
// read: over a size limit, in a filter it does not decode, or not decrypted
// (core.Reason). The text of such a page is incomplete, so it is left out and
// reported rather than returned short.
type declinedContent struct {
	reason core.Reason
	what   string // what was declined, in the page's terms
	stream *object.Stream
}

// decline records d as the page's declined content, keeping the first. A
// reason that is not a declined one, and cancellation (which stops the whole
// extraction and is reported as such), are not recorded.
func (r *textRun) decline(reason core.Reason, what string, stream *object.Stream) {
	if !reason.Declined() || reason == core.ReasonCanceled || r.declined != nil {
		return
	}
	r.declined = &declinedContent{reason: reason, what: what, stream: stream}
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
	run.declined = nil
	res := d.ResolveDict(v.InheritedPageAttr(page, "Resources"))
	content, key, r := v.ContentBytesAndKey(page.Get("Contents"))
	if r.Declined() {
		run.decline(r, "the page's content", d.declinedPart(page.Get("Contents")))
	}
	if key != nil && run.declined == nil {
		if t, ok := run.pages.Get(v, key, res); ok {
			v.ChargeCopy(len(t)) // the copy the caller is handed
			return t, nil
		}
	}
	var out strings.Builder
	d.extractContentText(run, res, content, &out, map[*object.Stream]bool{}, 0)
	if run.declined != nil {
		return "", d.declinedError(run.declined)
	}
	if key != nil {
		run.pages.Put(key, res, out.String())
	}
	return out.String(), nil
}

// declinedPart is the stream a page's /Contents names that pdf0 declined to
// read: the stream itself, or the first declined one of an array of them.
func (d *Document) declinedPart(contents object.Object) *object.Stream {
	v := d.view()
	switch c := d.Resolve(contents).(type) {
	case *object.Stream:
		return c
	case object.Array:
		for _, e := range c {
			if s, ok := d.Resolve(e).(*object.Stream); ok {
				if _, r := v.Content(s); r.Declined() {
					return s
				}
			}
		}
	}
	return nil
}

// declinedError is the error for a page whose text depends on content pdf0
// declined to read. A limit names the guard that tripped: the producer
// recorded it in the run against the stream's object, or, for the run's total
// of decoded content, against no object.
func (d *Document) declinedError(c *declinedContent) error {
	v := d.view()
	e := &contentDeclinedError{reason: c.reason, what: c.what}
	if c.stream != nil {
		e.obj = v.StreamObjNum(c.stream)
	}
	if c.reason != core.ReasonLimit || v.Run == nil {
		return e
	}
	trips := v.Run.Trips.Snapshot()
	for _, t := range trips {
		if g := t.Guard(); e.obj > 0 && t.Obj == e.obj && g != core.GuardUnsupportedFilter && g != core.GuardLocked {
			e.guard = g
			return e
		}
	}
	// No trip against the stream: it was declined for the run's total, which
	// is recorded against no object — unless the recorder dropped trips, when
	// the stream's own may be among them and the guard is not known.
	total := false
	for _, t := range trips {
		switch t.Guard() {
		case core.GuardReportOverflow:
			return e
		case core.GuardContentTotal:
			total = true
		}
	}
	if total {
		e.guard = core.GuardContentTotal
	}
	return e
}

// contentDeclinedError is declinedError's error. It matches ErrResourceLimit
// when the content was declined for a limit.
type contentDeclinedError struct {
	reason core.Reason
	guard  string // the guard that tripped, when the run recorded it
	what   string
	obj    int // the declined stream's object number, or 0
}

func (e *contentDeclinedError) Error() string {
	what := e.what
	if e.obj > 0 {
		what = fmt.Sprintf("%s (object %d)", what, e.obj)
	}
	switch e.reason {
	case core.ReasonLimit:
		if e.guard == "" {
			return fmt.Sprintf("resource limit reached: %s was not decoded, being over a size limit whose trip the run did not keep", what)
		}
		return fmt.Sprintf("resource limit reached (%s): %s was not decoded", e.guard, what)
	case core.ReasonUnsupported:
		return fmt.Sprintf("not implemented (%s): %s uses a filter pdf0 does not decode", core.GuardUnsupportedFilter, what)
	}
	return fmt.Sprintf("not decrypted (%s): %s did not decrypt under the document's key", core.GuardLocked, what)
}

func (e *contentDeclinedError) Is(target error) bool {
	return target == ErrResourceLimit && e.reason == core.ReasonLimit
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
		if cur.declined != nil && len(raw) > 0 {
			run.decline(cur.declined.reason, cur.declined.what, cur.declined.stream)
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
					// A wide gap reads as a space. The number is subtracted
					// from the coordinate the font writes along (ISO 32000-2
					// 9.4.3): across the page a gap is a negative number,
					// and down it, where the pen moves to lower y, a
					// positive one.
					if gap := el.Number(); (cur.vertical && gap > 100) || (!cur.vertical && gap < -100) {
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
						formData, r := d.view().Content(st)
						run.decline(r, "the content of form XObject /"+operands[len(operands)-1].Name, st)
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
	// vertical is a Type 0 font whose CMap writes down the page (WMode 1),
	// for which a TJ number moves the pen along y.
	vertical bool
	// declined is the font's ToUnicode or encoding CMap when pdf0 declined to
	// read it, and nil otherwise: text shown in the font is then decoded
	// without it, and the page that shows it is incomplete.
	declined *declinedContent
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
		toUnicode, r := core.ParseToUnicode(d.view(), f)
		ft := fontText{toUnicode: toUnicode}
		if r.Declined() && r != core.ReasonCanceled {
			st, _ := d.Resolve(f.Get("ToUnicode")).(*object.Stream)
			ft.declined = &declinedContent{reason: r, what: "the ToUnicode CMap of font /" + string(name), stream: st}
		}
		if st, _ := d.view().ResolveName(f.Get("Subtype")); st == "Type0" {
			ft.composite = true
			ft.vertical = core.FontWMode(d.view(), f) == 1
			var ok bool
			if ft.codes, ok = core.LoadFontCodes(d.view(), f); !ok {
				ft.codes = core.TwoByteFontCodes()
				// A CMap pdf0 declined to read cuts codes wrongly, not
				// merely without a mapping.
				if cm, isStream := d.Resolve(f.Get("Encoding")).(*object.Stream); isStream && ft.declined == nil {
					if _, r := d.view().Content(cm); r.Declined() && r != core.ReasonCanceled {
						ft.declined = &declinedContent{reason: r, what: "the encoding CMap of font /" + string(name), stream: cm}
					}
				}
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
