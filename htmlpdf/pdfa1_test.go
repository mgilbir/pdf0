package htmlpdf

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"
)

// opaquePage draws most of what htmlpdf can without transparency: text in an
// embedded composite face, a link, an opaque gradient in a rounded box, an
// opaque image, a rotation, a list and a table.
func opaquePage(t *testing.T) Input {
	t.Helper()
	return Input{
		HTML: `<html lang="nl"><head><title>Factuur</title></head><body>
<h1>Title <a href="https://example.com/">link</a></h1><p>Text <b>bold</b></p>
<div style="width: 200px; height: 30px; border-radius: 6px; background: linear-gradient(90deg, red, blue)"></div>
<img src="solid.png" style="width: 40px">
<div style="transform: rotate(15deg); width: 60px; height: 20px; background: #09c"></div>
<ul><li>one</li></ul><table border="1"><tr><td>a</td></tr></table></body></html>`,
		Fonts:     notoSansSet(t),
		Resources: bytesResolver{"solid.png": solidPNG(t)},
	}
}

// TestAPDFA1bPageIsOpaque: an opaque page is written into a PDF/A-1b document,
// through Options.PDFA and through RenderInto, and passes pdf0's validator,
// Save, and veraPDF --flavour 1b when PDF0_VERAPDF names it.
func TestAPDFA1bPageIsOpaque(t *testing.T) {
	viaOption, err := Render(opaquePage(t), Options{Page: A4, PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA1b}})
	if err != nil {
		t.Fatalf("Render with PDFA 1b: %v", err)
	}
	viaInto := pdfaDoc(t, pdfa.PDFA1b)
	if _, err := RenderInto(viaInto, opaquePage(t), Options{Page: A4}); err != nil {
		t.Fatalf("RenderInto a PDF/A-1b document: %v", err)
	}
	dir := t.TempDir()
	var files []string
	for name, doc := range map[string]*pdf0.Document{"option": viaOption.Document, "into": viaInto} {
		back, _ := writtenBack(t, doc)
		if back.Version != "1.4" {
			t.Errorf("%s: PDF %s; PDF/A-1 is PDF 1.4", name, back.Version)
		}
		for _, v := range pdf0.ValidatePDFA(back, pdfa.PDFA1b) {
			t.Errorf("%s: %v", name, v)
		}
		var b bytes.Buffer
		if err := doc.Save(&b); err != nil {
			t.Errorf("%s: Save refused it: %v", name, err)
			continue
		}
		files = append(files, writeFlavour(t, dir+"/"+name, "1b", b.Bytes()))
	}
	veraPDFPasses(t, files)
}

// TestAPDFA1PageWithTransparencyIsRefused: an opacity, an image with alpha and
// a translucent gradient each refuse the page under RuleTransparencyAtPDFA1,
// at Error whatever the policy says, naming what the page does; and the
// document rendered into is left as it was. The same pages are written at
// PDF/A-2b, which allows transparency.
func TestAPDFA1PageWithTransparencyIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ html, names string }{
		"opacity":             {`<p style="opacity: .5">translucent</p>`, "/ca"},
		"an image with alpha": {`<img src="alpha.png" style="width: 40px">`, "SMask"},
		"a translucent stop":  {`<div style="width: 100px; height: 20px; background: linear-gradient(90deg, red, rgba(0,0,255,.3))"></div>`, "SMask"},
	} {
		page := func(policy layout.Policy) Input {
			return Input{HTML: tc.html, Fonts: notoSansSet(t), Resources: bytesResolver{"alpha.png": pngBytes(t)}, Policy: policy}
		}
		for _, policy := range []layout.Policy{nil, {RuleTransparencyAtPDFA1: layout.Ignore}} {
			_, err := Render(page(policy), Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA1b}})
			var refused *RefusedError
			if !errors.As(err, &refused) {
				t.Errorf("%s, policy %v: rendered at PDF/A-1b (%v)", name, policy, err)
				continue
			}
			found := false
			for _, f := range refused.Findings {
				if f.Rule == RuleTransparencyAtPDFA1 && f.Severity == layout.Error && strings.Contains(f.Message, tc.names) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: refused with %v; want %s naming %s", name, refused.Findings, RuleTransparencyAtPDFA1, tc.names)
			}
		}

		doc := pdfaDoc(t, pdfa.PDFA1b)
		before := bytesOf(t, doc)
		if _, err := RenderInto(doc, page(nil), Options{}); err == nil {
			t.Errorf("%s: RenderInto wrote it into a PDF/A-1b document", name)
		}
		if !bytes.Equal(before, bytesOf(t, doc)) {
			t.Errorf("%s: a refused page changed the document", name)
		}
		if _, err := Render(page(nil), Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}}); err != nil {
			t.Errorf("%s: refused at PDF/A-2b, which allows transparency: %v", name, err)
		}
	}
}

// solidPNG is a small opaque RGB PNG: an image with no alpha to mask.
func solidPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 120, B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
