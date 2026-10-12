package fontcheck

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestSimpleFontCodeToNameTrueTypeFill: for a non-symbolic TrueType font whose
// Encoding is a dictionary, ISO 32000-1 9.6.6.4 fills "any undefined entries
// in the table ... using StandardEncoding" after BaseEncoding and Differences.
// A BaseEncoding naming no predefined encoding initializes nothing (corpus
// PDF_UA-1 7.21.6-t02-fail-a, /Identity). Type 1 fonts and symbolic TrueType
// fonts are not filled: 9.6.6.4 is TrueType's, and builds the table only for a
// non-symbolic font.
func TestSimpleFontCodeToNameTrueTypeFill(t *testing.T) {
	mk := func(subtype string, enc *object.Dictionary) *object.Dictionary {
		return object.NewDictionary(
			object.Entry{Key: "Subtype", Value: object.Name(subtype)},
			object.Entry{Key: "Encoding", Value: enc},
		)
	}
	identity := object.NewDictionary(
		object.Entry{Key: "BaseEncoding", Value: object.Name("Identity")},
		object.Entry{Key: "Differences", Value: object.Array{object.Integer(66), object.Name("Euro")}},
	)
	winAnsi := object.NewDictionary(object.Entry{Key: "BaseEncoding", Value: object.Name("WinAnsiEncoding")})
	doc := mkView(nil)
	cases := []struct {
		name     string
		font     *object.Dictionary
		symbolic bool
		code     byte
		want     string
	}{
		{"unknown base: StandardEncoding fills A", mk("TrueType", identity), false, 'A', "A"},
		{"unknown base: Differences still win", mk("TrueType", identity), false, 'B', "Euro"},
		{"WinAnsi base keeps its own names", mk("TrueType", winAnsi), false, 0x80, "Euro"},
		{"symbolic TrueType is not filled", mk("TrueType", identity), true, 'A', ""},
		{"Type 1 is not filled", mk("Type1", identity), false, 'A', ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SimpleFontCodeToName(doc, c.font, c.symbolic)[c.code]; got != c.want {
				t.Errorf("code %#x: got %q, want %q", c.code, got, c.want)
			}
		})
	}
}
