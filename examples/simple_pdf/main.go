// simple_pdf builds a one-page PDF 2.0 document with text and a little vector
// drawing, through the content builder and AddPage.
//
// The font is Helvetica, one of the fourteen a reader is required to have, so
// nothing is embedded. That is legal in a plain PDF and *not* in PDF/A, which
// requires every font to be embedded — see the fonts package for that, and
// simple_pdfa for a conforming document.
package main

import (
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

func main() {
	doc := &pdf.Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}}

	// A catalog and an empty page tree for AddPage to append to.
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	pagesRef := doc.Add(pages)

	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", doc.Add(catalog))

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

	f, err := os.Create("output.pdf")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating the file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := doc.Write(f); err != nil {
		fmt.Fprintf(os.Stderr, "writing: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("wrote output.pdf")
}
