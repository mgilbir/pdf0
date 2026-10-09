package pdf0

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestType3DocumentsAreValidForVeraPDF holds the documents pdf0's Type 3
// fonts produce — EBDT, bdat and CBDT bitmap faces at every PDF/A level, their
// opaque PDF/A-1 forms, fonts licensed for their bitmaps only, COLR colour
// glyphs with their soft masks, blend modes and calculator functions, and
// their PDF/A-1 outlines — to veraPDF, the PDF/A reference validator, as
// well as to pdf0's own. pdf0's validator and writer are one codebase and
// could share a misreading; veraPDF is not.
//
// PDF0_VERAPDF is the veraPDF command, run with --format text and the files;
// testdata/verapdf/verapdf-docker.sh runs the official image. The test skips
// without it.
func TestType3DocumentsAreValidForVeraPDF(t *testing.T) {
	vera := os.Getenv("PDF0_VERAPDF")
	if vera == "" {
		t.Skip("PDF0_VERAPDF is not set: no veraPDF to validate with (testdata/verapdf/verapdf-docker.sh)")
	}
	out := t.TempDir()
	var written []string
	save := func(name string, doc *Document) {
		_, data := writeAndRead(t, doc)
		if err := os.WriteFile(filepath.Join(out, name+".pdf"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		back, _ := writeAndRead(t, doc)
		level := map[string]pdfa.Level{"1b": pdfa.PDFA1b, "2b": pdfa.PDFA2b, "3b": pdfa.PDFA3b, "4": pdfa.PDFA4}[name[strings.LastIndex(name, "-")+1:]]
		for _, v := range ValidatePDFA(back, level) {
			t.Errorf("%s: pdf0: %s", name, v.Error())
		}
		written = append(written, filepath.Join(out, name+".pdf"))
	}
	levels := map[string]pdfa.Level{"2b": pdfa.PDFA2b, "3b": pdfa.PDFA3b, "4": pdfa.PDFA4}
	red := func(b *content.Builder) { b.SetRGB(1, 0, 0) }
	for name, level := range levels {
		// EBDT: 1-bit and 2-bit strikes, in colour and not, over two pages.
		face := bitmapFace(t, "Strikes.ttf")
		doc := mustPDFADoc(t, level)
		bitmapPage(t, doc, face, bitmapCases())
		bitmapPage(t, doc, face, []placedGlyph{{r: 'C', size: 12, x: 20, y: 80, colour: red, rgb: []float64{255, 0, 0}}, {r: 'D', size: 24, x: 60, y: 80}})
		save("ebdt-"+name, doc)
		// bdat
		apple := bitmapFace(t, "StrikesApple.ttf")
		doc = mustPDFADoc(t, level)
		bitmapPage(t, doc, apple, bitmapCases())
		save("bdat-"+name, doc)
		// CBDT emoji
		emoji := emojiFace(t)
		doc = mustPDFADoc(t, level)
		emojiPage(t, doc, emoji, "😀👍🏽🇪🇸🎉")
		save("cbdt-"+name, doc)
	}
	// PDF/A-1b: the opaque forms.
	doc := mustPDFADoc(t, pdfa.PDFA1b)
	bitmapPage(t, doc, bitmapFace(t, "Strikes.ttf"), []placedGlyph{
		{r: 'A', size: 9, x: 20, y: 80, colour: red, rgb: []float64{255, 0, 0}},
		{r: 'B', size: 12, x: 60, y: 80, colour: red, rgb: []float64{255, 0, 0}},
	})
	save("ebdt-opaque-1b", doc)
	doc = mustPDFADoc(t, pdfa.PDFA1b)
	emojiPage(t, doc, emojiFace(t), "😀🇪🇸")
	save("cbdt-opaque-1b", doc)
	// Fonts licensed for their bitmaps only.
	lic, err := fonts.Load(emojiWithOutlines(t, 0x0200))
	if err != nil {
		t.Fatal(err)
	}
	doc = mustPDFADoc(t, pdfa.PDFA2b)
	emojiPage(t, doc, lic, "😀🎉")
	save("licensed-cbdt-2b", doc)
	ebdtLic, err := fonts.Load(strikesWithOutlines(t, 0x0200))
	if err != nil {
		t.Fatal(err)
	}
	doc = mustPDFADoc(t, pdfa.PDFA2b)
	bitmapPage(t, doc, ebdtLic, bitmapCases())
	save("licensed-ebdt-2b", doc)
	// COLR: every conformance glyph pdf0 writes, and Noto's COLRv1 emoji.
	raw, err := os.ReadFile(filepath.Join("testdata", "colrv1", "test_glyphs-glyf_colr_1.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	conf := everyGlyphReachable(t, raw)
	notoData, err := os.ReadFile(testfiles.NotoEmoji.File(t, "Noto-COLRv1.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	for name, level := range map[string]pdfa.Level{"2b": pdfa.PDFA2b, "3b": pdfa.PDFA3b, "4": pdfa.PDFA4} {
		face, _ := fonts.Load(conf)
		var text strings.Builder
		for g := 1; g < face.NumGlyphs(); g++ {
			refuse := false
			for m := range summarise(t, face, g).modes {
				refuse = refuse || inexact[m]
			}
			if !refuse {
				text.WriteRune(rune(0xF0000 + g))
			}
		}
		noto, _ := fonts.Load(notoData)
		doc := mustPDFADoc(t, level)
		var b content.Builder
		b.SetRGB(0, 0, 0)
		b.BeginText().SetFont("F1", 20).SetTextMatrix(1, 0, 0, 1, 10, 150)
		face.DrawShaped(&b, text.String(), 20)
		b.EndText()
		b.BeginText().SetFont("F2", 20).SetTextMatrix(1, 0, 0, 1, 10, 10)
		noto.DrawShaped(&b, "😀🎉👍🏽🇪🇸🌈🔥🇬🇧🇯🇵", 20)
		b.EndText()
		if _, err := doc.AddPage(Page{Width: 600, Height: 200, Content: &b,
			Faces: map[object.Name]*fonts.Face{"F1": face, "F2": noto}}); err != nil {
			t.Fatal(err)
		}
		save("colr-"+name, doc)
	}
	// COLR in PDF/A-1b: outlines in the text colour.
	paint := everyGlyphReachable(t, formeFile(t, "testdata/harfbuzz/fonts/ColourPaint.ttf"))
	pface, _ := fonts.Load(paint)
	doc = mustPDFADoc(t, pdfa.PDFA1b)
	var b content.Builder
	b.SetRGB(1, 0, 0)
	b.BeginText().SetFont("F1", 40).SetTextMatrix(1, 0, 0, 1, 10, 10)
	pface.DrawShaped(&b, string(rune(0xF0000+4))+string(rune(0xF0000+6)), 40)
	b.EndText()
	if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b, Faces: map[object.Name]*fonts.Face{"F1": pface}}); err != nil {
		t.Fatal(err)
	}
	save("colr-outline-1b", doc)

	veraPDFPasses(t, vera, written)
	if len(written) < 17 {
		t.Errorf("only %d documents were written", len(written))
	}
}

// veraPDFPasses runs veraPDF (PDF0_VERAPDF) over documents, each at the
// level it claims, and fails the test for each it does not pass.
func veraPDFPasses(t *testing.T, vera string, paths []string) {
	t.Helper()
	cmd := exec.Command(vera, append([]string{"--format", "text"}, paths...)...)
	report, err := cmd.Output()
	if err != nil && len(report) == 0 {
		t.Fatalf("veraPDF: %v", err)
	}
	verdicts := map[string]string{}
	for _, line := range strings.Split(string(report), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && (f[0] == "PASS" || f[0] == "FAIL") {
			verdicts[filepath.Base(f[1])] = f[0] + " " + strings.Join(f[2:], " ")
		}
	}
	for _, path := range paths {
		name := filepath.Base(path)
		v, ok := verdicts[name]
		switch {
		case !ok:
			t.Errorf("%s: veraPDF gave no verdict", name)
		case !strings.HasPrefix(v, "PASS"):
			t.Errorf("%s: veraPDF: %s", name, v)
		}
	}
}

func emojiPage(t *testing.T, doc *Document, face *fonts.Face, text string) {
	t.Helper()
	var b content.Builder
	b.BeginText().SetFont("F1", 24).SetTextMatrix(1, 0, 0, 1, 20, 60)
	face.DrawShaped(&b, text, 24)
	b.EndText()
	if _, err := doc.AddPage(Page{Width: 300, Height: 120, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		t.Fatal(err)
	}
}
