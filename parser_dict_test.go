package pdf0

import (
	"fmt"
	"github.com/mgilbir/pdf0/object"
	"slices"
	"strings"
	"testing"
)

// TestParseDictLargeDedup verifies that the parser resolves duplicate keys
// exactly as successive Dictionary.Set calls do — same key order (first
// occurrence) and same values (last occurrence wins) — for a dictionary well
// past the size at which Dictionary keeps an index, with duplicate keys both
// before and after that size is reached.
func TestParseDictLargeDedup(t *testing.T) {
	type kv struct {
		k string
		v int
	}
	var seq []kv
	for i := 0; i < 200; i++ { // 200 keys → forces the map path (threshold 64)
		seq = append(seq, kv{fmt.Sprintf("K%d", i), i})
	}
	// A duplicate while still in the linear phase (position 5 < 64) and one in
	// the map phase (position 150 > 64); both must keep their first position and
	// take the new value.
	seq = append(seq, kv{"K5", 5000}, kv{"K150", 150000})

	var b strings.Builder
	b.WriteString("<<")
	for _, e := range seq {
		fmt.Fprintf(&b, " /%s %d", e.k, e.v)
	}
	b.WriteString(" >>")

	obj, err := NewParser([]byte(b.String())).ParseObject()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := obj.(*object.Dictionary)
	if !ok {
		t.Fatalf("parsed %T, want *Dictionary", obj)
	}

	// Reference dictionary built purely with Set (the pre-fix behavior).
	ref := &object.Dictionary{}
	for _, e := range seq {
		ref.Set(object.Name(e.k), object.Integer(e.v))
	}

	if got.Len() != ref.Len() {
		t.Fatalf("parsed %d keys, Set reference has %d", got.Len(), ref.Len())
	}
	gotKeys, refKeys := slices.Collect(got.Keys()), slices.Collect(ref.Keys())
	for i := range refKeys {
		if gotKeys[i] != refKeys[i] {
			t.Errorf("key[%d] = %q, reference %q", i, gotKeys[i], refKeys[i])
		}
		if got.Get(refKeys[i]) != ref.Get(refKeys[i]) {
			t.Errorf("value for %q = %v, reference %v", refKeys[i], got.Get(refKeys[i]), ref.Get(refKeys[i]))
		}
	}
	// Spot-check the deduped values directly.
	if got.Get("K5") != object.Integer(5000) {
		t.Errorf("K5 = %v, want 5000 (last value)", got.Get("K5"))
	}
	if got.Get("K150") != object.Integer(150000) {
		t.Errorf("K150 = %v, want 150000 (last value)", got.Get("K150"))
	}
	if got.Get("K42") != object.Integer(42) {
		t.Errorf("K42 = %v, want 42", got.Get("K42"))
	}
}
