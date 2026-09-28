package htmlpdf

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
)

// TestABoldRunIsEmbeddedAsTheBoldInstance: forme sets <b> in a variable
// face at weight 700, as CSS Fonts 4 places it, and the DrawText carries the
// instance (a face of its own, with its own program and name). This backend
// keys its fonts by the face a run carries, so the bold run is its own font,
// and that font is the instance's: every width /W states for the bold run is
// the advance HarfBuzz and fontTools give the glyph at weight 700
// (testdata/fontinstance/oracle.txt), and the regular run's are the default
// master's.
func TestABoldRunIsEmbeddedAsTheBoldInstance(t *testing.T) {
	bold := boldOracle(t)
	in := Input{HTML: `<p>AVATAR <b>AVATAR</b></p>`, Fonts: notoSansSet(t)}
	c := layout.Compose(in, Options{})
	var runs []layout.DrawText
	for _, op := range c.Ops {
		if v, ok := op.(layout.DrawText); ok && strings.TrimSpace(v.Text) != "" {
			runs = append(runs, v)
		}
	}
	if len(runs) != 2 || runs[0].Face == runs[1].Face {
		t.Fatalf("layout drew %d runs of text, in one face: %v; the case needs the regular and the bold", len(runs), runs)
	}
	regular, boldFace := runs[0].Face, runs[1].Face
	if boldFace.IsVariable() || !regular.IsVariable() {
		t.Fatalf("the bold run's face is variable (%v) or the regular run's is not (%v); forme no longer instances <b>",
			boldFace.IsVariable(), regular.IsVariable())
	}

	doc, raw := roundTrip(t, in, Options{})
	if got := strings.Join(strings.Fields(mustExtractText(t, doc)), " "); got != "AVATAR AVATAR" {
		t.Errorf("extracted %q", got)
	}
	for _, f := range fontFindings(doc, raw) {
		t.Error(f)
	}
	res := doc.ResolveDict(doc.PageList()[0].Get("Resources"))
	fontDict := doc.ResolveDict(res.Get("Font"))
	byName := map[string]*object.Dictionary{}
	for name := range fontDict.Keys() {
		top := doc.ResolveDict(fontDict.Get(name))
		base, _ := top.Get("BaseFont").(object.Name)
		_, face, _ := strings.Cut(string(base), "+")
		desc := doc.ResolveDict(doc.Resolve(top.Get("DescendantFonts")).(object.Array)[0])
		byName[face] = desc
	}
	if len(byName) != 2 {
		t.Fatalf("the page names fonts %v; want the regular and the bold instance", byName)
	}
	for _, tc := range []struct {
		face  string
		want  func(r rune) float64
		which string
	}{
		{regular.Name(), func(r rune) float64 { g, _ := regular.GlyphID(r); return regular.GlyphAdvance(g) }, "the default master"},
		{boldFace.Name(), func(r rune) float64 { return bold[r] }, "HarfBuzz at weight 700"},
	} {
		desc, ok := byName[tc.face]
		if !ok {
			t.Errorf("no font on the page is %s; the page has %v", tc.face, byName)
			continue
		}
		for _, r := range "AVTR" {
			gid, _ := boldFace.GlyphID(r) // the instance keeps the master's glyph indices
			got := widthOf(t, doc, desc, gid)
			if want := tc.want(r); got != want || want == 0 {
				t.Errorf("%s: %q is %v wide in /W; %s says %v", tc.face, r, got, tc.which, want)
			}
		}
	}
	if a, _ := regular.GlyphID('A'); regular.GlyphAdvance(a) == bold['A'] {
		t.Error("'A' advances as far in the regular face as at weight 700; the case cannot tell the two apart")
	}
}

// boldOracle is what the oracle says Noto Sans advances at weight 700.
func boldOracle(t *testing.T) map[rune]float64 {
	t.Helper()
	f, err := os.Open(testfiles.Committed(t, "testdata/fontinstance/oracle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[rune]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 5 || fs[0] != "notosans" || fs[1] != "wght=700" {
			continue
		}
		cp, err1 := strconv.ParseInt(strings.TrimPrefix(fs[2], "U+"), 16, 32)
		adv, err2 := strconv.Atoi(fs[4])
		if err1 != nil || err2 != nil {
			t.Fatalf("oracle line %q", sc.Text())
		}
		out[rune(cp)] = float64(adv)
	}
	if len(out) == 0 {
		t.Fatal("the oracle states nothing for Noto Sans at weight 700")
	}
	return out
}

// widthOf is a CID's width as a CIDFont dictionary states it: its /W entry,
// or /DW.
func widthOf(t *testing.T, doc interface {
	Resolve(object.Object) object.Object
}, cid *object.Dictionary, code int) float64 {
	t.Helper()
	num := func(o object.Object) float64 {
		switch v := doc.Resolve(o).(type) {
		case object.Integer:
			return float64(v)
		case object.Real:
			return float64(v)
		}
		t.Fatalf("a width is %v, not a number", o)
		return 0
	}
	if w, ok := doc.Resolve(cid.Get("W")).(object.Array); ok {
		for i := 0; i+1 < len(w); {
			first := int(num(w[i]))
			if run, ok := doc.Resolve(w[i+1]).(object.Array); ok {
				if code >= first && code < first+len(run) {
					return num(run[code-first])
				}
				i += 2
				continue
			}
			if i+2 < len(w) && code >= first && code <= int(num(w[i+1])) {
				return num(w[i+2])
			}
			i += 3
		}
	}
	if dw := cid.Get("DW"); dw != nil {
		return num(dw)
	}
	return 1000
}
