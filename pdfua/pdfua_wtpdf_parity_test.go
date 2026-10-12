package pdfua

// Rules the WTPDF examples (real PDF/UA-2 documents veraPDF passes) showed
// pdf0 applying the wrong way: part-1 rules run under part 2, attributes read
// from /A but not the class map, and a malformed XMP packet reported without
// its cause.

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// TestUAHeadingSequenceIsPartOne: ISO 14289-1 7.4.2 asks that the first
// numbered heading be H1 and that a descending sequence not skip a level;
// ISO 14289-2 8.2.5.12 NOTE 2 says part 2, following WCAG, has no such
// requirement, and the veraPDF PDF/UA-2 profile has no rule for it. The
// WTPDF example 47464-t-tagged starts at H3 (its sections are LaTeX
// subsections) and veraPDF passes it as PDF/UA-2.
func TestUAHeadingSequenceIsPartOne(t *testing.T) {
	mk := func() core.View {
		doc := mkView(nil, nil)
		headingDoc(doc, object.Array{heading(doc, 10, "H3"), heading(doc, 11, "H5")})
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	one := validateView(mk(), "1")
	if len(findings(one, "7.4.2", "first heading is H3")) != 1 || len(findings(one, "7.4.2", "H5 follows H3")) != 1 {
		t.Errorf("PDF/UA-1: H3 then H5 must be reported under 7.4.2, twice (first heading, skipped level), got %v", one)
	}
	for _, v := range validateView(mk(), "2") {
		if strings.HasPrefix(v.Clause, "7.4") {
			t.Errorf("PDF/UA-2 has no heading-sequence requirement, got %v", v)
		}
	}
}

// TestUAWeakHeadingRulesArePartOne: ISO 14289-1 7.4.4 constrains weakly
// structured documents — at most one <H> under a node, and <H> not mixed
// with <H1>-<H6>. ISO 14289-2 has no weakly structured documents: 8.2.5.12
// forbids <H> outright, which reports every <H> the 7.4.4 rules could fire
// on, and the veraPDF PDF/UA-2 profile has no rule but that one. So under
// PDF/UA-2 the document is reported under 8.2.5.12 alone.
func TestUAWeakHeadingRulesArePartOne(t *testing.T) {
	mk := func() core.View {
		doc := mkView(nil, nil)
		sect := heading(doc, 12, "Sect", heading(doc, 10, "H"), heading(doc, 11, "H"))
		headingDoc(doc, object.Array{sect, heading(doc, 13, "H1")})
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	one := validateView(mk(), "1")
	if len(findings(one, "7.4.4", "more than one child <H>")) != 1 || len(findings(one, "7.4.4", "mixes <H>")) != 1 {
		t.Errorf("PDF/UA-1: two <H> under one node, mixed with <H1>, must be reported under 7.4.4 twice, got %v", one)
	}
	two := validateView(mk(), "2")
	if got := findings(two, "7.4.4", ""); len(got) != 0 {
		t.Errorf("PDF/UA-2 has no 7.4.4, got %v", got)
	}
	if got := findings(two, "8.2.5.12", "H structure type"); len(got) != 2 {
		t.Errorf("PDF/UA-2: each <H> must be reported under 8.2.5.12, got %v", got)
	}
}

// TestUAUntaggedAnnotIsPartOne: ISO 14289-1 7.18.1 has every visible
// annotation in the structure tree; ISO 14289-2 8.9.2.2 lets any annotation be
// an artifact, and one not present in the structure tree is one, so a visible
// annotation with no /StructParent is a PDF/UA-1 finding only. The WTPDF
// example PHY-exam has such a widget (a tooltip button, /F 516), which
// veraPDF passes as PDF/UA-2 and reports as PDF/UA-1 (7.18.4).
func TestUAUntaggedAnnotIsPartOne(t *testing.T) {
	const msg = "annotation is not tagged"
	for _, st := range []object.Name{"Widget", "Square"} {
		doc := mkView(nil, nil)
		a := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Annot")},
			object.Entry{Key: "Subtype", Value: st},
			object.Entry{Key: "Contents", Value: str("a description")},
			object.Entry{Key: "TU", Value: str("a description")},
			object.Entry{Key: "F", Value: object.Integer(516)}, // Print | ToggleNoView: visible
		)
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: a}
		referenced(doc, 5)
		if got := findings(checkUAAnnotations(doc, nil, "1"), "7.18.1", msg); len(got) != 1 {
			t.Errorf("PDF/UA-1, untagged %s: want one finding, got %v", st, got)
		}
		if got := findings(checkUAAnnotations(doc, nil, "2"), "7.18.1", msg); len(got) != 0 {
			t.Errorf("PDF/UA-2, untagged %s: an annotation outside the structure tree is an artifact, got %v", st, got)
		}
	}
}

// tableDoc builds a Table of the given rows (each a list of cells) under a
// structure tree root carrying classMap, and returns the view.
func tableDoc(rows [][]*object.Dictionary, classMap *object.Dictionary) core.View {
	doc := mkView(nil, nil)
	var trs object.Array
	for _, r := range rows {
		var kids object.Array
		for _, c := range r {
			kids = append(kids, c)
		}
		trs = append(trs, object.NewDictionary(
			object.Entry{Key: "S", Value: object.Name("TR")},
			object.Entry{Key: "K", Value: kids},
		))
	}
	table := object.NewDictionary(
		object.Entry{Key: "S", Value: object.Name("Table")},
		object.Entry{Key: "K", Value: trs},
	)
	root := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("StructTreeRoot")},
		object.Entry{Key: "K", Value: table},
	)
	if classMap != nil {
		root.Set("ClassMap", classMap)
	}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
	cat := object.NewDictionary(object.Entry{Key: "StructTreeRoot", Value: object.IndirectRef{Number: 2}})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return doc
}

