package pdf0_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/htmlpdf"
)

// everyFamily is a font set answering every family with one face.
type everyFamily struct{ face *shape.Face }

func (s everyFamily) Face(string, bool, bool) (*shape.Face, bool) { return s.face, true }

type pictures map[string][]byte

func (p pictures) Resolve(ref string) ([]byte, error) { return p[ref], nil }

// TestAnHTMLPageFitsTheVersionOfTheDocumentItIsAddedTo holds what htmlpdf
// writes to the PDF version of the document RenderInto adds it to, by the
// Arlington model's grammar of that version: every key and object on the page
// is one the version has. It is the check behind RenderInto's raising a
// document older than 1.4 to 1.4 and leaving every later one alone.
//
// The page draws what htmlpdf can: embedded text in a composite font, a link,
// opacity, a rounded clip, linear and radial gradients, a picture with an
// alpha channel, a tiled background, a rotation, a text shadow, a list and a
// table. The oracle's teeth are the last check: the same page has keys PDF 1.3
// lacks (a transparency group, /ca, /SMask), and the 1.3 grammar has to say so.
func TestAnHTMLPageFitsTheVersionOfTheDocumentItIsAddedTo(t *testing.T) {
	face, err := notosans.Face()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: 200, G: 30, B: 30, A: uint8(60 + 24*x)})
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	html := `<h1 style="text-shadow: 1px 1px 0 #888">Title <a href="https://example.com/x">link</a></h1>
<p style="opacity: .5">translucent text</p>
<div style="width: 200px; height: 40px; border-radius: 12px; background: linear-gradient(90deg, red, rgba(0,0,255,.4))"></div>
<div style="width: 100px; height: 100px; border-radius: 50%; background: radial-gradient(circle, yellow, green)"></div>
<div style="width: 120px; height: 40px; background: url(a.png); background-size: 10px 10px"></div>
<img src="a.png" style="width: 60px">
<div style="transform: rotate(20deg); width: 80px; height: 20px; background: #09c">r</div>
<ul><li>one</li><li>two</li></ul><table border="1"><tr><td>a</td><td>b</td></tr></table>`
	page := func(version string) []byte {
		t.Helper()
		doc := pdf0.NewDocument()
		doc.Version = version
		in := htmlpdf.Input{HTML: html, Fonts: everyFamily{face}, Resources: pictures{"a.png": pngData.Bytes()}}
		if _, err := htmlpdf.RenderInto(doc, in, htmlpdf.Options{Page: htmlpdf.A4, EmbedAllFonts: true}); err != nil {
			t.Fatal(err)
		}
		if doc.Version != version {
			t.Fatalf("a %s document is %s after the page", version, doc.Version)
		}
		var b bytes.Buffer
		if err := doc.Write(&b); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	for _, version := range []string{"1.4", "1.7", "2.0"} {
		for _, f := range pdf0.ArlingtonVersionFindings(t, page(version), version) {
			t.Errorf("PDF %s: %s", version, f)
		}
	}

	var transparency bool
	for _, f := range pdf0.ArlingtonVersionFindings(t, page("1.4"), "1.3") {
		if strings.Contains(f, "/SMask") || strings.Contains(f, "/ca ") || strings.Contains(f, "/Group") {
			transparency = true
		}
	}
	if !transparency {
		t.Error("the page uses transparency, PDF 1.4, and the PDF 1.3 grammar does not say so: the check above proves nothing")
	}
}
