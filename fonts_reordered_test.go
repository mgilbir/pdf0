package pdf0

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// TestAReorderedStretchExtracts writes "ab שלום cd" as a left-to-right line
// with its Hebrew word in visual order, as shaping returns it, and reads the
// text back through pdf0 and poppler (#426).
//
// By default the Hebrew word alone is wrapped in an /ActualText: pdf0's
// extractor, which reads in drawing order, gets the line as written, and
// poppler keeps the word breaks either side of the word, which it lost when
// the whole run was one /ActualText. Poppler still returns the word itself
// reversed — it reorders an /ActualText as if it were in drawing order — and
// with SetMarkReordered(false), which leaves the order to it, it returns the
// line as written.
func TestAReorderedStretchExtracts(t *testing.T) {
	const text = "ab שלום cd"
	order := []int{0, 1, 2, 9, 7, 5, 3, 11, 12, 13} // visual order, HarfBuzz clusters
	var glyphs []fonttest.Glyph
	for _, r := range "abcd שלום" {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: r != ' '})
	}
	program := fonttest.SFNT(fonttest.SFNTOptions{Name: "Bidi", Glyphs: glyphs})

	write := func(t *testing.T, mark bool) (*Document, []byte) {
		t.Helper()
		face, err := fonts.Load(program)
		if err != nil {
			t.Fatal(err)
		}
		face.SetMarkReordered(mark)
		var run []fonts.Glyph
		for _, c := range order {
			g, _ := face.GlyphID([]rune(text[c:])[0])
			run = append(run, fonts.Glyph{GID: g, Cluster: c, XAdvance: 500})
		}
		var b content.Builder
		b.BeginText().SetFont("F1", 12).MoveText(20, 50)
		face.Draw(&b, text, run, 12)
		b.EndText()
		doc := NewDocument()
		if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b,
			Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
			t.Fatal(err)
		}
		return writeAndRead(t, doc)
	}

	t.Run("marked", func(t *testing.T) {
		back, data := write(t, true)
		if got, err := back.ExtractText(); err != nil || strings.TrimSpace(got) != text {
			t.Errorf("pdf0 extracts %q (%v); want %q", got, err, text)
		}
		words, ok := popplerRawWords(t, data)
		if !ok {
			return
		}
		if len(words) != 3 || words[0] != "ab" || words[2] != "cd" {
			t.Errorf("poppler extracts the words %q; want ab, the Hebrew word, cd", words)
		}
	})
	t.Run("unmarked", func(t *testing.T) {
		back, data := write(t, false)
		// Nothing says the order but the glyphs', which pdf0's extractor
		// reads as drawn: that is the cost of the setting, and what shows
		// no /ActualText was written.
		if got, err := back.ExtractText(); err != nil || strings.TrimSpace(got) != "ab םולש cd" {
			t.Errorf("pdf0 extracts %q (%v); want the Hebrew in drawing order, %q", got, err, "ab םולש cd")
		}
		words, ok := popplerRawWords(t, data)
		if !ok {
			return
		}
		if got := strings.Join(words, " "); got != text {
			t.Errorf("poppler extracts %q; want %q", got, text)
		}
	})
}

// popplerRawWords is the words pdftotext -raw reads from a one-page PDF, the
// directional embedding marks it puts around right-to-left text dropped; ok
// is false when pdftotext is not installed.
func popplerRawWords(t *testing.T, pdf []byte) (words []string, ok bool) {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Log("pdftotext is not installed; poppler's reading is not checked")
		return nil, false
	}
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-q", "-raw", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	s := strings.Map(func(r rune) rune {
		if r >= 0x202A && r <= 0x202E {
			return -1
		}
		return r
	}, string(out))
	return strings.Fields(s), true
}
