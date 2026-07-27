package pdf0

import "testing"

// TestParserRejectsNestedIndirectObject is the C31 guard: an indirect object
// DEFINITION ("N G obj … endobj") is valid only at the top level; appearing as a
// nested array element or dictionary value is malformed and must be rejected, not
// silently built into the object graph.
func TestParserRejectsNestedIndirectObject(t *testing.T) {
	if _, err := NewParser([]byte("<< /K 1 0 obj (x) endobj >>")).ParseObject(); err == nil {
		t.Error("nested 'N G obj' as a dictionary value must be rejected")
	}
	if _, err := NewParser([]byte("[ 1 0 obj (x) endobj ]")).ParseObject(); err == nil {
		t.Error("nested 'N G obj' as an array element must be rejected")
	}

	// A top-level definition still parses.
	if _, err := NewParser([]byte("1 0 obj (x) endobj")).ParseIndirectObject(); err != nil {
		t.Errorf("top-level 'N G obj' should still parse: %v", err)
	}
	// A normal indirect reference in an array is unaffected.
	obj, err := NewParser([]byte("[ 1 0 R ]")).ParseObject()
	if err != nil {
		t.Fatalf("indirect reference should parse: %v", err)
	}
	arr, ok := obj.(Array)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected a one-element array, got %T", obj)
	}
	if _, ok := arr[0].(IndirectRef); !ok {
		t.Fatalf("expected an IndirectRef element, got %T", arr[0])
	}
}

// TestIntRealEqualRelative is the C32 guard: cross-type Integer/Real equality
// uses a relative tolerance, so it is not spuriously true near zero nor
// spuriously false at large magnitudes.
func TestIntRealEqualRelative(t *testing.T) {
	if Equal(Integer(0), Real(1e-11)) {
		t.Error("Integer(0) must not equal Real(1e-11)")
	}
	if !Equal(Integer(1), Real(1.0)) {
		t.Error("Integer(1) should equal Real(1.0)")
	}
	if !Equal(Integer(1_000_000), Real(1_000_000.0)) {
		t.Error("Integer(1e6) should equal Real(1e6)")
	}
	// Serializer rounding noise on a whole number is tolerated (relative).
	if !Equal(Integer(1_000_000), Real(1_000_000.00001)) {
		t.Error("Integer(1e6) should equal Real(1000000.00001) within relative tolerance")
	}
	// Genuinely different values remain unequal.
	if Equal(Integer(1_000_000), Real(1_000_001)) {
		t.Error("Integer(1e6) must not equal Real(1000001)")
	}
}
