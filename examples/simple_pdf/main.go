package main

import (
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
)

func main() {
	// Content stream: draw "Hello, PDF 2.0!" in Helvetica 24pt
	content := []byte("BT\n/F1 24 Tf\n100 700 Td\n(Hello, PDF 2.0!) Tj\nET\n")

	// Build the document object graph bottom-up.

	// Object 1: Catalog
	catalog := &pdf.Dictionary{}
	catalog.Set("Type", pdf.Name("Catalog"))
	catalog.Set("Pages", pdf.IndirectRef{Number: 2})

	// Object 2: Pages
	pages := &pdf.Dictionary{}
	pages.Set("Type", pdf.Name("Pages"))
	pages.Set("Kids", pdf.Array{pdf.IndirectRef{Number: 3}})
	pages.Set("Count", pdf.Integer(1))

	// Object 3: Page
	page := &pdf.Dictionary{}
	page.Set("Type", pdf.Name("Page"))
	page.Set("Parent", pdf.IndirectRef{Number: 2})
	page.Set("MediaBox", pdf.Array{pdf.Integer(0), pdf.Integer(0), pdf.Integer(612), pdf.Integer(792)})
	page.Set("Contents", pdf.IndirectRef{Number: 4})
	page.Set("Resources", pdf.IndirectRef{Number: 5})

	// Object 4: Content stream
	streamDict := pdf.Dictionary{}
	contentStream := &pdf.Stream{
		Dict: streamDict,
		Data: content,
	}

	// Object 5: Resources
	fontRef := &pdf.Dictionary{}
	fontRef.Set("F1", pdf.IndirectRef{Number: 6})
	resources := &pdf.Dictionary{}
	resources.Set("Font", fontRef)

	// Object 6: Font
	font := &pdf.Dictionary{}
	font.Set("Type", pdf.Name("Font"))
	font.Set("Subtype", pdf.Name("Type1"))
	font.Set("BaseFont", pdf.Name("Helvetica"))

	doc := &pdf.Document{
		Version: "2.0",
		Objects: map[int]*pdf.IndirectObject{
			1: {Number: 1, Generation: 0, Value: catalog},
			2: {Number: 2, Generation: 0, Value: pages},
			3: {Number: 3, Generation: 0, Value: page},
			4: {Number: 4, Generation: 0, Value: contentStream},
			5: {Number: 5, Generation: 0, Value: resources},
			6: {Number: 6, Generation: 0, Value: font},
		},
		Trailer: pdf.Dictionary{
			Keys:   []pdf.Name{"Root"},
			Values: []pdf.Object{pdf.IndirectRef{Number: 1}},
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
