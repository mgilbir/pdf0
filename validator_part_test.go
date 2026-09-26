package pdf0

import (
	"strings"
	"testing"
)

// TestFindingsNameTheirPart is C145: a finding prints the part it is against.
// PDF/UA-2 findings printed "PDF/UA-1", PDF/VT-2 findings "PDF/VT-1", and
// PDF/VT-2's base findings were prefixed "pdfx-4/" although its base is
// PDF/X-5.
func TestFindingsNameTheirPart(t *testing.T) {
	// An untagged PDF 2.0 page: both UA parts have plenty to say.
	d := reachableFixture(t, "", "", nil)
	for _, c := range []struct {
		name, want string
		got        []error
	}{
		{"ValidatePDFUA", "[PDF/UA-1 ", errs(ValidatePDFUA(d))},
		{"ValidatePDFUA2", "[PDF/UA-2 ", errs(ValidatePDFUA2(d))},
	} {
		if len(c.got) == 0 {
			t.Fatalf("%s found nothing to report on an untagged document", c.name)
		}
		for _, e := range c.got {
			if !strings.HasPrefix(e.Error(), c.want) {
				t.Errorf("%s: %q does not name its part (%s)", c.name, e.Error(), c.want)
			}
		}
	}

	// A PDF/VT fixture with /Trapped removed, so the PDF/X base has a finding.
	untrap := func(doc *Document) *Document {
		info := doc.ResolveDict(doc.Trailer.Get("Info"))
		info.Delete("Trapped")
		return doc
	}
	for _, c := range []struct {
		name, part, base string
		got              []error
	}{
		{"ValidatePDFVT", "PDF/VT-1 ", "pdfx-4/trapped", errs(ValidatePDFVT(untrap(buildPDFVT1Doc())))},
		{"ValidatePDFVT2", "PDF/VT-2 ", "pdfx-5/trapped", errs(ValidatePDFVT2(untrap(buildPDFVT2Doc())))},
	} {
		if len(c.got) == 0 {
			t.Fatalf("%s found nothing to report", c.name)
		}
		base := false
		for _, e := range c.got {
			if !strings.HasPrefix(e.Error(), c.part) {
				t.Errorf("%s: %q does not name its part (%s)", c.name, e.Error(), c.part)
			}
			base = base || strings.Contains(e.Error(), c.base+":")
		}
		if !base {
			t.Errorf("%s: no base finding prefixed %s in %v", c.name, c.base, c.got)
		}
	}
}
