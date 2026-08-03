package pdf0

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"math"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// The write API against hostile and degenerate input.
//
// Everything that writes returns an error, and the contract this file pins is
// that the error is the *only* way it fails: no entry point may panic, whatever
// it is handed. That matters more here than in most packages, because the
// natural way to use a writer is to build a document from data that came from
// somewhere else — a form, a template, a web page — and a panic in a library
// takes the caller's process with it.
//
// The list is deliberately mechanical: every exported function and method that
// builds or writes, applied to the values a caller can reach by accident. A new
// one added without a line here is the gap this file exists to prevent.

// mustNotPanic runs f and fails with the recovered value if it panics. It
// returns whatever error f produced, so a case can also assert that the failure
// was reported rather than swallowed.
func mustNotPanic(t *testing.T, what string, f func() error) error {
	t.Helper()
	var (
		err       error
		panicked  any
		gotPanic  bool
		stackHint string
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked, gotPanic = r, true
				stackHint = fmt.Sprintf("%v", r)
			}
		}()
		err = f()
	}()
	if gotPanic {
		t.Errorf("%s panicked: %v (%v)", what, panicked, stackHint)
	}
	return err
}

// brokenDocuments are the shapes a Document can be in that a writer has to
// survive: empty, catalog-less, and pointing at things that are not there.
func brokenDocuments() map[string]*Document {
	noCatalog := &Document{Objects: map[int]*object.IndirectObject{}, Trailer: object.Dictionary{}}

	danglingRoot := &Document{Objects: map[int]*object.IndirectObject{}}
	danglingRoot.Trailer.Set("Root", object.IndirectRef{Number: 42})

	rootNotADict := &Document{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: object.Integer(7)},
	}}
	rootNotADict.Trailer.Set("Root", object.IndirectRef{Number: 1})

	noPageTree := &Document{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: &object.Dictionary{}},
	}}
	noPageTree.Trailer.Set("Root", object.IndirectRef{Number: 1})

	danglingPages := &Document{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Pages", object.IndirectRef{Number: 99})
			return d
		}()},
	}}
	danglingPages.Trailer.Set("Root", object.IndirectRef{Number: 1})

	return map[string]*Document{
		"zero value":            {},
		"no catalog":            noCatalog,
		"dangling root":         danglingRoot,
		"root is not a dict":    rootNotADict,
		"catalog has no pages":  noPageTree,
		"page tree is dangling": danglingPages,
	}
}

