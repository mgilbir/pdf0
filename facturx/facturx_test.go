package facturx

import (
	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// utf16be encodes s as a PDF text string: a UTF-16BE byte-order mark followed by
// big-endian code units, as Unicode file-spec /UF entries are stored.
func utf16be(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}

// afDoc builds a minimal document whose catalog carries one associated-file
// specification for an embedded XML named via /UF (UTF-16) with the given
// relationship and embedded-stream subtype.
func afDoc(ufName string, rel object.Name, subtype object.Name) core.View {
	d := mkV(core.View{Objects: map[int]*object.IndirectObject{}, Version: "1.6"})
	stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte("<xml/>")}
	stream.Dict.Set("Subtype", subtype)
	d.Objects[10] = &object.IndirectObject{Number: 10, Value: stream}
	ef := &object.Dictionary{}
	ef.Set("F", object.IndirectRef{Number: 10})
	fs := &object.Dictionary{}
	fs.Set("Type", object.Name("Filespec"))
	fs.Set("F", object.String{Value: []byte(ufName)})
	fs.Set("UF", object.String{Value: utf16be(ufName)})
	fs.Set("AFRelationship", rel)
	fs.Set("EF", ef)
	d.Objects[9] = &object.IndirectObject{Number: 9, Value: fs}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("AF", object.Array{object.IndirectRef{Number: 9}})
	d.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	*d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

func TestFacturXAttachmentDetection(t *testing.T) {
	cases := []struct {
		name     string
		wantFind bool
	}{
		{"factur-x.xml", true},
		{"zugferd-invoice.xml", true},
		{"FACTUR-X.XML", true}, // case-insensitive
		{"invoice.xml", false},
		{"attachment.pdf", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := afDoc(tc.name, "Data", "text/xml")
			cat := d.ResolveDict(d.Trailer.Get("Root"))
			fs, got, _ := FindAttachment(d, cat)
			if tc.wantFind {
				if fs == nil {
					t.Fatalf("expected to find attachment %q, found none", tc.name)
				}
				if !strings.EqualFold(got, tc.name) {
					t.Errorf("decoded name = %q, want %q", got, tc.name)
				}
			} else if fs != nil {
				t.Errorf("did not expect to match %q, but found %q", tc.name, got)
			}
		})
	}
}

func TestFacturXProfilesComplete(t *testing.T) {
	// Both the spaced and unspaced spellings map to the same profile.
	for _, p := range []string{"MINIMUM", "BASIC WL", "BASICWL", "BASIC", "EN 16931", "EN16931", "EXTENDED", "en 16931"} {
		if _, ok := formalis.ProfileFor(p); !ok {
			t.Errorf("ConformanceLevel %q not recognised", p)
		}
	}
	if _, ok := formalis.ProfileFor("NONSENSE"); ok {
		t.Error("NONSENSE must not be a profile")
	}
	if facturxIsXMLSubtype("application/pdf") {
		t.Error("application/pdf must not count as an XML subtype")
	}
	if !facturxIsXMLSubtype("text/xml") || !facturxIsXMLSubtype("application/xml") {
		t.Error("text/xml and application/xml must count as XML subtypes")
	}
}
