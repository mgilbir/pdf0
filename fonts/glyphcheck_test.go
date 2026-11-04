package fonts

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// brokenLetters is a font of every character WinAnsiEncoding names — so it
// can be embedded as a simple face too — with an OS/2 table whose fsType is
// fsType, whose glyph for b has an instruction length that runs past its
// record: the one fault forme's subsetter used to copy as it stood. The
// record is patched in place, so nothing else of the font moves.
func brokenLetters(t *testing.T, fsType uint16) (data []byte, broken int) {
	t.Helper()
	var gs []fonttest.Glyph
	seen := map[rune]bool{}
	for code := 32; code < 256; code++ {
		r, ok := winAnsiRune(code)
		if !ok || seen[r] {
			continue // WinAnsi names a few characters at two codes
		}
		seen[r] = true
		if r == 'b' {
			broken = len(gs) + 1 // glyph 0 is .notdef
		}
		gs = append(gs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: true})
	}
	os2 := make([]byte, 78) // version 0
	binary.BigEndian.PutUint16(os2[8:], fsType)
	data = fonttest.SFNT(fonttest.SFNTOptions{Name: "Broken", Glyphs: gs, Extra: map[string][]byte{"OS/2": os2}})
	tables := font.SFNTTables(data)
	head, loca, glyf := tables["head"], tables["loca"], tables["glyf"]
	var start, end int
	if binary.BigEndian.Uint16(head[50:]) == 1 {
		start, end = int(binary.BigEndian.Uint32(loca[4*broken:])), int(binary.BigEndian.Uint32(loca[4*broken+4:]))
	} else {
		start, end = 2*int(binary.BigEndian.Uint16(loca[2*broken:])), 2*int(binary.BigEndian.Uint16(loca[2*broken+2:]))
	}
	rec := glyf[start:end]
	if contours := int16(binary.BigEndian.Uint16(rec)); contours != 1 {
		t.Fatalf("the fixture's glyph has %d contours; the patch assumes one", contours)
	}
	// glyf is a slice of data: the instruction length after the one contour's
	// end point is written into the font itself.
	binary.BigEndian.PutUint16(rec[12:], 0xFFFF)
	return data, broken
}

func drawn(t *testing.T, f *Face, s string) {
	t.Helper()
	var b content.Builder
	b.BeginText().SetFont("F1", 12)
	f.DrawShaped(&b, s, 12)
	b.EndText()
	if err := b.Err(); err != nil {
		t.Fatal(err)
	}
}

type objects struct{ objs []object.Object }

func (c *objects) Add(o object.Object) object.IndirectRef {
	c.objs = append(c.objs, o)
	return object.IndirectRef{Number: len(c.objs)}
}

// TestCheckGlyphSaysWhatEmbedWillRefuse (#412): a face whose glyph b cannot
// be kept says so for b before anything is drawn, and nothing else; drawn
// without b it embeds, and drawn with it Embed is refused with a GlyphError
// naming b and only b — the composite and the simple form alike.
func TestCheckGlyphSaysWhatEmbedWillRefuse(t *testing.T) {
	data, broken := brokenLetters(t, 0)
	for _, kind := range []struct {
		name string
		load func([]byte) (*Face, error)
	}{{"composite", Load}, {"simple", LoadSimple}} {
		t.Run(kind.name, func(t *testing.T) {
			face, err := kind.load(data)
			if err != nil {
				t.Fatal(err)
			}
			// b as drawing hands it out: glyph 2, or the code 'b' for a
			// simple face, whose glyph is still 2 and is what forme names.
			b, _ := face.GlyphID('b')
			var ge *shape.GlyphError
			if err := face.CheckGlyph(b); !errors.As(err, &ge) || !slices.Equal(ge.GIDs, []int{broken}) {
				t.Errorf("CheckGlyph(%d): %v; want a GlyphError naming glyph %d", b, err, broken)
			}
			for _, r := range "acde" {
				g, _ := face.GlyphID(r)
				if err := face.CheckGlyph(g); err != nil {
					t.Errorf("CheckGlyph(%c): %v", r, err)
				}
			}

			without := face.Clone()
			drawn(t, without, "ace")
			if _, err := without.Embed(&objects{}); err != nil {
				t.Errorf("drawn without b, Embed: %v", err)
			}
			with := face.Clone()
			drawn(t, with, "abc")
			_, err = with.Embed(&objects{})
			if !errors.As(err, &ge) || !slices.Equal(ge.GIDs, []int{broken}) {
				t.Errorf("drawn with b, Embed: %v; want a GlyphError naming glyph %d", err, broken)
			}
		})
	}
}

