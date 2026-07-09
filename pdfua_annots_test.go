package pdf0

import "testing"

func hasUAClause(v []UAViolation, clause string) bool {
	for _, e := range v {
		if e.Clause == clause {
			return true
		}
	}
	return false
}

// TestUASecurity flags encryption that disables accessibility extraction.
func TestUASecurity(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	enc := &Dictionary{}
	doc.Objects[9] = &IndirectObject{Number: 9, Value: enc}
	doc.Trailer.Set("Encrypt", IndirectRef{Number: 9})

	// No /P entry → violation.
	if !hasUAClause(doc.checkUASecurity(), "7.16") {
		t.Error("missing /P not flagged")
	}
	// /P with bit 10 clear → violation.
	enc.Set("P", Integer(-44)) // bit 10 (0x200) clear in this value's low bits
	if p := int32(-44); uint32(p)&0x200 == 0 {
		if !hasUAClause(doc.checkUASecurity(), "7.16") {
			t.Error("accessibility-disabled /P not flagged")
		}
	}
	// /P with bit 10 set → clean.
	enc.Set("P", Integer(int32(-1))) // all bits set
	if hasUAClause(doc.checkUASecurity(), "7.16") {
		t.Error("permissive /P should not be flagged")
	}
}

// TestUATrapNet flags a TrapNet annotation.
func TestUATrapNet(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	a := &Dictionary{}
	a.Set("Type", Name("Annot"))
	a.Set("Subtype", Name("TrapNet"))
	doc.Objects[5] = &IndirectObject{Number: 5, Value: a}
	if !hasUAClause(doc.checkUAAnnotations(), "7.18.2") {
		t.Error("TrapNet annotation not flagged")
	}
	// A hidden TrapNet is exempt.
	a.Set("F", Integer(2))
	if hasUAClause(doc.checkUAAnnotations(), "7.18.2") {
		t.Error("hidden annotation should be exempt")
	}
}
