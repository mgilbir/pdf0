package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
	"time"
)

// TestUAStructNesting flags a misplaced table cell and accepts a well-formed
// table structure.
func TestUAStructNesting(t *testing.T) {
	// Build: StructTreeRoot -> Table -> kids. In the bad case a TD hangs directly
	// off the Table; in the good case Table -> TR -> TD.
	mk := func(good bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		elem := func(num int, s object.Name, kids object.Array) {
			d := &object.Dictionary{}
			d.Set("S", s)
			if kids != nil {
				d.Set("K", kids)
			}
			doc.Objects[num] = &object.IndirectObject{Number: num, Value: d}
		}
		if good {
			elem(12, "TD", nil)
			elem(11, "TR", object.Array{object.IndirectRef{Number: 12}})
			elem(10, "Table", object.Array{object.IndirectRef{Number: 11}})
		} else {
			elem(12, "TD", nil)
			elem(10, "Table", object.Array{object.IndirectRef{Number: 12}}) // TD directly under Table
		}
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", object.IndirectRef{Number: 10})
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	bad := mk(false)
	if len(checkUAStructNesting(bad, bad.ResolveDict(bad.Trailer.Get("Root")))) == 0 {
		t.Error("TD directly under Table not flagged")
	}
	good := mk(true)
	if v := checkUAStructNesting(good, good.ResolveDict(good.Trailer.Get("Root"))); len(v) != 0 {
		t.Errorf("well-formed table flagged: %v", v)
	}
}

// TestUAHeaderVersion flags a 2.0 header and accepts a 1.x one.
func TestUAHeaderVersion(t *testing.T) {
	d := mkViewVersion(nil, object.Dictionary{}, "2.0")
	if len(checkUAHeaderVersion(d)) == 0 {
		t.Error("2.0 header not flagged for PDF/UA-1")
	}
	d.Version = "1.7"
	if len(checkUAHeaderVersion(d)) != 0 {
		t.Error("1.7 header wrongly flagged")
	}
}

// TestUASuspects flags /MarkInfo /Suspects true.
func TestUASuspects(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	cat := &object.Dictionary{}
	mark := &object.Dictionary{}
	mark.Set("Suspects", object.Boolean(true))
	cat.Set("MarkInfo", mark)
	if len(checkUASuspects(doc, cat)) == 0 {
		t.Error("Suspects true not flagged")
	}
	mark.Set("Suspects", object.Boolean(false))
	if len(checkUASuspects(doc, cat)) != 0 {
		t.Error("Suspects false wrongly flagged")
	}
}

// TestUAStrongWeak flags a document mixing H and Hn headings.
func TestUAStrongWeak(t *testing.T) {
	mk := func(types ...object.Name) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		var kids object.Array
		n := 10
		for _, ty := range types {
			e := &object.Dictionary{}
			e.Set("S", ty)
			doc.Objects[n] = &object.IndirectObject{Number: n, Value: e}
			kids = append(kids, object.IndirectRef{Number: n})
			n++
		}
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", kids)
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat := &object.Dictionary{}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk("H", "H1"); len(checkUAStrongWeak(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("mixed H/H1 not flagged")
	}
	if d := mk("H1", "H2"); len(checkUAStrongWeak(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("pure strong structure wrongly flagged")
	}
	if d := mk("H", "H"); len(checkUAStrongWeak(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("pure weak structure wrongly flagged")
	}
}

// TestUANotes flags Note elements lacking IDs or sharing an ID.
func TestUANotes(t *testing.T) {
	mk := func(ids ...string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		var kids object.Array
		n := 10
		for _, id := range ids {
			e := &object.Dictionary{}
			e.Set("S", object.Name("Note"))
			if id != "" {
				e.Set("ID", object.String{Value: []byte(id)})
			}
			doc.Objects[n] = &object.IndirectObject{Number: n, Value: e}
			kids = append(kids, object.IndirectRef{Number: n})
			n++
		}
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", kids)
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat := &object.Dictionary{}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk(""); len(checkUANotes(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("Note without ID not flagged")
	}
	if d := mk("a", "a"); len(checkUANotes(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("duplicate Note ID not flagged")
	}
	if d := mk("a", "b"); len(checkUANotes(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("unique Note IDs wrongly flagged")
	}
}

// roleMapChainDoc builds a document whose structure tree is
// StructTreeRoot -> MyTable -> MyRow -> TD, with the custom types reaching their
// standard types through the supplied /RoleMap.
func roleMapChainDoc(roleMap *object.Dictionary) core.View {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	elem := func(num int, s object.Name, kids object.Array) {
		d := &object.Dictionary{}
		d.Set("S", s)
		if kids != nil {
			d.Set("K", kids)
		}
		doc.Objects[num] = &object.IndirectObject{Number: num, Value: d}
	}
	elem(12, "TD", nil)
	elem(11, "MyRow", object.Array{object.IndirectRef{Number: 12}})
	elem(10, "MyTable", object.Array{object.IndirectRef{Number: 11}})

	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	root.Set("K", object.IndirectRef{Number: 10})
	root.Set("RoleMap", roleMap)
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return doc
}

// TestRoleMapChainResolves pins that /RoleMap resolution follows a chain rather
// than a single hop. A role map may reach a standard type through intermediate
// custom types (MyPara -> Para -> P is legal, ISO 32000-1 14.7.3), and stopping
// after one hop declared the type unmapped: it fired 7.1 "neither standard nor
// mapped in /RoleMap" and then, because every dependent rule saw the raw type,
// a spray of 7.2 nesting findings on a conformant tree.
func TestRoleMapChainResolves(t *testing.T) {
	// MyTable -> TableBase -> Table and MyRow -> RowBase -> TR: two hops each.
	rm := &object.Dictionary{}
	rm.Set("MyTable", object.Name("TableBase"))
	rm.Set("TableBase", object.Name("Table"))
	rm.Set("MyRow", object.Name("RowBase"))
	rm.Set("RowBase", object.Name("TR"))
	doc := roleMapChainDoc(rm)
	cat := doc.ResolveDict(object.IndirectRef{Number: 1})

	if v := checkUARoleMap(doc, cat); len(v) != 0 {
		t.Errorf("two-step /RoleMap chain reported as unmapped: %+v", v)
	}
	if got := standardStructType(doc, doc.ResolveDict(object.IndirectRef{Number: 10}), rm); got != "Table" {
		t.Errorf("standardStructType(MyTable) = %q, want Table", got)
	}
	if got := standardStructType(doc, doc.ResolveDict(object.IndirectRef{Number: 11}), rm); got != "TR" {
		t.Errorf("standardStructType(MyRow) = %q, want TR", got)
	}
	// The nesting rules see Table -> TR -> TD, so a conformant tree is clean.
	if v := checkUAStructNesting(doc, cat); len(v) != 0 {
		t.Errorf("nesting findings on a tree whose types resolve through a chain: %+v", v)
	}
}

// TestRoleMapChainTerminates requires a cyclic role map to end the walk and
// still report the types as unmapped: following chains must not trade a
// single-hop false positive for a hang.
func TestRoleMapChainTerminates(t *testing.T) {
	rm := &object.Dictionary{}
	rm.Set("MyTable", object.Name("MyRow"))
	rm.Set("MyRow", object.Name("MyTable")) // a two-key cycle reaching no standard type
	doc := roleMapChainDoc(rm)
	cat := doc.ResolveDict(object.IndirectRef{Number: 1})

	done := make(chan []Violation, 1)
	go func() { done <- checkUARoleMap(doc, cat) }()
	select {
	case v := <-done:
		if len(v) != 2 {
			t.Errorf("cyclic /RoleMap: got %d findings, want one per unmapped type: %+v", len(v), v)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("checkUARoleMap did not terminate on a cyclic /RoleMap")
	}
	// A type mapping to itself is a cycle of length one.
	self := &object.Dictionary{}
	self.Set("MyTable", object.Name("MyTable"))
	sdoc := roleMapChainDoc(self)
	if got := standardStructType(sdoc, sdoc.ResolveDict(object.IndirectRef{Number: 10}), self); got != "MyTable" {
		t.Errorf("self-mapping type resolved to %q, want the raw type back", got)
	}
}

// TestRoleMapChainBudgetDeclines pins the incomplete-result rule for this walk:
// when the /RoleMap step budget stops the chain the mapping is unknown, so the
// checker declines to report "neither standard nor mapped" rather than
// manufacturing a finding out of a truncated answer.
func TestRoleMapChainBudgetDeclines(t *testing.T) {
	rm := &object.Dictionary{}
	rm.Set("MyTable", object.Name("TableBase"))
	rm.Set("TableBase", object.Name("Table"))
	rm.Set("MyRow", object.Name("RowBase"))
	rm.Set("RowBase", object.Name("TR"))
	doc := roleMapChainDoc(rm)
	doc.Limits.RoleMapSteps = 1 // room for the first hop only
	cat := doc.ResolveDict(object.IndirectRef{Number: 1})
	if v := checkUARoleMap(doc, cat); len(v) != 0 {
		t.Errorf("budget trip manufactured a role-map finding: %+v", v)
	}
}