// TestDocumentBuildersSurviveABrokenDocument is the main sweep: every builder,
// against every broken document.
func TestDocumentBuildersSurviveABrokenDocument(t *testing.T) {
	drawing := func() *content.Builder {
		var b content.Builder
		b.Rect(0, 0, 10, 10).Fill()
		return &b
	}
	page := object.IndirectRef{Number: 1}

	operations := map[string]func(*Document) error{
		"AddPage": func(d *Document) error {
			_, err := d.AddPage(Page{Width: 10, Height: 10, Content: drawing()})
			return err
		},
		"AddForm": func(d *Document) error {
			_, err := d.AddForm(Form{BBox: [4]float64{0, 0, 10, 10}, Content: drawing()})
			return err
		},
		"AddTilingPattern": func(d *Document) error {
			_, err := d.AddTilingPattern(TilingPattern{BBox: [4]float64{0, 0, 10, 10}, Content: drawing()})
			return err
		},
		"SetOutline": func(d *Document) error {
			return d.SetOutline([]OutlineItem{{Title: "x", Page: page}})
		},
		"SetOutline(nil)": func(d *Document) error { return d.SetOutline(nil) },
		"SetStructureTree": func(d *Document) error {
			return d.SetStructureTree([]StructElem{{Tag: "P", Page: &page, Content: []int{0}}}, nil)
		},
		"SetStructureTree(nil)": func(d *Document) error { return d.SetStructureTree(nil, nil) },
		"SetDocumentInfo": func(d *Document) error {
			return d.SetDocumentInfo(DocumentInfo{Title: "x"})
		},
		"Conformance": func(d *Document) error { _, _ = d.Conformance(); return nil },
		"Add":         func(d *Document) error { d.Add(object.Null{}); return nil },
		"Write":       func(d *Document) error { return d.Write(io.Discard) },
		"Save":        func(d *Document) error { return d.Save(io.Discard) },
		"StreamData": func(d *Document) error {
			_, err := d.StreamData(&object.Stream{Dict: object.Dictionary{}})
			return err
		},
		"embed an image": func(d *Document) error {
			_, err := images.Embed(d, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
			return err
		},
		"embed a stencil": func(d *Document) error {
			_, err := images.EmbedStencil(d, image.NewAlpha(image.Rect(0, 0, 2, 2)))
			return err
		},
	}

	for docName, makeDoc := range brokenDocuments() {
		for opName, op := range operations {
			t.Run(docName+"/"+opName, func(t *testing.T) {
				// A fresh document per case: these mutate.
				docs := brokenDocuments()
				doc := docs[docName]
				if doc == nil {
					doc = makeDoc
				}
				mustNotPanic(t, opName, func() error { return op(doc) })
			})
		}
	}
}

// TestBuildersSurviveDegenerateArguments hits the argument side rather than the
// document side: nil fields, absurd numbers, empty everything.
func TestBuildersSurviveDegenerateArguments(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	page := object.IndirectRef{Number: 1}

	cases := map[string]func(*Document) error{
		"page with no content":  func(d *Document) error { _, err := d.AddPage(Page{}); return err },
		"page with nil content": func(d *Document) error { _, err := d.AddPage(Page{Width: 1, Height: 1}); return err },
		"page with a nil face": func(d *Document) error {
			var b content.Builder
			b.Rect(0, 0, 1, 1).Fill()
			_, err := d.AddPage(Page{
				Width: 10, Height: 10, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F0": nil},
			})
			return err
		},
		"page with non-finite size": func(d *Document) error {
			var b content.Builder
			b.Rect(0, 0, 1, 1).Fill()
			_, err := d.AddPage(Page{Width: inf, Height: nan, Content: &b})
			return err
		},
		"form with no content": func(d *Document) error { _, err := d.AddForm(Form{}); return err },
		"pattern with no content": func(d *Document) error {
			_, err := d.AddTilingPattern(TilingPattern{})
			return err
		},
		"outline with an empty title": func(d *Document) error {
			return d.SetOutline([]OutlineItem{{Page: page}})
		},
		"outline naming object zero": func(d *Document) error {
			return d.SetOutline([]OutlineItem{{Title: "x"}})
		},
		"structure with an empty tag": func(d *Document) error {
			return d.SetStructureTree([]StructElem{{Page: &page}}, nil)
		},
		"structure with a nil page and content": func(d *Document) error {
			return d.SetStructureTree([]StructElem{{Tag: "P", Content: []int{0}}}, nil)
		},
		"structure with a nil role map value": func(d *Document) error {
			return d.SetStructureTree([]StructElem{{Tag: "Zzz", Page: &page}}, map[string]string{"Zzz": ""})
		},
		"link with no destination": func(d *Document) error {
			var b content.Builder
			b.Rect(0, 0, 1, 1).Fill()
			_, err := d.AddPage(Page{
				Width: 10, Height: 10, Content: &b,
				Links: []Link{{Rect: [4]float64{0, 0, 1, 1}}},
			})
			return err
		},
		"link with a nil page pointer": func(d *Document) error {
			var b content.Builder
			b.Rect(0, 0, 1, 1).Fill()
			_, err := d.AddPage(Page{
				Width: 10, Height: 10, Content: &b,
				Links: []Link{{Rect: [4]float64{0, 0, 1, 1}, To: Destination{Kind: AtTop}}},
			})
			return err
		},
		"embed a nil image":   func(d *Document) error { _, err := images.Embed(d, nil); return err },
		"embed a nil stencil": func(d *Document) error { _, err := images.EmbedStencil(d, nil); return err },
		"embed empty JPEG bytes": func(d *Document) error {
			_, err := images.EmbedJPEG(d, nil, 1, 1, 3)
			return err
		},
		"embed a JPEG with absurd geometry": func(d *Document) error {
			_, err := images.EmbedJPEG(d, []byte{0xFF, 0xD8}, -1, math.MaxInt32, 3)
			return err
		},
		"stream data for a nil stream": func(d *Document) error {
			_, err := d.StreamData(nil)
			return err
		},
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			doc := NewDocument()
			err := mustNotPanic(t, name, func() error { return op(doc) })
			_ = err // several of these are legitimately fine; the point is the absence of a panic
		})
	}
}

