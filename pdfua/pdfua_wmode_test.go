package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

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
	mk := func(dictWM int, inner string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		cmap := &object.Stream{Dict: object.Dictionary{}, Data: []byte("begincmap " + inner + " endcmap")}
		cmap.Dict.Set("WMode", object.Integer(dictWM))
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("Encoding", cmap)
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: f}
		return doc
	}
	// Mismatch (dict 1, inner 0) — but checkUACMapWMode iterates rendered fonts,
	// so exercise cmapInnerWMode directly against the mismatch scenario.
	doc := mk(1, "/WMode 0 def")
	s := doc.Objects[10].Value.(*object.Dictionary).Get("Encoding").(*object.Stream)
	inner, ok := cmapInnerWMode(doc.Content(s))
	if !ok || inner != 0 {
		t.Fatalf("inner WMode = (%d,%v), want (0,true)", inner, ok)
	}
	dictWM, _ := doc.Resolve(s.Dict.Get("WMode")).(object.Integer)
	if int(dictWM) == inner {
		t.Error("expected a WMode mismatch")
	}
}
