package pdf0

import (
	"bytes"
	"testing"
)

func TestEndstreamFollowsAt(t *testing.T) {
	// data = "<DATA>\nendstream", the data region is bytes [0,4).
	d := []byte("ABCD\nendstream")
	if !endstreamFollowsAt(d, 4) {
		t.Error("endstream after one EOL not recognized")
	}
	if !endstreamFollowsAt([]byte("ABCDendstream"), 4) {
		t.Error("endstream with no EOL not recognized")
	}
	if !endstreamFollowsAt([]byte("ABCD\r\n\r\nendstream"), 4) {
		t.Error("endstream after extra whitespace not recognized")
	}
	if endstreamFollowsAt(d, 2) {
		t.Error("wrong offset (mid-data) must not match")
	}
	if endstreamFollowsAt(d, 100) {
		t.Error("out-of-range offset must not match")
	}
}

func TestXrefLooksValid(t *testing.T) {
	if !xrefLooksValid([]byte("  \r\nxref\r\n0 1"), 0) {
		t.Error("traditional xref keyword not recognized")
	}
	if !xrefLooksValid([]byte("12 0 obj\n<</Type/XRef"), 0) {
		t.Error("xref stream object not recognized")
	}
	if xrefLooksValid([]byte("garbage here"), 0) {
		t.Error("non-xref content must not look valid")
	}
	if xrefLooksValid([]byte("xref"), 100) {
		t.Error("out-of-range offset must be false")
	}
}

func TestCheckStreamLength(t *testing.T) {
	mk := func(declared, actual int) *Document {
		data := bytes.Repeat([]byte("x"), actual)
		s := &Stream{Dict: Dictionary{}, Data: data}
		s.Dict.Set("Length", Integer(declared))
		return &Document{Objects: map[int]*IndirectObject{
			7: {Number: 7, Value: s},
		}}
	}
	if hasRuleMsg(checkStreamLength(mk(10, 10), PDFA4), "6.1.6.1") {
		t.Error("matching Length must not be flagged")
	}
	if !hasRuleMsg(checkStreamLength(mk(10, 8), PDFA4), "6.1.6.1") {
		t.Error("mismatched Length must be flagged at A-4")
	}
	if !hasRuleMsg(checkStreamLength(mk(10, 8), PDFA2b), "6.1.7.1") {
		t.Error("mismatched Length must be flagged at 2b with rule 6.1.7")
	}
}

func TestBrokenObjStmFlagged(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}, brokenObjStms: []int{4}}
	if !hasRuleMsg(checkObjectStreamDecodable(doc, PDFA4), "6.1.6") {
		t.Error("broken object stream must be flagged")
	}
	if len(checkObjectStreamDecodable(&Document{}, PDFA4)) != 0 {
		t.Error("no broken streams must produce no errors")
	}
}
