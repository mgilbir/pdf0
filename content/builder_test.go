package content

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

func mustBytes(t *testing.T, b *Builder) []byte {
	t.Helper()
	out, err := b.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return out
}

// TestOperandsPrecedeOperator pins the byte-level shape of the output. PDF is
// postfix — operands first, operator last — and a builder that got that
// backwards would produce a stream every reader rejects.
func TestOperandsPrecedeOperator(t *testing.T) {
	var b Builder
	b.Save().SetRGB(1, 0, 0).Rect(10, 20, 30, 40).Fill().Restore()
	got := string(mustBytes(t, &b))
	want := "q\n1 0 0 rg\n10 20 30 40 re\nf\nQ\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestNumbersAreFinite is the guard that matters most for a generated document:
// a NaN or an infinity reaching a content stream produces a file no reader can
// parse, and layout arithmetic over hostile input is exactly where one comes
// from. It must be refused at the point of writing, not discovered later.
func TestNumbersAreFinite(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		var b Builder
		b.MoveTo(0, 0).LineTo(bad, 10).Stroke()
		if _, err := b.Bytes(); err == nil {
			t.Errorf("%v was accepted as a coordinate", bad)
		} else if !strings.Contains(err.Error(), "non-finite") {
			t.Errorf("%v: error %q does not say the number was non-finite", bad, err)
		}
	}
	// A number too large for a PDF real is refused for the same reason.
	var b Builder
	b.MoveTo(0, 0).LineTo(1e300, 0).Stroke()
	if _, err := b.Bytes(); err == nil {
		t.Error("1e300 was accepted as a coordinate")
	}
}

// TestFirstErrorIsKept pins the accumulate-and-report contract: a page is
// hundreds of drawing calls, so they do not return errors, and the first
// failure must survive every call after it rather than being overwritten by a
// later, less informative one.
func TestFirstErrorIsKept(t *testing.T) {
	var b Builder
	b.SetLineWidth(-1)          // the real mistake
	b.MoveTo(0, 0).LineTo(1, 1) // more drawing on a broken builder
	b.SetGray(7)                // a second, different mistake
	_, err := b.Bytes()
	if err == nil {
		t.Fatal("a builder with an error returned bytes")
	}
	if !strings.Contains(err.Error(), "line width") {
		t.Errorf("error %q is not the first one", err)
	}
	// And nothing after the failure was written.
	if bytes.Contains(b.buf, []byte("m\n")) {
		t.Error("drawing continued after the first error")
	}
}

// TestUnbalancedStateIsRefused covers the three ways a stream can end in a
// state a consumer cannot make sense of. None is recoverable once the bytes are
// out, so Bytes is where they have to be caught.
func TestUnbalancedStateIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		draw  func(*Builder)
		wants string
	}{
		{"unbalanced q", func(b *Builder) { b.Save() }, "unbalanced q"},
		{"open text object", func(b *Builder) { b.BeginText() }, "inside a text object"},
		{"unpainted path", func(b *Builder) { b.MoveTo(0, 0).LineTo(1, 1) }, "unpainted path"},
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
	// Q without q is caught where it happens, not at the end.
	var b Builder
	b.Restore()
	if b.Err() == nil {
		t.Error("Restore without Save was accepted")
	}
}

// TestNestingLimit pins the bound at the value PDF/A allows. Emitting one level
// deeper would produce a file this module's own validator rejects, which is the
// one thing a writer in this repository must never do.
func TestNestingLimit(t *testing.T) {
	var b Builder
	for i := 0; i < MaxNestingDepth; i++ {
		b.Save()
	}
	if b.Err() != nil {
		t.Fatalf("depth %d was refused: %v", MaxNestingDepth, b.Err())
	}
	b.Save()
	if b.Err() == nil {
		t.Errorf("depth %d was accepted, above the limit of %d", MaxNestingDepth+1, MaxNestingDepth)
	}
}

