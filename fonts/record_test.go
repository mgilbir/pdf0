package fonts

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// drawByRecord writes a run as a caller with its own content stream would:
// Record for the stretches, AppendCode for the bytes, NominalAdvance for the
// pen, with nothing of this package's emitter. It is written against the
// documentation of the three, not copied from drawPlanned.
func drawByRecord(t *testing.T, b *content.Builder, f *Face, text string, glyphs []Glyph, size float64) {
	t.Helper()
	segs, err := f.Record(text, glyphs)
	if err != nil {
		t.Fatal(err)
	}
	var run []byte
	rise := 0.0
	flush := func() {
		if len(run) > 0 {
			b.ShowText(run)
			run = nil
		}
	}
	move := func(d float64) {
		if d != 0 {
			flush()
			b.ShowTextAdjusted(content.TextSpan{Adjust: -d})
		}
	}
	next := 0
	for _, s := range segs {
		if s.Start != next || s.End < s.Start || s.End > len(glyphs) {
			t.Fatalf("segments %+v do not cover the %d glyphs in order", segs, len(glyphs))
		}
		next = s.End
		if s.Marked {
			flush()
			b.BeginActualText(s.ActualText)
		}
		for _, g := range glyphs[s.Start:s.End] {
			if g.YOffset != rise {
				flush()
				b.SetRise(g.YOffset * size / 1000)
				rise = g.YOffset
			}
			move(g.XOffset)
			if run, err = f.AppendCode(run, g.GID); err != nil {
				t.Fatal(err)
			}
			move(g.XAdvance - f.NominalAdvance(g) - g.XOffset)
		}
		if s.Marked {
			flush()
			b.EndMarked()
		}
	}
	if next != len(glyphs) {
		t.Fatalf("segments %+v cover %d of %d glyphs", segs, next, len(glyphs))
	}
	flush()
	if rise != 0 {
		b.SetRise(0)
	}
}

// recordFaces are faces of every kind Record accepts, with text that takes
// each of plan's paths: plain letters, a ligature, a mark over its base, a
// right-to-left run, a conjunct, a radical drawn for its ideograph.
func recordFaces(t *testing.T) map[string]struct {
	face  *Face
	texts []string
} {
	t.Helper()
	out := map[string]struct {
		face  *Face
		texts []string
	}{}
	corpus := shapingCorpus(t)
	for name, f := range everyFaceKind(t) {
		texts := append([]string{"office", "Café", "a\u0301b", "Hello, world"}, corpus...)
		if name == "cid-keyed" {
			texts = append(texts, "日本語", "⽇日", "ｱｲｳ")
		}
		out[name] = struct {
			face  *Face
			texts []string
		}{f, texts}
	}
	out["ligature"] = struct {
		face  *Face
		texts []string
	}{shapingFace(t), []string{"fi", "AVfi", "fifi", "if"}}
	out["hebrew"] = struct {
		face  *Face
		texts []string
	}{hebrewFace(t), []string{"\u05D0\u05D1\u05D2", "ab \u05D0\u05D1 (12)"}}
	return out
}

