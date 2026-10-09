package htmlpdf

import (
	"sort"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestTheDrawingOperationsAgainstPDFA writes a page of every drawing operation
// forme 0.4.0 added, and of the ones it draws differently, and asks pdf0's
// PDF/A validator about each at every level. htmlpdf claims no PDF/A level —
// its page has no output intent and no XMP — so what the validator says of a
// plain page is about the claim, and is the same for every page here; what an
// operation adds is the question. Nothing may be added at PDF/A-2b, -3b or -4.
// At PDF/A-1b, whose base is PDF 1.4 and which forbids transparency (ISO
// 19005-1 6.4), the operations that need transparency — a group's opacity, a
// soft mask, an alpha below one — must be flagged, and the others must add
// nothing.
func TestTheDrawingOperationsAgainstPDFA(t *testing.T) {
	noto := notoSansSet(t)
	cases := []struct {
		name        string
		in          Input
		transparent bool
	}{
		{"path", Input{HTML: `<div style="width:100px;height:100px;border:10px solid rgb(0,128,0);border-radius:30px"></div>`}, false},
		{"clip", Input{HTML: `<div style="width:100px;height:100px;border-radius:30px;overflow:hidden"><div style="width:200px;height:200px;background:rgb(255,0,0)"></div></div>`}, false},
		{"opacity", Input{HTML: `<div style="filter: opacity(0.5)"><div style="background:red;width:50px;height:50px"></div><div style="background:blue;width:50px;height:50px;margin-top:-25px"></div></div>`}, true},
		{"drop-shadow", filterInput(t, `<img src="half.png" style="filter: drop-shadow(6px 6px 0 rgba(0,0,0,0.5))">`), true},
		{"text-shadow", Input{HTML: `<p style="text-shadow: 2px 3px rgb(255,0,0)">shadowed</p>`}, false},
		{"emphasis", Input{HTML: `<p style="text-emphasis: dot rgb(255,0,0)">emphasis</p>`}, false},
		{"squeeze", Input{HTML: `<p>12345</p>`, CSS: []Stylesheet{{Source: `html { writing-mode: vertical-rl } p { margin: 0; text-combine-upright: all }`}}}, false},
		{"transform", Input{HTML: `<div style="margin:60px;width:100px;height:50px;background:rgb(255,0,0);transform:rotate(30deg) skewX(10deg)"><p style="transform:scaleX(-1)">turned</p></div>`}, false},
		{"math", Input{HTML: `<math display="block"><msqrt><mfrac><mfrac>` + mathX + mathX + `</mfrac>` + mathX + `</mfrac></msqrt></math>`, Fonts: mathSet(t)}, false},
	}
	for _, g := range gradientCases {
		transparent := strings.Contains(g.name, "alpha") || strings.Contains(g.name, "fade") ||
			g.name == "radial-ellipse" || g.name == "repeating-radial"
		cases = append(cases, struct {
			name        string
			in          Input
			transparent bool
		}{"gradient-" + g.name, gradientDoc(g.background), transparent})
	}
	// What a plain page is told: no output intent for its device colour
	// (19005-1 6.2.3.3, -2 6.2.4.3), no XMP metadata (6.7.2), and a PDF 2.0
	// header where -2 and -3 are based on PDF 1.7 (6.1.2).
	claim := map[string]bool{"6.2.3.3": true, "6.2.4.3": true, "6.7.2": true, "6.1.2": true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			if in.Fonts == nil {
				in.Fonts = noto
			}
			doc, _ := roundTrip(t, in, Options{})
			for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
				added := map[string][]string{}
				for _, v := range pdf0.ValidatePDFA(doc, level) {
					if !claim[v.Rule] {
						added[v.Rule] = append(added[v.Rule], v.Error())
					}
				}
				if level == pdfa.PDFA1b && tc.transparent {
					if len(added["6.4"]) == 0 {
						t.Errorf("%s: a page with transparency is not flagged under 6.4", level)
					}
					delete(added, "6.4")
				}
				rules := make([]string, 0, len(added))
				for r := range added {
					rules = append(rules, r)
				}
				sort.Strings(rules)
				for _, r := range rules {
					t.Errorf("%s: %v", level, added[r])
				}
			}
		})
	}
}
