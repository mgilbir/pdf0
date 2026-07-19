package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/einvoice"
	"os"
	"path/filepath"
	"sort"
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
func afDoc(ufName string, rel Name, subtype Name) *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.6"}
	stream := &Stream{Dict: Dictionary{}, Data: []byte("<xml/>")}
	stream.Dict.Set("Subtype", subtype)
	d.Objects[10] = &IndirectObject{Number: 10, Value: stream}
	ef := &Dictionary{}
	ef.Set("F", IndirectRef{Number: 10})
	fs := &Dictionary{}
	fs.Set("Type", Name("Filespec"))
	fs.Set("F", String{Value: []byte(ufName)})
	fs.Set("UF", String{Value: utf16be(ufName)})
	fs.Set("AFRelationship", rel)
	fs.Set("EF", ef)
	d.Objects[9] = &IndirectObject{Number: 9, Value: fs}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("AF", Array{IndirectRef{Number: 9}})
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
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
			fs, got, _ := findFacturXAttachment(d, cat)
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
		if _, ok := einvoice.ProfileFor(p); !ok {
			t.Errorf("ConformanceLevel %q not recognised", p)
		}
	}
	if _, ok := einvoice.ProfileFor("NONSENSE"); ok {
		t.Error("NONSENSE must not be a profile")
	}
	if facturxIsXMLSubtype("application/pdf") {
		t.Error("application/pdf must not count as an XML subtype")
	}
	if !facturxIsXMLSubtype("text/xml") || !facturxIsXMLSubtype("application/xml") {
		t.Error("text/xml and application/xml must count as XML subtypes")
	}
}

// TestValidateFacturXCorpus is the FP=0 oracle: every conforming Factur-X /
// ZUGFeRD invoice (all profiles) must validate with no violations and a
// recognised profile, and the deliberately corrupt sample must be rejected. The
// corpus is not vendored; the test skips when testdata/facturx is absent.
func TestValidateFacturXCorpus(t *testing.T) {
	files, _ := filepath.Glob("testdata/facturx/*.pdf")
	if len(files) == 0 {
		t.Skip("Factur-X corpus not present (testdata/facturx)")
	}
	sort.Strings(files)
	seenProfiles := map[einvoice.Profile]bool{}
	for _, f := range files {
		name := filepath.Base(f)
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if strings.HasPrefix(name, "FAIL") {
			if err == nil {
				if res := ValidateFacturX(doc, data); len(res.Violations) == 0 {
					t.Errorf("%s: expected the corrupt sample to be rejected, got no violations", name)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: parse failed: %v", name, err)
			continue
		}
		res := ValidateFacturX(doc, data)
		if len(res.Violations) != 0 {
			t.Errorf("%s: expected 0 violations on a conforming invoice, got %d (first: %s: %s)",
				name, len(res.Violations), res.Violations[0].Rule, res.Violations[0].Message)
		}
		if res.Profile == "" {
			t.Errorf("%s: no conformance profile detected", name)
		}
		seenProfiles[res.Profile] = true
		if len(res.XML) == 0 {
			t.Errorf("%s: invoice XML was not extracted", name)
		}
	}
	// The corpus is meant to span profiles; make sure detection works broadly.
	if len(seenProfiles) < 3 {
		t.Errorf("expected the corpus to cover several profiles, saw %d", len(seenProfiles))
	}
}

// TestValidateFacturXMutations takes a conforming invoice and confirms the
// Factur-X-specific checks fire when the container is broken. Gated on the
// corpus, since it needs a real PDF/A-3 base to mutate.
func TestValidateFacturXMutations(t *testing.T) {
	files, _ := filepath.Glob("testdata/facturx/corpus_EN16931_Einfach.pdf")
	if len(files) == 0 {
		files, _ = filepath.Glob("testdata/facturx/*EN16931*.pdf")
	}
	if len(files) == 0 {
		t.Skip("Factur-X corpus not present")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	hasViolation := func(res FacturXResult, rule, substr string) bool {
		for _, v := range res.Violations {
			if v.Rule == rule && strings.Contains(v.Message, substr) {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name   string
		mutate func(doc *Document)
		rule   string
		substr string
	}{
		{"no /AF attachment", func(doc *Document) {
			doc.ResolveDict(doc.Trailer.Get("Root")).Delete("AF")
		}, "attachment", "no embedded invoice XML"},
		{"bad AFRelationship", func(doc *Document) {
			cat := doc.ResolveDict(doc.Trailer.Get("Root"))
			fs, _, _ := findFacturXAttachment(doc, cat)
			fs.Set("AFRelationship", Name("Unspecified"))
		}, "attachment", "AFRelationship"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc)
			res := ValidateFacturX(doc, data)
			if !hasViolation(res, tc.rule, tc.substr) {
				t.Errorf("expected %s violation containing %q; got %v", tc.rule, tc.substr, res.Violations)
			}
		})
	}
}
