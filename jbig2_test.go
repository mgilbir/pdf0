package pdf0

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"
)

// jbig2CCITTImage decodes the single JBIG2 image from a sample PDF and returns it.
func jbig2Image(t *testing.T, path string) ExtractedImage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", filepath.Base(path), err)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("%s: read: %v", filepath.Base(path), err)
	}
	for _, img := range doc.ExtractImages() {
		if img.Filter == "JBIG2Decode" {
			return img
		}
	}
	t.Fatalf("%s: no JBIG2 image", filepath.Base(path))
	return ExtractedImage{}
}

func grayPixels(t *testing.T, img ExtractedImage) *image.Gray {
	t.Helper()
	g, ok := img.Image.(*image.Gray)
	if !ok {
		t.Fatalf("image is %T, want *image.Gray", img.Image)
	}
	return g
}

// TestJBIG2GenericCrossCheck decodes every generic-region encoding of the shared
// test bitmap (arithmetic templates 0-3, custom AT, TPGDON typical prediction,
// MMR, striped pages, and an unknown-length segment) and asserts they all produce
// byte-identical pixels. Different coders agreeing on the same output is strong
// evidence the decoder is correct.
func TestJBIG2GenericCrossCheck(t *testing.T) {
	dir := "testdata/jbig2"
	generic := []string{
		"bitmap-template1.pdf",
		"bitmap-template2.pdf",
		"bitmap-template3.pdf",
		"bitmap-customat.pdf",
		"bitmap-tpgdon.pdf",
		"bitmap-template1-customat-tpgdon.pdf",
		"bitmap-mmr.pdf",
		// Striped pages (end-of-stripe segments) and a generic region written with
		// the unknown-length marker (7.2.7).
		"bitmap-stripe.pdf",
		"bitmap-stripe-single.pdf",
		"bitmap-stripe-last-implicit.pdf",
		"bitmap-stripe-initially-unknown-height.pdf",
		"bitmap-initially-unknown-size.pdf",
	}
	if _, err := os.Stat(filepath.Join(dir, generic[0])); err != nil {
		t.Skip("no JBIG2 sample PDFs; run `make jbig2`")
	}
	var want *image.Gray
	for _, name := range generic {
		img := jbig2Image(t, filepath.Join(dir, name))
		if !img.Decoded {
			t.Errorf("%s: not decoded: %s", name, img.Note)
			continue
		}
		if img.Width != 399 || img.Height != 400 {
			t.Errorf("%s: geometry %dx%d, want 399x400", name, img.Width, img.Height)
		}
		g := grayPixels(t, img)
		black, white := 0, 0
		for _, p := range g.Pix {
			if p == 0 {
				black++
			} else {
				white++
			}
		}
		if black == 0 || white == 0 {
			t.Errorf("%s: single-colour image (black=%d white=%d)", name, black, white)
		}
		if want == nil {
			want = g
			t.Logf("reference bitmap: %dx%d, black=%d white=%d", g.Rect.Dx(), g.Rect.Dy(), black, white)
			continue
		}
		if !bytes.Equal(g.Pix, want.Pix) {
			t.Errorf("%s: pixels differ from the reference generic-region decode", name)
		}
	}
}

// TestJBIG2SymbolText decodes the symbol-dictionary + text-region encodings of
// the shared bitmap — including every reference corner, transposition, and a
// negative S-offset — and asserts each reconstructs the same image as the
// generic-region reference. This exercises the integer arithmetic decoder, the
// symbol dictionary, and text-region symbol placement.
func TestJBIG2SymbolText(t *testing.T) {
	dir := "testdata/jbig2"
	ref := filepath.Join(dir, "bitmap-template1.pdf")
	if _, err := os.Stat(ref); err != nil {
		t.Skip("no JBIG2 sample PDFs; run `make jbig2`")
	}
	want := grayPixels(t, jbig2Image(t, ref)).Pix

	symbolFiles := []string{
		"bitmap-symbol.pdf",
		"bitmap-symbol-textbottomleft.pdf",
		"bitmap-symbol-textbottomright.pdf",
		"bitmap-symbol-texttopright.pdf",
		"bitmap-symbol-texttranspose.pdf",
		"bitmap-symbol-textbottomlefttranspose.pdf",
		"bitmap-symbol-texttoprighttranspose.pdf",
		"bitmap-symbol-negative-sbdsoffset.pdf",
	}
	for _, name := range symbolFiles {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		img := jbig2Image(t, path)
		if !img.Decoded {
			t.Errorf("%s: not decoded: %s", name, img.Note)
			continue
		}
		if !bytes.Equal(grayPixels(t, img).Pix, want) {
			t.Errorf("%s: pixels differ from the generic-region reference", name)
		}
	}
}

