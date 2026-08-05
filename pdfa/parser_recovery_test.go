package pdfa

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func TestCheckStreamLength(t *testing.T) {
	mk := func(declared, actual int) core.View {
		data := bytes.Repeat([]byte("x"), actual)
		s := &object.Stream{Dict: object.Dictionary{}, Data: data}
		s.Dict.Set("Length", object.Integer(declared))
		return mkV(core.View{Objects: map[int]*object.IndirectObject{
			7: {Number: 7, Value: s},
		}})
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
	doc := mkViewBroken(map[int]*object.IndirectObject{}, []int{4})
	if !hasRuleMsg(checkObjectStreamDecodable(doc, PDFA4), "6.1.6") {
		t.Error("broken object stream must be flagged")
	}
	if len(checkObjectStreamDecodable((mkView(nil, object.Dictionary{})), PDFA4)) != 0 {
		t.Error("no broken streams must produce no errors")
	}
}
