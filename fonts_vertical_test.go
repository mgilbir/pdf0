package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A face's vertical form (fonts.Face.Vertical) is an Identity-V Type 0 font
// over the same descendant as its Identity-H one: one program, one
// descriptor, one ToUnicode CMap, and a CIDFont whose /W2 states each kept
// glyph's own vertical metrics. The drawing-path matrix
// (fonts_drawpaths_test.go) holds its text and its PDF/A conformance, and
// htmlpdf's vertical tests where its glyphs land; these hold the embedding.

// uprightPage draws horiz across the page in the face and vert upright in its
// vertical form, on one page, and returns the written document read back.
// Either may be empty.
func uprightPage(t *testing.T, d *Document, face *fonts.Face, horiz, vert string, features shape.Features) *Document {
	t.Helper()
	var b content.Builder
	faces := map[object.Name]*fonts.Face{}
	if horiz != "" {
		b.BeginText().SetFont("F1", 12).MoveText(10, 180)
		face.DrawShaped(&b, horiz, 12)
		b.EndText()
		faces["F1"] = face
	}
	if vert != "" {
		v, err := face.Vertical()
		if err != nil {
			t.Fatal(err)
		}
		features.Vertical = true
		glyphs, _ := v.ShapeGlyphsInContext(vert, "", "", features)
		b.BeginText().SetFont("V1", 12).SetTextMatrix(1, 0, 0, 1, 200, 180)
		v.DrawUpright(&b, vert, glyphs, 12)
		b.EndText()
		faces["V1"] = v
	}
	if _, err := d.AddPage(Page{Width: 300, Height: 200, Content: &b, Faces: faces}); err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return back
}

// pageFont is the font dictionary a page names under name.
func pageFont(t *testing.T, d *Document, page *object.Dictionary, name object.Name) (*object.Dictionary, object.Object) {
	t.Helper()
	fontsDict := d.ResolveDict(d.ResolveDict(page.Get("Resources")).Get("Font"))
	ref := fontsDict.Get(name)
	f := d.ResolveDict(ref)
	if f == nil {
		t.Fatalf("the page names no font %s", name)
	}
	return f, ref
}

// TestBothFormsOfAFaceAreOneFont: text across the page and text upright,
// in one face on one page, are two Type 0 fonts — Identity-H and
// Identity-V — over one CIDFont, one descriptor and one ToUnicode CMap, and
// the CIDFont carries /DW2 and /W2 for the vertical one. The page validates
// at every PDF/A level and extracts both.
func TestBothFormsOfAFaceAreOneFont(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		face, err := fonts.Load(cidKeyedFace(t))
		if err != nil {
			t.Fatal(err)
		}
		back := uprightPage(t, mustPDFADoc(t, level), face, "ｱ日本", "日本語のテキスト", shape.Features{})
		page := back.PageList()[0]
		h, _ := pageFont(t, back, page, "F1")
		v, _ := pageFont(t, back, page, "V1")
		if h.Get("Encoding") != object.Name("Identity-H") || v.Get("Encoding") != object.Name("Identity-V") {
			t.Errorf("%s: encodings %v and %v", level, h.Get("Encoding"), v.Get("Encoding"))
		}
		for _, key := range []object.Name{"DescendantFonts", "ToUnicode", "BaseFont"} {
			if !object.Equal(h.Get(key), v.Get(key)) {
				t.Errorf("%s: the two forms' /%s differ: %v and %v", level, key, h.Get(key), v.Get(key))
			}
		}
		if n := fontDescriptors(back); n != 1 {
			t.Errorf("%s: %d font descriptors for one face", level, n)
		}
		cid := back.ResolveDict(back.Resolve(h.Get("DescendantFonts")).(object.Array)[0])
		if !cid.Has("DW2") {
			t.Errorf("%s: the CIDFont has no /DW2", level)
		}
		for _, f := range ValidatePDFA(back, level) {
			t.Errorf("%s: %s", level, f.Error())
		}
		if got := strings.Join(strings.Fields(mustExtractText(t, back)), ""); got != "ｱ日本日本語のテキスト" {
			t.Errorf("%s: extracted %q", level, got)
		}
	}
}

