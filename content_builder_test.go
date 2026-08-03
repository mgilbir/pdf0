package pdf0

import (
	"bytes"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// The content builder judged end to end by this module's own validator.
//
// The unit tests in the content package check the bytes it emits. These check
// the thing that actually matters: that a page drawn with it, written out and
// read back, is a document pdf0 itself accepts. A writer in this repository has
// a luxury most writers do not — the specification of its output is already
// here, executable, and hardened against 2896 corpus files.

// drawnPageDoc builds a conforming PDF/A-2b document whose single page is drawn
// by a content.Builder, and returns it with the bytes of the content stream.
func drawnPageDoc(t *testing.T, draw func(*content.Builder)) (*Document, []byte) {
	t.Helper()
	doc := NewPDFADocument(pdfa.PDFA2b)

	var b content.Builder
	draw(&b)
	data, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	if len(b.Resources().Fonts) != 0 {
		t.Fatalf("fixture uses a font; this helper builds no /Font, so the document would not conform")
	}

	stream := &object.Stream{Dict: object.Dictionary{}, Data: data}
	stream.Dict.Set("Length", object.Integer(len(data)))
	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: stream}

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792),
	})
	page.Set("Resources", &object.Dictionary{})
	page.Set("Contents", object.IndirectRef{Number: 20})
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: page}

	pages := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 21}})
	pages.Set("Count", object.Integer(1))
	return doc, data
}

// TestDrawnPageValidatesAsPDFA is the end-to-end oracle: a page drawn with the
// builder, written, re-read and validated must raise nothing. It covers the
// whole graphics half — state, transforms, paths, clipping, colour, marked
// content — because a rule that fires on any of it fires here.
//
// Colour is the part worth being explicit about. NewPDFADocument embeds an sRGB
// OutputIntent, which is what makes DeviceRGB and DeviceGray legal on this page;
// drawing in device colour without a matching intent is a 6.2.4 finding, so this
// also confirms the builder's output is judged against the real colour rules
// rather than skipping them. DeviceCMYK is deliberately not drawn here — an RGB
// intent does not cover it, which TestDrawnDeviceCMYKNeedsACMYKOutputIntent
// pins separately.
func TestDrawnPageValidatesAsPDFA(t *testing.T) {
	doc, _ := drawnPageDoc(t, func(b *content.Builder) {
		b.Save().
			SetRGB(0.9, 0.2, 0.2).
			Rect(72, 600, 200, 100).Fill().
			SetStrokeGray(0).SetLineWidth(2).SetLineCap(content.RoundCap).
			SetDash([]float64{6, 3}, 0).
			MoveTo(72, 580).LineTo(272, 580).Stroke().
			Restore()
		b.Save().
			Rect(72, 400, 200, 150).Clip().EndPath().
			SetGray(0.85).
			Rect(50, 380, 300, 200).Fill().
			Restore()
		b.Save().Translate(300, 300).Scale(2, 2).
			MoveTo(0, 0).CurveTo(10, 20, 30, 20, 40, 0).ClosePath().
			SetGray(0.4).FillStroke().
			Restore()
		b.BeginMarked("Artifact").EndMarked()
	})

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation on a drawn page: %s", e.Error())
	}
}

// TestDrawnPageSurvivesRoundTrip pins that the drawn bytes are still the drawn
// bytes after Write and Read. The builder's output goes into a stream like any
// other, and a content stream that a round trip alters is a content stream a
// signature would no longer cover.
func TestDrawnPageSurvivesRoundTrip(t *testing.T) {
	doc, drawn := drawnPageDoc(t, func(b *content.Builder) {
		b.Save().SetRGB(0, 0, 1).Rect(10, 10, 100, 100).Fill().Restore()
	})

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	stream, ok := rd.Resolve(object.IndirectRef{Number: 20}).(*object.Stream)
	if !ok {
		t.Fatal("the content stream did not survive as a stream")
	}
	if !bytes.Equal(stream.Data, drawn) {
		t.Errorf("content stream changed across a round trip:\n got %q\nwant %q", stream.Data, drawn)
	}
}

