package content

import (
	"errors"
	"slices"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// The font and fill colour a Builder reports are what the stream set, as q and
// Q save and restore them, and nothing before the stream sets them.

func wantFont(t *testing.T, b *Builder, name object.Name, size float64) {
	t.Helper()
	got, gotSize, ok := b.Font()
	if !ok || got != name || gotSize != size {
		t.Errorf("Font() = %q %v %v, want %q %v true", got, gotSize, ok, name, size)
	}
}

func wantFill(t *testing.T, b *Builder, space object.Name, components ...float64) {
	t.Helper()
	c, ok := b.FillColor()
	if !ok || c.Space != space || !slices.Equal(c.Components, components) {
		t.Errorf("FillColor() = %+v %v, want %s %v", c, ok, space, components)
	}
}

func TestNothingIsKnownBeforeTheStreamSetsIt(t *testing.T) {
	// A form's stream starts from the state it is drawn in, so neither the
	// page's default black nor "no font" is a fact about it.
	var b Builder
	if _, _, ok := b.Font(); ok {
		t.Error("a fresh stream reports a font")
	}
	if _, ok := b.FillColor(); ok {
		t.Error("a fresh stream reports a fill colour")
	}
	// scn before any cs sets components in the inherited space, which is
	// not known either.
	b.SetColor(0.5)
	if c, ok := b.FillColor(); ok {
		t.Errorf("scn in an inherited space reports %+v", c)
	}
}

func TestFontFollowsTfAndSurvivesTextObjects(t *testing.T) {
	var b Builder
	b.BeginText().SetFont("F1", 12)
	wantFont(t, &b, "F1", 12)
	b.SetFont("F2", 9).EndText()
	wantFont(t, &b, "F2", 9)
	// The text state outlives ET: the next text object shows in F2.
	b.BeginText()
	wantFont(t, &b, "F2", 9)
	b.EndText()
}

func TestQRestoresFontAndColour(t *testing.T) {
	var b Builder
	b.SetRGB(1, 0, 0).BeginText().SetFont("F1", 12).EndText()
	b.Save()
	b.SetCMYK(0, 0, 0, 0.5).BeginText().SetFont("F2", 30).EndText()
	b.Save().SetGray(0.25)
	wantFill(t, &b, "DeviceGray", 0.25)
	b.Restore()
	wantFill(t, &b, "DeviceCMYK", 0, 0, 0, 0.5)
	wantFont(t, &b, "F2", 30)
	b.Restore()
	wantFill(t, &b, "DeviceRGB", 1, 0, 0)
	wantFont(t, &b, "F1", 12)
	mustBytes(t, &b)
}

func TestQRestoresUnknown(t *testing.T) {
	// A colour set inside q is gone after Q, back to not known.
	var b Builder
	b.Save().SetGray(0).Restore()
	if c, ok := b.FillColor(); ok {
		t.Errorf("after Q the colour set inside it is still reported: %+v", c)
	}
}

func TestColourSpaceSelectionStartsAtItsInitialColour(t *testing.T) {
	var b Builder
	b.SetColorSpace("DeviceCMYK")
	wantFill(t, &b, "DeviceCMYK", 0, 0, 0, 1)
	b.SetColor(0.1, 0.2, 0.3, 0.4)
	wantFill(t, &b, "DeviceCMYK", 0.1, 0.2, 0.3, 0.4)

	// A resource space's initial colour depends on the resource.
	b.SetColorSpace("CS0")
	c, ok := b.FillColor()
	if !ok || c.Space != "CS0" || c.Components != nil {
		t.Errorf("after cs /CS0: %+v %v", c, ok)
	}
	if c.IsDevice() {
		t.Error("a resource space reports itself as a device colour")
	}
	b.SetColor(0.7)
	wantFill(t, &b, "CS0", 0.7)

	b.SetColorSpace("Pattern").SetPattern("P1")
	c, _ = b.FillColor()
	if c.Space != "Pattern" || c.Pattern != "P1" || c.IsDevice() {
		t.Errorf("after scn /P1: %+v", c)
	}
}

func TestFillColorIsACopy(t *testing.T) {
	var b Builder
	b.SetRGB(0.1, 0.2, 0.3)
	c, _ := b.FillColor()
	c.Components[0] = 1
	wantFill(t, &b, "DeviceRGB", 0.1, 0.2, 0.3)
}

func TestARefusedOperatorChangesNothing(t *testing.T) {
	var b Builder
	b.SetGray(0.5)
	b.SetGray(2) // outside [0,1]: refused
	wantFill(t, &b, "DeviceGray", 0.5)
	if b.Err() == nil {
		t.Fatal("SetGray(2) was not refused")
	}
}

func TestStrokeColourIsNotFill(t *testing.T) {
	var b Builder
	b.SetGray(0.5).SetStrokeRGB(1, 0, 0).SetStrokeGray(0).SetStrokeCMYK(0, 0, 0, 1).
		SetStrokeColorSpace("DeviceRGB").SetStrokeColor(0, 1, 0)
	wantFill(t, &b, "DeviceGray", 0.5)
}

func TestFailRefusesTheStream(t *testing.T) {
	var b Builder
	b.SetGray(0)
	b.Fail(nil) // nothing to record
	if b.Err() != nil {
		t.Fatalf("Fail(nil) recorded %v", b.Err())
	}
	first := errors.New("the glyph cannot be painted")
	b.Fail(first).Fail(errors.New("second"))
	if _, err := b.Bytes(); err != first {
		t.Errorf("Bytes() error = %v, want the first Fail", err)
	}
}
