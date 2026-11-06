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

// TestAReorderedStretchExtracts writes right-to-left text as a shaper draws
// it — its glyphs in visual order — and reads it back through pdf0 and
// poppler (#426, #435).
//
// By default a reordered stretch is left to the reader, which puts each line
// in reading order by the bidirectional algorithm: pdf0's ExtractText does,
// and so does poppler. With SetMarkReordered(true), as on a tagged page, the
// stretch is an /ActualText, which pdf0 reads as it stands; poppler reorders
// it again and gets the word backwards, but keeps the words apart.
func TestAReorderedStretchExtracts(t *testing.T) {
	var glyphs []fonttest.Glyph
	for _, r := range "abcd שלוםע" {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: r != ' '})
	}
	program := fonttest.SFNT(fonttest.SFNTOptions{Name: "Bidi", Glyphs: glyphs})

	write := func(t *testing.T, text string, order []int, mark bool) (*Document, []byte) {
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
		if _, err := doc.AddPage(Page{Width: 300, Height: 100, Content: &b,
			Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
			t.Fatal(err)
		}
		return writeAndRead(t, doc)
	}

	for _, tc := range []struct {
		name, text string
		order      []int // visual order, HarfBuzz clusters
	}{
		{"a Hebrew word in a Latin line", "ab שלום cd", []int{0, 1, 2, 9, 7, 5, 3, 11, 12, 13}},
		{"a Hebrew line", "שלום עולם", []int{15, 13, 11, 9, 8, 6, 4, 2, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mark := range []bool{false, true} {
				back, data := write(t, tc.text, tc.order, mark)
				if got, err := back.ExtractText(); err != nil || strings.TrimSpace(got) != tc.text {
					t.Errorf("marked %v: pdf0 extracts %q (%v); want %q", mark, got, err, tc.text)
				}
				if mark {
					continue
				}
				// Without its spaces: poppler's own layout puts the space
				// beside a right-to-left word inside the word's embedding,
				// mark or no mark, and that is not what this checks.
				nospace := func(s string) string { return strings.ReplaceAll(s, " ", "") }
				if got, ok := popplerText(t, data); ok && nospace(got) != nospace(tc.text) {
					t.Errorf("poppler extracts %q; want %q", got, tc.text)
				}
			}
		})
	}
}

// popplerText is what pdftotext reads from a one-page PDF, its words joined
// by single spaces and the directional embedding marks it puts around
// right-to-left text dropped; ok is false when pdftotext is not installed.
func popplerText(t *testing.T, pdf []byte) (string, bool) {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Log("pdftotext is not installed; poppler's reading is not checked")
		return "", false
	}
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-q", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	s := strings.Map(func(r rune) rune {
		if r >= 0x202A && r <= 0x202E {
			return -1
		}
		return r
	}, string(out))
	return strings.Join(strings.Fields(s), " "), true
}
