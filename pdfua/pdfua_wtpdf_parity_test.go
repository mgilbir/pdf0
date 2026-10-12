package pdfua

// Rules the WTPDF examples (real PDF/UA-2 documents veraPDF passes) showed
// pdf0 applying the wrong way: attributes read from /A but not the class map.

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

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
