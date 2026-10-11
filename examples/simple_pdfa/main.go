// simple_pdfa builds a conforming PDF/A-4 document and writes it with Save,
// which checks the bytes it is about to emit against the level the document
// claims and refuses to write them if they do not conform.
//
// It sets one line of text in the bundled Noto Sans, which is embedded: PDF/A
// requires every font a document shows to be in the file. examples/text sets a
// whole paragraph, and examples/flavored draws one page at several levels.
// docs/building.md walks through this API.
package main

import (
	"fmt"
	"log"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

func main() {
	doc, err := pdf.NewPDFADocument(pdfa.PDFA4)
	if err != nil {
		log.Fatal(err)
	}

	// The bundled face: no font file is needed from the machine this runs on.
	face, err := fonts.NotoSans()
	if err != nil {
		log.Fatal(err)
	}

	// Draw. The builder emits the operators and records which resources the
	// drawing named; AddPage checks that every one of them is defined.
	var page content.Builder
	page.BeginText().SetFont("F1", 24).MoveText(72, 740)
	face.DrawShaped(&page, "Hello, PDF/A-4", 24)
	page.EndText()
	page.Save().
		SetRGB(0.15, 0.35, 0.75).
		Rect(72, 600, 200, 120).Fill().
		Restore()
	page.Save().
		SetStrokeGray(0.2).SetLineWidth(3).SetLineCap(content.RoundCap).
		MoveTo(72, 560).LineTo(272, 560).Stroke().
		Restore()
	// Colour is set before the path: between MoveTo and the painting operator
	// only path operators are allowed, and the builder refuses anything else.
	page.Save().
		Translate(320, 600).
		SetGray(0.85).
		MoveTo(0, 0).CurveTo(40, 120, 120, 120, 160, 0).ClosePath().
		FillStroke().
		Restore()

	// The face is named, not embedded: AddPage embeds it once the drawing is
	// final, subset to the glyphs the page set.
	if _, err := doc.AddPage(pdf.Page{
		Width: 612, Height: 792, Content: &page,
		Faces: map[object.Name]*fonts.Face{"F1": face},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "adding the page: %v\n", err)
		os.Exit(1)
	}

	// Save, not Write. An example that produced a file it calls PDF/A without
	// checking would be teaching the wrong habit, and checking the model is not
	// enough: the file-structure rules (header, cross-reference table, stream
	// lengths, data after %%EOF) judge bytes, and a document built in memory
	// has none until it is written. Save writes to memory, reads the result
	// back, validates it at the level the document claims, and only then
	// copies it to the writer, so nothing reaches stdout unless the whole file
	// passed. Its error says which rules failed, or that a check could not
	// finish.
	//
	// To stdout, so the example composes and leaves nothing behind:
	//
	//	go run ./examples/simple_pdfa > out.pdf
	if err := doc.Save(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "not written: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "wrote a PDF/A-4 document, checked as written, to stdout")
}
