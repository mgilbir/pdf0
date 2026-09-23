package content

import (
	"strings"
	"testing"
)

// The structural rules the package doc promises, beyond q/Q and BT/ET: marked
// content is balanced, and a path under construction admits nothing but path
// construction, clipping and painting (ISO 32000-2 8.2, Figure 9)
// (audit 2026-09-22 C97).

// TestMarkedContentMustBalance pins that every BMC/BDC is closed by an EMC
// and that an EMC closes something. An unclosed sequence swallows the rest of
// the page into one structure element; a stray EMC closes the sequence an
// enclosing content stream opened, or nothing.
func TestMarkedContentMustBalance(t *testing.T) {
	cases := []struct {
		name  string
		draw  func(*Builder)
		wants string
	}{
		{"BeginTagged without EndMarked", func(b *Builder) {
			b.BeginTagged("P", 0).BeginText().SetFont("F1", 12).ShowText([]byte("x")).EndText()
		}, "unclosed marked-content"},
		{"BeginMarked without EndMarked", func(b *Builder) { b.BeginMarked("Artifact") }, "unclosed marked-content"},
		{"BeginActualText without EndMarked", func(b *Builder) {
			b.BeginText().SetFont("F1", 12).BeginActualText("x").EndText()
		}, "marked-content"},
		{"nested, one closed", func(b *Builder) {
			b.BeginMarked("Sect").BeginTagged("P", 1).EndMarked()
		}, "unclosed marked-content"},
		{"lone EndMarked", func(b *Builder) { b.EndMarked() }, "EndMarked without"},
		{"one EndMarked too many", func(b *Builder) { b.BeginMarked("X").EndMarked().EndMarked() }, "EndMarked without"},
		// A sequence opened inside a text object ends inside it, and one opened
		// outside does not end inside: the sequences and the text object nest.
		{"sequence straddles ET", func(b *Builder) {
			b.BeginText().BeginMarked("Span").EndText().EndMarked()
		}, "EndText"},
		{"sequence closed inside a later text object", func(b *Builder) {
			b.BeginMarked("P").BeginText().EndMarked().EndText()
		}, "EndMarked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b Builder
			tc.draw(&b)
			_, err := b.Bytes()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
		})
	}

	// The shapes that are right: nested sequences, a sequence inside a text
	// object, a text object inside a sequence.
	var ok Builder
	ok.BeginMarked("Sect").BeginTagged("P", 0).
		BeginText().SetFont("F1", 12).BeginMarked("Span").ShowText([]byte("a")).EndMarked().EndText().
		EndMarked().EndMarked()
	if _, err := ok.Bytes(); err != nil {
		t.Errorf("a well-nested stream was refused: %v", err)
	}
}

// TestNothingButPathOperatorsInsideAPath pins Figure 9's path object: after m
// or re, only path construction, W/W* and a painting operator may follow. A
// colour, graphics-state, marked-content or sh operator there is outside the
// grammar, and readers disagree about what it does.
func TestNothingButPathOperatorsInsideAPath(t *testing.T) {
	inPath := map[string]func(*Builder){
		"SetRGB":             func(b *Builder) { b.SetRGB(1, 0, 0) },
		"SetStrokeRGB":       func(b *Builder) { b.SetStrokeRGB(1, 0, 0) },
		"SetGray":            func(b *Builder) { b.SetGray(0) },
		"SetCMYK":            func(b *Builder) { b.SetCMYK(0, 0, 0, 1) },
		"SetColorSpace":      func(b *Builder) { b.SetColorSpace("DeviceRGB") },
		"SetColor":           func(b *Builder) { b.SetColor(1) },
		"SetPattern":         func(b *Builder) { b.SetPattern("P0") },
		"SetExtGState":       func(b *Builder) { b.SetExtGState("G0") },
		"SetLineWidth":       func(b *Builder) { b.SetLineWidth(1) },
		"SetLineCap":         func(b *Builder) { b.SetLineCap(RoundCap) },
		"SetLineJoin":        func(b *Builder) { b.SetLineJoin(RoundJoin) },
		"SetMiterLimit":      func(b *Builder) { b.SetMiterLimit(2) },
		"SetDash":            func(b *Builder) { b.SetDash([]float64{1}, 0) },
		"SetRenderingIntent": func(b *Builder) { b.SetRenderingIntent(Perceptual) },
		"SetFlatness":        func(b *Builder) { b.SetFlatness(1) },
		"Concat":             func(b *Builder) { b.Concat(1, 0, 0, 1, 0, 0) },
		"BeginMarked":        func(b *Builder) { b.BeginMarked("X") },
		"BeginTagged":        func(b *Builder) { b.BeginTagged("P", 0) },
		"BeginMarkedProps":   func(b *Builder) { b.BeginMarkedProperties("X", "MC0") },
		"MarkPoint":          func(b *Builder) { b.MarkPoint("X") },
		"MarkPointProps":     func(b *Builder) { b.MarkPointProperties("X", "MC0") },
		"Shading":            func(b *Builder) { b.Shading("Sh0") },
		"Draw":               func(b *Builder) { b.Draw("Im0") },
		"BeginText":          func(b *Builder) { b.BeginText() },
		"Save":               func(b *Builder) { b.Save() },
	}
	for name, op := range inPath {
		t.Run(name, func(t *testing.T) {
			var b Builder
			b.MoveTo(0, 0)
			op(&b)
			b.LineTo(1, 1).Stroke()
			if b.Err() == nil {
				t.Fatalf("%s inside a path was accepted: %q", name, b.buf)
			}
		})
	}

	// The audit's own sequence.
	var b Builder
	b.MoveTo(0, 0).SetRGB(1, 0, 0).SetExtGState("G").BeginMarked("X").LineTo(1, 1).Stroke().EndMarked()
	if _, err := b.Bytes(); err == nil {
		t.Error("rg, gs and BMC inside a path were accepted")
	}

	// Every path operator is still allowed in a path.
	var ok Builder
	ok.MoveTo(0, 0).LineTo(1, 1).CurveTo(1, 2, 3, 4, 5, 6).ClosePath().Rect(0, 0, 1, 1).Clip().EndPath()
	if _, err := ok.Bytes(); err != nil {
		t.Errorf("a path of path operators was refused: %v", err)
	}
}

// TestAClipIsFollowedByItsPaintingOperator pins the other half of Figure 9:
// W and W* end path construction, and the only thing that may follow is the
// painting operator that applies them.
func TestAClipIsFollowedByItsPaintingOperator(t *testing.T) {
	var b Builder
	b.Rect(0, 0, 10, 10).Clip().LineTo(5, 5).EndPath()
	if b.Err() == nil {
		t.Errorf("path construction after W was accepted: %q", b.buf)
	}
	var ok Builder
	ok.Rect(0, 0, 10, 10).ClipEvenOdd().Fill()
	if _, err := ok.Bytes(); err != nil {
		t.Errorf("W* f was refused: %v", err)
	}
}
