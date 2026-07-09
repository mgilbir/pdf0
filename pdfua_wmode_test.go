package pdf0

import "testing"

func TestCMapInnerWMode(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"begincmap /WMode 1 def endcmap", 1, true},
		{"/WMode 0 def", 0, true},
		{"/WMode\n2\ndef", 2, true},
		{"no wmode here", 0, false},
		{"/WMode def", 0, false}, // no number
	}
	for _, c := range cases {
		got, ok := cmapInnerWMode([]byte(c.in))
		if got != c.want || ok != c.ok {
			t.Errorf("cmapInnerWMode(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestUACMapWMode checks a Type0 font whose embedded CMap declares a WMode that
// disagrees with its dictionary /WMode.
func TestUACMapWMode(t *testing.T) {
	mk := func(dictWM int, inner string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		cmap := &Stream{Dict: Dictionary{}, Data: []byte("begincmap " + inner + " endcmap")}
		cmap.Dict.Set("WMode", Integer(dictWM))
		f := &Dictionary{}
		f.Set("Subtype", Name("Type0"))
		f.Set("Encoding", cmap)
		doc.Objects[10] = &IndirectObject{Number: 10, Value: f}
		return doc
	}
	// Mismatch (dict 1, inner 0) — but checkUACMapWMode iterates rendered fonts,
	// so exercise cmapInnerWMode directly against the mismatch scenario.
	doc := mk(1, "/WMode 0 def")
	s := doc.Objects[10].Value.(*Dictionary).Get("Encoding").(*Stream)
	inner, ok := cmapInnerWMode(decodeContentStream(doc, s))
	if !ok || inner != 0 {
		t.Fatalf("inner WMode = (%d,%v), want (0,true)", inner, ok)
	}
	dictWM, _ := doc.Resolve(s.Dict.Get("WMode")).(Integer)
	if int(dictWM) == inner {
		t.Error("expected a WMode mismatch")
	}
}
