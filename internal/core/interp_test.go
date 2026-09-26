package core

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// The content-state interpreter (interp.go), tested on one-page documents
// built here. Each test is a way the boolean content scans it replaced got the
// answer wrong (audit 2026-09-22 C73, C74, C153), and each asserts the answer.

// testDoc is a document under construction: numbered objects and a page.
type testDoc struct {
	v    View
	page *object.Dictionary
	next int
}

// newTestDoc builds a one-page document whose page has the given content and
// resources. The page is object 3.
func newTestDoc(content string, res *object.Dictionary) *testDoc {
	d := &testDoc{v: View{
		Objects: map[int]*object.IndirectObject{},
		Trailer: &object.Dictionary{},
		Limits:  DefaultLimits(),
		Run:     NewRun(&Recorder{}),
	}, next: 10}
	cs := d.stream(content, nil)
	d.page = object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Page")},
		object.Entry{Key: "Parent", Value: object.IndirectRef{Number: 2}},
		object.Entry{Key: "Contents", Value: cs},
	)
	if res != nil {
		d.page.Set("Resources", res)
	}
	d.put(3, d.page)
	d.put(2, object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Pages")},
		object.Entry{Key: "Kids", Value: object.Array{object.IndirectRef{Number: 3}}},
		object.Entry{Key: "Count", Value: object.Integer(1)},
	))
	d.put(1, object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Catalog")},
		object.Entry{Key: "Pages", Value: object.IndirectRef{Number: 2}},
	))
	d.v.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

func (d *testDoc) put(n int, o object.Object) {
	d.v.Objects[n] = &object.IndirectObject{Number: n, Value: o}
}

// add stores o under a fresh number and returns a reference to it.
func (d *testDoc) add(o object.Object) object.IndirectRef {
	n := d.next
	d.next++
	d.put(n, o)
	return object.IndirectRef{Number: n}
}

// stream adds a stream with the given data and dictionary entries.
func (d *testDoc) stream(data string, dict *object.Dictionary) object.IndirectRef {
	if dict == nil {
		dict = &object.Dictionary{}
	}
	dict.Set("Length", object.Integer(len(data)))
	return d.add(object.NewStream(dict, []byte(data)))
}

// form adds a form XObject.
func (d *testDoc) form(data string, res *object.Dictionary) object.IndirectRef {
	dict := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Form")},
		object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
	)
	if res != nil {
		dict.Set("Resources", res)
	}
	return d.stream(data, dict)
}

func (d *testDoc) use() devSet {
	return devSetOf(PageDeviceColourUse(d.v, d.page))
}

func res(entries ...object.Entry) *object.Dictionary { return object.NewDictionary(entries...) }

func sub(key object.Name, entries ...object.Entry) object.Entry {
	return object.Entry{Key: key, Value: object.NewDictionary(entries...)}
}

func ent(k object.Name, v object.Object) object.Entry { return object.Entry{Key: k, Value: v} }

var labCS = object.Array{object.Name("Lab"), object.NewDictionary(ent("WhitePoint", object.Array{object.Real(0.9505), object.Integer(1), object.Real(1.089)}))}

