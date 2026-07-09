package pdf0

import "testing"

// TestUACIDSystemInfo checks the 7.21.3.1 CMap/CIDFont CIDSystemInfo match.
func TestUACIDSystemInfo(t *testing.T) {
	// Build a Type0 font with an embedded CMap (its own CIDSystemInfo) and a
	// descendant CIDFont whose CIDSystemInfo may or may not match.
	mk := func(cmapReg, cmapOrd, cidReg, cidOrd string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		si := func(r, o string) *Dictionary {
			d := &Dictionary{}
			d.Set("Registry", String{Value: []byte(r)})
			d.Set("Ordering", String{Value: []byte(o)})
			return d
		}
		cmap := &Stream{Dict: Dictionary{}}
		cmap.Dict.Set("CIDSystemInfo", si(cmapReg, cmapOrd))
		cid := &Dictionary{}
		cid.Set("Subtype", Name("CIDFontType2"))
		cid.Set("CIDSystemInfo", si(cidReg, cidOrd))
		doc.Objects[11] = &IndirectObject{Number: 11, Value: cid}
		f := &Dictionary{}
		f.Set("Subtype", Name("Type0"))
		f.Set("Encoding", cmap)
		f.Set("DescendantFonts", Array{IndirectRef{Number: 11}})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: f}
		return doc
	}
	check := func(doc *Document) []UAViolation {
		return doc.checkOneUACIDSystemInfo(doc.Objects[10].Value.(*Dictionary))
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
