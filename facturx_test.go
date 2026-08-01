package pdf0

import (
	"bytes"
	"github.com/mgilbir/formalis"
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

// containerFindings returns the findings pdf0 itself made — its container rules
// and the PDF/A-3 base — as opposed to those adopted from the invoice rule
// engine, which carry that engine's Source.
func containerFindings(res FacturXResult) []FacturXViolation {
	var out []FacturXViolation
	for _, v := range res.Violations {
		if v.Source == formalis.SourceNone {
			out = append(out, v)
		}
	}
	return out
}

// facturxInvoiceRuleFindings is how many corpus files the *invoice* rule engine
// reports a fatal finding on, and it is a ratchet rather than a target.
//
// These are not container defects and pdf0 does not decide them. The count fell
// from 17 to 5 on the formalis v0.3.0 bump, and what remains is a different
// claim from what it replaced, so the drop is not merely "fewer findings".
//
// At v0.2.0 the findings were CEN's CII syntax-binding rules (CII-DT-018/021/
// 027/031) applied to Factur-X tiers that do not adopt them — CII-DT-031 appears
// nowhere in the Factur-X 1.09 bundle — so they were false positives against the
// standard this validator names. v0.3.0 made Profile select the binding, and
// they are gone.
//
// The five that remain are Factur-X's *own* rules, from its own artefact:
//
//   - fnfe_BASIC (14), fnfe_MINIMUM and fnfe_MINIMUM_UE (5 each),
//     intarsys_MINIMUM (1): FX-DM-* data-model rules, which mark an element or
//     attribute "not used" at that tier. Mostly @currencyID on the summation and
//     tax amounts, and a buyer PostalTradeAddress / SpecifiedTaxRegistration at
//     MINIMUM. Two independent authorities agree on the @currencyID ones: they
//     are what CEN's CII-DT-031 reported before, now reported by Factur-X's own
//     data model, which is evidence the samples carry them rather than that the
//     rule is misscoped.
//   - official_XRECHNUNG_Betriebskostenabrechnung (3): PEPPOL-EN16931-R001/R010/
//     R020, the Peppol rules the XRechnung artefact merges. It is the only one of
//     the four XRECHNUNG samples counted here; the other three draw advisory
//     findings, which are reported separately as InvoiceWarnings.
//
// So this is no longer a seam artefact to be argued away. It says FNFE's own
// published samples depart from FNFE's own published data model at the two
// leanest tiers.
//
// Upstream now says the same. The four lean-tier documents were contributed to
// formalis (its testdata/facturx/extracted) and are pinned there as an
// expected-failure table naming every rule, the node it fires at, and a checked
// reason — so these two ratchets describe the same documents from either side of
// the module boundary and should move together. A change here that formalis did
// not make is the interesting case.
//
// The number is pinned so that a change on either side of the seam is visible.
// Lower it when formalis narrows the scope; investigate any increase.
// It is a table keyed by document rather than a total, because the corpus is not
// one fixed set: a CI checkout has the 16 documents of sources.tsv, while a
// developer's tree also holds the 59 unpacked from the FNFE specification
// bundle, which is not redistributable and has no fetch target. A total would
// mean one number for two corpora, and the smaller one would have to assert
// nothing to stay true. Per document, every checkout asserts everything it can
// see, and the count of a document it does not have is simply not consulted.
//
// Absent from the table means zero. Both directions are checked, and the
// vanishing case is the more dangerous: a document that stops drawing a finding
// means a rule that is present, reachable and now inert.
var facturxInvoiceRuleFindings = map[string]int{
	"fnfe_BASIC.pdf":       14,
	"fnfe_MINIMUM.pdf":     5,
	"fnfe_MINIMUM_UE.pdf":  5,
	"intarsys_MINIMUM.pdf": 1,
	"official_XRECHNUNG_Betriebskostenabrechnung_fx.pdf": 3,
}

// TestValidateFacturXCorpus is the FP=0 oracle for the half pdf0 owns: every
// conforming Factur-X / ZUGFeRD invoice (all profiles) must validate with no
// *container* findings and a recognised conformance level, and the deliberately
// corrupt sample must be rejected. The corpus is not vendored; the test skips
// when testdata/facturx is absent.
//
// Findings adopted from the invoice rule engine are counted rather than
// forbidden — see facturxInvoiceRuleFindings. Folding them into the FP=0 claim
// made a rule this repository does not own able to break an oracle about
// containers, which is what happened on the formalis v0.2.0 bump.
func TestValidateFacturXCorpus(t *testing.T) {
	files, _ := filepath.Glob("testdata/facturx/*.pdf")
	if len(files) == 0 {
		t.Skip("Factur-X corpus not present (testdata/facturx)")
	}
	sort.Strings(files)
	seenProfiles := map[formalis.Profile]bool{}
	seenCIUS := map[formalis.CIUS]bool{}
	conforming := 0
	invoiceFindings := map[string]int{}
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
		conforming++
		res := ValidateFacturX(doc, data)
		container := containerFindings(res)
		if len(container) != 0 {
			t.Errorf("%s: expected 0 container violations on a conforming invoice, got %d (first: %s)",
				name, len(container), container[0])
		}
		if n := len(res.Violations) - len(container); n > 0 {
			invoiceFindings[name] = n
		}
		// The level names exactly one of the two things it can name: a
		// data-richness profile, or a CIUS ("XRECHNUNG", which four of these files
		// carry). Neither is a defect; naming nothing recognisable is.
		if res.Profile == "" && res.CIUS == formalis.CIUSNone {
			t.Errorf("%s: no conformance profile or CIUS detected", name)
		}
		if res.Profile != "" {
			seenProfiles[res.Profile] = true
		}
		if res.CIUS != formalis.CIUSNone {
			seenCIUS[res.CIUS] = true
		}
		if len(res.XML) == 0 {
			t.Errorf("%s: invoice XML was not extracted", name)
		}
	}
	if conforming == 0 {
		t.Fatal("no conforming Factur-X container was validated")
	}
	// The corpus is meant to span profiles; make sure detection works broadly.
	if len(seenProfiles) < 3 {
		t.Errorf("expected the corpus to cover several profiles, saw %d", len(seenProfiles))
	}
	// Only the fuller corpus carries a CIUS-declaring document; sources.tsv has
	// none, so this asserts that routing works where there is something to route
	// rather than that every checkout has one.
	if len(seenCIUS) == 0 && facturxCorpusHasCIUS(files) {
		t.Error("the corpus carries a CIUS conformance level that was not detected")
	}
	for _, f := range files {
		name := filepath.Base(f)
		got, want := invoiceFindings[name], facturxInvoiceRuleFindings[name]
		if got != want {
			t.Errorf("%s: invoice rule engine reported %d fatal findings, expected %d",
				name, got, want)
		}
	}
}

// facturxCorpusHasCIUS reports whether the corpus holds a document whose
// conformance level names a CIUS rather than a data-richness profile. The four
// XRechnung samples come from the specification bundle, so a checkout with only
// sources.tsv has none.
func facturxCorpusHasCIUS(files []string) bool {
	for _, f := range files {
		if strings.Contains(strings.ToUpper(filepath.Base(f)), "XRECHNUNG") {
			return true
		}
	}
	return false
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
