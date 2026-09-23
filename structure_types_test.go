package pdf0

import (
	"strings"
	"testing"
)

// Structure checks compare the resolved type — role-mapped, and resolved in
// the element's namespace — never the /S as written (audit 2026-09-22 C80,
// C81, and the PDF/UA-2 namespace false positive PR 17 reported).

// taggedFixture is reachableFixture with a structure tree whose root holds
// rootExtra (a /RoleMap, a /Namespaces array) and whose /K is kids.
func taggedFixture(t *testing.T, annots, rootExtra, kids string, extra map[int]string) *Document {
	t.Helper()
	return reachableFixture(t, annots, "/MarkInfo << /Marked true >> /Lang (en) /StructTreeRoot 4 0 R", mergeObjs(map[int]string{
		4: "<< /Type /StructTreeRoot " + rootExtra + " /K [" + kids + "] >>",
	}, extra))
}

func mergeObjs(a, b map[int]string) map[int]string {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// TestUAFigureIsTheResolvedType is C80: an element mapped to Figure is a
// figure, and needs alternate text like one.
func TestUAFigureIsTheResolvedType(t *testing.T) {
	for _, c := range []struct {
		name, elem string
		want       bool
	}{
		{"written Figure", "<< /Type /StructElem /S /Figure /P 4 0 R >>", true},
		{"Img mapped to Figure", "<< /Type /StructElem /S /Img /P 4 0 R >>", true},
		{"Img mapped to Figure, with /Alt", "<< /Type /StructElem /S /Img /P 4 0 R /Alt (a chart) >>", false},
	} {
		d := taggedFixture(t, "", "/RoleMap << /Img /Figure >>", "5 0 R", map[int]string{5: c.elem})
		got := strings.Contains(messages(ValidatePDFUA(d)), "[PDF/UA-1 7.3] figure structure element has no non-empty alternate text")
		if got != c.want {
			t.Errorf("%s: 7.3 reported = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestUAAnnotationPlacementIsTheResolvedType is C81: an annotation's
// enclosing element is judged by its resolved type.
func TestUAAnnotationPlacementIsTheResolvedType(t *testing.T) {
	link := "<< /Type /Annot /Subtype /Link /Rect [0 0 1 1] /Contents (go) /StructParent 0 >>"
	for _, c := range []struct {
		s    string
		want string // the finding expected, "" for none
	}{
		{"Link", ""},
		{"MyLink", ""}, // mapped to Link
		{"MyPara", "nested in a <P> element, expected <Link>"},
	} {
		d := taggedFixture(t, "6 0 R", "/RoleMap << /MyLink /Link /MyPara /P >>", "5 0 R", map[int]string{
			5: "<< /Type /StructElem /S /" + c.s + " /P 4 0 R /K [<< /Type /OBJR /Obj 6 0 R >>] >>",
			6: link,
		})
		got := messages(ValidatePDFUA(d))
		if c.want == "" && strings.Contains(got, "is nested in a") {
			t.Errorf("/S /%s: %s", c.s, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("/S /%s: want %q in\n%s", c.s, c.want, got)
		}
	}
}

// TestUA2StructureNamespaces pins ISO 32000-2 14.8.6: a type is standard in
// the namespace the element names; the PDF 2.0 namespace drops the Annex M
// types and adds its own; a namespace's /RoleMapNS maps its types, into the
// default namespace by name or into another by [name ns]; MathML needs no
// mapping. PR 17 reported the first case: /Title in the PDF 2.0 namespace
// was "neither standard nor mapped".
func TestUA2StructureNamespaces(t *testing.T) {
	const (
		pdf2    = "10 0 R"
		custom  = "11 0 R"
		mathml  = "12 0 R"
		bare    = "13 0 R" // a namespace with no /RoleMapNS
		unknown = "is neither standard nor mapped"
	)
	spaces := map[int]string{
		10: "<< /Type /Namespace /NS (http://iso.org/pdf2/ssn) >>",
		11: "<< /Type /Namespace /NS (http://example.com/ns) /RoleMapNS << /Heading [/H1 10 0 R] /Para /P /Loop [/Loop2 11 0 R] /Loop2 [/Loop 11 0 R] >> >>",
		12: "<< /Type /Namespace /NS (http://www.w3.org/1998/Math/MathML) >>",
		13: "<< /Type /Namespace /NS (http://example.com/bare) >>",
	}
	root := "/Namespaces [10 0 R 11 0 R 12 0 R 13 0 R]"
	for _, c := range []struct {
		name, s, ns string
		mapped      bool
	}{
		{"Title in the PDF 2.0 namespace", "Title", pdf2, true},
		{"Title with no namespace (PDF 1.7)", "Title", "", false},
		{"Note is not a PDF 2.0 type", "Note", pdf2, false},
		{"FENote is", "FENote", pdf2, true},
		{"H7 is", "H7", pdf2, true},
		{"H07 is not", "H07", pdf2, false},
		{"mapped into the PDF 2.0 namespace", "Heading", custom, true},
		{"mapped into the default namespace", "Para", custom, true},
		{"a RoleMapNS cycle", "Loop", custom, false},
		{"MathML", "math", mathml, true},
		{"a namespace with no role map", "Heading", bare, false},
	} {
		ns := ""
		if c.ns != "" {
			ns = "/NS " + c.ns
		}
		d := taggedFixture(t, "", root, "5 0 R", mergeObjs(map[int]string{
			5: "<< /Type /StructElem /S /" + c.s + " " + ns + " /P 4 0 R >>",
		}, spaces))
		d.Version = "2.0"
		got := messages(ValidatePDFUA2(d))
		if has := strings.Contains(got, "structure type /"+c.s+" "+unknown); has == c.mapped {
			t.Errorf("%s: unmapped reported = %v, want %v\n%s", c.name, has, !c.mapped, got)
		}
	}

	// The resolved type is what every check reads: a custom heading mapped
	// to H1 in the PDF 2.0 namespace is the first heading, and one mapped to
	// H3 after it skips a level.
	d := taggedFixture(t, "", root, "5 0 R 6 0 R", mergeObjs(map[int]string{
		5: "<< /Type /StructElem /S /Heading /NS 11 0 R /P 4 0 R >>",
		6: "<< /Type /StructElem /S /H3 /NS 10 0 R /P 4 0 R >>",
	}, spaces))
	if got := messages(ValidatePDFUA2(d)); !strings.Contains(got, "heading level H3 follows H1") {
		t.Errorf("the namespaced headings were not read as H1 then H3:\n%s", got)
	}
}

// TestUA2NamespaceRules pins the two PDF/UA-2 rules the namespaced model
// brings, in the shapes of the veraPDF corpus files: a type in an explicit
// namespace mapped back into that namespace, directly or through another
// (8.2.4, 8.2.4-t03-fail-a/-b), and MathML's math outside a Formula
// (8.2.5.29). An element with no /NS mapped by the root /RoleMap within the
// default namespace is the ordinary case and is not a violation
// (8.2.4-t03-pass-a).
func TestUA2NamespaceRules(t *testing.T) {
	const pdf2 = "<< /Type /Namespace /NS (http://iso.org/pdf2/ssn) >>"
	for _, c := range []struct {
		name, root, elem, want string
		extra                  map[int]string
	}{
		{"mapped within its own namespace", "/Namespaces [11 0 R 12 0 R]",
			"<< /Type /StructElem /S /Q /NS 11 0 R /P 4 0 R >>", "8.2.4",
			map[int]string{11: "<< /Type /Namespace /NS (http://www.w3.org/1999/xhtml) /RoleMapNS << /Q [/P 11 0 R] /P [/P 12 0 R] >> >>", 12: pdf2}},
		{"mapped out and back", "/Namespaces [10 0 R 11 0 R]",
			"<< /Type /StructElem /S /Q /NS 11 0 R /P 4 0 R >>", "8.2.4",
			map[int]string{10: "<< /Type /Namespace /NS (http://www.w3.org/1999/xhtml) /RoleMapNS << /T [/P 11 0 R] >> >>",
				11: "<< /Type /Namespace /NS (http://iso.org/pdf2/ssn) /RoleMapNS << /Q [/T 10 0 R] >> >>"}},
		{"the default namespace's own role map", "/Namespaces [11 0 R] /RoleMap << /Q /P >>",
			"<< /Type /StructElem /S /Q /P 4 0 R >>", "",
			map[int]string{11: pdf2}},
		{"math outside a Formula", "/Namespaces [11 0 R]",
			"<< /Type /StructElem /S /P /P 4 0 R /K [12 0 R] >>", "8.2.5.29",
			map[int]string{11: "<< /Type /Namespace /NS (http://www.w3.org/1998/Math/MathML) >>",
				12: "<< /Type /StructElem /S /math /NS 11 0 R /P 5 0 R >>"}},
		{"math in a Formula", "/Namespaces [11 0 R]",
			"<< /Type /StructElem /S /Formula /P 4 0 R /K [12 0 R] >>", "",
			map[int]string{11: "<< /Type /Namespace /NS (http://www.w3.org/1998/Math/MathML) >>",
				12: "<< /Type /StructElem /S /math /NS 11 0 R /P 5 0 R >>"}},
	} {
		d := taggedFixture(t, "", c.root, "5 0 R", mergeObjs(map[int]string{5: c.elem}, c.extra))
		d.Version = "2.0"
		got := messages(ValidatePDFUA2(d))
		for _, clause := range []string{"8.2.4", "8.2.5.29"} {
			if has := strings.Contains(got, " "+clause+"]"); has != (clause == c.want) {
				t.Errorf("%s: %s reported = %v\n%s", c.name, clause, has, got)
			}
		}
		// PDF/UA-1 has no namespaces and no such rules.
		if got := messages(ValidatePDFUA(d)); strings.Contains(got, " 8.2.") {
			t.Errorf("%s: a PDF/UA-2 rule ran for PDF/UA-1:\n%s", c.name, got)
		}
	}
}
