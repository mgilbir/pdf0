package pdf0

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Single-defect files, made here: a PDF/A skeleton veraPDF passes at each
// part, given one page and one thing wrong with it — or one thing that only
// looks wrong. Each records the clauses veraPDF fails it on at PDF/A-1b, -2b
// and -4, and pdf0 must report exactly those. The files are built by the
// test, never committed, and when PDF0_VERAPDF names veraPDF it is run over
// them too, so the recorded answers cannot drift from it.
//
// They pin what the corpus does not test: most of them are rules where pdf0
// once said more than veraPDF (a non-standard rendering intent or PostScript
// at PDF/A-4, which its profile has no rule for; a name no resource
// dictionary defines at PDF/A-1 and -2; a non-widget annotation without /AP
// at PDF/A-1) or less (BitsPerComponent, FileAttachment at PDF/A-4).

// defectFixture is one file: what is wrong with it, and veraPDF's clauses at
// 1b, 2b and 4 — none for a file it passes.
type defectFixture struct {
	name  string
	build func(d *Document, page, res *object.Dictionary) string // the page's content
	want  [3][]string
}

var defectLevels = [3]struct {
	level   pdfa.Level
	flavour string
}{{pdfa.PDFA1b, "1b"}, {pdfa.PDFA2b, "2b"}, {pdfa.PDFA4, "4"}}

func fxInt(v int) object.Integer { return object.Integer(v) }

// fxAnnot puts a on the page as its one annotation, printable, 100pt square.
func fxAnnot(d *Document, page, a *object.Dictionary) {
	a.Set("Type", object.Name("Annot"))
	a.Set("Rect", object.Array{fxInt(10), fxInt(10), fxInt(110), fxInt(110)})
	a.Set("F", fxInt(4))
	page.Set("Annots", object.Array{d.Add(a)})
}

// fxAppearance is an empty form XObject, an appearance stream.
func fxAppearance(d *Document) object.IndirectRef {
	return d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Form")},
		object.Entry{Key: "BBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(100), fxInt(100)}},
	), nil))
}

// fxImage draws a 1×1 DeviceGray image of the given BitsPerComponent; tweak
// changes its dictionary first.
func fxImage(d *Document, res *object.Dictionary, bpc int, tweak func(*object.Dictionary)) string {
	dict := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Image")},
		object.Entry{Key: "Width", Value: fxInt(1)},
		object.Entry{Key: "Height", Value: fxInt(1)},
		object.Entry{Key: "ColorSpace", Value: object.Name("DeviceGray")},
		object.Entry{Key: "BitsPerComponent", Value: fxInt(bpc)},
	)
	if tweak != nil {
		tweak(dict)
	}
	res.Set("XObject", object.NewDictionary(object.Entry{Key: "Im0", Value: d.Add(object.NewStream(dict, make([]byte, (bpc+7)/8)))}))
	return "q 10 0 0 10 0 0 cm /Im0 Do Q"
}

