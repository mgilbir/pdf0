package pdf0

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Tiling patterns.

// cell draws one tile: a filled square, which is enough to be a pattern and
// small enough that the stream can be read in an assertion.
func cell() *content.Builder {
	var b content.Builder
	b.SetRGB(1, 0, 0).Rect(0, 0, 5, 5).Fill()
	return &b
}

// shapeOnly draws a tile that states no colour, which is what an uncoloured
// pattern requires.
func shapeOnly() *content.Builder {
	var b content.Builder
	b.Rect(0, 0, 5, 5).Fill()
	return &b
}

func patternDict(t *testing.T, d *Document, ref object.IndirectRef) *object.Stream {
	t.Helper()
	s, ok := d.Resolve(ref).(*object.Stream)
	if !ok {
		t.Fatal("the pattern is not a stream")
	}
	return s
}

// TestTilingPatternHasTheKeysAReaderNeeds pins the dictionary. A tiling pattern
// is a stream whose dictionary a reader consults before it draws anything, and
// a missing step or paint type is not a degraded tiling but no tiling.
func TestTilingPatternHasTheKeysAReaderNeeds(t *testing.T) {
	doc := NewDocument()
	ref, err := doc.AddTilingPattern(TilingPattern{BBox: [4]float64{0, 0, 10, 20}, Content: cell()})
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	s := patternDict(t, doc, ref)
	for key, want := range map[object.Name]object.Object{
		"Type":        object.Name("Pattern"),
		"PatternType": object.Integer(1),
		"PaintType":   object.Integer(1),
		"TilingType":  object.Integer(1),
		"XStep":       object.Integer(10),
		"YStep":       object.Integer(20),
	} {
		if got := s.Dict.Get(key); got != want {
			t.Errorf("/%s = %v, want %v", key, got, want)
		}
	}
	if s.Dict.Get("Resources") == nil {
		t.Error("the pattern has no /Resources; a reader needs one even when it is empty")
	}
}

// TestStepDefaultsToTheCellSize pins the default that makes tiles abut, and
// that an explicit step is not overridden by it. A step and a box that disagree
// is how a gap, an overlap or a brick offset is expressed, so deriving one from
// the other would remove the only way to say any of that.
func TestStepDefaultsToTheCellSize(t *testing.T) {
	doc := NewDocument()

	abut, err := doc.AddTilingPattern(TilingPattern{BBox: [4]float64{0, 0, 8, 4}, Content: cell()})
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	s := patternDict(t, doc, abut)
	if s.Dict.Get("XStep") != object.Integer(8) || s.Dict.Get("YStep") != object.Integer(4) {
		t.Errorf("step = (%v,%v), want the cell size (8,4)", s.Dict.Get("XStep"), s.Dict.Get("YStep"))
	}

	spaced, err := doc.AddTilingPattern(TilingPattern{
		BBox: [4]float64{0, 0, 8, 4}, XStep: 12, YStep: 6, Content: cell(),
	})
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	s = patternDict(t, doc, spaced)
	if s.Dict.Get("XStep") != object.Integer(12) || s.Dict.Get("YStep") != object.Integer(6) {
		t.Errorf("step = (%v,%v), want the stated (12,6)", s.Dict.Get("XStep"), s.Dict.Get("YStep"))
	}
}

// TestUncoloredPatternRefusesToSetItsOwnColour is the guard that matters most
// here.
//
// An uncoloured pattern takes its colour from where it is painted; ISO 32000-2
// 8.7.3.1 leaves the result *undefined* if its cell sets one. Undefined means
// each reader chooses, so the file looks different in different viewers — a
// fault that is never traced back to the pattern. Refusing to write it is the
// only place the mistake can still be attributed.
func TestUncoloredPatternRefusesToSetItsOwnColour(t *testing.T) {
	doc := NewDocument()
	_, err := doc.AddTilingPattern(TilingPattern{
		BBox: [4]float64{0, 0, 5, 5}, Uncolored: true, Content: cell(),
	})
	if err == nil {
		t.Fatal("an uncoloured pattern whose cell sets a colour was accepted")
	}
	if !strings.Contains(err.Error(), "colour") {
		t.Errorf("the error is %q; it should say what is wrong with the cell", err)
	}

	// The same pattern without colour is fine, and is PaintType 2.
	ref, err := doc.AddTilingPattern(TilingPattern{
		BBox: [4]float64{0, 0, 5, 5}, Uncolored: true, Content: shapeOnly(),
	})
	if err != nil {
		t.Fatalf("a shape-only cell was refused: %v", err)
	}
	if got := patternDict(t, doc, ref).Dict.Get("PaintType"); got != object.Integer(2) {
		t.Errorf("/PaintType = %v, want 2", got)
	}
}

