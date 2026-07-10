package pdf0

import (
	"strings"
	"testing"
)

// buildContainer makes a StructTreeRoot whose single container element (type
// container) has the given ordered child types, and returns the catalog.
func buildContainer(container Name, childTypes ...Name) (*Document, *Dictionary) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	var kids Array
	n := 20
	for _, ct := range childTypes {
		c := &Dictionary{}
		c.Set("S", ct)
		doc.Objects[n] = &IndirectObject{Number: n, Value: c}
		kids = append(kids, IndirectRef{Number: n})
		n++
	}
	cont := &Dictionary{}
	cont.Set("S", container)
	cont.Set("K", kids)
	doc.Objects[10] = &IndirectObject{Number: 10, Value: cont}
	root := &Dictionary{}
	root.Set("Type", Name("StructTreeRoot"))
	root.Set("K", IndirectRef{Number: 10})
	doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
	cat := &Dictionary{}
	cat.Set("StructTreeRoot", IndirectRef{Number: 2})
	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	return doc, cat
}

func TestUATableStructure(t *testing.T) {
	cases := []struct {
		name      string
		container Name
		kids      []Name
		wantSub   string // "" means expect no violation
	}{
		{"two captions", "Table", []Name{"Caption", "TR", "Caption"}, "more than one Caption"},
		{"caption middle", "Table", []Name{"TR", "Caption", "TR"}, "first or last child"},
		{"caption first ok", "Table", []Name{"Caption", "TR"}, ""},
		{"caption last ok", "Table", []Name{"TR", "Caption"}, ""},
		{"two thead", "Table", []Name{"THead", "THead", "TBody"}, "more than one THead"},
		{"two tfoot", "Table", []Name{"TBody", "TFoot", "TFoot"}, "more than one TFoot"},
		{"thead no tbody", "Table", []Name{"THead", "TR"}, "no TBody"},
		{"thead with tbody ok", "Table", []Name{"THead", "TBody"}, ""},
		{"plain rows ok", "Table", []Name{"TR", "TR"}, ""},
		{"list two captions", "L", []Name{"Caption", "LI", "Caption"}, "more than one Caption"},
		{"list caption not first", "L", []Name{"LI", "Caption"}, "first child"},
		{"list caption first ok", "L", []Name{"Caption", "LI"}, ""},
		{"toc caption last", "TOC", []Name{"TOCI", "Caption"}, "first child"},
		{"toc caption first ok", "TOC", []Name{"Caption", "TOCI"}, ""},
	}
	for _, c := range cases {
		doc, cat := buildContainer(c.container, c.kids...)
		v := doc.checkUATableListStructure(cat)
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