func (s devSet) String() string {
	var parts []string
	for _, f := range []struct {
		b devSet
		n string
	}{{devRGB, "RGB"}, {devCMYK, "CMYK"}, {devGray, "Gray"}} {
		if s&f.b != 0 {
			parts = append(parts, f.n)
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// TestShadingPaintsInItsOwnSpace is C73's first scenario: sh paints in the
// shading's colour space, never in the current colour, so a page that paints
// only a Lab shading uses no device colour. It used to count as painting in
// the initial DeviceGray.
func TestShadingPaintsInItsOwnSpace(t *testing.T) {
	d := newTestDoc("/Sh0 sh", nil)
	sh := d.add(object.NewDictionary(ent("ShadingType", object.Integer(2)), ent("ColorSpace", labCS)))
	d.page.Set("Resources", res(sub("Shading", ent("Sh0", sh))))
	if got := d.use(); got != 0 {
		t.Errorf("a Lab shading uses %v, want no device colour", got)
	}
	// A DeviceRGB shading is DeviceRGB.
	d = newTestDoc("/Sh0 sh", nil)
	sh = d.add(object.NewDictionary(ent("ShadingType", object.Integer(2)), ent("ColorSpace", object.Name("DeviceRGB"))))
	d.page.Set("Resources", res(sub("Shading", ent("Sh0", sh))))
	if got := d.use(); got != devRGB {
		t.Errorf("a DeviceRGB shading uses %v, want RGB", got)
	}
}

// TestFormInheritsTheCallersColour is C73's second scenario: a form painting
// without setting a colour paints in its caller's, here Lab (8.10.1). It used
// to be assumed to start in DeviceGray.
func TestFormInheritsTheCallersColour(t *testing.T) {
	d := newTestDoc("/CS0 cs 50 0 0 sc /Fm0 Do", nil)
	fm := d.form("0 0 10 10 re f", nil)
	d.page.Set("Resources", res(sub("ColorSpace", ent("CS0", labCS)), sub("XObject", ent("Fm0", fm))))
	if got := d.use(); got != 0 {
		t.Errorf("a form filling in its caller's Lab colour uses %v, want nothing", got)
	}
	// And in the initial colour when the caller set none: DeviceGray.
	d = newTestDoc("/Fm0 Do", nil)
	fm = d.form("0 0 10 10 re f", nil)
	d.page.Set("Resources", res(sub("XObject", ent("Fm0", fm))))
	if got := d.use(); got != devGray {
		t.Errorf("a form filling in the initial colour uses %v, want Gray", got)
	}
}

// TestStrokeAndFillAreSeparate: setting a fill colour does not set the
// stroke colour, so a stroke after a Lab fill colour paints in the initial
// DeviceGray. The single "some colour operator was seen" flag hid it.
func TestStrokeAndFillAreSeparate(t *testing.T) {
	d := newTestDoc("/CS0 cs 1 0 0 sc 0 0 10 10 re S", res(sub("ColorSpace", ent("CS0", labCS))))
	if got := d.use(); got != devGray {
		t.Errorf("a stroke after a Lab fill colour uses %v, want Gray (the initial stroke colour)", got)
	}
	d = newTestDoc("/CS0 CS 1 0 0 SC 0 0 10 10 re f", res(sub("ColorSpace", ent("CS0", labCS))))
	if got := d.use(); got != devGray {
		t.Errorf("a fill after a Lab stroke colour uses %v, want Gray (the initial fill colour)", got)
	}
}

// TestSelectingADeviceSpaceIsAUse: setting a device colour is a use of it
// whether or not anything is painted with it. The corpus fails DeviceRGB set
// for stroking where the only text is filled (6-2-4-3-t01-fail-s), and
// DeviceRGB set for filling where the only text is stroked (fail-t).
func TestSelectingADeviceSpaceIsAUse(t *testing.T) {
	for content, want := range map[string]devSet{
		"0 0.74 1 RG BT 0 Tr /F1 1 Tf (x) Tj ET":    devRGB | devGray,
		"0 0.74 1 rg BT 1 Tr /F1 1 Tf (x) Tj ET":    devRGB | devGray,
		"0 0 0 1 k":                                 devCMYK,
		"/DeviceGray CS":                            devGray,
		"/CS0 cs 0 0 1 1 re n":                      devCMYK,
		"q /CS0 cs 1 0 0 sc Q 0 0 10 10 re f":       devCMYK | devGray,
		"BT /F1 1 Tf 3 Tr (x) Tj ET 0 0 10 10 re n": 0,
	} {
		d := newTestDoc(content, res(sub("ColorSpace", ent("CS0", object.Array{object.Name("Indexed"), object.Name("DeviceCMYK"), object.Integer(0), object.String{Value: []byte{0, 0, 0, 0}}}))))
		if got := d.use(); got != want {
			t.Errorf("%q: uses %v, want %v", content, got, want)
		}
	}
}

// TestQRestoresColour: q/Q is the graphics state stack, colour included.
func TestQRestoresColour(t *testing.T) {
	d := newTestDoc("q /CS0 cs 1 0 0 sc Q 0 0 10 10 re f", res(sub("ColorSpace", ent("CS0", labCS))))
	if got := d.use(); got != devGray {
		t.Errorf("filling after q /CS0 cs Q uses %v, want Gray (the initial colour is restored)", got)
	}
	d = newTestDoc("/CS0 cs 1 0 0 sc q Q 0 0 10 10 re f", res(sub("ColorSpace", ent("CS0", labCS))))
	if got := d.use(); got != 0 {
		t.Errorf("filling in Lab after q Q uses %v, want nothing", got)
	}
}

// TestDefaultIsPerSelectionScope: a Default* space covers a device space
// selected in its own resource scope. A form painting in a DeviceRGB colour
// its caller selected is judged by the caller's DefaultRGB, and a form
// selecting DeviceRGB itself by its own.
func TestDefaultIsPerSelectionScope(t *testing.T) {
	icc := func(d *testDoc) object.Array {
		p := d.stream("", object.NewDictionary(ent("N", object.Integer(3))))
		return object.Array{object.Name("ICCBased"), p}
	}
	// The page has DefaultRGB and selects DeviceRGB; the form fills with it.
	d := newTestDoc("1 0 0 rg /Fm0 Do", nil)
	fm := d.form("0 0 10 10 re f", nil)
	d.page.Set("Resources", res(sub("ColorSpace", ent("DefaultRGB", icc(d))), sub("XObject", ent("Fm0", fm))))
	if got := d.use(); got != 0 {
		t.Errorf("a form filling in its caller's covered DeviceRGB uses %v, want nothing", got)
	}
	// The page has DefaultRGB; the form selects DeviceRGB in its own scope,
	// which has none.
	d = newTestDoc("/Fm0 Do", nil)
	fm = d.form("1 0 0 rg 0 0 10 10 re f", res())
	d.page.Set("Resources", res(sub("ColorSpace", ent("DefaultRGB", icc(d))), sub("XObject", ent("Fm0", fm))))
	if got := d.use(); got != devRGB {
		t.Errorf("a form selecting DeviceRGB in a scope with no DefaultRGB uses %v, want RGB", got)
	}
}

// TestOnlyExecutedResourcesCount is the executed-content model the corpus
// established: a form that is listed but never invoked, a colour space that
// is never selected and a Type 3 font that shows no text use nothing.
func TestOnlyExecutedResourcesCount(t *testing.T) {
	d := newTestDoc("0 0 10 10 re n", nil)
	fm := d.form("1 0 0 rg 0 0 10 10 re f", nil)
	cp := d.stream("0 0 1 1 d1 0 0 1 1 re f", nil)
	t3 := d.add(object.NewDictionary(
		ent("Type", object.Name("Font")), ent("Subtype", object.Name("Type3")),
		ent("CharProcs", object.NewDictionary(ent("a", cp))),
		ent("Encoding", object.NewDictionary(ent("Differences", object.Array{object.Integer(97), object.Name("a")}))),
		ent("Resources", res(sub("ColorSpace", ent("C", object.Name("DeviceCMYK"))))),
	))
	d.page.Set("Resources", res(
		sub("XObject", ent("Fm0", fm)),
		sub("ColorSpace", ent("CS0", object.Array{object.Name("Indexed"), object.Name("DeviceRGB"), object.Integer(1), object.String{Value: []byte("abcdef")}})),
		sub("Font", ent("T3", t3)),
	))
	if got := d.use(); got != 0 {
		t.Errorf("content that invokes nothing uses %v, want nothing", got)
	}
}

// TestType3GlyphsAreExecutedWhenShown is C153: a Type 3 glyph description is
// content, entered by showing its code. It runs in the state where the text is
// shown — so a d1 glyph (colour operators ignored) fills in the text colour —
// and the XObjects it draws are counted.
func TestType3GlyphsAreExecutedWhenShown(t *testing.T) {
	build := func(content, glyph string) *testDoc {
		d := newTestDoc(content, nil)
		img := d.stream("\x00", object.NewDictionary(
			ent("Type", object.Name("XObject")), ent("Subtype", object.Name("Image")),
			ent("Width", object.Integer(1)), ent("Height", object.Integer(1)),
			ent("ColorSpace", object.Name("DeviceCMYK")), ent("BitsPerComponent", object.Integer(8)),
		))
		cp := d.stream(glyph, nil)
		t3 := d.add(object.NewDictionary(
			ent("Type", object.Name("Font")), ent("Subtype", object.Name("Type3")),
			ent("CharProcs", object.NewDictionary(ent("a", cp))),
			ent("Encoding", object.NewDictionary(ent("Differences", object.Array{object.Integer(97), object.Name("a")}))),
			ent("Resources", res(sub("XObject", ent("Im0", img)))),
		))
		d.page.Set("Resources", res(sub("Font", ent("T3", t3)), sub("ColorSpace", ent("CS0", labCS))))
		return d
	}
	// d1: the glyph's "1 0 0 rg" is ignored and it fills in the text colour
	// (Lab); the image it draws is DeviceCMYK.
	d := build("/CS0 cs 1 0 0 sc BT /T3 1 Tf (a) Tj ET", "0 0 1 1 0 0 d1 1 0 0 rg 0 0 1 1 re f /Im0 Do")
	if got := d.use(); got != devCMYK {
		t.Errorf("a d1 glyph: uses %v, want CMYK (its image) and not RGB (its ignored rg)", got)
	}
	// d0: the glyph sets its own colour.
	d = build("/CS0 cs 1 0 0 sc BT /T3 1 Tf (a) Tj ET", "1 0 d0 1 0 0 rg 0 0 1 1 re f")
	if got := d.use(); got != devRGB {
		t.Errorf("a d0 glyph setting RGB uses %v, want RGB", got)
	}
	// Only the glyphs shown run: a font whose unshown glyph sets CMYK, shown
	// only through a glyph that sets RGB, uses RGB.
	d = build("BT /T3 1 Tf (a) Tj ET", "1 0 d0 1 0 0 rg 0 0 1 1 re f")
	t3 := d.v.ResolveDict(d.v.ResolveDict(d.page.Get("Resources")).Get("Font")).Get("T3")
	font := d.v.ResolveDict(t3)
	font.Set("CharProcs", object.NewDictionary(
		ent("a", d.stream("1 0 d0 1 0 0 rg 0 0 1 1 re f", nil)),
		ent("b", d.stream("1 0 d0 0 0 0 1 k 0 0 1 1 re f", nil)),
	))
	font.Set("Encoding", object.NewDictionary(ent("Differences", object.Array{object.Integer(97), object.Name("a"), object.Name("b")})))
	if got := d.use(); got != devRGB {
		t.Errorf("a Type 3 font showing only its RGB glyph: %v, want RGB (its CMYK glyph is never shown)", got)
	}
	// A code with no glyph in the encoding runs nothing; invisible text
	// paints nothing.
	d = build("BT /T3 1 Tf (b) Tj 3 Tr (a) Tj ET", "1 0 d0 1 0 0 rg 0 0 1 1 re f")
	if got := d.use(); got != 0 {
		t.Errorf("an unencoded code and invisible text use %v, want nothing", got)
	}
}

// TestRenderModeFollowsQ is C74: Tr is graphics state, so "q 3 Tr Q" leaves
// the mode 0 it found, and the text after it is visible. The mode used to be a
// running value that Q did not restore, which hid a visible, unembedded font.
func TestRenderModeFollowsQ(t *testing.T) {
	d := newTestDoc("q 3 Tr Q BT /F1 12 Tf (y) Tj ET", nil)
	f1 := d.add(object.NewDictionary(ent("Type", object.Name("Font")), ent("Subtype", object.Name("Type1")), ent("BaseFont", object.Name("Helvetica"))))
	d.page.Set("Resources", res(sub("Font", ent("F1", f1))))
	u := CollectFontTextUsage(d.v)[d.v.ResolveDict(f1)]
	if u == nil || !u.Modes[0] || u.Modes[3] {
		t.Fatalf("modes recorded %v, want exactly {0}", u)
	}
	// And a form shows text in its caller's font and mode.
	d = newTestDoc("BT /F1 12 Tf 3 Tr ET /Fm0 Do", nil)
	f1 = d.add(object.NewDictionary(ent("Type", object.Name("Font")), ent("Subtype", object.Name("Type1")), ent("BaseFont", object.Name("Helvetica"))))
	fm := d.form("BT (x) Tj ET", nil)
	d.page.Set("Resources", res(sub("Font", ent("F1", f1)), sub("XObject", ent("Fm0", fm))))
	u = CollectFontTextUsage(d.v)[d.v.ResolveDict(f1)]
	if u == nil || !u.Modes[3] || u.Modes[0] || len(u.Strings) != 1 || string(u.Strings[0]) != "x" {
		t.Errorf("a form showing text in its caller's font: usage %+v, want (x) in mode 3", u)
	}
}

// TestInlineImages: an inline image paints in its colour space — a device
// name, its abbreviation or a named resource — and an image mask in the fill
// colour. Its sample bytes are never read as operators, however they look:
// here a stray " EI " followed by 'k', skipped because /L says so (C149).
func TestInlineImages(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    devSet
	}{
		{"q BI /W 6 /H 1 /BPC 8 /CS /G /L 6 ID  EI k EI Q", devGray},
		{"BI /W 1 /H 1 /BPC 8 /CS /RGB ID \x00\x00\x00 EI", devRGB},
		{"BI /W 1 /H 1 /BPC 8 /CS /CS0 ID \x00 EI", 0},
		{"BI /W 1 /H 1 /BPC 8 /CS [/I /CMYK 1 <00000000FFFFFFFF>] ID \x00 EI", devCMYK},
		{"/CS0 cs 1 0 0 sc BI /W 1 /H 1 /IM true ID \x00 EI", 0},
		{"BI /W 1 /H 1 /IM true ID \x00 EI", devGray},
	} {
		d := newTestDoc(tc.content, res(sub("ColorSpace", ent("CS0", labCS))))
		if got := d.use(); got != tc.want {
			t.Errorf("%q: uses %v, want %v", tc.content, got, tc.want)
		}
	}
}

// TestOverlongRunPaintsNothing: a run of binary too long to be a token is
// dropped whole, so a 'k' at its end is not DeviceCMYK. Cutting the run at the
// token cap used to leave a one-byte "k" operator behind.
func TestOverlongRunPaintsNothing(t *testing.T) {
	d := newTestDoc("q "+strings.Repeat("A", 514)+"k 0 0 1 1 re f Q", nil)
	if got := d.use(); got != devGray {
		t.Errorf("a 515-byte binary run then a fill: %v, want Gray only", got)
	}
}

// TestPatterns: painting with a tiling pattern runs its cell, in the state at
// the beginning of the stream that selected it (8.7.3.1); an uncoloured one
// paints in its underlying space; a shading pattern in its shading's space.
// Selecting a pattern paints nothing.
func TestPatterns(t *testing.T) {
	tiling := func(d *testDoc, paintType int, cell string) object.IndirectRef {
		return d.stream(cell, object.NewDictionary(
			ent("PatternType", object.Integer(1)), ent("PaintType", object.Integer(paintType)),
			ent("TilingType", object.Integer(1)), ent("XStep", object.Integer(10)), ent("YStep", object.Integer(10)),
			ent("BBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}),
		))
	}
	d := newTestDoc("/Pattern cs /P0 scn 0 0 10 10 re f", nil)
	d.page.Set("Resources", res(sub("Pattern", ent("P0", tiling(d, 1, "1 0 0 1 k 0 0 1 1 re f")))))
	if got := d.use(); got != devCMYK {
		t.Errorf("a coloured tiling pattern filling in CMYK: %v, want CMYK", got)
	}
	d = newTestDoc("/Pattern cs /P0 scn", nil)
	d.page.Set("Resources", res(sub("Pattern", ent("P0", tiling(d, 1, "1 0 0 1 k 0 0 1 1 re f")))))
	if got := d.use(); got != 0 {
		t.Errorf("a pattern selected and never painted with: %v, want nothing", got)
	}
	d = newTestDoc("/PCS cs 1 0 0 /P0 scn 0 0 10 10 re f", nil)
	d.page.Set("Resources", res(
		sub("Pattern", ent("P0", tiling(d, 2, "0 0 1 1 re f"))),
		sub("ColorSpace", ent("PCS", object.Array{object.Name("Pattern"), object.Name("DeviceRGB")})),
	))
	if got := d.use(); got != devRGB {
		t.Errorf("an uncoloured pattern over DeviceRGB: %v, want RGB", got)
	}
	d = newTestDoc("/Pattern cs /P0 scn 0 0 10 10 re f", nil)
	sh := d.add(object.NewDictionary(ent("ShadingType", object.Integer(2)), ent("ColorSpace", labCS)))
	d.page.Set("Resources", res(sub("Pattern", ent("P0", d.add(object.NewDictionary(ent("PatternType", object.Integer(2)), ent("Shading", sh)))))))
	if got := d.use(); got != 0 {
		t.Errorf("a Lab shading pattern: %v, want nothing", got)
	}
}

// TestMemoKeepsInheritedStateApart: one form, invoked in two states, is two
// executions — the memo is keyed on the state it inherits. And a form shared
// by many pages in one state is one execution.
func TestMemoKeepsInheritedStateApart(t *testing.T) {
	d := newTestDoc("q /CS0 cs 1 0 0 sc /Fm0 Do Q /Fm0 Do", nil)
	fm := d.form("0 0 10 10 re f", nil)
	d.page.Set("Resources", res(sub("ColorSpace", ent("CS0", labCS)), sub("XObject", ent("Fm0", fm))))
	if got := d.use(); got != devGray {
		t.Errorf("a form invoked in Lab and then in the initial colour: %v, want Gray", got)
	}
	if n := d.v.Run.ContentExecutions(); n != 3 {
		t.Errorf("%d executions, want 3: the page and the form once per inherited state", n)
	}
}

// TestCyclesAreMemoisedWhereComplete is C147's scenario on the interpreter.
// Form A paints DeviceRGB and draws B; B draws A. Reached through A, B's
// execution lacks A's usage (the cycle cuts it off), so it must not be
// memoised: a page drawing only B paints DeviceRGB. The execution a cycle
// closes on is complete, and is memoised — which is what keeps a chain of
// self-referencing forms linear rather than exponential.
func TestCyclesAreMemoisedWhereComplete(t *testing.T) {
	d := newTestDoc("/A Do", nil)
	a := d.form("1 0 0 rg 0 0 1 1 re f /B Do", nil)
	b := d.form("/A Do", nil)
	xo := res(sub("XObject", ent("A", a), ent("B", b)))
	d.v.Resolve(a).(*object.Stream).Dict.Set("Resources", xo)
	d.v.Resolve(b).(*object.Stream).Dict.Set("Resources", xo)
	d.page.Set("Resources", xo)
	page2 := object.NewDictionary(ent("Type", object.Name("Page")), ent("Contents", d.stream("/B Do", nil)), ent("Resources", xo))
	if got := d.use(); got != devRGB {
		t.Errorf("page drawing A: %v, want RGB", got)
	}
	if got := devSetOf(PageDeviceColourUse(d.v, page2)); got != devRGB {
		t.Errorf("page drawing only B, after a page drawing A: %v, want RGB (B's cut-off result was memoised)", got)
	}

	// Twenty forms, each drawing the next twice and itself once. Every
	// self-reference closes on the form that makes it, so each form is
	// executed once, not 2^20 times — run capped, because the failure this
	// guards against is exponential work.
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		d := newTestDoc("/F Do", nil)
		var prev object.IndirectRef
		for i := 0; i < 20; i++ {
			content := "0 0 1 1 re f /Self Do"
			if i > 0 {
				content += " /F Do /F Do"
			}
			f := d.form(content, nil)
			r := res(sub("XObject", ent("Self", f)))
			if i > 0 {
				r.Get("XObject").(*object.Dictionary).Set("F", prev)
			}
			d.v.Resolve(f).(*object.Stream).Dict.Set("Resources", r)
			prev = f
		}
		d.page.Set("Resources", res(sub("XObject", ent("F", prev))))
		if got := d.use(); got != devGray {
			t.Errorf("a chain of self-referencing forms: %v, want Gray", got)
		}
		if n := d.v.Run.ContentExecutions(); n > 21 {
			t.Errorf("%d executions for a page and 20 forms, want at most 21", n)
		}
		if tr := d.v.Run.Trips.Snapshot(); len(tr) != 0 {
			t.Errorf("trips %v, want none", tr)
		}
	})
}

// TestInterpreterHostile: shapes a file controls that multiply the
// interpreter's work — cycles through forms and Type 3 glyphs, deep nesting,
// and a million q — each end in bounded time, with a bounded result.
func TestInterpreterHostile(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: time.Minute, MaxStack: 8 << 20}, func(t *testing.T) {
		// A form that invokes itself, and one that invokes the next of 200.
		d := newTestDoc("/Fm0 Do", nil)
		self := d.form("1 0 0 rg 0 0 1 1 re f /Fm0 Do", nil)
		selfStream := d.v.Resolve(self).(*object.Stream)
		selfStream.Dict.Set("Resources", res(sub("XObject", ent("Fm0", self))))
		d.page.Set("Resources", res(sub("XObject", ent("Fm0", self))))
		if got := d.use(); got != devRGB {
			t.Errorf("a self-invoking form: %v, want RGB", got)
		}

		d = newTestDoc("/F Do", nil)
		prev := d.form("0 0 1 1 re f", nil)
		for i := 0; i < 200; i++ {
			prev = d.form("/F Do", res(sub("XObject", ent("F", prev))))
		}
		d.page.Set("Resources", res(sub("XObject", ent("F", prev))))
		d.use()
		if tr := d.v.Run.Trips.Snapshot(); len(tr) != 1 || tr[0].Guard() != GuardContentState {
			t.Errorf("forms nested 200 deep: trips %v, want one %s", tr, GuardContentState)
		}

		// Two Type 3 fonts whose glyphs show text in each other.
		d = newTestDoc("BT /A 1 Tf (a) Tj ET", nil)
		fa, fb := d.add(&object.Dictionary{}), d.add(&object.Dictionary{})
		for _, f := range []struct {
			ref   object.IndirectRef
			other object.IndirectRef
		}{{fa, fb}, {fb, fa}} {
			cp := d.stream("1 0 d0 0 0 1 1 re f BT /X 1 Tf (a) Tj ET", nil)
			font := d.v.ResolveDict(f.ref)
			font.Set("Type", object.Name("Font"))
			font.Set("Subtype", object.Name("Type3"))
			font.Set("CharProcs", object.NewDictionary(ent("a", cp)))
			font.Set("Encoding", object.NewDictionary(ent("Differences", object.Array{object.Integer(97), object.Name("a")})))
			font.Set("Resources", res(sub("Font", ent("X", f.other))))
		}
		d.page.Set("Resources", res(sub("Font", ent("A", fa))))
		if got := d.use(); got != devGray {
			t.Errorf("mutually recursive Type 3 fonts: %v, want Gray", got)
		}

		// A million q, and as many Q.
		d = newTestDoc(strings.Repeat("q ", 1<<20)+"/CS0 cs 1 0 0 sc "+strings.Repeat("Q ", 1<<20)+"0 0 1 1 re f", res(sub("ColorSpace", ent("CS0", labCS))))
		if got := d.use(); got != devGray {
			t.Errorf("q×2^20 /CS0 cs Q×2^20 f: %v, want Gray (the initial colour is restored)", got)
		}
	})
}
