package pdf0

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// One embedded font per face per document (audit 2026-09-22 C94).

// facePage adds a page that sets text in face under the name F0.
func facePage(t *testing.T, d *Document, face *fonts.Face, text string, extra map[object.Name]*fonts.Face) (object.IndirectRef, error) {
	t.Helper()
	var b content.Builder
	b.BeginText().SetFont("F0", 12).MoveText(10, 100)
	face.DrawShaped(&b, text, 12)
	b.EndText()
	faces := map[object.Name]*fonts.Face{"F0": face}
	for k, v := range extra {
		faces[k] = v
	}
	return d.AddPage(Page{Width: 300, Height: 200, Content: &b, Faces: faces})
}

// fontDescriptors counts the font descriptors in the document, which is one
// per embedded font program.
func fontDescriptors(d *Document) int {
	n := 0
	for _, o := range d.Objects {
		if dict, ok := o.Value.(*object.Dictionary); ok && dict.Get("Type") == object.Name("FontDescriptor") {
			n++
		}
	}
	return n
}

// fontObjectNumbers lists the numbers of the font objects in the document:
// fonts, descendants, descriptors and the streams they name.
func fontObjectNumbers(d *Document) []int {
	var out []int
	for num, o := range d.Objects {
		switch v := o.Value.(type) {
		case *object.Dictionary:
			switch v.Get("Type") {
			case object.Name("Font"), object.Name("FontDescriptor"):
				out = append(out, num)
			}
		case *object.Stream:
			if v.Dict.Has("Length1") || v.Dict.Get("Subtype") == object.Name("OpenType") || isFontAux(d, num) {
				out = append(out, num)
			}
		}
	}
	slices.Sort(out)
	return out
}

// isFontAux reports whether a stream is named by a font as its /ToUnicode or
// by a descriptor as its /CIDSet.
func isFontAux(d *Document, num int) bool {
	for _, o := range d.Objects {
		dict, ok := o.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		for _, key := range []object.Name{"ToUnicode", "CIDSet"} {
			if ref, ok := dict.Get(key).(object.IndirectRef); ok && ref.Number == num {
				return true
			}
		}
	}
	return false
}

