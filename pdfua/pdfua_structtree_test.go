package pdfua

import (
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestStructTreeFlatten checks that the cached flattened structure tree visits
// every reachable structure element exactly once — descending through arrays
// and single /K links, deduping indirect references so a cycle terminates — and
// records the fields the per-check walks rely on (raw and role-map-resolved
// types, and each node's ordered child types). It also verifies that the result
// is memoized across calls and that walkStructElems yields the /S nodes in the
// same pre-order.
func TestStructTreeFlatten(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	put := func(num int, v object.Object) { doc.Objects[num] = &object.IndirectObject{Number: num, Value: v} }
	elem := func(num int, s object.Name, k object.Object) {
		d := &object.Dictionary{}
		if s != "" {
			d.Set("S", s)
		}
		if k != nil {
			d.Set("K", k)
		}
		put(num, d)
	}

	// Tree: Document -> [Sect -> (H1, P), Custom(->P via RoleMap), <cycle back to Sect>]
	elem(20, "H1", nil)
	elem(21, "P", nil)
	elem(10, "Sect", object.Array{object.IndirectRef{Number: 20}, object.IndirectRef{Number: 21}})
	elem(11, "MyPara", object.IndirectRef{Number: 21}) // non-standard type, mapped to P
	// Node 12 references node 10 again (already visited) -> must be deduped, no loop.
	elem(12, "Div", object.Array{object.IndirectRef{Number: 10}})
	elem(1, "Document", object.Array{object.IndirectRef{Number: 10}, object.IndirectRef{Number: 11}, object.IndirectRef{Number: 12}})

	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	root.Set("K", object.IndirectRef{Number: 1})
	roleMap := &object.Dictionary{}
	roleMap.Set("MyPara", object.Name("P"))
	root.Set("RoleMap", roleMap)
	put(2, root)
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
	put(3, cat)
	doc.Trailer.Set("Root", object.IndirectRef{Number: 3})

	// Install a validation cache so structTree memoizes.
	nodes := structTree(doc, cat)

	// Expected pre-order object numbers: Document(1), Sect(10), H1(20), P(21),
	// MyPara(11) [P(21) already seen -> not revisited], Div(12) [Sect(10) already
	// seen -> not revisited].
	wantOrder := []int{1, 10, 20, 21, 11, 12}
	if len(nodes) != len(wantOrder) {
		t.Fatalf("visited %d nodes, want %d: %+v", len(nodes), len(wantOrder), nodes)
	}
	for i, want := range wantOrder {
		if nodes[i].objNum != want {
			t.Errorf("node %d objNum=%d, want %d", i, nodes[i].objNum, want)
		}
	}

	// Role-map resolution: MyPara -> P as stdType, but rawS stays MyPara.
	myPara := nodes[4]
	if myPara.rawS != "MyPara" || myPara.stdType != "P" {
		t.Errorf("MyPara node rawS=%q stdType=%q, want MyPara/P", myPara.rawS, myPara.stdType)
	}

	// childTypes of the Document node (index 1): its /S children are Sect, MyPara(->P), Div.
	docNode := nodes[0]
	got := docNode.childTypes
	want := []object.Name{"Sect", "P", "Div"}
	if len(got) != len(want) {
		t.Fatalf("Document childTypes=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Document childTypes[%d]=%q, want %q", i, got[i], want[i])
		}
	}

	// Memoization: a second call returns the identical backing slice.
	if again := structTree(doc, cat); &again[0] != &nodes[0] {
		t.Error("structTree not memoized (returned a fresh slice)")
	}

	// walkStructElems must visit exactly the /S nodes, in the same order.
	var walked []int
	walkStructElems(doc, cat, func(e *object.Dictionary, _ object.Name) {
		walked = append(walked, doc.DictObjNum(e))
	})
	if len(walked) != len(wantOrder) {
		t.Fatalf("walkStructElems visited %d, want %d", len(walked), len(wantOrder))
	}
	for i, want := range wantOrder {
		if walked[i] != want {
			t.Errorf("walkStructElems[%d]=%d, want %d", i, walked[i], want)
		}
	}
}