// TestAFaceNeverSetUprightWritesNoVerticalFont: without a vertical form the
// embedding is what it was — one Type 0 font, no /DW2, no /W2.
func TestAFaceNeverSetUprightWritesNoVerticalFont(t *testing.T) {
	face, err := fonts.Load(cidKeyedFace(t))
	if err != nil {
		t.Fatal(err)
	}
	back := uprightPage(t, NewDocument(), face, "日本語", "", shape.Features{})
	h, _ := pageFont(t, back, back.PageList()[0], "F1")
	cid := back.ResolveDict(back.Resolve(h.Get("DescendantFonts")).(object.Array)[0])
	if cid.Has("DW2") || cid.Has("W2") {
		t.Errorf("a face never set upright has vertical metrics: /DW2 %v /W2 %v", cid.Get("DW2"), cid.Get("W2"))
	}
	n := 0
	for _, o := range back.Objects {
		if dict, ok := o.Value.(*object.Dictionary); ok && dict.Get("Subtype") == object.Name("Type0") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d Type 0 fonts for a face drawn one way", n)
	}
}

// TestTheVerticalMetricsAreTheGlyphsOwn: every CID the subset kept has, by
// /W2 or by /DW2 and half its /W width, exactly the vertical advance and
// origin forme gives the glyph itself — not the shaped values, which 'vpal'
// changes for the kana and the punctuation here. "B" is 657 units wide and
// hung 328 across, not at half its width, so it needs a /W2 entry for its
// origin alone. Noto Sans states no vmtx, so
// its metrics are HarfBuzz's fallbacks (the line's height, the ink centred),
// which differ glyph by glyph and so fill /W2.
func TestTheVerticalMetricsAreTheGlyphsOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		load func(t *testing.T) (*fonts.Face, error)
		text string
	}{
		{"cid-keyed-cff", func(t *testing.T) (*fonts.Face, error) { return fonts.Load(cidKeyedFace(t)) }, "テスト、です。ｱ日本AB"},
		{"truetype-without-vmtx", func(*testing.T) (*fonts.Face, error) { return fonts.NotoSans() }, "Wiqé office"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			face, err := tc.load(t)
			if err != nil {
				t.Fatal(err)
			}
			back := uprightPage(t, NewDocument(), face, "", tc.text, shape.Features{Tags: "vpal"})
			v, _ := pageFont(t, back, back.PageList()[0], "V1")
			cid := back.ResolveDict(back.Resolve(v.Get("DescendantFonts")).(object.Array)[0])
			num := func(o object.Object) float64 {
				switch n := back.Resolve(o).(type) {
				case object.Integer:
					return float64(n)
				case object.Real:
					return float64(n)
				}
				t.Fatalf("%v is not a number", o)
				return 0
			}
			// The widths, the vertical metrics given, and the default.
			w := map[int]float64{}
			if arr, ok := back.Resolve(cid.Get("W")).(object.Array); ok {
				for i := 0; i+1 < len(arr); i += 2 {
					first := int(num(arr[i]))
					for j, x := range back.Resolve(arr[i+1]).(object.Array) {
						w[first+j] = num(x)
					}
				}
			}
			dw := num(cid.Get("DW"))
			w2 := map[int][3]float64{}
			if arr, ok := back.Resolve(cid.Get("W2")).(object.Array); ok {
				for i := 0; i+1 < len(arr); i += 2 {
					first := int(num(arr[i]))
					run := back.Resolve(arr[i+1]).(object.Array)
					for j := 0; j+2 < len(run); j += 3 {
						w2[first+j/3] = [3]float64{num(run[j]), num(run[j+1]), num(run[j+2])}
					}
				}
			}
			dw2 := back.Resolve(cid.Get("DW2")).(object.Array)
			checked, fromW2 := 0, 0
			for _, gid := range face.Used() {
				code := face.GlyphCode(gid)
				got, ok := w2[code]
				if ok {
					fromW2++
				} else {
					width, ok := w[code]
					if !ok {
						width = dw
					}
					got = [3]float64{num(dw2[1]), width / 2, num(dw2[0])}
				}
				a, x, y := face.GlyphVerticalMetrics(gid)
				if got != [3]float64{a, x, y} {
					t.Errorf("CID %d (glyph %d) is stated [w1y vx vy] = %v; the glyph's own are %v",
						code, gid, got, [3]float64{a, x, y})
				}
				checked++
			}
			if checked == 0 {
				t.Fatal("no glyph was checked")
			}
			if tc.name == "truetype-without-vmtx" && fromW2 == 0 {
				t.Error("no CID has a /W2 entry; the case tests nothing of /W2")
			}
		})
	}
}

