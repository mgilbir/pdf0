package content

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// An uncoloured tiling pattern (PaintType 2) has no colour of its own: it is
// painted with the colour given beside its name, as one operator,
// c1 … cn /P scn (ISO 32000-2 8.7.3.3), in a /Pattern colour space with an
// underlying space that says what c1 … cn mean. A name alone, or the
// components in a separate scn, is an operand-count error a reader rejects
// (poppler: "Incorrect number of arguments in 'scn' command").

// TestUncoloredPatternIsOneOperator pins the bytes: the space selected by name,
// then the components and the pattern name in a single scn (SCN to stroke).
func TestUncoloredPatternIsOneOperator(t *testing.T) {
	var b Builder
	b.SetUncoloredPatternSpace("CS0", "DeviceRGB").
		SetUncoloredPattern("P0", 0.8, 0.1, 0.1).
		SetStrokeUncoloredPatternSpace("CS1", "DeviceGray").
		SetStrokeUncoloredPattern("P0", 0.5).
		Rect(0, 0, 10, 10).FillStroke()
	got := string(mustBytes(t, &b))
	want := "/CS0 cs\n0.8 0.1 0.1 /P0 scn\n/CS1 CS\n0.5 /P0 SCN\n0 0 10 10 re\nB\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	res := b.Resources()
	if strings.Join(names(res.ColorSpaces), ",") != "CS0,CS1" || strings.Join(names(res.Patterns), ",") != "P0" {
		t.Errorf("resources: colour spaces %v, patterns %v; want [CS0 CS1], [P0]", res.ColorSpaces, res.Patterns)
	}
	c, ok := b.FillColor()
	if !ok || c.Space != "CS0" || c.Underlying != "DeviceRGB" || c.Pattern != "P0" || len(c.Components) != 3 {
		t.Errorf("FillColor = %+v, %v; want CS0 over DeviceRGB, P0, three components", c, ok)
	}
}

func names(ns []object.Name) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = string(n)
	}
	return out
}

// TestUncoloredPatternRefusesInvalidContent pins that the Builder fails rather
// than write an scn a reader rejects, or one whose colour is undefined.
func TestUncoloredPatternRefusesInvalidContent(t *testing.T) {
	cases := map[string]struct {
		draw func(*Builder)
		want string
	}{
		"no uncoloured space selected": {
			func(b *Builder) { b.SetUncoloredPattern("P0", 1, 0, 0) }, "uncoloured pattern space"},
		"after a plain colour space": {
			func(b *Builder) { b.SetColorSpace("CS0").SetUncoloredPattern("P0", 1, 0, 0) }, "uncoloured pattern space"},
		"after a device colour replaced it": {
			func(b *Builder) {
				b.SetUncoloredPatternSpace("CS0", "DeviceRGB").SetRGB(1, 0, 0).SetUncoloredPattern("P0", 1, 0, 0)
			}, "uncoloured pattern space"},
		"too few components": {
			func(b *Builder) { b.SetUncoloredPatternSpace("CS0", "DeviceRGB").SetUncoloredPattern("P0", 1, 0) }, "3"},
		"too many components": {
			func(b *Builder) { b.SetUncoloredPatternSpace("CS0", "DeviceGray").SetUncoloredPattern("P0", 1, 0) }, "1"},
		"component out of range": {
			func(b *Builder) {
				b.SetUncoloredPatternSpace("CS0", "DeviceCMYK").SetUncoloredPattern("P0", 0, 0, 0, 2)
			}, "[0,1]"},
		"underlying not a device space": {
			func(b *Builder) { b.SetUncoloredPatternSpace("CS0", "CS9") }, "DeviceGray, DeviceRGB or DeviceCMYK"},
		"space named as a device space": {
			func(b *Builder) { b.SetUncoloredPatternSpace("Pattern", "DeviceRGB") }, "resource"},
		"name alone in an uncoloured space": {
			func(b *Builder) { b.SetUncoloredPatternSpace("CS0", "DeviceRGB").SetPattern("P0") }, "SetUncoloredPattern"},
		"components alone in an uncoloured space": {
			func(b *Builder) { b.SetUncoloredPatternSpace("CS0", "DeviceRGB").SetColor(1, 0, 0) }, "SetUncoloredPattern"},
		"stroke: no uncoloured space selected": {
			func(b *Builder) { b.SetStrokeUncoloredPattern("P0", 1, 0, 0) }, "uncoloured pattern space"},
		"stroke: name alone in an uncoloured space": {
			func(b *Builder) { b.SetStrokeUncoloredPatternSpace("CS0", "DeviceRGB").SetStrokePattern("P0") }, "SetStrokeUncoloredPattern"},
		"stroke: components alone in an uncoloured space": {
			func(b *Builder) { b.SetStrokeUncoloredPatternSpace("CS0", "DeviceRGB").SetStrokeColor(1, 0, 0) }, "SetStrokeUncoloredPattern"},
		"stroke: after a device colour replaced it": {
			func(b *Builder) {
				b.SetStrokeUncoloredPatternSpace("CS0", "DeviceRGB").SetStrokeGray(0).SetStrokeUncoloredPattern("P0", 1, 0, 0)
			}, "uncoloured pattern space"},
		"Restore puts back the space before it": {
			func(b *Builder) {
				b.Save().SetUncoloredPatternSpace("CS0", "DeviceRGB").Restore().SetUncoloredPattern("P0", 1, 0, 0)
			}, "uncoloured pattern space"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var b Builder
			c.draw(&b)
			_, err := b.Bytes()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestUncoloredSpaceSurvivesSave pins the other half of q/Q: a space selected
// before a Save is still selected inside it.
func TestUncoloredSpaceSurvivesSave(t *testing.T) {
	var b Builder
	b.SetUncoloredPatternSpace("CS0", "DeviceRGB").SetStrokeUncoloredPatternSpace("CS0", "DeviceRGB").
		Save().SetUncoloredPattern("P0", 0, 0, 1).SetStrokeUncoloredPattern("P0", 0, 0, 1).Restore()
	mustBytes(t, &b)
}
