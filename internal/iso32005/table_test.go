package iso32005

import (
	"html"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
)

// iso32005Rules is the set of rules Children implies, as the
// checker enforces them, each "kind|Parent|Child": not (the row does not
// list the child), notNonGrouping (ø*), maxOne (0..1), maxOneGrouping (an ø*
// Caption), exactlyOne (1), noContent (no content item).
func iso32005Rules() map[string]bool {
	var types []string
	for t := range Children {
		if t != "StructTreeRoot" {
			types = append(types, string(t))
		}
	}
	rules := map[string]bool{}
	for p, row := range Children {
		if AllDescribed(row) {
			continue
		}
		for _, c := range types {
			key := string(p) + "|" + c
			switch inc := row[object.Name(c)]; {
			case Unenforced[string(p)+"-"+c]:
				if inc == Grouping && c == "Caption" {
					rules["maxOneGrouping|"+key] = true
				}
			case inc == 0:
				rules["not|"+key] = true
			case inc == Grouping:
				rules["notNonGrouping|"+key] = true
				if c == "Caption" {
					rules["maxOneGrouping|"+key] = true
				}
			case inc == AtMostOne:
				rules["maxOne|"+key] = true
			case inc == ExactlyOne:
				rules["exactlyOne|"+key] = true
			}
		}
		if row[ContentItem] == 0 {
			rules["noContent|"+string(p)+"|"] = true
		}
	}
	return rules
}

// iso32005Stricter are pdf0 rules veraPDF states more strictly: it forbids
// an Art in a heading outright where the table permits one, and allows a
// Note one Caption however the Note is used. Every file pdf0 fails on them,
// veraPDF fails too.
var iso32005Stricter = []string{"maxOne|H|Art", "maxOne|Hn|Art", "maxOneGrouping|Note|Caption"}

// iso32005NotImplemented are veraPDF's rules the published table does not
// state, which pdf0 therefore does not report.
var iso32005NotImplemented = []string{
	// ISO 32000-2 Annex L's errata (July 2024) permit a Figure in a WP;
	// veraPDF follows the 2023 table, which does not.
	"not|WP|Figure",
	"maxOne|Art|Caption", "maxOne|Code|Caption", "maxOne|Index|Caption", "maxOne|Note|Caption", "maxOneGrouping|Reference|Caption", "not|BlockQuote|Title", "not|H|Art", "not|Hn|Art", "notNonGrouping|Quote|Div", "notNonGrouping|Reference|Aside", "notNonGrouping|Reference|BlockQuote", "notNonGrouping|Reference|Caption", "notNonGrouping|Reference|DocumentFragment", "notNonGrouping|Reference|Form", "notNonGrouping|Reference|H", "notNonGrouping|Reference|Hn", "notNonGrouping|Reference|L", "notNonGrouping|Reference|P", "notNonGrouping|Reference|Part", "notNonGrouping|Reference|Table", "notNonGrouping|Reference|Title"}

// TestISO32005TableAgreesWithVeraPDF compares the rules pdf0's encoding of
// ISO/TS 32005 Table 5 implies with the ones veraPDF's PDF/UA-2 profile
// states (its "Table 5" rules), and holds the differences to the two lists
// above: no rule pdf0 enforces that veraPDF does not, beyond the stricter
// statements, and none of veraPDF's missing but those.
func TestISO32005TableAgreesWithVeraPDF(t *testing.T) {
	data, err := os.ReadFile(testfiles.VeraPDFProfiles.File(t, "PDF_UA/PDFUA-2-ISO32005.xml"))
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`(?s)<id specification="ISO_32005" clause="[^"]+" testNumber="\d+"/>\s*<description>(.*?)</description>`)
	desc := regexp.MustCompile(`^<?(\w+)>?(, when used as a (non-)?grouping element,)? shall (not contain|contain at most one|contain exactly one) (<(\w+)>|content items)$`)
	vera := map[string]bool{}
	for _, m := range rule.FindAllSubmatch(data, -1) {
		d := html.UnescapeString(string(m[1]))
		p := desc.FindStringSubmatch(d)
		if p == nil {
			t.Fatalf("a veraPDF ISO 32005 rule this test cannot read: %q", d)
		}
		kind := map[string]string{"not contain": "not", "contain at most one": "maxOne", "contain exactly one": "exactlyOne"}[p[4]]
		switch {
		case p[5] == "content items":
			kind = "noContent"
		case p[3] != "":
			kind = "notNonGrouping"
		case p[2] != "":
			kind = "maxOneGrouping"
		}
		vera[kind+"|"+p[1]+"|"+p[6]] = true
	}
	if len(vera) < 1000 {
		t.Fatalf("read %d veraPDF ISO 32005 rules; the profile has 1636", len(vera))
	}
	mine := iso32005Rules()
	var extra, missing []string
	for r := range mine {
		if !vera[r] && !slices.Contains(iso32005Stricter, r) {
			extra = append(extra, r)
		}
	}
	for r := range vera {
		if !mine[r] && !slices.Contains(iso32005NotImplemented, r) {
			missing = append(missing, r)
		}
	}
	for _, r := range iso32005NotImplemented {
		if mine[r] || !vera[r] {
			t.Errorf("%s is listed as veraPDF's alone, and it is not", r)
		}
	}
	slices.Sort(extra)
	slices.Sort(missing)
	if len(extra) > 0 {
		t.Errorf("rules pdf0 enforces that veraPDF does not state:\n%s", strings.Join(extra, "\n"))
	}
	if len(missing) > 0 {
		t.Errorf("rules veraPDF states that pdf0 does not, and not listed:\n%s", strings.Join(missing, "\n"))
	}
	t.Logf("%d rules implied by the table; %d stated by veraPDF; %d of them not implemented", len(mine), len(vera), len(iso32005NotImplemented))
}
