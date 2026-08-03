package main

import (
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

func main() {
	// Draw the page. The builder emits the operators; it also records that /F1
	// was used, which is what the /Resources dictionary below has to define.
	var page1 content.Builder
	page1.BeginText().
		SetFont("F1", 24).
		MoveText(100, 700).
		ShowText([]byte("Hello, PDF 2.0!")).
		EndText()

	// A red rule under the text, to show the graphics side.
	page1.Save().
		SetStrokeRGB(0.8, 0.1, 0.1).SetLineWidth(1.5).
		MoveTo(100, 690).LineTo(340, 690).Stroke().
		Restore()

	drawn, err := page1.Bytes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "drawing the page: %v\n", err)
		os.Exit(1)
	}

	// Build the document object graph bottom-up.

	// Object 1: Catalog
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", object.IndirectRef{Number: 2})

	// Object 2: Pages
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))

	// Object 3: Page
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	page.Set("Contents", object.IndirectRef{Number: 4})
	page.Set("Resources", object.IndirectRef{Number: 5})

	// Object 4: Content stream
	streamDict := object.Dictionary{}
	contentStream := &object.Stream{
		Dict: streamDict,
		Data: drawn,
	}

	// Object 5: Resources
	fontRef := &object.Dictionary{}
	fontRef.Set("F1", object.IndirectRef{Number: 6})
	resources := &object.Dictionary{}
	resources.Set("Font", fontRef)

	// Object 6: Font
	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type1"))
	font.Set("BaseFont", object.Name("Helvetica"))

	doc := &pdf.Document{
		Version: "2.0",
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Generation: 0, Value: catalog},
			2: {Number: 2, Generation: 0, Value: pages},
			3: {Number: 3, Generation: 0, Value: page},
			4: {Number: 4, Generation: 0, Value: contentStream},
			5: {Number: 5, Generation: 0, Value: resources},
			6: {Number: 6, Generation: 0, Value: font},
		},
		Trailer: object.Dictionary{
			Keys:   []object.Name{"Root"},
			Values: []object.Object{object.IndirectRef{Number: 1}},
		},
	}

	f, err := os.Create("output.pdf")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if err := doc.Write(f); err != nil {
		fmt.Fprintf(os.Stderr, "error writing PDF: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("wrote output.pdf")
}
