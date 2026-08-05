package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUACIDSystemInfo checks the 7.21.3.1 CMap/CIDFont CIDSystemInfo match.
func TestUACIDSystemInfo(t *testing.T) {
	// Build a Type0 font with an embedded CMap (its own CIDSystemInfo) and a
	// descendant CIDFont whose CIDSystemInfo may or may not match.
	mk := func(cmapReg, cmapOrd, cidReg, cidOrd string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		si := func(r, o string) *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Registry", object.String{Value: []byte(r)})
			d.Set("Ordering", object.String{Value: []byte(o)})
			return d
		}
		cmap := &object.Stream{Dict: object.Dictionary{}}
		cmap.Dict.Set("CIDSystemInfo", si(cmapReg, cmapOrd))
		cid := &object.Dictionary{}
		cid.Set("Subtype", object.Name("CIDFontType2"))
		cid.Set("CIDSystemInfo", si(cidReg, cidOrd))
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: cid}
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("Encoding", cmap)
		f.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 11}})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: f}
		return doc
	}
	check := func(doc core.View) []Violation {
		return checkOneUACIDSystemInfo(doc, doc.Objects[10].Value.(*object.Dictionary))
	}
	if len(check(mk("Adobe", "Korea1", "adobe", "Korea1"))) == 0 {
		t.Error("Registry case mismatch not flagged")
	}
	if len(check(mk("Adobe", "Korea1", "Adobe", "China1"))) == 0 {
		t.Error("Ordering mismatch not flagged")
	}
	if v := check(mk("Adobe", "Korea1", "Adobe", "Korea1")); len(v) != 0 {
		t.Errorf("matching CIDSystemInfo wrongly flagged: %v", v)
	}
}

// TestUACIDSupplement flags a CIDFont whose Supplement exceeds the CMap's.
func TestUACIDSupplement(t *testing.T) {
	mk := func(cmapSup, cidSup int) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		si := func(sup int) *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Registry", object.String{Value: []byte("Adobe")})
			d.Set("Ordering", object.String{Value: []byte("Japan1")})
			d.Set("Supplement", object.Integer(sup))
			return d
		}
		cmap := &object.Stream{Dict: object.Dictionary{}}
		cmap.Dict.Set("CIDSystemInfo", si(cmapSup))
		cid := &object.Dictionary{}
		cid.Set("Subtype", object.Name("CIDFontType0"))
		cid.Set("CIDSystemInfo", si(cidSup))
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: cid}
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("Encoding", cmap)
		f.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 11}})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: f}
		return doc
	}
	check := func(doc core.View) []Violation {
		return checkOneUACIDSystemInfo(doc, doc.Objects[10].Value.(*object.Dictionary))
	}
	if len(check(mk(2, 3))) == 0 {
		t.Error("CIDFont Supplement exceeding CMap not flagged")
	}
	if v := check(mk(3, 3)); len(v) != 0 {
		t.Errorf("equal supplements wrongly flagged: %v", v)
	}
	if v := check(mk(6, 2)); len(v) != 0 {
		t.Errorf("lower CIDFont supplement wrongly flagged: %v", v)
	}
}