// TestTextOperatorsNeedATextObject pins that the text operators cannot escape
// BT/ET. Outside one they are undefined, and a validator reports them.
func TestTextOperatorsNeedATextObject(t *testing.T) {
	ops := map[string]func(*Builder){
		"SetFont":       func(b *Builder) { b.SetFont("F1", 12) },
		"ShowText":      func(b *Builder) { b.ShowText([]byte("x")) },
		"MoveText":      func(b *Builder) { b.MoveText(1, 1) },
		"SetTextMatrix": func(b *Builder) { b.SetTextMatrix(1, 0, 0, 1, 0, 0) },
		"NextLine":      func(b *Builder) { b.NextLine() },
		"SetLeading":    func(b *Builder) { b.SetLeading(14) },
	}
	for name, draw := range ops {
		t.Run(name, func(t *testing.T) {
			var b Builder
			draw(&b)
			if b.Err() == nil {
				t.Errorf("%s was accepted outside BT/ET", name)
			}
		})
	}
	// BT does not nest.
	var b Builder
	b.BeginText().BeginText()
	if b.Err() == nil {
		t.Error("nested BeginText was accepted")
	}
}

// TestStringEscaping pins that a literal string cannot be ended early, and that
// binary codes survive intact. A two-byte glyph index is not text: if 0x28
// turns up as the high byte of a CID it is a byte, not an opening parenthesis,
// and a builder that fails to escape it silently corrupts the glyph.
func TestStringEscaping(t *testing.T) {
	var b Builder
	b.BeginText().SetFont("F1", 12).ShowText([]byte{'(', ')', '\\', 0x28, 0x00, 0xFF, '\r'}).EndText()
	got := string(mustBytes(t, &b))
	if !strings.Contains(got, `(\(\)\\\(`+"\x00\xff"+`\r)`) {
		t.Errorf("escaping is wrong; stream was:\n%q", got)
	}
	// The two bytes that carry no special meaning must pass through unchanged.
	if !strings.Contains(got, "\x00\xff") {
		t.Error("a binary code was altered on the way out")
	}
}

// TestShowTextAdjustedSignConvention pins the TJ array's shape. The number is
// subtracted from the position, so a positive value tightens; writing the array
// wrongly is invisible in a byte diff and obvious only on a rendered page.
func TestShowTextAdjustedSignConvention(t *testing.T) {
	var b Builder
	b.BeginText().SetFont("F1", 12).
		ShowTextAdjusted(
			TextSpan{Codes: []byte("A")},
			TextSpan{Adjust: -120},
			TextSpan{Codes: []byte("V")},
		).EndText()
	got := string(mustBytes(t, &b))
	if !strings.Contains(got, "[(A) -120 (V)] TJ") {
		t.Errorf("TJ array is wrong; stream was:\n%q", got)
	}
}

// TestResourcesRecordsEveryNamedResource pins the bookkeeping a caller needs to
// build /Resources. A name used here and missing there is a broken page, and
// the caller has no other way to know which names to define.
func TestResourcesRecordsEveryNamedResource(t *testing.T) {
	var b Builder
	b.SetExtGState("GS0").
		SetColorSpace("CS0").
		SetStrokeColorSpace("CS1").
		SetPattern("P0").
		Shading("Sh0").
		Draw("Im0").
		BeginMarkedProperties("Span", "MC0").EndMarked()
	b.BeginText().SetFont("F1", 12).ShowText([]byte("x")).EndText()
	b.Draw("Im0") // a repeat must not duplicate
	mustBytes(t, &b)

	res := b.Resources()
	check := func(name string, got []object.Name, want ...object.Name) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", name, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s = %v, want %v", name, got, want)
				return
			}
		}
	}
	check("Fonts", res.Fonts, "F1")
	check("XObjects", res.XObjects, "Im0")
	check("ExtGStates", res.ExtGStates, "GS0")
	check("ColorSpaces", res.ColorSpaces, "CS0", "CS1")
	check("Patterns", res.Patterns, "P0")
	check("Shadings", res.Shadings, "Sh0")
	check("Properties", res.Properties, "MC0")
}

// TestDeviceColorSpacesAreNotResources pins the exception: the device spaces
// are named directly by cs and CS and need no /ColorSpace entry, so recording
// them would tell a caller to define something the specification says is
// built in.
func TestDeviceColorSpacesAreNotResources(t *testing.T) {
	var b Builder
	b.SetColorSpace("DeviceRGB").SetStrokeColorSpace("DeviceCMYK").SetColorSpace("Pattern")
	mustBytes(t, &b)
	if got := b.Resources().ColorSpaces; len(got) != 0 {
		t.Errorf("device colour spaces were recorded as resources: %v", got)
	}
}