// TestAFormAddedLaterKeepsTheFontNumbers: a document embeds a face once,
// and rewrites it in place when a later page sets more (faceembed.go), and
// writes only the forms of the face its pages name. A form a later page names
// first is added to the embedding, and each font dictionary an earlier page
// names keeps its number: the vertical form added after the horizontal one is
// written after it, and the horizontal one added after the vertical one is
// written before it, which moves the vertical one and has to move it back.
// The second page draws glyphs the first already set, so that the form is all
// that changed; a third sets more glyphs both ways. Nothing is left over,
// there is one program throughout, and the file validates and extracts.
func TestAFormAddedLaterKeepsTheFontNumbers(t *testing.T) {
	for _, verticalFirst := range []bool{false, true} {
		face, err := fonts.Load(cidKeyedFace(t))
		if err != nil {
			t.Fatal(err)
		}
		d := mustPDFADoc(t, pdfa.PDFA2b)
		draw := func(horiz, vert string) object.IndirectRef {
			t.Helper()
			var b content.Builder
			faces := map[object.Name]*fonts.Face{}
			if horiz != "" {
				b.BeginText().SetFont("F1", 12).MoveText(10, 180)
				face.DrawShaped(&b, horiz, 12)
				b.EndText()
				faces["F1"] = face
			}
			if vert != "" {
				v, err := face.Vertical()
				if err != nil {
					t.Fatal(err)
				}
				glyphs, _ := v.ShapeGlyphsInContext(vert, "", "", shape.Features{Vertical: true})
				b.BeginText().SetFont("V1", 12).SetTextMatrix(1, 0, 0, 1, 200, 180)
				v.DrawUpright(&b, vert, glyphs, 12)
				b.EndText()
				faces["V1"] = v
			}
			page, err := d.AddPage(Page{Width: 300, Height: 200, Content: &b, Faces: faces})
			if err != nil {
				t.Fatal(err)
			}
			return page
		}
		fontOf := func(page object.IndirectRef, name object.Name) object.Object {
			return d.ResolveDict(d.ResolveDict(d.ResolveDict(page).Get("Resources")).Get("Font")).Get(name)
		}
		var p1, p2 object.IndirectRef
		var h, v object.Object
		// 日本語 is the same glyphs both ways: no 'vert' form replaces an
		// ideograph.
		if verticalFirst {
			p1 = draw("", "日本語の")
			v = fontOf(p1, "V1")
			p2 = draw("日本語", "")
			h = fontOf(p2, "F1")
		} else {
			p1 = draw("日本語の", "")
			h = fontOf(p1, "F1")
			p2 = draw("", "日本語")
			v = fontOf(p2, "V1")
		}
		if n := len(face.Used()); n != 4 {
			t.Fatalf("verticalFirst=%v: the second page set new glyphs (%d in all); the case needs the same ones", verticalFirst, n)
		}
		if v == h {
			t.Fatalf("verticalFirst=%v: the two forms are one font %v", verticalFirst, h)
		}
		p3 := draw("テ", "キスト")
		type naming struct {
			page object.IndirectRef
			name object.Name
			want object.Object
		}
		checks := []naming{{p3, "F1", h}, {p3, "V1", v}}
		if verticalFirst {
			checks = append(checks, naming{p1, "V1", v}, naming{p2, "F1", h})
		} else {
			checks = append(checks, naming{p1, "F1", h}, naming{p2, "V1", v})
		}
		for _, c := range checks {
			if got := fontOf(c.page, c.name); got != c.want {
				t.Errorf("verticalFirst=%v: a page names %s as %v, want %v", verticalFirst, c.name, got, c.want)
			}
		}
		if enc := d.ResolveDict(h).Get("Encoding"); enc != object.Name("Identity-H") {
			t.Errorf("verticalFirst=%v: %v is %v", verticalFirst, h, enc)
		}
		if enc := d.ResolveDict(v).Get("Encoding"); enc != object.Name("Identity-V") {
			t.Errorf("verticalFirst=%v: %v is %v", verticalFirst, v, enc)
		}
		if !object.Equal(d.ResolveDict(h).Get("DescendantFonts"), d.ResolveDict(v).Get("DescendantFonts")) {
			t.Errorf("verticalFirst=%v: the two forms have different descendants", verticalFirst)
		}
		if o := orphans(d); len(o) != 0 {
			t.Errorf("verticalFirst=%v: objects %v are in the document and nothing refers to them", verticalFirst, o)
		}
		if n := fontDescriptors(d); n != 1 {
			t.Errorf("verticalFirst=%v: %d font programs for one face", verticalFirst, n)
		}
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range ValidatePDFA(back, pdfa.PDFA2b) {
			t.Errorf("verticalFirst=%v: %s", verticalFirst, f.Error())
		}
		if got := strings.Join(strings.Fields(mustExtractText(t, back)), ""); got != "日本語の日本語テキスト" {
			t.Errorf("verticalFirst=%v: extracted %q", verticalFirst, got)
		}
	}
}

