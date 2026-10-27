package htmlpdf

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

// TestEveryLayoutOptionIsDecided holds layoutOptions to layout.Options field by
// field. Each is either the caller's, passed through from the Options field of
// the same name, or the backend's, stated by layoutOptions whatever the caller
// passed. A field a newer forme adds is neither until someone decides which,
// and it fails here rather than reaching forme as a zero value nobody chose.
func TestEveryLayoutOptionIsDecided(t *testing.T) {
	callers := map[string]bool{"Page": true, "MinScale": true, "MinFontSizePt": true, "AllowScaleUp": true}
	backend := map[string]any{"TransformGroups": true}

	// Every caller field set to something other than its zero value, so a
	// field layoutOptions drops shows up as one that did not arrive.
	var opts Options
	ov := reflect.ValueOf(&opts).Elem()
	for name := range callers {
		f := ov.FieldByName(name)
		if !f.IsValid() {
			t.Fatalf("Options has no %s to pass to layout.Options", name)
		}
		switch f.Kind() {
		case reflect.Float64:
			f.SetFloat(0.25)
		case reflect.Bool:
			f.SetBool(true)
		default:
			f.Set(reflect.ValueOf(Letter))
		}
	}
	got := reflect.ValueOf(opts.layoutOptions())
	lt := got.Type()
	for i := 0; i < lt.NumField(); i++ {
		name := lt.Field(i).Name
		switch {
		case callers[name]:
			if want := ov.FieldByName(name).Interface(); !reflect.DeepEqual(got.Field(i).Interface(), want) {
				t.Errorf("layout.Options.%s is %v; the caller passed %v", name, got.Field(i).Interface(), want)
			}
		case backend[name] != nil:
			if !reflect.DeepEqual(got.Field(i).Interface(), backend[name]) {
				t.Errorf("layout.Options.%s is %v; this backend states %v", name, got.Field(i).Interface(), backend[name])
			}
		default:
			t.Errorf("layout.Options.%s is neither passed from Options nor stated for the backend: decide which", name)
		}
	}
}

// bytesResolver hands a document the bytes it names, and nothing else.
type bytesResolver map[string][]byte

func (r bytesResolver) Resolve(ref string) ([]byte, error) {
	if b, ok := r[ref]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("no %q", ref)
}

