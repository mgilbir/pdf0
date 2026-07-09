package pdf0

import "testing"

// TestUAOptionalContent covers the 7.10 OC-configuration rules.
func TestUAOptionalContent(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	mk := func(cfg *Dictionary) *Dictionary {
		ocp := &Dictionary{}
		ocp.Set("D", cfg)
		cat := &Dictionary{}
		cat.Set("OCProperties", ocp)
		return cat
	}
	// No /Name -> flagged.
	noName := &Dictionary{}
	if len(doc.checkUAOptionalContent(mk(noName))) == 0 {
		t.Error("OC config without /Name not flagged")
	}
	// /AS present -> flagged.
	withAS := &Dictionary{}
	withAS.Set("Name", String{Value: []byte("Default")})
	withAS.Set("AS", Array{})
	if len(doc.checkUAOptionalContent(mk(withAS))) == 0 {
		t.Error("OC config with /AS not flagged")
	}
	// Proper config -> clean.
	good := &Dictionary{}
	good.Set("Name", String{Value: []byte("Default")})
	if v := doc.checkUAOptionalContent(mk(good)); len(v) != 0 {
		t.Errorf("well-formed OC config flagged: %v", v)
	}
}

// TestUAEmbeddedFiles covers the 7.11 embedded-file filespec rules.
func TestUAEmbeddedFiles(t *testing.T) {
	mk := func(setF, setUF bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		fs := &Dictionary{}
		fs.Set("Type", Name("Filespec"))
		fs.Set("EF", &Dictionary{})
		if setF {
			fs.Set("F", String{Value: []byte("a.txt")})
		}
		if setUF {
			fs.Set("UF", String{Value: []byte("a.txt")})
		}
		doc.Objects[5] = &IndirectObject{Number: 5, Value: fs}
		return doc
	}
	if len(mk(false, false).checkUAEmbeddedFiles()) == 0 {
		t.Error("embedded filespec missing /F and /UF not flagged")
	}
	if len(mk(true, false).checkUAEmbeddedFiles()) == 0 {
		t.Error("embedded filespec missing /UF not flagged")
	}
	if v := mk(true, true).checkUAEmbeddedFiles(); len(v) != 0 {
		t.Errorf("embedded filespec with /F and /UF flagged: %v", v)
	}
}