// orphans lists the objects nothing reachable from the trailer refers to.
func orphans(d *Document) []int {
	reached := map[int]bool{}
	var walk func(o object.Object)
	walk = func(o object.Object) {
		switch v := o.(type) {
		case object.IndirectRef:
			if reached[v.Number] {
				return
			}
			reached[v.Number] = true
			if io := d.Objects[v.Number]; io != nil {
				walk(io.Value)
			}
		case *object.Dictionary:
			for val := range v.Values() {
				walk(val)
			}
		case *object.Stream:
			walk(&v.Dict)
		case object.Array:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(&d.Trailer)
	var out []int
	for num := range d.Objects {
		if !reached[num] {
			out = append(out, num)
		}
	}
	slices.Sort(out)
	return out
}

func programBytes(t *testing.T, pdf []byte) int {
	t.Helper()
	rd, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range rd.Objects {
		dict, ok := o.Value.(*object.Dictionary)
		if !ok || dict.Get("Type") != object.Name("FontDescriptor") {
			continue
		}
		for _, key := range []object.Name{"FontFile2", "FontFile3"} {
			if s, ok := rd.Resolve(dict.Get(key)).(*object.Stream); ok {
				n += len(s.Data)
			}
		}
	}
	return n
}

// TestAFaceIsEmbeddedOncePerDocument is the audit's scenario: page after page
// in one face, each setting glyphs the ones before did not. Every page, and a
// form that uses the face too, name one font; that font holds
// every glyph the document sets, so the file validates and every page's text
// extracts; and the file carries one font program no larger than embedding the
// finished face once — not one per page, each larger than the last.
func TestAFaceIsEmbeddedOncePerDocument(t *testing.T) {
	for _, kind := range []string{"composite", "simple"} {
		t.Run(kind, func(t *testing.T) {
			load := fonts.NotoSans
			if kind == "simple" {
				load = fonts.NotoSansSimple
			}
			face, err := load()
			if err != nil {
				t.Fatal(err)
			}
			d := mustPDFADoc(t, pdfa.PDFA2b)
			texts := []string{"Alpha", "bravo", "Charlie 42", "delta?", "ECHO & foxtrot", "Golf, hotel; India!", "juliet KILO lima"}
			var pages []object.IndirectRef
			var fontNums []int
			for i, text := range texts {
				objects := len(d.Objects)
				ref, err := facePage(t, d, face, text, nil)
				if err != nil {
					t.Fatal(err)
				}
				pages = append(pages, ref)
				// Rewritten in place: after the first page each page adds its
				// own two objects and the font keeps its numbers.
				nums := fontObjectNumbers(d)
				if i == 0 {
					fontNums = nums
				} else {
					if got := len(d.Objects) - objects; got != 2 {
						t.Errorf("page %d added %d objects, want 2", i, got)
					}
					if !slices.Equal(nums, fontNums) {
						t.Errorf("page %d: the font's objects are %v, were %v", i, nums, fontNums)
					}
				}
			}
			var formDrawing content.Builder
			formDrawing.BeginText().SetFont("F0", 9).MoveText(0, 0)
			face.DrawShaped(&formDrawing, "Mike", 9)
			formDrawing.EndText()
			formRef, err := d.AddForm(Form{BBox: [4]float64{0, 0, 40, 12}, Content: &formDrawing,
				Faces: map[object.Name]*fonts.Face{"F0": face}})
			if err != nil {
				t.Fatal(err)
			}
			// Last, a page whose codes come from the shaping face itself: forme
			// records the glyphs for the subset, and nothing in this package
			// sees them drawn — so only the face's used set says the font must
			// be rewritten for them.
			var direct content.Builder
			codes, _ := face.Face.Encode("Zq")
			direct.BeginText().SetFont("F0", 12).MoveText(10, 50).ShowText(codes).EndText()
			if _, err := d.AddPage(Page{Width: 300, Height: 200, Content: &direct,
				Faces: map[object.Name]*fonts.Face{"F0": face}}); err != nil {
				t.Fatal(err)
			}

			var want object.Object
			for i, ref := range pages {
				res := d.ResolveDict(d.ResolveDict(ref).Get("Resources"))
				got := d.ResolveDict(res.Get("Font")).Get("F0")
				if i == 0 {
					want = got
				} else if got != want {
					t.Errorf("page %d names font %v, page 0 names %v; the face should be one font", i, got, want)
				}
			}
			formFont := d.ResolveDict(d.ResolveDict(d.Resolve(formRef).(*object.Stream).Dict.Get("Resources")).Get("Font")).Get("F0")
			if formFont != want {
				t.Errorf("the form names font %v, the pages %v", formFont, want)
			}
			if n := fontDescriptors(d); n != 1 {
				t.Errorf("%d font programs for one face", n)
			}

			var buf bytes.Buffer
			if err := d.Write(&buf); err != nil {
				t.Fatal(err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if v := ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()); len(v) != 0 {
				t.Errorf("the document does not validate: %v", v)
			}
			all, err := rd.ExtractText()
			if err != nil {
				t.Fatal(err)
			}
			for i, text := range texts {
				if !strings.Contains(all, text) {
					t.Errorf("page %d's %q does not extract; the document extracts %q", i, text, all)
				}
			}

			// The one program is the size of embedding the finished face once.
			once, err := load()
			if err != nil {
				t.Fatal(err)
			}
			d2 := mustPDFADoc(t, pdfa.PDFA2b)
			if _, err := facePage(t, d2, once, strings.Join(texts, " ")+" Mike", nil); err != nil {
				t.Fatal(err)
			}
			var buf2 bytes.Buffer
			if err := d2.Write(&buf2); err != nil {
				t.Fatal(err)
			}
			got, single := programBytes(t, buf.Bytes()), programBytes(t, buf2.Bytes())
			if got > single+single/10 {
				t.Errorf("%d pages carry %d bytes of font program; the face embedded once is %d", len(texts), got, single)
			}
		})
	}
}

// TestAPageThatAddsNoGlyphsRewritesNothing pins that the embedding is only
// rewritten when the face has set something new: a page repeating text adds its
// page and content stream and nothing else.
func TestAPageThatAddsNoGlyphsRewritesNothing(t *testing.T) {
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDocument()
	if _, err := facePage(t, d, face, "the same words", nil); err != nil {
		t.Fatal(err)
	}
	before := len(d.Objects)
	ref := d.ResolveDict(d.ResolveDict(d.PageList()[0].Get("Resources")).Get("Font")).Get("F0").(object.IndirectRef)
	font := d.Objects[ref.Number].Value
	for i := 0; i < 5; i++ {
		if _, err := facePage(t, d, face, "words the same", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(d.Objects) - before; got != 10 {
		t.Errorf("five repeat pages added %d objects, want 10 (a page and its content each)", got)
	}
	if d.Objects[ref.Number].Value != font {
		t.Error("the font was rewritten although no glyph was added")
	}
}

// TestARefusedRewriteKeepsTheEmbedding pins the rule for the rewrite: a page
// whose faces cannot all be embedded changes nothing — including the embedding
// of a face that would have been rewritten for it.
func TestARefusedRewriteKeepsTheEmbedding(t *testing.T) {
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDocument()
	if _, err := facePage(t, d, face, "first", nil); err != nil {
		t.Fatal(err)
	}
	before := docSnapshot(t, d)
	unused, err := fonts.NotoSans() // has set nothing: embedding it is refused
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facePage(t, d, face, "NEW GLYPHS", map[object.Name]*fonts.Face{"F1": unused}); err == nil {
		t.Fatal("a page naming a face that cannot be embedded was accepted")
	}
	if docSnapshot(t, d) != before {
		t.Error("the refused page changed the document")
	}
}

// TestSwapRefs covers the rewrite that writes a different number of objects
// than the embedding it replaces, so its font dictionary lands on another
// number than the one the pages name: the two numbers are exchanged throughout
// what was written.
func TestSwapRefs(t *testing.T) {
	a := &object.IndirectObject{Number: 7, Value: object.NewDictionary(
		object.Entry{Key: "Next", Value: object.IndirectRef{Number: 9}},
		object.Entry{Key: "Kids", Value: object.Array{object.IndirectRef{Number: 7}, object.IndirectRef{Number: 3}}},
	)}
	s := &object.IndirectObject{Number: 9, Value: object.NewStream(object.NewDictionary(
		object.Entry{Key: "Back", Value: object.IndirectRef{Number: 7}},
	), nil)}
	swapRefs([]*object.IndirectObject{a, s}, 7, 9)
	if a.Number != 9 || s.Number != 7 {
		t.Fatalf("numbers %d, %d; want 9, 7", a.Number, s.Number)
	}
	ad := a.Value.(*object.Dictionary)
	if ad.Get("Next") != (object.IndirectRef{Number: 7}) {
		t.Errorf("Next = %v", ad.Get("Next"))
	}
	kids := ad.Get("Kids").(object.Array)
	if kids[0] != (object.IndirectRef{Number: 9}) || kids[1] != (object.IndirectRef{Number: 3}) {
		t.Errorf("Kids = %v", kids)
	}
	if got := s.Value.(*object.Stream).Dict.Get("Back"); got != (object.IndirectRef{Number: 9}) {
		t.Errorf("Back = %v", got)
	}
}

// TestARewriteOfADifferentSizeKeepsTheFontNumber drives the path no face takes
// today: a rewrite that writes one object more than the embedding before it,
// then one fewer. The font dictionary the pages name keeps its number, nothing
// is left dangling, and the numbers only the larger embedding used are gone.
func TestARewriteOfADifferentSizeKeepsTheFontNumber(t *testing.T) {
	calls := 0
	defer func(orig func(*fonts.Face, fonts.Allocator) (object.IndirectRef, error)) { embedFace = orig }(embedFace)
	orig := embedFace
	embedFace = func(f *fonts.Face, a fonts.Allocator) (object.IndirectRef, error) {
		calls++
		if calls != 2 {
			return orig(f, a)
		}
		// One object more, written first, and named by the font dictionary
		// so that it belongs to the embedding.
		extra := a.Add(object.Integer(1))
		rec := &recordingAllocator{Allocator: a, values: map[int]object.Object{}}
		top, err := orig(f, rec)
		if err == nil {
			rec.values[top.Number].(*object.Dictionary).Set("PieceInfoForTest", extra)
		}
		return top, err
	}
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	d := mustPDFADoc(t, pdfa.PDFA2b)
	first, err := facePage(t, d, face, "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	fontRef := func(page object.IndirectRef) object.Object {
		return d.ResolveDict(d.ResolveDict(d.ResolveDict(page).Get("Resources")).Get("Font")).Get("F0")
	}
	want := fontRef(first)
	for i, text := range []string{"two", "THREE"} {
		page, err := facePage(t, d, face, text, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := fontRef(page); got != want {
			t.Errorf("page %d names %v, the first page %v", i+2, got, want)
		}
		if f := d.ResolveDict(want); f == nil || f.Get("Subtype") != object.Name("Type0") {
			t.Fatalf("after rewrite %d the font number %v holds %v", i+1, want, f)
		}
		if o := orphans(d); len(o) != 0 {
			t.Errorf("after rewrite %d, objects %v are in the document and nothing refers to them", i+1, o)
		}
	}
	if calls != 3 {
		t.Fatalf("embedded %d times, want 3", calls)
	}
	if n := fontDescriptors(d); n != 1 {
		t.Errorf("%d font programs for one face", n)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if v := ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()); len(v) != 0 {
		t.Errorf("the document does not validate: %v", v)
	}
}

// recordingAllocator passes Adds through and remembers what each number holds.
type recordingAllocator struct {
	fonts.Allocator
	values map[int]object.Object
}

func (r *recordingAllocator) Add(v object.Object) object.IndirectRef {
	ref := r.Allocator.Add(v)
	r.values[ref.Number] = v
	return ref
}
