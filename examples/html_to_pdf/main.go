// Command html_to_pdf turns HTML and CSS into a PDF of a given page size.
//
// This is the whole point of the exercise stated in one file: a document, a
// stylesheet, a sheet of paper, and a PDF. Everything above the last step is
// github.com/mgilbir/forme — the HTML parser, the cascade, the layout engine —
// and everything at it is this repository.
//
//	go run ./examples/html_to_pdf > out.pdf
package main

import (
	"fmt"
	"os"

	"github.com/mgilbir/pdf0/render"
)

const document = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Invoice</title></head>
<body>
  <h1>Aurora Instruments</h1>
  <p class="lede">Invoice <strong>2026-0814</strong> &mdash; due on receipt.</p>

  <table>
    <thead>
      <tr><th>Item</th><th class="n">Qty</th><th class="n">Each</th><th class="n">Total</th></tr>
    </thead>
    <tbody>
      <tr><td>Spectrometer calibration</td><td class="n">2</td><td class="n">180.00</td><td class="n">360.00</td></tr>
      <tr><td>Replacement diffraction grating</td><td class="n">1</td><td class="n">445.50</td><td class="n">445.50</td></tr>
      <tr><td>On-site alignment, half day</td><td class="n">1</td><td class="n">240.00</td><td class="n">240.00</td></tr>
    </tbody>
    <tfoot>
      <tr><th colspan="3">Total</th><td class="n">1045.50</td></tr>
    </tfoot>
  </table>

  <p class="note">Payment within 30 days. Late payment attracts interest at the
  statutory rate. Quote the invoice number with any correspondence, and address
  queries to accounts@example.invalid.</p>
</body>
</html>`

const stylesheet = `
  body   { font-family: Helvetica; font-size: 11pt; color: #1a1a1a; margin: 0 }
  h1     { font-size: 20pt; margin: 0 0 4pt; border-bottom: 2pt solid #b8860b;
           padding-bottom: 4pt }
  .lede  { margin: 0 0 18pt; color: #555 }

  table  { width: 100%; border-collapse: collapse; margin-bottom: 18pt }
  th, td { padding: 5pt 6pt; border-bottom: 0.5pt solid #ccc; text-align: left }
  thead th { background-color: #f0ece0; border-bottom: 1pt solid #b8860b }
  .n     { text-align: right }
  tfoot th, tfoot td { font-weight: bold; border-bottom: none; border-top: 1pt solid #333 }

  .note  { font-size: 9pt; color: #666; line-height: 1.5 }
`

func main() {
	// The sheet, which is the third of the three inputs and the caller's to
	// choose. render.A5 is this exact size with a margin already on it; it is
	// spelled out here so that the general form is visible — any width, any
	// height, any margin, in the points a PDF records.
	page := render.PageSizePt(419.53, 595.28).WithMarginPt(42.52)

	out, err := render.Render(render.Input{
		HTML: document,
		CSS:  []render.Stylesheet{{Source: stylesheet}},
	}, render.Options{Page: page})
	if err != nil {
		fmt.Fprintf(os.Stderr, "rendering: %v\n", err)
		os.Exit(1)
	}

	// A finding is the engine saying something about the document rather than
	// about itself: a property it does not implement, a page the content did
	// not fit on. They are worth printing even when a document was produced.
	for _, f := range out.Findings {
		fmt.Fprintf(os.Stderr, "%s: %s\n", f.Rule, f.Message)
	}
	if out.Document == nil {
		fmt.Fprintln(os.Stderr, "no document: a rule fired at error severity")
		os.Exit(1)
	}
	if out.Scale != 1 {
		fmt.Fprintf(os.Stderr, "content was scaled to %.3f to fit the sheet\n", out.Scale)
	}

	if err := out.Document.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "writing: %v\n", err)
		os.Exit(1)
	}
}