// TestNameEscaping pins that a name operand cannot be ended early either. A
// caller may pass a name from an untrusted source — a font name out of an HTML
// document, eventually — and an unescaped delimiter would change which resource
// the operator refers to.
func TestNameEscaping(t *testing.T) {
	var b Builder
	b.SetExtGState(object.Name("a b/c#d(e"))
	got := string(mustBytes(t, &b))
	if !strings.Contains(got, "/a#20b#2Fc#23d#28e gs") {
		t.Errorf("name escaping is wrong; stream was:\n%q", got)
	}
}

// TestClipNeedsAPaintingOperator pins that W is followed by one. On its own it
// leaves the path open and the clip unapplied, and the next operator would be
// swallowed into the path.
func TestClipNeedsAPaintingOperator(t *testing.T) {
	var b Builder
	b.Rect(0, 0, 10, 10).Clip()
	if _, err := b.Bytes(); err == nil {
		t.Error("a clip with no painting operator was accepted")
	}

	var ok Builder
	ok.Rect(0, 0, 10, 10).Clip().EndPath()
	if got := string(mustBytes(t, &ok)); got != "0 0 10 10 re\nW\nn\n" {
		t.Errorf("got %q", got)
	}
}

// TestEveryOperatorIsOneISO32000Defines is the strongest check available
// without rendering, and it uses this module's own validator as the oracle: the
// PDF/A rule that rejects any operator outside Annex A Table A.1 is already
// written, already exercised against 2896 corpus files, and it is exactly the
// specification this package must satisfy. A stream the Builder produces must
// contain nothing that rule would flag.
//
// The tokenizer here is core's, the same one the validator scans with, so this
// also confirms the output is tokenizable at all.
func TestEveryOperatorIsOneISO32000Defines(t *testing.T) {
	// Exercise every operator-emitting method the package has.
	var b Builder
	b.Save().
		Concat(1, 0, 0, 1, 10, 10).Translate(5, 5).Scale(2, 2).
		SetLineWidth(2).SetLineCap(RoundCap).SetLineJoin(BevelJoin).SetMiterLimit(4).
		SetDash([]float64{3, 2}, 1).SetExtGState("GS0").
		SetRenderingIntent(Perceptual).SetFlatness(1).
		SetGray(0.5).SetStrokeGray(0.2).
		SetRGB(1, 0, 0).SetStrokeRGB(0, 1, 0).
		SetCMYK(0, 0, 0, 1).SetStrokeCMYK(1, 0, 0, 0).
		SetColorSpace("CS0").SetColor(0.1, 0.2).
		SetStrokeColorSpace("CS1").SetStrokeColor(0.3).
		SetColorSpace("Pattern").SetPattern("P0").SetStrokePattern("P1").
		MoveTo(0, 0).LineTo(10, 0).CurveTo(10, 5, 5, 10, 0, 10).ClosePath().Fill().
		Rect(0, 0, 5, 5).FillEvenOdd().
		MoveTo(0, 0).LineTo(1, 1).Stroke().
		MoveTo(0, 0).LineTo(1, 1).CloseStroke().
		Rect(0, 0, 2, 2).FillStroke().
		Rect(0, 0, 2, 2).FillStrokeEvenOdd().
		Rect(0, 0, 3, 3).Clip().EndPath().
		Rect(0, 0, 3, 3).ClipEvenOdd().EndPath().
		Shading("Sh0").Draw("Im0").
		BeginMarked("Artifact").EndMarked().
		BeginMarkedProperties("Span", "MC0").EndMarked().
		MarkPoint("Anchor").MarkPointProperties("Anchor", "MC1")
	b.BeginText().
		SetFont("F1", 12).SetCharSpacing(0.5).SetWordSpacing(1).SetHorizontalScale(90).
		SetLeading(14).SetRise(2).SetTextRenderMode(FillText).
		SetTextMatrix(1, 0, 0, 1, 0, 0).MoveText(10, 10).MoveTextSetLeading(0, -14).NextLine().
		ShowText([]byte("hello")).
		ShowTextNextLine([]byte("world")).
		ShowTextAdjusted(TextSpan{Codes: []byte("A")}, TextSpan{Adjust: -50}, TextSpan{Codes: []byte("V")}).
		EndText()
	b.Restore()

	data := mustBytes(t, &b)

	// Collect the operators the tokenizer sees.
	var ops []string
	core.ForEachContentToken(core.Canceler{}, data, func(tok []byte, isName bool) {
		if isName {
			return
		}
		if len(tok) == 0 {
			return
		}
		// Operands are numbers, strings and arrays; operators are bare keywords.
		if c := tok[0]; c == '(' || c == '[' || c == '<' || c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9') {
			return
		}
		ops = append(ops, string(tok))
	})
	if len(ops) < 40 {
		t.Fatalf("only %d operators were tokenized from %d bytes; the fixture is not exercising the package", len(ops), len(data))
	}
	for _, op := range ops {
		if !iso32000Operators[op] {
			t.Errorf("emitted operator %q is not defined by ISO 32000 Annex A Table A.1", op)
		}
	}
	if t.Failed() {
		t.Logf("stream was:\n%s", data)
	}
}

