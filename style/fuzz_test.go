package style

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// Fuzzing the styling stage.
//
// Both inputs are untrusted and they meet here for the first time, which is what
// makes this worth fuzzing separately from the two parsers: a document and a
// stylesheet that are each individually harmless can still combine into work
// neither bounds on its own — a selector that backtracks over a deep tree is
// exactly that shape.
//
// What is checked are the properties that must hold for every pair:
//
//   - Totality. Styling never panics and always terminates.
//   - Completeness. Every element gets a style, and every style holds every
//     property, so no consumer has to handle an absent one.
//   - Honesty. A budget that trips is reported, and the report is bounded.

func FuzzApply(f *testing.F) {
	docs := []string{
		"<p>x</p>",
		`<div class="a"><p id="t" class="b" style="color: red">x</p></div>`,
		"<ul><li>a<li>b</ul>",
		"<table><tr><td>a<td>b</table>",
		"",
	}
	sheets := []string{
		"p { color: red }",
		"p, div { margin: 1px 2px 3px 4px }",
		"#t { color: a !important } .b { color: b }",
		"p { color: inherit } div { color: initial } span { color: unset }",
		"p { flex-wrap: wrap }",
		"@media print { p { color: c } }",
		"@page { margin: 1cm }",
		"* { padding: inherit }",
		"p { margin: }",
		"p:nth-child(2n+1) { color: a }",
		"p:hover { color: a }",
		"{}", ";", "p{", "}",
	}
	for _, d := range docs {
		for _, s := range sheets {
			f.Add(d, s)
		}
	}

	f.Fuzz(func(t *testing.T, src, sheetSrc string) {
		// Bound the inputs: the fuzzer is looking for logic faults, and a
		// hundred-megabyte input only finds the memory it takes to hold.
		if len(src) > 1<<16 || len(sheetSrc) > 1<<16 {
			return
		}

		doc, _, _ := html.Parse(src)
		rules, _ := css.ParseStylesheet(sheetSrc)
		got := Apply(doc, []Sheet{{Origin: OriginAuthor, Rules: rules}})

		if got.Styles == nil {
			t.Fatal("no styles at all")
		}

		// Every element, and only elements.
		n := 0
		doc.Walk(func(node *html.Node) bool {
			if node.Type != html.ElementNode {
				if _, ok := got.Styles[node]; ok {
					t.Fatalf("a %v node was given a style", node.Type)
				}
				return true
			}
			n++
			cs, ok := got.Styles[node]
			if !ok {
				t.Fatalf("<%s> has no computed style", node.Name)
			}
			// Every property present, so no consumer has to tell "unset" from
			// "absent" — a distinction that would be silently wrong wherever the
			// empty string is a legal value.
			if len(cs) != len(properties) {
				t.Fatalf("<%s> has %d properties, want all %d",
					node.Name, len(cs), len(properties))
			}
			for name := range properties {
				if _, ok := cs[name]; !ok {
					t.Fatalf("<%s> is missing %s", node.Name, name)
				}
			}
			return true
		})
		if len(got.Styles) != n {
			t.Fatalf("there are %d elements and %d styles", n, len(got.Styles))
		}

		if len(got.Findings) > maxFindings+1 {
			t.Fatalf("reported %d findings, past the bound of %d",
				len(got.Findings), maxFindings)
		}
		for _, fi := range got.Findings {
			if fi.Message == "" {
				t.Fatal("a finding with no message")
			}
			if fi.Offset < 0 {
				t.Fatalf("a finding at offset %d", fi.Offset)
			}
		}

		// An incomplete run has to say so, and it is the only thing that may.
		if got.Incomplete {
			said := false
			for _, fi := range got.Findings {
				if strings.Contains(fi.Message, "stopped early") {
					said = true
				}
			}
			if !said {
				t.Fatal("styling stopped early and did not report it")
			}
		}
	})
}

// FuzzApplyIsDeterministic pins that the same inputs give the same answer.
//
// It is a separate target because it costs two runs per input, and because the
// fault it looks for is invisible to the one above: the cascade reads several
// maps, Go randomises map iteration on every run, and an ordering that happens
// to be stable in one process is not evidence of anything.
func FuzzApplyIsDeterministic(f *testing.F) {
	f.Add(`<div class="a"><p class="b">x</p></div>`,
		"p { margin: 1px 2px; color: a } .b { color: b; padding: 3px } div p { margin-left: 9px }")
	f.Add("<p>x</p>", "p { color: a } p { color: b }")

	f.Fuzz(func(t *testing.T, src, sheetSrc string) {
		if len(src) > 1<<14 || len(sheetSrc) > 1<<14 {
			return
		}
		doc, _, _ := html.Parse(src)
		rules, _ := css.ParseStylesheet(sheetSrc)
		sheets := []Sheet{{Origin: OriginAuthor, Rules: rules}}

		first := Apply(doc, sheets)
		second := Apply(doc, sheets)

		if len(first.Styles) != len(second.Styles) {
			t.Fatalf("two runs styled %d and %d elements",
				len(first.Styles), len(second.Styles))
		}
		for node, a := range first.Styles {
			b, ok := second.Styles[node]
			if !ok {
				t.Fatal("an element styled in one run and not the other")
			}
			for name, av := range a {
				if bv := b[name]; av != bv {
					t.Fatalf("<%s> %s is %q then %q", node.Name, name, av, bv)
				}
			}
		}
		if len(first.Findings) != len(second.Findings) {
			t.Fatalf("two runs reported %d and %d findings",
				len(first.Findings), len(second.Findings))
		}
	})
}
