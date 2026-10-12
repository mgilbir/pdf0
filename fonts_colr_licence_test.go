package pdf0

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A colour face and a licence that permits embedding only bitmaps.
//
// OS/2 fsType bit 9 (0x0200), in the OpenType specification's words: "only
// bitmaps contained in the font may be embedded. No outline data may be
// embedded. If there are no bitmaps available in the font, then the font is
// considered unembeddable and the embedding services will fail." A COLR glyph
// is painted from glyph outlines — its layers, its clips — so drawing it in
// colour embeds outline data as surely as embedding the program does, only as
// Type 3 path procedures instead of a font file. A face drawn in colour used
// to be written so whatever the bit said.

// colourGlyphIDs is up to n glyphs of face that have a COLR painting.
func colourGlyphIDs(face *fonts.Face, n int) []int {
	var out []int
	for gid := 1; gid < face.NumGlyphs() && len(out) < n; gid++ {
		if k := face.GlyphColour(gid, 0); k == shape.ColourLayers || k == shape.ColourPaint {
			out = append(out, gid)
		}
	}
	return out
}

// pageOfGlyphs adds a page drawing gids in face, named F1, and returns the
// error AddPage reports.
func pageOfGlyphs(doc *Document, face *fonts.Face, gids []int) error {
	var b content.Builder
	b.SetRGB(0, 0, 0).BeginText().SetFont("F1", 40).MoveText(20, 20)
	glyphs := make([]fonts.Glyph, len(gids))
	text := make([]byte, len(gids))
	for i, gid := range gids {
		glyphs[i] = fonts.Glyph{GID: gid, Cluster: i, XAdvance: float64(face.GlyphAdvance(gid))}
		text[i] = 'a'
	}
	face.Draw(&b, string(text), glyphs, 40)
	b.EndText()
	_, err := doc.AddPage(Page{Width: 400, Height: 100, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}})
	return err
}

// procOperators are the operators a Type 3 glyph procedure painting a bitmap
// uses: the glyph's metrics, a transform and the image. Anything else — a path
// construction, a fill, a clip, a shading — draws outline data.
var procOperators = map[string]bool{
	"d0": true, "d1": true, "q": true, "Q": true, "cm": true, "Do": true,
	"BI": true, "ID": true, "EI": true, "gs": true,
}

// outlineOperators is every operator in doc's Type 3 glyph procedures that is
// not one a bitmap glyph needs.
func outlineOperators(t *testing.T, doc *Document) []string {
	t.Helper()
	var found []string
	procs := 0
	for _, o := range doc.Objects {
		fd, ok := o.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		if st, _ := doc.Resolve(fd.Get("Subtype")).(object.Name); st != "Type3" {
			continue
		}
		cps := doc.ResolveDict(fd.Get("CharProcs"))
		if cps == nil {
			continue
		}
		for k := range cps.Keys() {
			s, ok := doc.Resolve(cps.Get(k)).(*object.Stream)
			if !ok {
				continue
			}
			data, err := doc.StreamData(s)
			if err != nil {
				t.Fatalf("glyph procedure %s: %v", k, err)
			}
			procs++
			lx := core.NewContentLexer(core.Canceler{}, data)
			var tk core.ContentTok
			for lx.Next(&tk) {
				if tk.Kind == core.ContentOperator && !procOperators[string(tk.Raw)] {
					found = append(found, string(k)+": "+string(tk.Raw))
				}
			}
		}
	}
	if procs == 0 {
		t.Fatal("the document has no Type 3 glyph procedures to inspect")
	}
	return found
}