// TestEmbedWritesTheFormItIsCalledOn: Embed on a vertical form is its
// Identity-V font, and EmbedForms on either form writes both; a simple or
// standard face has no vertical form.
func TestEmbedWritesTheFormItIsCalledOn(t *testing.T) {
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	v, err := face.Vertical()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := face.Vertical(); again != v {
		t.Error("asking again made a second vertical form")
	}
	if self, _ := v.Vertical(); self != v || v.Horizontal() != face || face.Horizontal() != face {
		t.Error("the forms do not name each other")
	}
	face.DrawShaped(&content.Builder{}, "x", 12)
	d := NewDocument()
	ref, err := v.Embed(d)
	if err != nil {
		t.Fatal(err)
	}
	if enc := d.ResolveDict(ref).Get("Encoding"); enc != object.Name("Identity-V") {
		t.Errorf("Embed on the vertical form wrote %v", enc)
	}
	type0s := func(d *Document) int {
		n := 0
		for _, o := range d.Objects {
			if dict, ok := o.Value.(*object.Dictionary); ok && dict.Get("Subtype") == object.Name("Type0") {
				n++
			}
		}
		return n
	}
	if n := type0s(d); n != 1 {
		t.Errorf("Embed on the vertical form wrote %d Type 0 fonts; the horizontal one is named by nothing", n)
	}
	both := NewDocument()
	e, err := v.EmbedForms(both, fonts.Forms{Horizontal: true, Vertical: true})
	if err != nil || e.Horizontal.Number == 0 || e.Vertical.Number == 0 || type0s(both) != 2 {
		t.Errorf("EmbedForms for both forms: %+v, %v", e, err)
	}
	if _, err := face.EmbedForms(NewDocument(), fonts.Forms{}); err == nil {
		t.Error("EmbedForms for no form wrote something")
	}
	if c := v.Clone(); !c.IsVertical() || c.Horizontal() == face {
		t.Error("a vertical form's clone is not the vertical form of a clone")
	}
	for _, load := range []func() (*fonts.Face, error){fonts.NotoSansSimple, func() (*fonts.Face, error) { return fonts.Standard("Helvetica") }} {
		f, err := load()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Vertical(); err == nil {
			t.Errorf("%s has a vertical form", f.Name())
		}
		f.DrawShaped(&content.Builder{}, "x", 12)
		if _, err := f.EmbedForms(NewDocument(), fonts.Forms{Vertical: true}); err == nil {
			t.Errorf("%s embedded a vertical font", f.Name())
		}
	}
}

// TestAFontRemovedFromTheDocumentIsWrittenAfresh: embedFaces reuses a face's
// embedding only while its font dictionaries are still in the document. A
// caller who deletes one from Objects gets a fresh embedding on the next page
// rather than a page naming nothing — for either form.
func TestAFontRemovedFromTheDocumentIsWrittenAfresh(t *testing.T) {
	for _, form := range []object.Name{"F1", "V1"} {
		face, err := fonts.Load(cidKeyedFace(t))
		if err != nil {
			t.Fatal(err)
		}
		d := NewDocument()
		page := func() object.IndirectRef {
			t.Helper()
			var b content.Builder
			b.BeginText().SetFont("F1", 12).MoveText(10, 180)
			face.DrawShaped(&b, "日本", 12)
			b.EndText()
			v, err := face.Vertical()
			if err != nil {
				t.Fatal(err)
			}
			glyphs, _ := v.ShapeGlyphsInContext("日本", "", "", shape.Features{Vertical: true})
			b.BeginText().SetFont("V1", 12).SetTextMatrix(1, 0, 0, 1, 200, 180)
			v.DrawUpright(&b, "日本", glyphs, 12)
			b.EndText()
			p, err := d.AddPage(Page{Width: 300, Height: 200, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F1": face, "V1": v}})
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		fontOf := func(p object.IndirectRef, name object.Name) object.IndirectRef {
			ref, _ := d.ResolveDict(d.ResolveDict(d.ResolveDict(p).Get("Resources")).Get("Font")).Get(name).(object.IndirectRef)
			return ref
		}
		first := page()
		delete(d.Objects, fontOf(first, form).Number)
		second := page()
		for _, name := range []object.Name{"F1", "V1"} {
			if d.ResolveDict(fontOf(second, name)) == nil {
				t.Errorf("with %s removed, the next page names %s as %v, which is not in the document",
					form, name, fontOf(second, name))
			}
		}
	}
}