// TestRecordingAndDrawingAreOneImplementation draws the same runs, in the same
// order, into two clones of each face: one through Draw, one through Record,
// AppendCode and NominalAdvance (drawByRecord). The content streams must be
// byte for byte the same, and so must the fonts Embed writes from each clone
// — the ToUnicode CMap above all, which is what Record exists to fill.
func TestRecordingAndDrawingAreOneImplementation(t *testing.T) {
	for name, c := range recordFaces(t) {
		t.Run(name, func(t *testing.T) {
			drawn, recorded := c.face.Clone(), c.face.Clone()
			var bd, br content.Builder
			bd.BeginText().SetFont("F1", 12)
			br.BeginText().SetFont("F1", 12)
			for _, s := range c.texts {
				glyphs, _ := drawn.ShapeGlyphs(s)
				drawn.Draw(&bd, s, glyphs, 12)
				again, _ := recorded.ShapeGlyphs(s)
				drawByRecord(t, &br, recorded, s, again, 12)
			}
			bd.EndText()
			br.EndText()
			d, err := bd.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			r, err := br.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(d, r) {
				t.Errorf("the streams differ:\nDraw:   %q\nRecord: %q", d, r)
			}
			ed, er := &recordCollector{}, &recordCollector{}
			if _, err := drawn.Embed(ed); err != nil {
				t.Fatal(err)
			}
			if _, err := recorded.Embed(er); err != nil {
				t.Fatal(err)
			}
			if len(ed.objs) != len(er.objs) {
				t.Fatalf("Embed wrote %d objects after Draw, %d after Record", len(ed.objs), len(er.objs))
			}
			for i := range ed.objs {
				if !object.Equal(ed.objs[i], er.objs[i]) {
					t.Errorf("object %d of the embedding differs after Record:\nDraw:   %v\nRecord: %v", i+1, ed.objs[i], er.objs[i])
				}
			}
		})
	}
}

// TestRecordSaysWhatUseCannot is the issue Record answers (#415): a face whose
// glyphs a caller only marked as used, through forme's Use, embeds a CMap that
// knows each glyph by the character the font's cmap names it by, and a
// ligature by nothing; recorded, the ligature says the letters it was drawn
// for.
func TestRecordSaysWhatUseCannot(t *testing.T) {
	marked, recorded := shapingFace(t), shapingFace(t)
	// Shaped through forme, as a caller drawing its own stream shapes:
	// shaping marks what it returns as used.
	glyphs, _ := marked.ShapeGlyphs("fi")
	if len(glyphs) != 1 {
		t.Fatalf("the fixture is wrong: \"fi\" shaped to %d glyphs, want the ligature", len(glyphs))
	}
	marked.Use(glyphs[0].GID)
	again, _ := recorded.ShapeGlyphs("fi")
	if _, err := recorded.Record("fi", again); err != nil {
		t.Fatal(err)
	}
	cmapOf := func(f *Face) string {
		c := &recordCollector{}
		if _, err := f.Embed(c); err != nil {
			t.Fatal(err)
		}
		for _, o := range c.objs {
			if s, ok := o.(*object.Stream); ok {
				if data := inflate(t, s); bytes.Contains(data, []byte("beginbfchar")) {
					return string(data)
				}
			}
		}
		return ""
	}
	lig := []byte{byte(glyphs[0].GID >> 8), byte(glyphs[0].GID)}
	entry := "<" + hexOf(lig) + "> <00660069>" // f i
	if got := cmapOf(marked); bytes.Contains([]byte(got), []byte(entry)) {
		t.Fatalf("the fixture is wrong: Use alone already maps the ligature: %s", got)
	}
	if got := cmapOf(recorded); !bytes.Contains([]byte(got), []byte(entry)) {
		t.Errorf("recorded, the ligature's ToUnicode entry is not %s:\n%s", entry, got)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

// TestRecordRefusesAType3Face: a bitmap face's codes are given out as it
// draws, by size and colour, which only Draw knows.
func TestRecordRefusesAType3Face(t *testing.T) {
	f := strikesFace(t)
	glyphs, _ := f.ShapeGlyphs("A")
	used := len(f.Used())
	if _, err := f.Record("A", glyphs); !errors.Is(err, ErrDrawnAsType3) {
		t.Errorf("Record: %v; want ErrDrawnAsType3", err)
	}
	if _, err := f.AppendCode(nil, glyphs[0].GID); !errors.Is(err, ErrDrawnAsType3) {
		t.Errorf("AppendCode: %v; want ErrDrawnAsType3", err)
	}
	if len(f.Used()) != used {
		t.Errorf("a refused Record marked glyphs used: %v", f.Used())
	}
}

// recordCollector is an Allocator that keeps every object added.
type recordCollector struct{ objs []object.Object }

func (c *recordCollector) Add(o object.Object) object.IndirectRef {
	c.objs = append(c.objs, o)
	return object.IndirectRef{Number: len(c.objs)}
}
