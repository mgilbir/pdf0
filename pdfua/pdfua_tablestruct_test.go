package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// buildContainer makes a StructTreeRoot whose single container element (type
// container) has the given ordered child types, and returns the catalog.
func buildContainer(container object.Name, childTypes ...object.Name) (core.View, *object.Dictionary) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	var kids object.Array
	n := 20
	for _, ct := range childTypes {
		c := &object.Dictionary{}
		c.Set("S", ct)
		doc.Objects[n] = &object.IndirectObject{Number: n, Value: c}
		kids = append(kids, object.IndirectRef{Number: n})
		n++
	}
	cont := &object.Dictionary{}
	cont.Set("S", container)
	cont.Set("K", kids)
	doc.Objects[10] = &object.IndirectObject{Number: 10, Value: cont}
	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	root.Set("K", object.IndirectRef{Number: 10})
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
	cat := &object.Dictionary{}
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	return doc, cat
}

func TestUATableStructure(t *testing.T) {
	cases := []struct {
		name      string
		container object.Name
		kids      []object.Name
		wantSub   string // "" means expect no violation
	}{
		{"two captions", "Table", []object.Name{"Caption", "TR", "Caption"}, "more than one Caption"},
		{"caption middle", "Table", []object.Name{"TR", "Caption", "TR"}, "first or last child"},
		{"caption first ok", "Table", []object.Name{"Caption", "TR"}, ""},
		{"caption last ok", "Table", []object.Name{"TR", "Caption"}, ""},
		{"two thead", "Table", []object.Name{"THead", "THead", "TBody"}, "more than one THead"},
		{"two tfoot", "Table", []object.Name{"TBody", "TFoot", "TFoot"}, "more than one TFoot"},
		{"thead no tbody", "Table", []object.Name{"THead", "TR"}, "no TBody"},
		{"thead with tbody ok", "Table", []object.Name{"THead", "TBody"}, ""},
		{"plain rows ok", "Table", []object.Name{"TR", "TR"}, ""},
		{"list two captions", "L", []object.Name{"Caption", "LI", "Caption"}, "more than one Caption"},
		{"list caption not first", "L", []object.Name{"LI", "Caption"}, "first child"},
		{"list caption first ok", "L", []object.Name{"Caption", "LI"}, ""},
		{"toc caption last", "TOC", []object.Name{"TOCI", "Caption"}, "first child"},
		{"toc caption first ok", "TOC", []object.Name{"Caption", "TOCI"}, ""},
	}
	for _, c := range cases {
		doc, cat := buildContainer(c.container, c.kids...)
		v := checkUATableListStructure(doc, cat)
		got := ""
		if len(v) > 0 {
			got = v[0].Message
		}
		if c.wantSub == "" {
			if len(v) != 0 {
				t.Errorf("%s: expected clean, got %v", c.name, v)
			}
		} else if !strings.Contains(got, c.wantSub) {
			t.Errorf("%s: expected a violation containing %q, got %v", c.name, c.wantSub, v)
		}
	}
}
