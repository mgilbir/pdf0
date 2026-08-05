package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func TestValidBCP47(t *testing.T) {
	valid := []string{"en", "en-US", "pt-PT", "zh-Hans-CN", "de-DE-1901", "x-klingon", "es-419"}
	for _, tag := range valid {
		if !core.ValidBCP47(tag) {
			t.Errorf("%q should be valid", tag)
		}
	}
	invalid := []string{"", "portugues-pt", "1-pt", "-pt", "en-", "nl-1234abcde", "e", "en--US"}
	for _, tag := range invalid {
		if core.ValidBCP47(tag) {
			t.Errorf("%q should be invalid", tag)
		}
	}
}

func TestUALang(t *testing.T) {
	mk := func(catLang string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		cat := &object.Dictionary{}
		if catLang != "" {
			cat.Set("Lang", object.String{Value: []byte(catLang)})
		}
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk("1-pt"); len(checkUALang(d, d.ResolveDict(object.IndirectRef{Number: 1}))) == 0 {
		t.Error("invalid catalog /Lang not flagged")
	}
	if d := mk("en-US"); len(checkUALang(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("valid catalog /Lang wrongly flagged")
	}
	// Absent /Lang is not this check's concern (a separate rule requires it).
	if d := mk(""); len(checkUALang(d, d.ResolveDict(object.IndirectRef{Number: 1}))) != 0 {
		t.Error("absent /Lang wrongly flagged by BCP-47 check")
	}
}
