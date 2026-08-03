// Command flavored draws the same page at several PDF conformance levels, to
// show that the level is a decision taken once — when the document is created —
// and enforced once, when it is saved.
//
// Everything between those two points is the same code. drawPage below does not
// know what flavour of document it is drawing into: it embeds a font, sets
// text, draws a chart and places an image, and none of that varies by level.
// What varies is what Save does with the result.
//
//	go run ./examples/flavored -font /usr/share/fonts/truetype/dejavu/DejaVuSans.ttf
//
// The text is set in the bundled Noto Sans unless -font names another file. A
// conforming PDF/A must embed every font it shows, so there is a real font here
// either way; nothing is named and left for the reader to supply.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

const (
	pageW, pageH = 420.0, 320.0
	margin       = 36.0
)

func main() {
	fontPath := flag.String("font", "", "a TrueType or OpenType file to embed; the bundled Noto Sans if empty")
	flag.Parse()

	face, err := loadFace(*fontPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading the face: %v\n", err)
		os.Exit(1)
	}

	// The flavour is the only thing that differs between these. The drawing is
	// identical, and none of it mentions a level.
	flavours := []struct {
		name string
		make func() *pdf.Document
	}{
		{"plain PDF 2.0", pdf.NewDocument},
		{"PDF/A-2b", func() *pdf.Document { return pdf.NewPDFADocument(pdfa.PDFA2b) }},
		{"PDF/A-4", func() *pdf.Document { return pdf.NewPDFADocument(pdfa.PDFA4) }},
	}

	for _, f := range flavours {
		doc := f.make()
		if err := doc.SetDocumentInfo(pdf.DocumentInfo{
			Title:   "Quarterly results",
			Author:  "pdf0",
			Creator: "examples/flavored",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "%s: describing: %v\n", f.name, err)
			os.Exit(1)
		}
		if err := drawPage(doc, face); err != nil {
			fmt.Fprintf(os.Stderr, "%s: drawing: %v\n", f.name, err)
			os.Exit(1)
		}
		report(f.name, doc)
	}
}

// report saves the document and says what happened, which is the whole point of
// the example.
func report(name string, doc *pdf.Document) {
	file := "flavored-" + strings.NewReplacer("/", "", " ", "-", ".", "").Replace(name) + ".pdf"
	out, err := os.Create(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
	defer out.Close()

	// One call. It knows what the document claims because the document says so.
	if err := doc.Save(out); err != nil {
		os.Remove(file)
		fmt.Printf("%-14s  refused:\n", name)
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Printf("                %s\n", strings.TrimSpace(line))
		}
		return
	}
	info, _ := os.Stat(file)
	level, claimed := doc.Conformance()
	what := "claims nothing"
	if claimed {
		what = "verified as " + level.String()
	}
	fmt.Printf("%-14s  wrote %s (%d bytes, %s)\n", name, file, info.Size(), what)
}

// drawPage puts text, a chart and an image on a page.
//
// Nothing here depends on the document's conformance level. That is the claim
// the example exists to demonstrate: a caller draws, and the level is somebody
// else's problem.
func drawPage(doc *pdf.Document, face *fonts.Face) error {
	imageRef, err := images.Embed(doc, swatch())
	if err != nil {
		return fmt.Errorf("embedding the image: %w", err)
	}

	var b content.Builder

	// Text.
	b.BeginText().
		SetFont("F0", 16).
		SetRGB(0.1, 0.1, 0.15).
		MoveText(margin, pageH-margin-16)
	face.DrawShaped(&b, "Quarterly results", 16)
	b.EndText()

	b.BeginText().
		SetFont("F0", 9).
		SetRGB(0.35, 0.35, 0.4).
		MoveText(margin, pageH-margin-34)
	face.DrawShaped(&b, "Revenue by quarter, in arbitrary units.", 9)
	b.EndText()

	// A chart: axes and bars, plain vector graphics.
	const (
		chartX, chartY = margin, 80.0
		chartW, chartH = 210.0, 150.0
	)
	b.Save().
		SetStrokeRGB(0.7, 0.7, 0.75).
		SetLineWidth(1).
		MoveTo(chartX, chartY+chartH).LineTo(chartX, chartY).LineTo(chartX+chartW, chartY).
		Stroke().
		Restore()

	values := []float64{0.45, 0.62, 0.55, 0.88}
	barW := chartW / float64(len(values)) * 0.6
	gap := chartW / float64(len(values))
	for i, v := range values {
		x := chartX + gap*float64(i) + (gap-barW)/2
		b.Save().
			SetRGB(0.20, 0.45, 0.85).
			Rect(x, chartY, barW, chartH*v).
			Fill().
			Restore()

		b.BeginText().SetFont("F0", 8).SetRGB(0.35, 0.35, 0.4).
			MoveText(x, chartY-12)
		face.DrawShaped(&b, fmt.Sprintf("Q%d", i+1), 8)
		b.EndText()
	}

	// The image, placed by a matrix: an image XObject draws into the unit
	// square, so the matrix is where and how big it is.
	b.Save().Concat(120, 0, 0, 120, pageW-margin-120, 100).Draw("Im0").Restore()

	_, err = doc.AddPage(pdf.Page{
		Width: pageW, Height: pageH, Content: &b,
		// The face is named, not embedded: AddPage embeds it once the drawing
		// above is final, which is when subsetting knows what to keep.
		Faces:    map[object.Name]*fonts.Face{"F0": face},
		XObjects: map[object.Name]object.Object{"Im0": imageRef},
	})
	return err
}

// loadFace loads the font to embed: the bundled one unless another is named.
func loadFace(path string) (*fonts.Face, error) {
	if path == "" {
		return fonts.NotoSans()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return fonts.Load(data)
}

// swatch builds a small gradient so the page has a real image on it.
func swatch() image.Image {
	const n = 64
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(40 + x*2),
				G: uint8(90 + y),
				B: uint8(200 - x),
				A: 255,
			})
		}
	}
	return img
}
