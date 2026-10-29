package pdfa

import (
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/internal/rulecov"
	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestClausesAreTheProfilesNumbers holds the clause each check reports, per
// part, to the veraPDF profile rule that check implements: the rule exists in
// that part's profile, and the check reports its clause. ISO 19005-1, -2 and
// -4 number the same requirement differently (an undefined operator is 6.2.10
// at PDF/A-1 and 6.2.2 later; a missing /Metadata is 6.7.2, 6.6.2.1 and
// 6.7.2.1), and a number carried over from one part is a finding a reader
// looks up under the wrong rule. TestRuleCoverage measures the same against
// the corpus, but only for the rules the corpus tests; this covers the rest.
// A blank rule ID is a part whose profile has no such rule.
func TestClausesAreTheProfilesNumbers(t *testing.T) {
	dir := filepath.Dir(testfiles.VeraPDFProfiles.File(t, "PDF_A/PDFA-1B.xml"))
	parts := []struct {
		level   Level
		profile string
	}{{PDFA1b, "PDFA-1B.xml"}, {PDFA2b, "PDFA-2B.xml"}, {PDFA4, "PDFA-4.xml"}}
	clauseOf := make([]map[string]string, len(parts))
	for i, p := range parts {
		rules, err := rulecov.LoadProfile(filepath.Join(dir, p.profile))
		if err != nil {
			t.Fatal(err)
		}
		clauseOf[i] = map[string]string{}
		for _, r := range rules {
			clauseOf[i][r.ID()] = r.Clause
		}
	}
	interp := func(l Level) string { c, _ := imageClauses(l); return c }
	imageIntent := func(l Level) string { _, c := imageClauses(l); return c }
	for _, c := range []struct {
		name  string
		got   func(Level) string
		rules [3]string // the profile rule at PDF/A-1b, -2b, -4
	}{
		{"an undefined operator", func(l Level) string { return contentClause(kindOperator, l) }, [3]string{"6.2.10-t01", "6.2.2-t01", "6.2.2-t01"}},
		{"a rendering intent (ri, /RI)", func(l Level) string { return contentClause(kindIntent, l) }, [3]string{"6.2.9-t01", "6.2.6-t01", ""}},
		{"an image's rendering intent", imageIntent, [3]string{"6.2.9-t01", "6.2.6-t01", ""}},
		{"a PostScript XObject", func(l Level) string { return contentClause(kindPostScript, l) }, [3]string{"6.2.7-t01", "6.2.9-t03", ""}},
		{"a form's /Subtype2 /PS", func(l Level) string { return contentClause(kindSubtype2PS, l) }, [3]string{"6.2.5-t01", "6.2.9-t01", ""}},
		{"a form's /PS entry", func(l Level) string { return contentClause(kindPSEntry, l) }, [3]string{"6.2.5-t01", "6.2.9-t01", ""}},
		{"a blend mode", transparencyClause, [3]string{"", "6.2.10-t01", "6.2.9-t01"}},
		{"a transparent page's blending colour space", transparencyClause, [3]string{"", "6.2.10-t02", "6.2.9-t02"}},
		{"the catalog's /AA", func(l Level) string { return annotActionClause("catalogAA", l) }, [3]string{"6.6.2-t03", "6.5.2-t01", "6.6.3-t01"}},
		{"the catalog's /Metadata", func(l Level) string { return metadataClause("catalogMetadata", l) }, [3]string{"6.7.2-t01", "6.6.2.1-t01", "6.7.2.1-t01"}},
		{"an XMP property's schema and value type", func(l Level) string { return metadataClause("xmpProperties", l) }, [3]string{"6.7.9-t02", "6.6.2.3.1-t01", ""}},
		{"an extension schema's container", func(l Level) string { return metadataClause("extSchema", l) }, [3]string{"6.7.8-t02", "6.6.2.3.3-t01", ""}},
		{"a stream's /F, /FFilter, /FDecodeParms", externalStreamClause, [3]string{"6.1.7-t03", "6.1.7.1-t03", "6.1.6.1-t02"}},
		{"a /Perms key", permsClause, [3]string{"", "6.1.12-t01", "6.1.11-t01"}},
		{"a spot colorant without a Colorants entry", func(l Level) string { return colourClause("spot", l) }, [3]string{"", "6.2.4.4-t01", "6.2.4.4-t01"}},
		{"DeviceN's colorant limit", deviceNLimitClause, [3]string{"6.1.12-t09", "6.1.13-t09", ""}},
		{"an image's BitsPerComponent", func(l Level) string { return imageClause("image", l) }, [3]string{"6.2.4-t04", "6.2.8-t04", "6.2.7.1-t04"}},
		{"an image's /Interpolate", interp, [3]string{"6.2.4-t03", "6.2.8-t03", "6.2.7.1-t03"}},
	} {
		for i, p := range parts {
			id := c.rules[i]
			if id == "" {
				continue
			}
			want, ok := clauseOf[i][id]
			if !ok {
				t.Errorf("%s: %s has no rule %s; the table is out of date with the profile", c.name, p.profile, id)
				continue
			}
			if got := c.got(p.level); got != want {
				t.Errorf("%s at %s: reported under %s; the profile's rule %s is clause %s", c.name, p.level, got, id, want)
			}
		}
	}
}
