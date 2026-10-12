// simple_pdf17 builds a one-page PDF 1.7 document with text and a little vector
// drawing, through the content builder and AddPage.
//
// The font is Helvetica, one of the fourteen a reader is required to have, so
// nothing is embedded. That is legal in a plain PDF and *not* in PDF/A, which
// requires every font to be embedded — see examples/text for an embedded font,
// and examples/simple_pdfa for a conforming document. docs/building.md walks
// through this API.
package main

import (
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

func main() {
	// A catalog, an empty page tree for AddPage to append to, and a file
	// identifier (/ID). NewDocument starts a PDF 2.0 document; setting Version
	// before anything is added makes it a 1.7 one, written with a %PDF-1.7
	// header.
	doc := pdf.NewDocument()
	doc.Version = "1.7"

	// One of the standard fourteen faces, referenced rather than embedded.
	helvetica := &object.Dictionary{}
	helvetica.Set("Type", object.Name("Font"))
	helvetica.Set("Subtype", object.Name("Type1"))
	helvetica.Set("BaseFont", object.Name("Helvetica"))
	fontRef := doc.Add(helvetica)

	var page content.Builder
	page.BeginText().
		SetFont("F1", 24).
		MoveText(100, 700).
		ShowText([]byte("Hello, PDF 1.7!")).
		EndText()
	page.Save().
		SetStrokeRGB(0.8, 0.1, 0.1).SetLineWidth(1.5).
		MoveTo(100, 690).LineTo(340, 690).Stroke().
		Restore()

	if _, err := doc.AddPage(pdf.Page{
		Width: 612, Height: 792,
		Content: &page,
		Fonts:   map[object.Name]object.Object{"F1": fontRef},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "adding the page: %v\n", err)
		os.Exit(1)
	}

	// To stdout, so the example composes and leaves nothing behind:
	//
	//	go run ./examples/simple_pdf17 > out.pdf
	//
	// It used to write output.pdf into whatever directory it was run from.
	if err := doc.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "writing: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "wrote a PDF 1.7 document to stdout")
}
