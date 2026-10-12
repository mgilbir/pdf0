// simple_pdf builds a one-page PDF 2.0 document with text and a little vector
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
	// A catalog, an empty page tree for AddPage to append to, and the file
	// identifier (/ID) PDF 2.0 requires in the trailer (ISO 32000-2 Table 15).
	doc := pdf.NewDocument()

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
		ShowText([]byte("Hello, PDF 2.0!")).
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
	//	go run ./examples/simple_pdf > out.pdf
	//
	// It used to write output.pdf into whatever directory it was run from.
	if err := doc.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "writing: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "wrote a plain PDF 2.0 document to stdout")
}
