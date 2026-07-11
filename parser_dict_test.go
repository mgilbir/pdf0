package pdf0

import (
	"fmt"
	"strings"
	"testing"
)

// TestParseDictLargeDedup verifies that the map-backed key indexing used for
// large dictionaries produces exactly the same result as the linear
// Dictionary.Set path — same key order (first occurrence) and same values (last
// occurrence wins) — for a dictionary well past dictIndexThreshold, with
// duplicate keys in both the linear phase and the map phase.
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
	got, ok := obj.(*Dictionary)
	if !ok {
		t.Fatalf("parsed %T, want *Dictionary", obj)
	}

	// Reference dictionary built purely with Set (the pre-fix behavior).
	ref := &Dictionary{}
	for _, e := range seq {
		ref.Set(Name(e.k), Integer(e.v))
	}

	if len(got.Keys) != len(ref.Keys) {
		t.Fatalf("parsed %d keys, Set reference has %d", len(got.Keys), len(ref.Keys))
	}
	for i := range ref.Keys {
		if got.Keys[i] != ref.Keys[i] {
			t.Errorf("key[%d] = %q, reference %q", i, got.Keys[i], ref.Keys[i])
		}
		if got.Values[i] != ref.Values[i] {
			t.Errorf("value for %q = %v, reference %v", got.Keys[i], got.Values[i], ref.Values[i])
		}
	}
	// Spot-check the deduped values directly.
	if got.Get("K5") != Integer(5000) {
		t.Errorf("K5 = %v, want 5000 (last value)", got.Get("K5"))
	}
	if got.Get("K150") != Integer(150000) {
		t.Errorf("K150 = %v, want 150000 (last value)", got.Get("K150"))
	}
	if got.Get("K42") != Integer(42) {
		t.Errorf("K42 = %v, want 42", got.Get("K42"))
	}
}
