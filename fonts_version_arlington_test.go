package pdf0

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// arlVersionDeviations are the cases where Arlington's model states an
// earlier version than ISO 32000-2, whose version Embedded.Version follows:
// for them the check one version earlier is that Arlington still finds
// nothing.
var arlVersionDeviations = map[string]string{
	// ISO 32000-2 Table 124: FontFile2 "may appear in the font descriptor
	// ... (PDF 1.3) for a CIDFontType2 CIDFont dictionary". Arlington's
	// FontDescriptorCIDType2 has FontFile2 since 1.2.
	"truetype": "FontFile2 in a CIDFontType2 descriptor: ISO 32000-2 Table 124 says PDF 1.3, Arlington 1.2",
}

// TestEmbeddedVersionAgreesWithArlington holds fonts.Embedded.Version to an
// oracle outside this module: the PDF Association's Arlington model, which
// states for every key and value the PDF version that introduced it. Each
// kind of face is written into a page, and the font objects are walked
// against Arlington's grammar of the version Embedded.Version names, which
// must find nothing in them; and against the version before it, which must
// find something, or the version stated is later than the font needs.
func TestEmbeddedVersionAgreesWithArlington(t *testing.T) {
	cases := []struct {
		name  string
		face  func(t *testing.T) *fonts.Face
		text  string
		forms fonts.Forms
	}{
		{"truetype", textFontKinds[0].face, "plain text", fonts.Forms{Horizontal: true}},
		{"namecff", textFontKinds[2].face, "Hello", fonts.Forms{Horizontal: true}},
		{"namecff-bare", textFontKinds[2].face, "Hello", fonts.Forms{Horizontal: true, BareCFF: true}},
		{"cidcff", textFontKinds[1].face, "日本語", fonts.Forms{Horizontal: true}},
		{"cidcff-bare", textFontKinds[1].face, "日本語", fonts.Forms{Horizontal: true, BareCFF: true}},
		{"simple", func(t *testing.T) *fonts.Face { return simpleFace(t) }, "Hello", fonts.Forms{Horizontal: true}},
		{"strikes", func(t *testing.T) *fonts.Face { return bitmapFace(t, "Strikes.ttf") }, "AB", fonts.Forms{Horizontal: true}},
		{"strikes-opaque", func(t *testing.T) *fonts.Face { return bitmapFace(t, "Strikes.ttf") }, "AB", fonts.Forms{Horizontal: true, Opaque: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			face := c.face(t)
			var b content.Builder
			b.BeginText().SetFont("F1", 12)
			face.DrawShaped(&b, c.text, 12)
			b.EndText()
			doc := NewDocument()
			doc.Version = "1.7"
			e, err := face.EmbedForms(doc, c.forms)
			if err != nil {
				t.Fatal(err)
			}
			fontsOf := map[object.Name]object.Object{"F1": e.Horizontal}
			for k, ref := range e.Subfonts {
				fontsOf[fonts.SubfontName("F1", k+1)] = ref
			}
			if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b, Fonts: fontsOf}); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatal(err)
			}
			fontFindings := func(version string) []string {
				var out []string
				for _, f := range arlVersionFindings(t, buf.Bytes(), version) {
					if strings.Contains(f, "Font") {
						out = append(out, f)
					}
				}
				return out
			}
			if got := fontFindings(e.Version); len(got) > 0 {
				t.Errorf("Version %s, and Arlington's PDF %s grammar finds %v", e.Version, e.Version, got)
			}
			minor, _ := strconv.Atoi(strings.TrimPrefix(e.Version, "1."))
			before := "1." + strconv.Itoa(minor-1)
			if why, ok := arlVersionDeviations[c.name]; ok {
				// Arlington is held to its deviation instead: if it comes to
				// agree with ISO 32000-2, the entry goes.
				if got := fontFindings(before); len(got) > 0 {
					t.Errorf("Arlington now finds %v at PDF %s; the deviation (%s) is gone, remove it", got, before, why)
				}
				return
			}
			if got := fontFindings(before); len(got) == 0 {
				t.Errorf("Version %s, but Arlington's PDF %s grammar finds nothing the font needs a later version for", e.Version, before)
			} else {
				t.Logf("Version %s; at PDF %s Arlington finds %v", e.Version, before, got)
			}
		})
	}
}
