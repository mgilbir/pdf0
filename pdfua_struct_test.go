package pdf0

import (
	"github.com/mgilbir/pdf0/internal/core"
	"testing"
	"time"
)

// TestUAStructNesting flags a misplaced table cell and accepts a well-formed
// table structure.
func TestUAStructNesting(t *testing.T) {
	// Build: StructTreeRoot -> Table -> kids. In the bad case a TD hangs directly
	// off the Table; in the good case Table -> TR -> TD.
	mk := func(good bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		elem := func(num int, s Name, kids Array) {
			d := &Dictionary{}
			d.Set("S", s)
			if kids != nil {
				d.Set("K", kids)
			}
			doc.Objects[num] = &IndirectObject{Number: num, Value: d}
		}
		if good {
			elem(12, "TD", nil)
			elem(11, "TR", Array{IndirectRef{Number: 12}})
			elem(10, "Table", Array{IndirectRef{Number: 11}})
		} else {
			elem(12, "TD", nil)
			elem(10, "Table", Array{IndirectRef{Number: 12}}) // TD directly under Table
		}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", IndirectRef{Number: 10})
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	bad := mk(false)
	if len(checkUAStructNesting(bad.view(), bad.ResolveDict(bad.Trailer.Get("Root")))) == 0 {
		t.Error("TD directly under Table not flagged")
	}
	good := mk(true)
	if v := checkUAStructNesting(good.view(), good.ResolveDict(good.Trailer.Get("Root"))); len(v) != 0 {
		t.Errorf("well-formed table flagged: %v", v)
	}
}

// TestUAHeaderVersion flags a 2.0 header and accepts a 1.x one.
func TestUAHeaderVersion(t *testing.T) {
	d := &Document{Version: "2.0"}
	if len(checkUAHeaderVersion(d.view())) == 0 {
		t.Error("2.0 header not flagged for PDF/UA-1")
	}
	d.Version = "1.7"
	if len(checkUAHeaderVersion(d.view())) != 0 {
		t.Error("1.7 header wrongly flagged")
	}
}

// TestUASuspects flags /MarkInfo /Suspects true.
func TestUASuspects(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	mark := &Dictionary{}
	mark.Set("Suspects", Boolean(true))
	cat.Set("MarkInfo", mark)
	if len(checkUASuspects(doc.view(), cat)) == 0 {
		t.Error("Suspects true not flagged")
	}
	mark.Set("Suspects", Boolean(false))
	if len(checkUASuspects(doc.view(), cat)) != 0 {
		t.Error("Suspects false wrongly flagged")
	}
}

// TestUAStrongWeak flags a document mixing H and Hn headings.
func TestUAStrongWeak(t *testing.T) {
	mk := func(types ...Name) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		var kids Array
		n := 10
		for _, ty := range types {
			e := &Dictionary{}
			e.Set("S", ty)
			doc.Objects[n] = &IndirectObject{Number: n, Value: e}
			kids = append(kids, IndirectRef{Number: n})
			n++
		}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", kids)
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat := &Dictionary{}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk("H", "H1"); len(checkUAStrongWeak(d.view(), d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("mixed H/H1 not flagged")
	}
	if d := mk("H1", "H2"); len(checkUAStrongWeak(d.view(), d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("pure strong structure wrongly flagged")
	}
	if d := mk("H", "H"); len(checkUAStrongWeak(d.view(), d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("pure weak structure wrongly flagged")
	}
}

// TestUANotes flags Note elements lacking IDs or sharing an ID.
func TestUANotes(t *testing.T) {
	mk := func(ids ...string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		var kids Array
		n := 10
		for _, id := range ids {
			e := &Dictionary{}
			e.Set("S", Name("Note"))
			if id != "" {
				e.Set("ID", String{Value: []byte(id)})
			}
			doc.Objects[n] = &IndirectObject{Number: n, Value: e}
			kids = append(kids, IndirectRef{Number: n})
			n++
		}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", kids)
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat := &Dictionary{}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk(""); len(checkUANotes(d.view(), d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("Note without ID not flagged")
	}
	if d := mk("a", "a"); len(checkUANotes(d.view(), d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("duplicate Note ID not flagged")
	}
	if d := mk("a", "b"); len(checkUANotes(d.view(), d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("unique Note IDs wrongly flagged")
	}
}

// roleMapChainDoc builds a document whose structure tree is
// StructTreeRoot -> MyTable -> MyRow -> TD, with the custom types reaching their
// standard types through the supplied /RoleMap.
func roleMapChainDoc(roleMap *Dictionary) *Document {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	elem := func(num int, s Name, kids Array) {
		d := &Dictionary{}
		d.Set("S", s)
		if kids != nil {
			d.Set("K", kids)
		}
		doc.Objects[num] = &IndirectObject{Number: num, Value: d}
	}
	elem(12, "TD", nil)
	elem(11, "MyRow", Array{IndirectRef{Number: 12}})
	elem(10, "MyTable", Array{IndirectRef{Number: 11}})

	root := &Dictionary{}
	root.Set("Type", Name("StructTreeRoot"))
	root.Set("K", IndirectRef{Number: 10})
	root.Set("RoleMap", roleMap)
	doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("StructTreeRoot", IndirectRef{Number: 2})
	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})
	doc.valCache = newValidationCache(core.Canceler{})
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
	rm := &Dictionary{}
	rm.Set("MyTable", Name("TableBase"))
	rm.Set("TableBase", Name("Table"))
	rm.Set("MyRow", Name("RowBase"))
	rm.Set("RowBase", Name("TR"))
	doc := roleMapChainDoc(rm)
	cat := doc.ResolveDict(IndirectRef{Number: 1})

	if v := checkUARoleMap(doc.view(), cat); len(v) != 0 {
		t.Errorf("two-step /RoleMap chain reported as unmapped: %+v", v)
	}
	if got := standardStructType(doc.view(), doc.ResolveDict(IndirectRef{Number: 10}), rm); got != "Table" {
		t.Errorf("standardStructType(MyTable) = %q, want Table", got)
	}
	if got := standardStructType(doc.view(), doc.ResolveDict(IndirectRef{Number: 11}), rm); got != "TR" {
		t.Errorf("standardStructType(MyRow) = %q, want TR", got)
	}
	// The nesting rules see Table -> TR -> TD, so a conformant tree is clean.
	if v := checkUAStructNesting(doc.view(), cat); len(v) != 0 {
		t.Errorf("nesting findings on a tree whose types resolve through a chain: %+v", v)
	}
}

// TestRoleMapChainTerminates requires a cyclic role map to end the walk and
// still report the types as unmapped: following chains must not trade a
// single-hop false positive for a hang.
func TestRoleMapChainTerminates(t *testing.T) {
	rm := &Dictionary{}
	rm.Set("MyTable", Name("MyRow"))
	rm.Set("MyRow", Name("MyTable")) // a two-key cycle reaching no standard type
	doc := roleMapChainDoc(rm)
	cat := doc.ResolveDict(IndirectRef{Number: 1})

	done := make(chan []UAViolation, 1)
	go func() { done <- checkUARoleMap(doc.view(), cat) }()
	select {
	case v := <-done:
		if len(v) != 2 {
			t.Errorf("cyclic /RoleMap: got %d findings, want one per unmapped type: %+v", len(v), v)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("checkUARoleMap did not terminate on a cyclic /RoleMap")
	}
	// A type mapping to itself is a cycle of length one.
	self := &Dictionary{}
	self.Set("MyTable", Name("MyTable"))
	sdoc := roleMapChainDoc(self)
	if got := standardStructType(sdoc.view(), sdoc.ResolveDict(IndirectRef{Number: 10}), self); got != "MyTable" {
		t.Errorf("self-mapping type resolved to %q, want the raw type back", got)
	}
}

// TestRoleMapChainBudgetDeclines pins the incomplete-result rule for this walk:
// when the /RoleMap step budget stops the chain the mapping is unknown, so the
// checker declines to report "neither standard nor mapped" rather than
// manufacturing a finding out of a truncated answer.
func TestRoleMapChainBudgetDeclines(t *testing.T) {
	rm := &Dictionary{}
	rm.Set("MyTable", Name("TableBase"))
	rm.Set("TableBase", Name("Table"))
	rm.Set("MyRow", Name("RowBase"))
	rm.Set("RowBase", Name("TR"))
	doc := roleMapChainDoc(rm)
	doc.limits.RoleMapSteps = 1 // room for the first hop only
	cat := doc.ResolveDict(IndirectRef{Number: 1})
	if v := checkUARoleMap(doc.view(), cat); len(v) != 0 {
		t.Errorf("budget trip manufactured a role-map finding: %+v", v)
	}
}