// TestJBIG2Refinement decodes the refinement encodings of the shared bitmap —
// standalone generic refinement regions (templates 0/1, TPGRON, custom AT, whole
// page) and symbol refinement (SBREFINE text regions and SDREFAGG symbol
// dictionaries, single-instance and multi-instance aggregate, arithmetic and
// Huffman) — and asserts each matches the generic-region reference.
func TestJBIG2Refinement(t *testing.T) {
	dir := "testdata/jbig2"
	ref := filepath.Join(dir, "bitmap-template1.pdf")
	if _, err := os.Stat(ref); err != nil {
		t.Skip("no JBIG2 sample PDFs; run `make jbig2`")
	}
	want := grayPixels(t, jbig2Image(t, ref)).Pix

	for _, name := range []string{
		"bitmap-refine.pdf",
		"bitmap-refine-template1.pdf",
		"bitmap-refine-tpgron.pdf",
		"bitmap-refine-customat.pdf",
		"bitmap-refine-page.pdf",
		"bitmap-symbol-refine.pdf",
		"bitmap-symbol-symbolrefineone.pdf",
		"bitmap-symbol-symbolrefineone-template1.pdf",
		"bitmap-symbol-symbolrefineone-customat.pdf",
		"bitmap-symbol-textrefine.pdf",
		"bitmap-symbol-textrefine-customat.pdf",
		"bitmap-symbol-textrefine-negative-delta-width.pdf",
		// Arithmetic multi-instance aggregation (SDREFAGG > 1) and combined
		// symbol+text refinement (6.5.8.2.1).
		"bitmap-symbol-symbolrefineseveral.pdf",
		"bitmap-symbol-symbolrefine-textrefine.pdf",
		// Huffman symbol refinement/aggregation (SDHUFF+SDREFAGG, 6.5.8.2).
		"bitmap-symbol-symhuffrefineone.pdf",
		"bitmap-symbol-symhuffrefineseveral.pdf",
		"bitmap-symbol-symhuffrefine-textrefine.pdf",
		// Huffman text-region refinement (SBHUFF+SBREFINE, 6.4.11): standard,
		// B.15 size, and custom RDW/RDH/RDX/RDY/RSIZE tables.
		"bitmap-symbol-texthuffrefine.pdf",
		"bitmap-symbol-texthuffrefineB15.pdf",
		"bitmap-symbol-texthuffrefinecustom.pdf",
		"bitmap-symbol-texthuffrefinecustomdims.pdf",
		"bitmap-symbol-texthuffrefinecustompos.pdf",
		"bitmap-symbol-texthuffrefinecustomposdims.pdf",
		"bitmap-symbol-texthuffrefinecustomsize.pdf",
		// Intermediate regions (types 36/40) referenced as the refinement's
		// reference bitmap, then composed with AND/XNOR/XOR/REPLACE (7.4.6/7.4.7).
		"bitmap-composite-and-xnor-refine.pdf",
		"bitmap-composite-or-xor-replace-refine.pdf",
		"bitmap-trailing-7fff-stripped-harder-refine.pdf",
	} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		img := jbig2Image(t, path)
		if !img.Decoded {
			t.Errorf("%s: not decoded: %s", name, img.Note)
			continue
		}
		if !bytes.Equal(grayPixels(t, img).Pix, want) {
			t.Errorf("%s: pixels differ from the generic-region reference", name)
		}
	}
}

