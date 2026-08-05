package pdf0

import (
	"github.com/mgilbir/pdf0/syntax"
	"testing"
)

func TestEndstreamFollowsAt(t *testing.T) {
	// data = "<DATA>\nendstream", the data region is bytes [0,4).
	d := []byte("ABCD\nendstream")
	if !syntax.EndstreamFollowsAt(d, 4) {
		t.Error("endstream after one EOL not recognized")
	}
	if !syntax.EndstreamFollowsAt([]byte("ABCDendstream"), 4) {
		t.Error("endstream with no EOL not recognized")
	}
	if !syntax.EndstreamFollowsAt([]byte("ABCD\r\n\r\nendstream"), 4) {
		t.Error("endstream after extra whitespace not recognized")
	}
	if syntax.EndstreamFollowsAt(d, 2) {
		t.Error("wrong offset (mid-data) must not match")
	}
	if syntax.EndstreamFollowsAt(d, 100) {
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
