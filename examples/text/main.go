// Command text sets a paragraph of real text on a page: it measures words,
// breaks lines to a column width, and draws them.
//
// It uses Helvetica, one of the fourteen faces a PDF reader is required to
// have, so it needs no font file and embeds nothing. That is what makes it
// self-contained and also what makes its output *not* a conforming PDF/A: a
// conforming document must embed every font it shows. For that, load a real
// face with fonts.Load and embed it — the metrics, the drawing and the line
// breaking below are identical either way, because Face answers the same
// questions whichever kind it is.
//
//	go run ./examples/text
package main

import (
	"fmt"
	"os"
	"strings"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

const (
	pageW, pageH = 595.0, 842.0 // A4, in points
	margin       = 72.0         // one inch
	bodySize     = 11.0
	leading      = 15.0
)

const body = `Typesetting a paragraph needs one thing a font can answer and a ` +
	`content stream cannot: how wide a word is. That is what Face.Measure is ` +
	`for. Everything else here follows from it — words are measured, lines are ` +
	`filled until the next word would not fit, and each line is drawn at a ` +
	`baseline one leading below the last. Break the measurement and the text ` +
	`spills out of its column, which is the failure this arrangement makes ` +
	`visible rather than subtle.`

func main() {
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading the face: %v\n", err)
		os.Exit(1)
	}
	bold, err := fonts.Standard("Helvetica-Bold")
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading the face: %v\n", err)
		os.Exit(1)
	}

	doc := &pdf.Document{Version: "1.7", Objects: map[int]*object.IndirectObject{}}
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	pagesRef := doc.Add(pages)
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", doc.Add(catalog))

	var page content.Builder
	y := pageH - margin

	// A heading, set in the bold face and measured with it — a different face
	// means different widths, which is why measurement belongs to the face and
	// not to the string.
	const heading = "Measuring, breaking, drawing"
	headingCodes, missing := bold.Encode(heading)
	if missing != 0 {
		fmt.Fprintf(os.Stderr, "%d characters of the heading are outside the encoding\n", missing)
	}
	page.BeginText().
		SetFont("HB", 18).
		MoveText(margin, y).
		ShowText(headingCodes).
		EndText()
	y -= 2 * leading

	// The body, wrapped to the column.
	column := pageW - 2*margin
	lines := wrap(face, body, bodySize, column)
	for _, line := range lines {
		// The face turns characters into the codes this font's encoding uses;
		// the builder writes them. Keeping the two steps apart is what lets the
		// same drawing code serve a standard face and an embedded one, whose
		// codes are entirely different kinds of number.
		codes, _ := face.Encode(line)
		page.BeginText().
			SetFont("H", bodySize).
			MoveText(margin, y).
			ShowText(codes).
			EndText()
		y -= leading
	}

	// A rule showing the column edge, so a line that overflowed would be seen.
	page.Save().
		SetStrokeGray(0.75).SetLineWidth(0.5).
		MoveTo(margin+column, pageH-margin+6).LineTo(margin+column, y+leading-4).Stroke().
		Restore()

	if _, err := doc.AddPage(pdf.Page{
		Width: pageW, Height: pageH,
		Content: &page,
		Fonts: map[object.Name]object.Object{
			"H":  mustEmbed(doc, face),
			"HB": mustEmbed(doc, bold),
		},
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
	fmt.Printf("wrote output.pdf: %d lines of body text in a %.0fpt column\n", len(lines), column)
}

// wrap breaks text into lines no wider than width, measuring with the face the
// lines will be set in.
//
// Greedy: a word goes on the current line if it fits and starts a new one if it
// does not. That is not the best paragraph a typesetter could produce — a
// proper line breaker weighs the whole paragraph at once — but it is the one
// that follows directly from being able to measure, and it is what the phrase
// "lay text out" means before anything more ambitious.
func wrap(face *fonts.Face, text string, size, width float64) []string {
	var lines []string
	var line string
	for _, word := range strings.Fields(text) {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if face.Measure(candidate, size) > width && line != "" {
			lines = append(lines, line)
			line = word
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func mustEmbed(doc *pdf.Document, face *fonts.Face) object.IndirectRef {
	ref, err := face.Embed(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "embedding %s: %v\n", face.Name(), err)
		os.Exit(1)
	}
	return ref
}