// TestFreeFunctionsSurviveDegenerateArguments covers the builders that are not
// methods: the graphics states, the shadings, the destinations.
func TestFreeFunctionsSurviveDegenerateArguments(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	cases := map[string]func() error{
		"opacity out of range": func() error { _, err := Opacity(nan, inf); return err },
		"unknown blend mode":   func() error { _, err := Blend("Nonsense"); return err },
		"blend with bad alpha": func() error { _, err := BlendWithOpacity(BlendNormal, nan, 2); return err },
		"gradient with no stops": func() error {
			_, err := LinearGradient(0, 0, 1, 1, nil)
			return err
		},
		"gradient with one stop": func() error {
			_, err := LinearGradient(0, 0, 1, 1, []Stop{{Offset: 0}})
			return err
		},
		"gradient with non-finite coords": func() error {
			_, err := LinearGradient(nan, inf, 1, 1, []Stop{{Offset: 0}, {Offset: 1}})
			return err
		},
		"radial with a negative radius": func() error {
			_, err := RadialGradient(0, 0, -1, 0, 0, -5, []Stop{{Offset: 0}, {Offset: 1}})
			return err
		},
		"radial with no stops": func() error {
			_, err := RadialGradient(0, 0, 1, 0, 0, 2, nil)
			return err
		},
		"shading pattern over nil": func() error { _ = ShadingPattern(nil); return nil },
		"luminosity mask over nil": func() error { _, err := LuminositySoftMask(nil, [3]float64{}); return err },
		"luminosity mask, bad backdrop": func() error {
			_, err := LuminositySoftMask(object.Null{}, [3]float64{nan, inf, -1})
			return err
		},
		"alpha mask over nil": func() error { _, err := AlphaSoftMask(nil); return err },
		"no soft mask":        func() error { _ = NoSoftMask(); return nil },
		"uncoloured pattern space with an empty name": func() error {
			_ = UncoloredPatternSpace("")
			return nil
		},
		"destination of an unknown kind": func() error {
			_, err := Destination{Kind: 99}.destination(object.IndirectRef{Number: 1})
			return err
		},
		"destination with non-finite coordinates": func() error {
			_, err := Destination{Kind: AtPosition, Left: nan, Top: inf, Zoom: nan}.
				destination(object.IndirectRef{Number: 1})
			return err
		},
		"validate a nil document": func() error {
			_ = ValidatePDFABytes(nil, pdfa.PDFA2b, nil)
			return nil
		},
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) { mustNotPanic(t, name, op) })
	}
}

// TestTheContentBuilderSurvivesMisuse pins that drawing cannot panic either. A
// builder records the first error and reports it from Bytes, so every one of
// these must come back as an error and none as a crash.
func TestTheContentBuilderSurvivesMisuse(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	cases := map[string]func(*content.Builder){
		"text operator outside a text object": func(b *content.Builder) { b.ShowText([]byte("x")) },
		"nested text objects":                 func(b *content.Builder) { b.BeginText().BeginText() },
		"end text without begin":              func(b *content.Builder) { b.EndText() },
		"restore without save":                func(b *content.Builder) { b.Restore() },
		"non-finite coordinates":              func(b *content.Builder) { b.MoveTo(nan, inf).LineTo(1, 2).Stroke() },
		"non-finite matrix":                   func(b *content.Builder) { b.Concat(nan, 0, 0, inf, 0, 0) },
		"colour out of range":                 func(b *content.Builder) { b.SetRGB(-1, 2, nan) },
		"negative font size":                  func(b *content.Builder) { b.BeginText().SetFont("F0", -1) },
		"unknown render mode":                 func(b *content.Builder) { b.BeginText().SetTextRenderMode(99) },
		"negative marked-content id":          func(b *content.Builder) { b.BeginTagged("P", -1) },
		"empty adjusted text":                 func(b *content.Builder) { b.BeginText().ShowTextAdjusted() },
		"clip with no path":                   func(b *content.Builder) { b.Clip() },
		"empty name":                          func(b *content.Builder) { b.SetFont("", 12) },
		"nil codes":                           func(b *content.Builder) { b.BeginText().ShowText(nil) },
		"deeply nested save": func(b *content.Builder) {
			for i := 0; i < content.MaxNestingDepth+5; i++ {
				b.Save()
			}
		},
		"dash with non-finite phase": func(b *content.Builder) { b.SetDash([]float64{nan}, inf) },
	}
	for name, draw := range cases {
		t.Run(name, func(t *testing.T) {
			mustNotPanic(t, name, func() error {
				var b content.Builder
				draw(&b)
				_, err := b.Bytes()
				return err
			})
		})
	}
}