// TestDrawnPageOperatorsPassTheContentRule aims the PDF/A operator rule at the
// builder directly. TestDrawnPageValidatesAsPDFA would catch a bad operator
// too, but only among every other rule; this says which rule is the one being
// relied on, and it is the rule that is this package's specification.
func TestDrawnPageOperatorsPassTheContentRule(t *testing.T) {
	doc, _ := drawnPageDoc(t, func(b *content.Builder) {
		b.Save().SetGray(0.5).Rect(0, 0, 10, 10).FillEvenOdd().Restore()
		b.Save().MoveTo(0, 0).LineTo(5, 5).CloseStroke().Restore()
	})
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if e.RuleID() == "6.2.2" {
			t.Errorf("the builder emitted something the content rule rejects: %s", e.Error())
		}
	}
}

// TestDrawnPageOracleHasTeeth proves the end-to-end check can fail: a content
// stream carrying an operator ISO 32000 does not define must be reported. If
// this passes with a planted defect, the tests above prove nothing.
func TestDrawnPageOracleHasTeeth(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	bad := []byte("q\n1 0 0 rg\n0 0 10 10 re\nf\nZz\nQ\n")
	stream := &object.Stream{Dict: object.Dictionary{}, Data: bad}
	stream.Dict.Set("Length", object.Integer(len(bad)))
	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: stream}
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792),
	})
	page.Set("Resources", &object.Dictionary{})
	page.Set("Contents", object.IndirectRef{Number: 20})
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: page}
	pages := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 21}})
	pages.Set("Count", object.Integer(1))

	var found bool
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if e.RuleID() == "6.2.2" {
			found = true
		}
	}
	if !found {
		t.Error("an undefined operator was not reported, so the checks above could not fail either")
	}
}

// TestBuilderRefusesWhatTheValidatorWouldReject pins the relationship between
// the two halves of this design: the builder's own limits are set to the
// validator's, so a stream it agrees to produce cannot fail the rule it is
// written against. The q/Q bound is where the two meet numerically.
func TestBuilderRefusesWhatTheValidatorWouldReject(t *testing.T) {
	var b content.Builder
	for i := 0; i < content.MaxNestingDepth+1; i++ {
		b.Save()
	}
	if b.Err() == nil {
		t.Fatalf("the builder emitted %d levels of q, which PDF/A rejects", content.MaxNestingDepth+1)
	}

	// And at the limit it produces a stream the validator accepts.
	var ok content.Builder
	for i := 0; i < content.MaxNestingDepth; i++ {
		ok.Save()
	}
	ok.Rect(0, 0, 1, 1).Fill()
	for i := 0; i < content.MaxNestingDepth; i++ {
		ok.Restore()
	}
	data, err := ok.Bytes()
	if err != nil {
		t.Fatalf("the builder refused a stream at the documented limit: %v", err)
	}
	doc := NewPDFADocument(pdfa.PDFA2b)
	stream := &object.Stream{Dict: object.Dictionary{}, Data: data}
	stream.Dict.Set("Length", object.Integer(len(data)))
	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: stream}
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792),
	})
	page.Set("Resources", &object.Dictionary{})
	page.Set("Contents", object.IndirectRef{Number: 20})
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: page}
	pages := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 21}})
	pages.Set("Count", object.Integer(1))
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if e.RuleID() == "6.1.13" {
			t.Errorf("a stream at the builder's own limit was rejected: %s", e.Error())
		}
	}
}

// TestDrawnDeviceCMYKNeedsACMYKOutputIntent records a constraint that binds
// anything drawing through this package, and that is easy to meet by accident
// and then be surprised by: an OutputIntent covers the colour space it
// describes, not device colour in general. NewPDFADocument embeds sRGB, so a
// page it hosts may draw in DeviceRGB and DeviceGray and may not draw in
// DeviceCMYK.
//
// The builder cannot enforce this — it does not know which document its stream
// will land in, and the same stream is conforming in one and not in another. So
// the rule is the caller's to meet, and this is the test that says so.
func TestDrawnDeviceCMYKNeedsACMYKOutputIntent(t *testing.T) {
	doc, _ := drawnPageDoc(t, func(b *content.Builder) {
		b.Save().SetCMYK(0, 0, 0, 0.15).Rect(50, 380, 300, 200).Fill().Restore()
	})
	var found bool
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if e.RuleID() == "6.2.4.3" {
			found = true
		}
	}
	if !found {
		t.Error("DeviceCMYK under an sRGB OutputIntent was not reported")
	}
}
