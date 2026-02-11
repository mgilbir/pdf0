package main

import (
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
)

func main() {
	// Create a PDF/A-4 document
	doc := pdf.NewPDFADocument(pdf.PDFA4)

	// Add a page with text content
	content := []byte("BT\n/F1 24 Tf\n100 700 Td\n(Hello, PDF/A-4!) Tj\nET\n")

	page := &pdf.Dictionary{}
	page.Set("Type", pdf.Name("Page"))
	page.Set("Parent", pdf.IndirectRef{Number: 2})
	page.Set("MediaBox", pdf.Array{pdf.Integer(0), pdf.Integer(0), pdf.Integer(612), pdf.Integer(792)})
	page.Set("Contents", pdf.IndirectRef{Number: 7})
	page.Set("Resources", pdf.IndirectRef{Number: 8})

	contentStream := &pdf.Stream{
		Dict: pdf.Dictionary{},
		Data: content,
	}
	contentStream.Dict.Set("Length", pdf.Integer(len(content)))

	fontRef := &pdf.Dictionary{}
	fontRef.Set("F1", pdf.IndirectRef{Number: 9})
	resources := &pdf.Dictionary{}
	resources.Set("Font", fontRef)

	// Font with embedded font program (FontDescriptor with FontFile)
	font := &pdf.Dictionary{}
	font.Set("Type", pdf.Name("Font"))
	font.Set("Subtype", pdf.Name("Type1"))
	font.Set("BaseFont", pdf.Name("Helvetica"))
	font.Set("FontDescriptor", pdf.IndirectRef{Number: 10})

	fontDescriptor := &pdf.Dictionary{}
	fontDescriptor.Set("Type", pdf.Name("FontDescriptor"))
	fontDescriptor.Set("FontName", pdf.Name("Helvetica"))
	fontDescriptor.Set("Flags", pdf.Integer(32))
	fontDescriptor.Set("FontBBox", pdf.Array{pdf.Integer(-166), pdf.Integer(-225), pdf.Integer(1000), pdf.Integer(931)})
	fontDescriptor.Set("ItalicAngle", pdf.Integer(0))
	fontDescriptor.Set("Ascent", pdf.Integer(718))
	fontDescriptor.Set("Descent", pdf.Integer(-207))
	fontDescriptor.Set("CapHeight", pdf.Integer(718))
	fontDescriptor.Set("StemV", pdf.Integer(88))
	fontDescriptor.Set("FontFile3", pdf.IndirectRef{Number: 11})

	// Minimal font file (just a placeholder stream for the example)
	fontFile := &pdf.Stream{
		Dict: pdf.Dictionary{},
		Data: []byte{0}, // placeholder
	}
	fontFile.Dict.Set("Length", pdf.Integer(1))
	fontFile.Dict.Set("Subtype", pdf.Name("Type1C"))

	// Update page tree
	pages := doc.Objects[2].Value.(*pdf.Dictionary)
	pages.Set("Kids", pdf.Array{pdf.IndirectRef{Number: 6}})
	pages.Set("Count", pdf.Integer(1))

	// Update metadata with title
	xmpData := pdf.GenerateXMPMetadata(pdf.PDFA4, "PDF/A-4 Example", "pdf0")
	metaStream := doc.Objects[3].Value.(*pdf.Stream)
	metaStream.Data = xmpData
	metaStream.Dict.Set("Length", pdf.Integer(len(xmpData)))

	// Add objects
	doc.Objects[6] = &pdf.IndirectObject{Number: 6, Generation: 0, Value: page}
	doc.Objects[7] = &pdf.IndirectObject{Number: 7, Generation: 0, Value: contentStream}
	doc.Objects[8] = &pdf.IndirectObject{Number: 8, Generation: 0, Value: resources}
	doc.Objects[9] = &pdf.IndirectObject{Number: 9, Generation: 0, Value: font}
	doc.Objects[10] = &pdf.IndirectObject{Number: 10, Generation: 0, Value: fontDescriptor}
	doc.Objects[11] = &pdf.IndirectObject{Number: 11, Generation: 0, Value: fontFile}

	// Validate before writing
	errs := pdf.ValidatePDFA(doc, pdf.PDFA4)
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "PDF/A-4 validation errors:\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %v\n", e)
		}
		os.Exit(1)
	}

	// Write the document
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

	fmt.Println("wrote output.pdf (PDF/A-4 conformant)")
}
