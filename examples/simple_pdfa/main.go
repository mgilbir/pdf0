package main

import (
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

func main() {
	// Create a PDF/A-4 document
	doc := pdf.NewPDFADocument(pdfa.PDFA4)

	// Add a page with text content
	content := []byte("BT\n/F1 24 Tf\n100 700 Td\n(Hello, PDF/A-4!) Tj\nET\n")

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	page.Set("Contents", object.IndirectRef{Number: 7})
	page.Set("Resources", object.IndirectRef{Number: 8})

	contentStream := &object.Stream{
		Dict: object.Dictionary{},
		Data: content,
	}
	contentStream.Dict.Set("Length", object.Integer(len(content)))

	fontRef := &object.Dictionary{}
	fontRef.Set("F1", object.IndirectRef{Number: 9})
	resources := &object.Dictionary{}
	resources.Set("Font", fontRef)

	// Font with embedded font program (FontDescriptor with FontFile)
	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type1"))
	font.Set("BaseFont", object.Name("Helvetica"))
	font.Set("FontDescriptor", object.IndirectRef{Number: 10})

	fontDescriptor := &object.Dictionary{}
	fontDescriptor.Set("Type", object.Name("FontDescriptor"))
	fontDescriptor.Set("FontName", object.Name("Helvetica"))
	fontDescriptor.Set("Flags", object.Integer(32))
	fontDescriptor.Set("FontBBox", object.Array{object.Integer(-166), object.Integer(-225), object.Integer(1000), object.Integer(931)})
	fontDescriptor.Set("ItalicAngle", object.Integer(0))
	fontDescriptor.Set("Ascent", object.Integer(718))
	fontDescriptor.Set("Descent", object.Integer(-207))
	fontDescriptor.Set("CapHeight", object.Integer(718))
	fontDescriptor.Set("StemV", object.Integer(88))
	fontDescriptor.Set("FontFile3", object.IndirectRef{Number: 11})

	// Minimal font file (just a placeholder stream for the example)
	fontFile := &object.Stream{
		Dict: object.Dictionary{},
		Data: []byte{0}, // placeholder
	}
	fontFile.Dict.Set("Length", object.Integer(1))
	fontFile.Dict.Set("Subtype", object.Name("Type1C"))

	// Update page tree
	pages := doc.Objects[2].Value.(*object.Dictionary)
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 6}})
	pages.Set("Count", object.Integer(1))

	// Update metadata with title
	xmpData := pdf.GenerateXMPMetadata(pdfa.PDFA4, "PDF/A-4 Example", "pdf0")
	metaStream := doc.Objects[3].Value.(*object.Stream)
	metaStream.Data = xmpData
	metaStream.Dict.Set("Length", object.Integer(len(xmpData)))

	// Add objects
	doc.Objects[6] = &object.IndirectObject{Number: 6, Generation: 0, Value: page}
	doc.Objects[7] = &object.IndirectObject{Number: 7, Generation: 0, Value: contentStream}
	doc.Objects[8] = &object.IndirectObject{Number: 8, Generation: 0, Value: resources}
	doc.Objects[9] = &object.IndirectObject{Number: 9, Generation: 0, Value: font}
	doc.Objects[10] = &object.IndirectObject{Number: 10, Generation: 0, Value: fontDescriptor}
	doc.Objects[11] = &object.IndirectObject{Number: 11, Generation: 0, Value: fontFile}

	// Validate and report. The embedded font program here is a one-byte
	// placeholder, not a real font, so the font-embedding checks fire — this demo
	// shows the builder and validator APIs, not a fully conformant file. The
	// findings are informational; the document is still written.
	if errs := pdf.ValidatePDFA(doc, pdfa.PDFA4); len(errs) > 0 {
		fmt.Printf("validation reported %d issue(s) (expected: the demo font is a placeholder):\n", len(errs))
		for _, e := range errs {
			fmt.Printf("  %v\n", e)
		}
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

	// Note: this demonstrates the builder API. The OutputIntent ICC profile is a
	// real sRGB profile, but the embedded font program is a placeholder, so the
	// result is not a fully valid PDF/A file — validate real output with veraPDF.
	fmt.Println("wrote output.pdf (PDF/A-4 builder demo; not veraPDF-validated)")
}
