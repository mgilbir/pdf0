package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAOptionalContent covers the 7.10 OC-configuration rules.
func TestUAOptionalContent(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	mk := func(cfg *object.Dictionary) *object.Dictionary {
		ocp := &object.Dictionary{}
		ocp.Set("D", cfg)
		cat := &object.Dictionary{}
		cat.Set("OCProperties", ocp)
		return cat
	}
	// No /Name -> flagged.
	noName := &object.Dictionary{}
	if len(checkUAOptionalContent(doc, mk(noName))) == 0 {
		t.Error("OC config without /Name not flagged")
	}
	// /AS present -> flagged.
	withAS := &object.Dictionary{}
	withAS.Set("Name", object.String{Value: []byte("Default")})
	withAS.Set("AS", object.Array{})
	if len(checkUAOptionalContent(doc, mk(withAS))) == 0 {
		t.Error("OC config with /AS not flagged")
	}
	// Proper config -> clean.
	good := &object.Dictionary{}
	good.Set("Name", object.String{Value: []byte("Default")})
	if v := checkUAOptionalContent(doc, mk(good)); len(v) != 0 {
		t.Errorf("well-formed OC config flagged: %v", v)
	}
}

// TestUAEmbeddedFiles covers the 7.11 embedded-file filespec rules.
func TestUAEmbeddedFiles(t *testing.T) {
	mk := func(setF, setUF bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		fs := &object.Dictionary{}
		fs.Set("Type", object.Name("Filespec"))
		fs.Set("EF", &object.Dictionary{})
		if setF {
			fs.Set("F", object.String{Value: []byte("a.txt")})
		}
		if setUF {
			fs.Set("UF", object.String{Value: []byte("a.txt")})
		}
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: fs}
		return doc
	}
	if len(checkUAEmbeddedFiles(mk(false, false))) == 0 {
		t.Error("embedded filespec missing /F and /UF not flagged")
	}
	if len(checkUAEmbeddedFiles(mk(true, false))) == 0 {
		t.Error("embedded filespec missing /UF not flagged")
	}
	if v := checkUAEmbeddedFiles(mk(true, true)); len(v) != 0 {
		t.Errorf("embedded filespec with /F and /UF flagged: %v", v)
	}
}