// TestWritingSurvivesAHostileObjectGraph is the serializer's own share. A
// caller can put anything in Objects, including cycles and references to
// nothing, and Write has to report rather than fall over.
func TestWritingSurvivesAHostileObjectGraph(t *testing.T) {
	cases := map[string]func() *Document{
		"object zero": func() *Document {
			d := NewDocument()
			d.Objects[0] = &object.IndirectObject{Number: 0, Value: object.Null{}}
			return d
		},
		"negative object number": func() *Document {
			d := NewDocument()
			d.Objects[-1] = &object.IndirectObject{Number: -1, Value: object.Null{}}
			return d
		},
		"a nil entry": func() *Document {
			d := NewDocument()
			d.Objects[9] = nil
			return d
		},
		"an object holding nil": func() *Document {
			d := NewDocument()
			d.Objects[9] = &object.IndirectObject{Number: 9, Value: nil}
			return d
		},
		"a self-referential dictionary": func() *Document {
			d := NewDocument()
			dict := &object.Dictionary{}
			dict.Set("Self", object.IndirectRef{Number: 9})
			d.Objects[9] = &object.IndirectObject{Number: 9, Value: dict}
			return d
		},
		"a dangling reference": func() *Document {
			d := NewDocument()
			dict := &object.Dictionary{}
			dict.Set("Gone", object.IndirectRef{Number: 4242})
			d.Objects[9] = &object.IndirectObject{Number: 9, Value: dict}
			return d
		},
		"a stream whose Length lies": func() *Document {
			d := NewDocument()
			s := &object.Stream{Dict: object.Dictionary{}, Data: []byte("abc")}
			s.Dict.Set("Length", object.Integer(1<<30))
			d.Objects[9] = &object.IndirectObject{Number: 9, Value: s}
			return d
		},
		"no trailer at all": func() *Document {
			return &Document{Objects: map[int]*object.IndirectObject{}}
		},
	}
	for name, make := range cases {
		t.Run(name, func(t *testing.T) {
			mustNotPanic(t, "Write "+name, func() error {
				var buf bytes.Buffer
				return make().Write(&buf)
			})
			mustNotPanic(t, "Save "+name, func() error {
				var buf bytes.Buffer
				return make().Save(&buf)
			})
		})
	}
}

// TestWritingToAFailingWriterReportsRatherThanPanics pins the other end. A
// writer that errors part-way — a full disk, a closed socket — must come back as
// an error.
func TestWritingToAFailingWriterReportsRatherThanPanics(t *testing.T) {
	build := func() *Document {
		d := NewPDFADocument(pdfa.PDFA2b)
		var b content.Builder
		b.SetRGB(0, 0, 0).Rect(0, 0, 10, 10).Fill()
		if _, err := d.AddPage(Page{Width: 100, Height: 100, Content: &b}); err != nil {
			t.Fatalf("adding the page: %v", err)
		}
		return d
	}
	for _, after := range []int{0, 1, 64, 1024} {
		t.Run(fmt.Sprintf("fails after %d bytes", after), func(t *testing.T) {
			w := &failingWriter{after: after}
			if err := mustNotPanic(t, "Write", func() error { return build().Write(w) }); err == nil {
				t.Error("a failing writer was not reported")
			}
			w2 := &failingWriter{after: after}
			if err := mustNotPanic(t, "Save", func() error { return build().Save(w2) }); err == nil {
				t.Error("a failing writer was not reported by Save")
			}
		})
	}
}

// failingWriter accepts a fixed number of bytes and then refuses.
type failingWriter struct {
	after   int
	written int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	room := w.after - w.written
	if room <= 0 {
		return 0, io.ErrShortWrite
	}
	if len(p) <= room {
		w.written += len(p)
		return len(p), nil
	}
	w.written = w.after
	return room, io.ErrShortWrite
}
