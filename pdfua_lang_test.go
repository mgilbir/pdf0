package pdf0

import "testing"

func TestValidBCP47(t *testing.T) {
	valid := []string{"en", "en-US", "pt-PT", "zh-Hans-CN", "de-DE-1901", "x-klingon", "es-419"}
	for _, tag := range valid {
		if !validBCP47(tag) {
			t.Errorf("%q should be valid", tag)
		}
	}
	invalid := []string{"", "portugues-pt", "1-pt", "-pt", "en-", "nl-1234abcde", "e", "en--US"}
	for _, tag := range invalid {
		if validBCP47(tag) {
			t.Errorf("%q should be invalid", tag)
		}
	}
}

func TestUALang(t *testing.T) {
	mk := func(catLang string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		cat := &Dictionary{}
		if catLang != "" {
			cat.Set("Lang", String{Value: []byte(catLang)})
		}
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk("1-pt"); len(d.checkUALang(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("invalid catalog /Lang not flagged")
	}
	if d := mk("en-US"); len(d.checkUALang(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("valid catalog /Lang wrongly flagged")
	}
	// Absent /Lang is not this check's concern (a separate rule requires it).
	if d := mk(""); len(d.checkUALang(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("absent /Lang wrongly flagged by BCP-47 check")
	}
}
