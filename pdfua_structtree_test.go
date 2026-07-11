package pdf0

import "testing"

// TestStructTreeFlatten checks that the cached flattened structure tree visits
// every reachable structure element exactly once — descending through arrays
// and single /K links, deduping indirect references so a cycle terminates — and
// records the fields the per-check walks rely on (raw and role-map-resolved
// types, and each node's ordered child types). It also verifies that the result
// is memoized across calls and that walkStructElems yields the /S nodes in the
// same pre-order.
func TestStructTreeFlatten(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	put := func(num int, v Object) { doc.Objects[num] = &IndirectObject{Number: num, Value: v} }
	elem := func(num int, s Name, k Object) {
		d := &Dictionary{}
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
	elem(10, "Sect", Array{IndirectRef{Number: 20}, IndirectRef{Number: 21}})
	elem(11, "MyPara", IndirectRef{Number: 21}) // non-standard type, mapped to P
	// Node 12 references node 10 again (already visited) -> must be deduped, no loop.
	elem(12, "Div", Array{IndirectRef{Number: 10}})
	elem(1, "Document", Array{IndirectRef{Number: 10}, IndirectRef{Number: 11}, IndirectRef{Number: 12}})

	root := &Dictionary{}
	root.Set("Type", Name("StructTreeRoot"))
	root.Set("K", IndirectRef{Number: 1})
	roleMap := &Dictionary{}
	roleMap.Set("MyPara", Name("P"))
	root.Set("RoleMap", roleMap)
	put(2, root)
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("StructTreeRoot", IndirectRef{Number: 2})
	put(3, cat)
	doc.Trailer.Set("Root", IndirectRef{Number: 3})

	// Install a validation cache so structTree memoizes.
	doc.valCache = &validationCache{}
	nodes := doc.structTree(cat)

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
	want := []Name{"Sect", "P", "Div"}
	if len(got) != len(want) {
		t.Fatalf("Document childTypes=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Document childTypes[%d]=%q, want %q", i, got[i], want[i])
		}
	}

	// Memoization: a second call returns the identical backing slice.
	if again := doc.structTree(cat); &again[0] != &nodes[0] {
		t.Error("structTree not memoized (returned a fresh slice)")
	}

	// walkStructElems must visit exactly the /S nodes, in the same order.
	var walked []int
	doc.walkStructElems(cat, func(e *Dictionary, _ Name) {
		walked = append(walked, doc.dictObjNum(e))
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