// TestAColourFaceHonoursABitmapOnlyLicence: a face with COLR glyphs whose
// licence permits embedding only bitmaps is held to the bit as a face with
// outlines alone is. With no bitmap strikes it is refused, in colour or not,
// at every level; with strikes its glyphs are their bitmaps, never their
// paintings, and a glyph with no bitmap is refused.
func TestAColourFaceHonoursABitmapOnlyLicence(t *testing.T) {
	t.Run("no strikes", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("testdata", "colrv1", "test_glyphs-glyf_colr_1.ttf"))
		if err != nil {
			t.Fatal(err)
		}
		probe, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(probe.Strikes()) != 0 {
			t.Fatal("the conformance font has strikes; the case needs a face without")
		}
		gids := colourGlyphIDs(probe, 4)
		if len(gids) == 0 {
			t.Fatal("the conformance font has no colour glyphs")
		}
		for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b} {
			for _, colour := range []bool{true, false} {
				face, err := fonts.Load(withOS2FSType(t, data, 0x0200))
				if err != nil {
					t.Fatal(err)
				}
				face.SetColourGlyphs(colour)
				doc := mustPDFADoc(t, level)
				err = pageOfGlyphs(doc, face, gids)
				if !errors.Is(err, fonts.ErrBitmapEmbeddingOnly) {
					t.Errorf("%s, colour %v: AddPage returned %v, want ErrBitmapEmbeddingOnly", level, colour, err)
					if err == nil {
						back, _ := writeAndRead(t, doc)
						if colour {
							t.Errorf("%s: outline data written: %v", level, outlineOperators(t, back))
						}
					}
				}
			}
		}
		// The same face under a licence that permits it is drawn in colour,
		// which is what makes the refusal the licence's doing.
		face, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := pageOfGlyphs(NewDocument(), face, gids); err != nil {
			t.Errorf("an unrestricted colour face: %v", err)
		}
	})

	t.Run("no subsetting", func(t *testing.T) {
		// A colour face's Type 3 fonts carry only the glyphs drawn, which a
		// licence forbidding subsetting does not allow; the face embedded
		// whole as its outlines is allowed.
		data, err := os.ReadFile(filepath.Join("testdata", "colrv1", "test_glyphs-glyf_colr_1.ttf"))
		if err != nil {
			t.Fatal(err)
		}
		for _, colour := range []bool{true, false} {
			face, err := fonts.Load(withOS2FSType(t, data, 0x0100))
			if err != nil {
				t.Fatal(err)
			}
			face.SetColourGlyphs(colour)
			err = pageOfGlyphs(NewDocument(), face, colourGlyphIDs(face, 2))
			switch {
			case colour && !errors.Is(err, fonts.ErrBitmapNoSubsetting):
				t.Errorf("in colour: %v, want ErrBitmapNoSubsetting", err)
			case !colour && err != nil:
				t.Errorf("as its outlines, whole: %v", err)
			}
		}
	})

	t.Run("strikes", func(t *testing.T) {
		data, emoji, space := emojiWithColour(t, 0x0200)
		face, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		if k := face.GlyphColour(emoji, 0); k != shape.ColourLayers {
			t.Fatalf("the built face's emoji is not a COLR glyph (%v)", k)
		}
		doc := NewDocument()
		if err := pageOfGlyphs(doc, face, []int{emoji}); err != nil {
			t.Fatalf("a colour glyph with a bitmap, under a bitmap-only licence: %v", err)
		}
		back, _ := writeAndRead(t, doc)
		if ops := outlineOperators(t, back); len(ops) > 0 {
			t.Errorf("the glyph procedures draw outline data, which the licence forbids embedding: %v", ops)
		}

		// The space is a box outline with no bitmap: only its outline could
		// draw it, and the licence forbids that.
		fresh, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		err = pageOfGlyphs(NewDocument(), fresh, []int{space})
		if !errors.Is(err, fonts.ErrBitmapEmbeddingOnly) {
			t.Errorf("a glyph that is only an outline, under a bitmap-only licence: %v, want ErrBitmapEmbeddingOnly", err)
		}

		// Unrestricted, the same face is drawn in colour, as its painting:
		// the case tells the two apart.
		open, _, _ := emojiWithColour(t, 0)
		free, err := fonts.Load(open)
		if err != nil {
			t.Fatal(err)
		}
		doc = NewDocument()
		if err := pageOfGlyphs(doc, free, []int{emoji}); err != nil {
			t.Fatal(err)
		}
		back, _ = writeAndRead(t, doc)
		if ops := outlineOperators(t, back); len(ops) == 0 {
			t.Error("the unrestricted face's emoji was not drawn as its COLR painting; the case cannot tell the paths apart")
		}
	})
}

// emojiWithColour is emojiWithOutlines with a COLRv0 table making one emoji a
// colour glyph whose one layer is the space's box outline, in red: a face with
// colour strikes, outlines and COLR glyphs at once, which no fixture to hand
// is. It returns the program, the emoji's glyph and the space's.
func emojiWithColour(t *testing.T, fsType uint16) (data []byte, emoji, space int) {
	t.Helper()
	base := emojiWithOutlines(t, fsType)
	f, err := fonts.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	if emoji, ok = f.GlyphID('😀'); !ok {
		t.Fatal("the emoji face has no U+1F600")
	}
	if space, ok = f.GlyphID(' '); !ok {
		t.Fatal("the emoji face has no space")
	}
	tables := font.SFNTTables(base)
	var colr []byte
	colr = binary.BigEndian.AppendUint16(colr, 0)  // version
	colr = binary.BigEndian.AppendUint16(colr, 1)  // base glyph records
	colr = binary.BigEndian.AppendUint32(colr, 14) // their offset
	colr = binary.BigEndian.AppendUint32(colr, 20) // the layer records' offset
	colr = binary.BigEndian.AppendUint16(colr, 1)  // layer records
	colr = binary.BigEndian.AppendUint16(colr, uint16(emoji))
	colr = binary.BigEndian.AppendUint16(colr, 0) // first layer
	colr = binary.BigEndian.AppendUint16(colr, 1) // layers
	colr = binary.BigEndian.AppendUint16(colr, uint16(space))
	colr = binary.BigEndian.AppendUint16(colr, 0) // palette entry
	var cpal []byte
	cpal = binary.BigEndian.AppendUint16(cpal, 0)  // version
	cpal = binary.BigEndian.AppendUint16(cpal, 1)  // entries per palette
	cpal = binary.BigEndian.AppendUint16(cpal, 1)  // palettes
	cpal = binary.BigEndian.AppendUint16(cpal, 1)  // colour records
	cpal = binary.BigEndian.AppendUint32(cpal, 14) // their offset
	cpal = binary.BigEndian.AppendUint16(cpal, 0)  // palette 0's first record
	cpal = append(cpal, 0, 0, 0xFF, 0xFF)          // BGRA: opaque red
	tables["COLR"], tables["CPAL"] = colr, cpal
	return assembleSFNT(tables), emoji, space
}