// TestJBIG2Halftone decodes the halftone encodings of the shared bitmap —
// pattern dictionary + halftone region across templates, grid vectors, the skip
// optimisation, a 10-bit-per-pixel grid (arithmetic and MMR), compositing and
// refinement — and asserts each matches the generic-region reference.
func TestJBIG2Halftone(t *testing.T) {
	dir := "testdata/jbig2"
	ref := filepath.Join(dir, "bitmap-template1.pdf")
	if _, err := os.Stat(ref); err != nil {
		t.Skip("no JBIG2 sample PDFs; run `make jbig2`")
	}
	want := grayPixels(t, jbig2Image(t, ref)).Pix

	for _, name := range []string{
		"bitmap-halftone.pdf",
		"bitmap-halftone-template1.pdf",
		"bitmap-halftone-template2.pdf",
		"bitmap-halftone-template3.pdf",
		"bitmap-halftone-grid.pdf",
		"bitmap-halftone-skip-grid.pdf",
		"bitmap-halftone-10bpp.pdf",
		"bitmap-halftone-10bpp-mmr.pdf",
		"bitmap-halftone-composite.pdf",
		"bitmap-halftone-refine.pdf",
	} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		img := jbig2Image(t, path)
		if !img.Decoded {
			t.Errorf("%s: not decoded: %s", name, img.Note)
			continue
		}
		if !bytes.Equal(grayPixels(t, img).Pix, want) {
			t.Errorf("%s: pixels differ from the generic-region reference", name)
		}
	}
}

// TestJBIG2Huffman decodes the Huffman-coded symbol/text encodings of the shared
// bitmap — standard tables, alternate standard tables, custom table segments, and
// an uncompressed collective bitmap — and asserts each matches the arithmetic
// generic-region reference.
func TestJBIG2Huffman(t *testing.T) {
	dir := "testdata/jbig2"
	ref := filepath.Join(dir, "bitmap-template1.pdf")
	if _, err := os.Stat(ref); err != nil {
		t.Skip("no JBIG2 sample PDFs; run `make jbig2`")
	}
	want := grayPixels(t, jbig2Image(t, ref)).Pix

	for _, name := range []string{
		"bitmap-symbol-symhuff-texthuff.pdf",
		"bitmap-symbol-symhuff-texthuffB10B13.pdf",
		"bitmap-symbol-symhuffB5B3-texthuffB7B9B12.pdf",
		"bitmap-symbol-symhuffcustom-texthuffcustom.pdf",
		"bitmap-symbol-symhuffuncompressed-texthuff.pdf",
	} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		img := jbig2Image(t, path)
		if !img.Decoded {
			t.Errorf("%s: not decoded: %s", name, img.Note)
			continue
		}
		if !bytes.Equal(grayPixels(t, img).Pix, want) {
			t.Errorf("%s: pixels differ from the generic-region reference", name)
		}
	}
}

// TestJBIG2EdgeCases decodes the shared bitmap through structural edge cases —
// symbol-dictionary coding-context reuse/retention (SDUSEDCONTEXT/SDRETAINCONTEXT),
// empty symbol dictionaries, large referred-to segment numbers (2- and 4-byte
// referencing), and region compositing with every operator — and asserts each
// matches the generic-region reference.
func TestJBIG2EdgeCases(t *testing.T) {
	dir := "testdata/jbig2"
	ref := filepath.Join(dir, "bitmap-template1.pdf")
	if _, err := os.Stat(ref); err != nil {
		t.Skip("no JBIG2 sample PDFs; run `make jbig2`")
	}
	want := grayPixels(t, jbig2Image(t, ref)).Pix

	for _, name := range []string{
		"bitmap-symbol-context-reuse.pdf",
		"bitmap-symbol-empty.pdf",
		"bitmap-symbol-big-segmentid.pdf",
		"bitmap-composite-and-xnor.pdf",
		"bitmap-composite-or-xor-replace.pdf",
	} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		img := jbig2Image(t, path)
		if !img.Decoded {
			t.Errorf("%s: not decoded: %s", name, img.Note)
			continue
		}
		if !bytes.Equal(grayPixels(t, img).Pix, want) {
			t.Errorf("%s: pixels differ from the generic-region reference", name)
		}
	}
}

// TestJBIG2Malformed rejects garbage without panicking.
func TestJBIG2Malformed(t *testing.T) {
	if _, err := decodeJBIG2(nil, []byte{0, 0, 0, 0, 0x30, 0x00, 0x01}, 8, 8); err == nil {
		t.Error("expected an error on malformed JBIG2 data")
	}
}