// TestAFaceEmbeddedWholeCarriesEveryGlyph: a face whose licence forbids
// subsetting is embedded as the font file states it, the broken glyph with
// the rest, so CheckGlyph has nothing to refuse and Embed refuses nothing.
func TestAFaceEmbeddedWholeCarriesEveryGlyph(t *testing.T) {
	data, broken := brokenLetters(t, 0x0100) // no subsetting
	face, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := face.CheckGlyph(broken); err != nil {
		t.Errorf("CheckGlyph on a face embedded whole: %v", err)
	}
	drawn(t, face, "abc")
	c := &objects{}
	if _, err := face.Embed(c); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	found := false
	for _, o := range c.objs {
		if s, ok := o.(*object.Stream); ok && s.Dict.Get("Length1") != nil {
			found = found || bytes.Equal(inflate(t, s), face.Program())
		}
	}
	if !found {
		t.Error("the program embedded is not the font file whole")
	}
}

// TestEmbedFormsContextStopsWhereItIsAsked (#413): a context already done
// stops the embedding before anything is written; a limit on the bytes a
// subset may write fails it with ErrSubsetLimit; a face written as Type 3
// fonts asks the context before each glyph it paints; and with a live
// context and no limits the embedding is EmbedForms' own, object for object.
func TestEmbedFormsContextStopsWhereItIsAsked(t *testing.T) {
	forms := Forms{Horizontal: true}

	t.Run("done", func(t *testing.T) {
		face := mustFace(t)(NotoSans())
		drawn(t, face, "Hello")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := &objects{}
		if _, err := face.EmbedFormsContext(ctx, c, forms, SubsetLimits{}); !errors.Is(err, context.Canceled) {
			t.Errorf("EmbedFormsContext: %v; want context.Canceled", err)
		}
		if len(c.objs) != 0 {
			t.Errorf("a done context wrote %d objects", len(c.objs))
		}
	})

	t.Run("bytes", func(t *testing.T) {
		face := mustFace(t)(NotoSans())
		drawn(t, face, "Hello")
		_, err := face.EmbedFormsContext(context.Background(), &objects{}, forms, SubsetLimits{MaxBytes: 64})
		if !errors.Is(err, ErrSubsetLimit) {
			t.Errorf("EmbedFormsContext with 64 bytes: %v; want ErrSubsetLimit", err)
		}
	})

	t.Run("type3", func(t *testing.T) {
		// The context is done once it has been asked after the embedding
		// started: an embedding that asks it before each glyph stops at the
		// second, one that asks only at the start does not stop.
		face := strikesFace(t)
		drawn(t, face, "ABAB")
		ctx := &doneAfter{Context: context.Background(), n: 2}
		_, err := face.EmbedFormsContext(ctx, &objects{}, forms, SubsetLimits{})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("EmbedFormsContext: %v; want context.Canceled from the glyph loop", err)
		}
	})

	t.Run("same", func(t *testing.T) {
		for name, load := range map[string]func(*testing.T) *Face{
			"composite": func(t *testing.T) *Face { return mustFace(t)(NotoSans()) },
			"simple":    func(t *testing.T) *Face { return mustFace(t)(NotoSansSimple()) },
			"type3":     strikesFace,
		} {
			plain, bounded := load(t), load(t)
			drawn(t, plain, "AB")
			drawn(t, bounded, "AB")
			a, b := &objects{}, &objects{}
			if _, err := plain.EmbedForms(a, forms); err != nil {
				t.Fatal(err)
			}
			if _, err := bounded.EmbedFormsContext(context.Background(), b, forms, SubsetLimits{}); err != nil {
				t.Fatal(err)
			}
			if len(a.objs) != len(b.objs) {
				t.Fatalf("%s: %d objects against %d", name, len(a.objs), len(b.objs))
			}
			for i := range a.objs {
				if !object.Equal(a.objs[i], b.objs[i]) {
					t.Errorf("%s: object %d differs", name, i+1)
				}
			}
		}
	})
}

// doneAfter is a context that reports itself cancelled from its n-th Err on.
type doneAfter struct {
	context.Context
	n     int64
	calls atomic.Int64
}

func (c *doneAfter) Err() error {
	if c.calls.Add(1) >= c.n {
		return context.Canceled
	}
	return nil
}