// iso32000Operators is Annex A Table A.1, the set the PDF/A validator enforces.
// It is repeated here rather than imported because the validator's copy is
// unexported and, more to the point, a writer and the rule that judges it
// should not share one table: two independent copies disagree loudly, one
// shared copy agrees with itself even when both are wrong.
var iso32000Operators = map[string]bool{
	"q": true, "Q": true, "cm": true, "w": true, "J": true, "j": true,
	"M": true, "d": true, "ri": true, "i": true, "gs": true,
	"m": true, "l": true, "c": true, "v": true, "y": true, "h": true, "re": true,
	"S": true, "s": true, "f": true, "F": true, "f*": true, "B": true,
	"B*": true, "b": true, "b*": true, "n": true,
	"W": true, "W*": true,
	"BT": true, "ET": true,
	"Tc": true, "Tw": true, "Tz": true, "TL": true, "Tf": true, "Tr": true, "Ts": true,
	"Td": true, "TD": true, "Tm": true, "T*": true,
	"Tj": true, "TJ": true, "'": true, "\"": true,
	"d0": true, "d1": true,
	"CS": true, "cs": true, "SC": true, "SCN": true, "sc": true, "scn": true,
	"G": true, "g": true, "RG": true, "rg": true, "K": true, "k": true,
	"sh": true,
	"BI": true, "ID": true, "EI": true,
	"Do": true,
	"MP": true, "DP": true, "BMC": true, "BDC": true, "EMC": true,
	"BX": true, "EX": true,
}

// TestOperatorOracleHasTeeth proves the check above can fail. A table-driven
// assertion that has never been seen to reject anything is decorative.
func TestOperatorOracleHasTeeth(t *testing.T) {
	if iso32000Operators["Zz"] {
		t.Fatal("the fixture table is wrong")
	}
	var found bool
	core.ForEachContentToken(core.Canceler{}, []byte("1 2 Zz\n"), func(tok []byte, isName bool) {
		if !isName && string(tok) == "Zz" {
			found = true
		}
	})
	if !found {
		t.Error("the tokenizer did not surface an undefined operator, so the check could never fire")
	}
}

// TestRenderingIntentIsOneOfFour pins the closed set. The operand is a name a
// reader must recognise, and this module's own validator flags one it does not
// — so a stream naming a private intent could not be produced by accident.
func TestRenderingIntentIsOneOfFour(t *testing.T) {
	var b Builder
	b.SetRenderingIntent("Perceptual")
	if b.Err() != nil {
		t.Fatalf("a defined intent was refused: %v", b.Err())
	}
	var bad Builder
	bad.SetRenderingIntent("Photographic")
	if bad.Err() == nil {
		t.Error("an intent outside the four was accepted")
	}
}

// TestFlatnessIsBounded pins the operand range ISO 32000 gives it.
func TestFlatnessIsBounded(t *testing.T) {
	for _, v := range []float64{-1, 101} {
		var b Builder
		b.SetFlatness(v)
		if b.Err() == nil {
			t.Errorf("flatness %v was accepted", v)
		}
	}
	var ok Builder
	ok.SetFlatness(0) // 0 asks the device for its own default
	if ok.Err() != nil {
		t.Errorf("flatness 0 was refused: %v", ok.Err())
	}
}

// TestMarkPointsRecordTheirProperties pins that a marked point's properties
// reach the resource bookkeeping, as a marked sequence's do.
func TestMarkPointsRecordTheirProperties(t *testing.T) {
	var b Builder
	b.MarkPoint("Anchor").MarkPointProperties("Anchor", "MC7")
	mustBytes(t, &b)
	res := b.Resources().Properties
	if len(res) != 1 || res[0] != "MC7" {
		t.Errorf("Properties = %v, want [MC7]", res)
	}
}
