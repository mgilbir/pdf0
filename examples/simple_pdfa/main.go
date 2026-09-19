// simple_pdfa builds a conforming PDF/A-4 document and validates it before
// writing, so that the file this produces is one the validator accepts.
//
// It draws vector graphics rather than text, deliberately. PDF/A requires every
// font a document shows to be embedded in it, and this repository ships no font
// to embed — see the fonts package, whose Load and Embed put a real face into a
// document, and examples in fonts_embed_test.go that set text conformantly.
package main

import (
	"fmt"
	"log"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/pdfa"
)

func main() {
	doc, err := pdf.NewPDFADocument(pdfa.PDFA4)
	if err != nil {
		log.Fatal(err)
	}

	// Draw. The builder emits the operators and records which resources the
	// drawing named; AddPage checks that every one of them is defined.
	var page content.Builder
	page.Save().
		SetRGB(0.15, 0.35, 0.75).
		Rect(72, 600, 200, 120).Fill().
		Restore()
	page.Save().
		SetStrokeGray(0.2).SetLineWidth(3).SetLineCap(content.RoundCap).
		MoveTo(72, 560).LineTo(272, 560).Stroke().
		Restore()
	page.Save().
		Translate(320, 600).
		MoveTo(0, 0).CurveTo(40, 120, 120, 120, 160, 0).ClosePath().
		SetGray(0.85).FillStroke().
		Restore()

	if _, err := doc.AddPage(pdf.Page{Width: 612, Height: 792, Content: &page}); err != nil {
		fmt.Fprintf(os.Stderr, "adding the page: %v\n", err)
		os.Exit(1)
	}

	// Validate before writing. An example that produced a file it calls PDF/A
	// without checking would be teaching the wrong habit.
	if errs := pdf.ValidatePDFA(doc, pdfa.PDFA4); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "not conforming: %s\n", e.Error())
		}
		os.Exit(1)
	}

	// To stdout, so the example composes and leaves nothing behind:
	//
	//	go run ./examples/simple_pdfa > out.pdf
	//
	// It used to write output.pdf into whatever directory it was run from.
	if err := doc.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "writing: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "wrote a PDF/A-4 document, validated to stdout")
}