func tcell(s object.Name, entries ...object.Entry) *object.Dictionary {
	return object.NewDictionary(append([]object.Entry{{Key: "S", Value: s}}, entries...)...)
}

func tableAttr(entries ...object.Entry) *object.Dictionary {
	return object.NewDictionary(append([]object.Entry{{Key: "O", Value: object.Name("Table")}}, entries...)...)
}

// TestUATableAttributesFromClassMap: a cell's Table attributes are those in
// its /A and those of the classes its /C names in the /ClassMap (ISO 32000-2
// 14.7.6.2), /A taking precedence; veraPDF reads Scope, RowSpan, ColSpan and
// Headers that way (AttributeHelper.getAttributeValue). The WTPDF example
// amsldoc-tagged scopes every TH through /C /TH-col, which veraPDF passes in
// both parts.
func TestUATableAttributesFromClassMap(t *testing.T) {
	scopeCol := object.Entry{Key: "Scope", Value: object.Name("Column")}
	// amsldoc's shape: a data row above the header row, so a TD is left that
	// no header is above, and only the Scope makes the table determinable.
	rows := func(th func() *object.Dictionary) [][]*object.Dictionary {
		return [][]*object.Dictionary{
			{tcell("TD"), tcell("TD")},
			{th(), th()},
			{tcell("TD"), tcell("TD")},
		}
	}
	headerFindings := func(v core.View, part string) []string {
		return findings(checkUATableGrid(v, v.ResolveDict(object.IndirectRef{Number: 1}), part), "7.5", "")
	}
	cases := []struct {
		name     string
		th       func() *object.Dictionary
		classMap *object.Dictionary
		want     bool // the data cells are reported
	}{
		{"no Scope anywhere", func() *object.Dictionary { return tcell("TH", object.Entry{Key: "C", Value: object.Name("TH-col")}) }, nil, true},
		{"class names Scope", func() *object.Dictionary { return tcell("TH", object.Entry{Key: "C", Value: object.Name("TH-col")}) },
			object.NewDictionary(object.Entry{Key: "TH-col", Value: tableAttr(scopeCol)}), false},
		{"class array, attribute array", func() *object.Dictionary {
			return tcell("TH", object.Entry{Key: "C", Value: object.Array{object.Name("other"), object.Integer(0), object.Name("TH-col")}})
		}, object.NewDictionary(
			object.Entry{Key: "other", Value: object.NewDictionary(object.Entry{Key: "O", Value: object.Name("Layout")}, scopeCol)},
			object.Entry{Key: "TH-col", Value: object.Array{object.NewDictionary(object.Entry{Key: "O", Value: object.Name("Layout")}), tableAttr(scopeCol)}},
		), false},
		{"class owned by another owner", func() *object.Dictionary { return tcell("TH", object.Entry{Key: "C", Value: object.Name("TH-col")}) },
			object.NewDictionary(object.Entry{Key: "TH-col", Value: object.NewDictionary(object.Entry{Key: "O", Value: object.Name("Layout")}, scopeCol)}), true},
	}
	for _, c := range cases {
		for _, part := range []string{"1", "2"} {
			got := headerFindings(tableDoc(rows(c.th), c.classMap), part)
			if (len(got) > 0) != c.want {
				t.Errorf("%s, PDF/UA-%s: reported=%v, want %v (%v)", c.name, part, len(got) > 0, c.want, got)
			}
		}
	}

	// Spans come from the class too, and /A wins where both say.
	span2 := object.NewDictionary(object.Entry{Key: "span2", Value: tableAttr(object.Entry{Key: "RowSpan", Value: object.Integer(2)})})
	lastRow := func(cell *object.Dictionary) core.View {
		return tableDoc([][]*object.Dictionary{{tcell("TD"), tcell("TD")}, {tcell("TD"), cell}}, span2)
	}
	beyond := func(v core.View) bool {
		return len(findings(checkUATableGrid(v, v.ResolveDict(object.IndirectRef{Number: 1}), "2"), "7.2", "beyond the last row")) > 0
	}
	if !beyond(lastRow(tcell("TD", object.Entry{Key: "C", Value: object.Name("span2")}))) {
		t.Error("a RowSpan of 2 from the class map on a last-row cell is not reported")
	}
	if beyond(lastRow(tcell("TD", object.Entry{Key: "C", Value: object.Name("span2")}, object.Entry{Key: "A", Value: tableAttr(object.Entry{Key: "RowSpan", Value: object.Integer(1)})}))) {
		t.Error("the cell's /A RowSpan 1 must take precedence over its class's RowSpan 2")
	}
}

// TestUAMalformedXMPSaysWhy: a packet that is not well-formed XML is reported
// with the parser's reason, naming the line. The WTPDF example
// tb139mitt-deims24 has a U+0001 in a dc:creator item, which XML 1.0's Char
// production excludes; veraPDF's XMP parser replaces ASCII controls before
// parsing (ParseOptions FIX_CONTROL_CHARS, on by default) and so does not
// report it.
func TestUAMalformedXMPSaysWhy(t *testing.T) {
	const ns = ` xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/"`
	d, cat := uaXMPView(uaPacket(ns, "<pdfuaid:part>2</pdfuaid:part>\n<pdfuaid:rev>2024</pdfuaid:rev>\x01"))
	got := checkUAIdentifier(d, cat, "2")
	if len(got) != 1 || !strings.Contains(got[0].Message, "not well-formed") || !strings.Contains(got[0].Message, "U+0001") || !strings.Contains(got[0].Message, "line 2") {
		t.Errorf("want one finding naming U+0001 on line 2, got %v", got)
	}
}