// fxForm draws a form XObject whose dictionary has extra entries.
func fxForm(d *Document, res *object.Dictionary, body string, extra ...object.Entry) string {
	dict := object.NewDictionary(append([]object.Entry{
		{Key: "Type", Value: object.Name("XObject")},
		{Key: "Subtype", Value: object.Name("Form")},
		{Key: "BBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(10), fxInt(10)}},
	}, extra...)...)
	res.Set("XObject", object.NewDictionary(object.Entry{Key: "X0", Value: d.Add(object.NewStream(dict, []byte(body)))}))
	return "/X0 Do"
}

var defectFixtures = []defectFixture{
	{name: "base", build: func(d *Document, p, r *object.Dictionary) string { return "" }},
	{name: "image-bpc8", build: func(d *Document, p, r *object.Dictionary) string { return fxImage(d, r, 8, nil) }},
	{name: "image-bpc16", build: func(d *Document, p, r *object.Dictionary) string { return fxImage(d, r, 16, nil) },
		want: [3][]string{{"6.2.4"}, nil, nil}},
	{name: "image-bpc12", build: func(d *Document, p, r *object.Dictionary) string { return fxImage(d, r, 12, nil) },
		want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "inline-image-bpc12", build: func(d *Document, p, r *object.Dictionary) string {
		return "q 10 0 0 10 0 0 cm BI /W 1 /H 1 /BPC 12 /CS /G ID \x00\x00 EI Q"
	}, want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "image-intent-custom", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) { i.Set("Intent", object.Name("Custom")) })
	}, want: [3][]string{{"6.2.9"}, {"6.2.6"}, nil}},
	{name: "ri-custom", build: func(d *Document, p, r *object.Dictionary) string { return "/Custom ri" },
		want: [3][]string{{"6.2.9"}, {"6.2.6"}, nil}},
	{name: "extgstate-ri-custom", build: func(d *Document, p, r *object.Dictionary) string {
		r.Set("ExtGState", object.NewDictionary(object.Entry{Key: "G0", Value: object.NewDictionary(object.Entry{Key: "RI", Value: object.Name("Custom")})}))
		return "/G0 gs"
	}, want: [3][]string{{"6.2.9"}, {"6.2.6"}, nil}},
	{name: "undefined-operator", build: func(d *Document, p, r *object.Dictionary) string { return "q BogusOp Q" },
		want: [3][]string{{"6.2.10"}, {"6.2.2"}, {"6.2.2"}}},
	{name: "xobject-defined-nowhere", build: func(d *Document, p, r *object.Dictionary) string { return "q /X0 Do Q" },
		want: [3][]string{nil, nil, {"6.2.2"}}},
	{name: "form-inherits-colour-space", build: func(d *Document, p, r *object.Dictionary) string {
		r.Set("ColorSpace", object.NewDictionary(object.Entry{Key: "CS0", Value: object.Array{object.Name("CalGray"),
			object.NewDictionary(object.Entry{Key: "WhitePoint", Value: object.Array{object.Real(0.9505), fxInt(1), object.Real(1.089)}})}}))
		return fxForm(d, r, "/CS0 cs 0.5 sc 0 0 5 5 re f")
	}, want: [3][]string{nil, {"6.2.2"}, {"6.2.2"}}},
	{name: "postscript-xobject", build: func(d *Document, p, r *object.Dictionary) string {
		ps := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("PS")},
		), []byte("%!PS"))
		r.Set("XObject", object.NewDictionary(object.Entry{Key: "P0", Value: d.Add(ps)}))
		return "/P0 Do"
	}, want: [3][]string{{"6.2.7"}, {"6.2.9"}, nil}},
	{name: "form-ps-entry", build: func(d *Document, p, r *object.Dictionary) string {
		return fxForm(d, r, "0 0 5 5 re f",
			object.Entry{Key: "PS", Value: d.Add(object.NewStream(&object.Dictionary{}, []byte("%!")))},
			object.Entry{Key: "Resources", Value: &object.Dictionary{}})
	}, want: [3][]string{{"6.2.5"}, {"6.2.9"}, nil}},
	{name: "text-annotation-without-ap", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Text")}))
		return ""
	}, want: [3][]string{nil, {"6.3.3"}, {"6.3.3"}}},
	{name: "widget-without-ap", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Widget")},
			object.Entry{Key: "FT", Value: object.Name("Tx")}, object.Entry{Key: "T", Value: object.String{Value: []byte("f")}}))
		return ""
	}, want: [3][]string{{"6.9"}, {"6.3.3"}, {"6.3.3"}}},
	{name: "popup-ap-with-down", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Popup")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)}, object.Entry{Key: "D", Value: fxAppearance(d)})}))
		return ""
	}, want: [3][]string{{"6.5.3"}, {"6.3.3"}, {"6.3.3"}}},
	{name: "file-attachment-annotation", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("FileAttachment")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})}))
		return ""
	}, want: [3][]string{{"6.5.2"}, nil, {"6.3.1"}}},
}