// TestEveryColourOperatorCountsAsSettingColour pins that the check is on the
// operators and not on one convenience method. A cell that reaches a colour
// through a colour space, a pattern or a stroke is just as undefined.
func TestEveryColourOperatorCountsAsSettingColour(t *testing.T) {
	cases := map[string]func(*content.Builder){
		"SetGray":             func(b *content.Builder) { b.SetGray(0.5) },
		"SetStrokeGray":       func(b *content.Builder) { b.SetStrokeGray(0.5) },
		"SetRGB":              func(b *content.Builder) { b.SetRGB(1, 0, 0) },
		"SetStrokeRGB":        func(b *content.Builder) { b.SetStrokeRGB(1, 0, 0) },
		"SetCMYK":             func(b *content.Builder) { b.SetCMYK(0, 0, 0, 1) },
		"SetStrokeCMYK":       func(b *content.Builder) { b.SetStrokeCMYK(0, 0, 0, 1) },
		"SetColorSpace":       func(b *content.Builder) { b.SetColorSpace("DeviceRGB") },
		"SetStrokeColorSpace": func(b *content.Builder) { b.SetStrokeColorSpace("DeviceRGB") },
		"SetColor":            func(b *content.Builder) { b.SetColor(0.5) },
		"SetStrokeColor":      func(b *content.Builder) { b.SetStrokeColor(0.5) },
	}
	for name, set := range cases {
		var b content.Builder
		set(&b)
		if !b.SetsColor() {
			t.Errorf("%s did not count as setting a colour", name)
		}
	}
	// And a drawing that sets none says so.
	var plain content.Builder
	plain.Rect(0, 0, 1, 1).Fill()
	if plain.SetsColor() {
		t.Error("a drawing that sets no colour reported that it did")
	}
}

// TestTilingPatternRefusesWhatCannotTile collects the geometry a reader cannot
// act on. A zero step is the dangerous one: a reader asked to fill an area with
// tiles that never advance does not finish.
func TestTilingPatternRefusesWhatCannotTile(t *testing.T) {
	doc := NewDocument()
	cases := map[string]TilingPattern{
		"no content":         {BBox: [4]float64{0, 0, 5, 5}},
		"empty cell":         {BBox: [4]float64{0, 0, 0, 5}, Content: cell()},
		"inverted cell":      {BBox: [4]float64{5, 0, 0, 5}, Content: cell()},
		"non-finite cell":    {BBox: [4]float64{0, 0, inf(), 5}, Content: cell()},
		"non-finite x step":  {BBox: [4]float64{0, 0, 5, 5}, XStep: inf(), Content: cell()},
		"non-finite y step":  {BBox: [4]float64{0, 0, 5, 5}, YStep: nan(), Content: cell()},
		"non-finite matrix":  {BBox: [4]float64{0, 0, 5, 5}, Matrix: &[6]float64{1, 0, 0, 1, inf(), 0}, Content: cell()},
		"unknown spacing":    {BBox: [4]float64{0, 0, 5, 5}, Spacing: 99, Content: cell()},
		"undefined resource": {BBox: [4]float64{0, 0, 5, 5}, Content: usesUndefinedName()},
	}
	for name, p := range cases {
		if _, err := doc.AddTilingPattern(p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A step of zero cannot be expressed through the struct — it means "use the
// cell size" — so the guard underneath it is checked directly, on a cell whose
// size is itself zero in one axis. That is refused earlier, which is why the
// step check needs its own test.
func TestZeroStepIsRefused(t *testing.T) {
	if err := checkStep("XStep", 0); err == nil {
		t.Error("a zero step was accepted; a reader filling an area with it would not terminate")
	}
	if err := checkStep("XStep", -4); err != nil {
		t.Errorf("a negative step was refused: %v — tiling in the other direction is legitimate", err)
	}
}

func usesUndefinedName() *content.Builder {
	var b content.Builder
	b.Save().Draw("Im0").Restore()
	return &b
}

func inf() float64 { return math.Inf(1) }
func nan() float64 { return math.NaN() }

// TestPatternPaintsAndValidates is the end-to-end claim: a page that fills with
// a pattern is a file, and one a conformance checker accepts.
func TestPatternPaintsAndValidates(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			patRef, err := doc.AddTilingPattern(TilingPattern{
				BBox: [4]float64{0, 0, 10, 10}, Content: cell(),
			})
			if err != nil {
				t.Fatalf("adding the pattern: %v", err)
			}

			var b content.Builder
			b.Save().SetColorSpace("Pattern").SetPattern("P0").Rect(50, 50, 200, 100).Fill().Restore()
			_, err = doc.AddPage(Page{
				Width: 300, Height: 200, Content: &b,
				Patterns: map[object.Name]object.Object{"P0": patRef},
			})
			if err != nil {
				t.Fatalf("adding the page: %v", err)
			}

			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if v := ValidatePDFABytes(rd, level, buf.Bytes()); len(v) != 0 {
				t.Errorf("a page filled with a pattern is not %s: %v", level, v)
			}

			// The cell's drawing survived the trip.
			var found bool
			for _, iobj := range rd.Objects {
				s, ok := iobj.Value.(*object.Stream)
				if !ok || s.Dict.Get("PatternType") == nil {
					continue
				}
				data, err := rd.StreamData(s)
				if err != nil {
					t.Fatalf("reading the pattern's cell: %v", err)
				}
				if !bytes.Contains(data, []byte("re")) || !bytes.Contains(data, []byte("rg")) {
					t.Errorf("the cell's drawing did not survive: %q", data)
				}
				found = true
			}
			if !found {
				t.Error("no pattern survived into the written file")
			}
		})
	}
}

// TestUncoloredPatternSpace pins the colour space an uncoloured pattern has to
// be painted through. Selecting one with plain /Pattern leaves the colour
// components meaningless, which paints an unpredictable colour rather than
// failing.
func TestUncoloredPatternSpace(t *testing.T) {
	space := UncoloredPatternSpace("DeviceRGB")
	if len(space) != 2 || space[0] != object.Name("Pattern") || space[1] != object.Name("DeviceRGB") {
		t.Errorf("got %v, want [/Pattern /DeviceRGB]", space)
	}
}