// notoSansBytes is forme's bundled Noto Sans, as a file a document's
// @font-face can load.
func notoSansBytes(t *testing.T) []byte {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/mgilbir/forme").Output()
	if err != nil {
		t.Fatalf("locating forme: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "fonts", "notosans", "NotoSans-Variable.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fontsOf is every font dictionary in a document, by /BaseFont, and whether a
// program for it is embedded: a FontFile, FontFile2 or FontFile3 in its
// descriptor, or in its descendant's for a Type 0 font. A Type 3 font is its
// own program.
func fontsOf(t *testing.T, doc *pdf0.Document) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, obj := range doc.Objects {
		d, ok := obj.Value.(*object.Dictionary)
		if !ok || d.Get("Type") != object.Name("Font") {
			continue
		}
		sub := d.Get("Subtype")
		if sub == object.Name("CIDFontType0") || sub == object.Name("CIDFontType2") {
			continue // seen through its Type 0 parent
		}
		base, _ := d.Get("BaseFont").(object.Name)
		embedded := sub == object.Name("Type3")
		program := d
		if sub == object.Name("Type0") {
			kids, _ := doc.Resolve(d.Get("DescendantFonts")).(object.Array)
			if len(kids) == 1 {
				program = doc.ResolveDict(kids[0])
			}
		}
		if fd := doc.ResolveDict(program.Get("FontDescriptor")); fd != nil {
			for _, k := range []object.Name{"FontFile", "FontFile2", "FontFile3"} {
				if fd.Get(k) != nil {
					embedded = true
				}
			}
		}
		out[string(base)] = embedded
	}
	return out
}

const notoFace = `<style>@font-face { font-family: Doc; src: url(noto.ttf) }
	@font-face { font-family: Doc; font-weight: bold; src: url(noto.ttf) }</style>`

// TestEmbedAllFontsLaysOutInTheDocumentsOwnFaces: with no font set, a document
// whose @font-face covers its text renders, and every font in the file is
// embedded. Without the option the same document's sans-serif and monospace
// come out as standard fonts, which is what the option is for.
func TestEmbedAllFontsLaysOutInTheDocumentsOwnFaces(t *testing.T) {
	res := bytesResolver{"noto.ttf": notoSansBytes(t)}
	in := Input{
		HTML: notoFace + `<p style="font-family: Doc">Own face, <b>bold too</b>.</p>` +
			`<ul style="font-family: Doc; text-shadow: 1px 1px red"><li>a marker and a shadow</li></ul>`,
		Resources: res,
	}
	out, err := Render(in, Options{Page: A4, EmbedAllFonts: true})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	fonts := fontsOf(t, out.Document)
	if len(fonts) == 0 {
		t.Fatal("the page has no fonts")
	}
	for name, embedded := range fonts {
		if !embedded {
			t.Errorf("%s is not embedded", name)
		}
	}

	generic := Input{HTML: `<p style="font-family: sans-serif">x</p><p style="font-family: monospace">y</p>`}
	out, err = Render(generic, Options{Page: A4})
	if err != nil {
		t.Fatalf("rendering without the option: %v", err)
	}
	fonts = fontsOf(t, out.Document)
	if embedded, ok := fonts["Helvetica"]; !ok || embedded {
		t.Errorf("without the option sans-serif is %v; want Helvetica, unembedded", fonts)
	}
}

// TestEmbedAllFontsRefusesAFamilyNothingSupplies: with no font set and a
// family the document brings no face for, the page is refused, not set in a
// standard font.
func TestEmbedAllFontsRefusesAFamilyNothingSupplies(t *testing.T) {
	res := bytesResolver{"noto.ttf": notoSansBytes(t)}
	for _, family := range []string{"sans-serif", "monospace", "Helvetica", "Courier"} {
		in := Input{
			HTML:      notoFace + `<p style="font-family: Doc">covered</p><p style="font-family: ` + family + `">not</p>`,
			Resources: res,
		}
		out, err := Render(in, Options{Page: A4, EmbedAllFonts: true})
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Errorf("%s: rendered (%v, fonts %v); want a refusal", family, err, fontsIfAny(t, out))
			continue
		}
		found := false
		for _, f := range refused.Findings {
			if f.Rule == layout.RuleNoFace && f.Severity == layout.Error {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: refused with %v; want %s", family, refused.Findings, layout.RuleNoFace)
		}
	}
}

func fontsIfAny(t *testing.T, out Result) map[string]bool {
	if out.Document == nil {
		return nil
	}
	return fontsOf(t, out.Document)
}

// TestEmbedAllFontsRefusesAStandardFaceFromTheCallersSet: a caller's own set
// that answers with a standard face is refused when the page is written, under
// RuleStandardFont, naming the face — and a policy that lowers the rule does
// not lower it, since the option already says what such a page is worth.
func TestEmbedAllFontsRefusesAStandardFaceFromTheCallersSet(t *testing.T) {
	for _, policy := range []layout.Policy{nil, {RuleStandardFont: layout.Ignore}, {RuleStandardFont: layout.Warn}} {
		in := Input{HTML: `<p style="font-family: sans-serif">x</p>`, Fonts: layout.StandardFonts(), Policy: policy}
		_, err := Render(in, Options{Page: A4, EmbedAllFonts: true})
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Errorf("policy %v: rendered (%v); want a refusal", policy, err)
			continue
		}
		found := false
		for _, f := range refused.Findings {
			if f.Rule == RuleStandardFont && f.Severity == layout.Error && strings.Contains(f.Message, "Helvetica") {
				found = true
			}
		}
		if !found {
			t.Errorf("policy %v: refused with %v; want %s at Error naming Helvetica", policy, refused.Findings, RuleStandardFont)
		}
	}
	// The same page without the option is the ordinary standard-font page.
	in := Input{HTML: `<p style="font-family: sans-serif">x</p>`, Fonts: layout.StandardFonts()}
	if _, err := Render(in, Options{Page: A4}); err != nil {
		t.Errorf("without the option: %v", err)
	}
}

// TestTheStandardFontRuleReadsEveryFaceInTheDisplayList: a face reaches the
// page from a run, a run's shadow, an emphasis mark and glyphs drawn by index,
// at any depth of clip, filter and transform group. checkDrawable reads each,
// and only under the option.
func TestTheStandardFontRuleReadsEveryFaceInTheDisplayList(t *testing.T) {
	std, err := shape.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	run := layout.DrawText{Text: "x", Face: std, Size: 10}
	nest := func(op layout.Op) layout.Op {
		return layout.ClipPath{Ops: []layout.Op{layout.FilterGroup{Ops: []layout.Op{
			layout.TransformGroup{Matrix: [6]float64{1, 0, 0, 1, 0, 0}, Ops: []layout.Op{op}}}}}}
	}
	for name, op := range map[string]layout.Op{
		"run":           run,
		"shadow":        layout.DrawTextShadow{Run: run},
		"emphasis mark": layout.DrawEmphasisMark{Mark: run},
		"glyphs":        layout.DrawGlyphs{Text: "x", Face: std, Size: 10},
		"nested run":    nest(run),
		"nested shadow": nest(layout.DrawTextShadow{Run: run}),
	} {
		composed := layout.Composed{Ops: []layout.Op{op}}
		findings, refused := checkDrawable(composed, nil, true)
		hit := false
		for _, f := range findings {
			if f.Rule == RuleStandardFont {
				hit = true
			}
		}
		if !hit || !refused {
			t.Errorf("%s: findings %v, refused %v; want %s", name, findings, refused, RuleStandardFont)
		}
		findings, _ = checkDrawable(composed, nil, false)
		for _, f := range findings {
			if f.Rule == RuleStandardFont {
				t.Errorf("%s: %s without the option", name, RuleStandardFont)
			}
		}
	}
}