// buildDefect writes one fixture at one level: the level's skeleton with one
// page, as the fixture draws and dresses it.
func buildDefect(t *testing.T, f defectFixture, level pdfa.Level) []byte {
	t.Helper()
	d, err := NewPDFADocument(level)
	if err != nil {
		t.Fatal(err)
	}
	res, page := &object.Dictionary{}, &object.Dictionary{}
	drawn := f.build(d, page, res)
	pagesRef := d.ResolveDict(d.Trailer.Get("Root")).Get("Pages")
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", pagesRef)
	page.Set("MediaBox", object.Array{fxInt(0), fxInt(0), fxInt(200), fxInt(200)})
	page.Set("Resources", res)
	page.Set("Contents", d.Add(object.NewStream(&object.Dictionary{}, []byte(drawn))))
	pages := d.ResolveDict(pagesRef)
	pages.Set("Kids", object.Array{d.Add(page)})
	pages.Set("Count", fxInt(1))
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pdf0Clauses is the distinct clauses pdf0 fails a file on at a level,
// checker findings aside.
func pdf0Clauses(t *testing.T, data []byte, level pdfa.Level) []string {
	t.Helper()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range ValidatePDFA(doc, level) {
		if !IsCheckerFinding(v) && !slices.Contains(out, v.Rule) {
			out = append(out, v.Rule)
		}
	}
	slices.Sort(out)
	return out
}

// TestSingleDefectFilesAgreeWithVeraPDF: pdf0 fails each fixture on exactly
// the clauses veraPDF does, at each part; and when PDF0_VERAPDF names
// veraPDF, veraPDF still says what the fixtures record.
func TestSingleDefectFilesAgreeWithVeraPDF(t *testing.T) {
	vera := os.Getenv("PDF0_VERAPDF")
	for i, lv := range defectLevels {
		dir := filepath.Join(t.TempDir(), lv.flavour)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range defectFixtures {
			data := buildDefect(t, f, lv.level)
			want := slices.Clone(f.want[i])
			slices.Sort(want)
			if got := pdf0Clauses(t, data, lv.level); !slices.Equal(got, want) {
				t.Errorf("%s at %s: pdf0 fails it on %v; veraPDF, as the fixture records, on %v", f.name, lv.flavour, got, want)
			}
			if err := os.WriteFile(filepath.Join(dir, f.name+".pdf"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if vera == "" {
			continue
		}
		verdicts := veraPDFClauses(t, vera, lv.flavour, dir)
		for _, f := range defectFixtures {
			want := slices.Clone(f.want[i])
			slices.Sort(want)
			got, ok := verdicts[f.name+".pdf"]
			if !ok {
				t.Errorf("%s at %s: veraPDF gave no verdict", f.name, lv.flavour)
				continue
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s at %s: veraPDF fails it on %v, and the fixture records %v", f.name, lv.flavour, got, want)
			}
		}
	}
	if vera == "" {
		t.Log("PDF0_VERAPDF is not set: the fixtures were held to the clauses they record, not to veraPDF run now")
	}
}

// veraPDFClauses runs veraPDF over every PDF in dir at a flavour and returns,
// per file name, the distinct clauses it fails the file on (none for a pass).
func veraPDFClauses(t *testing.T, vera, flavour, dir string) map[string][]string {
	t.Helper()
	out, err := exec.Command(vera, "--flavour", flavour, "--format", "text", "-v", "--recurse", dir).Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("veraPDF: %v", err)
	}
	verdicts := map[string][]string{}
	var current string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "PASS "), strings.HasPrefix(line, "FAIL "):
			rest := line[5:]
			if i := strings.LastIndexByte(rest, ' '); i > 0 {
				rest = rest[:i]
			}
			current = filepath.Base(rest)
			verdicts[current] = nil
		case strings.HasPrefix(line, "  FAIL ") && current != "":
			rule := strings.TrimSpace(line[len("  FAIL "):])
			if i := strings.LastIndexByte(rule, '-'); i > 0 {
				rule = rule[:i]
			}
			if !slices.Contains(verdicts[current], rule) {
				verdicts[current] = append(verdicts[current], rule)
			}
		}
	}
	for k := range verdicts {
		slices.Sort(verdicts[k])
	}
	return verdicts
}
