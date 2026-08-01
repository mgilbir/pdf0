package pdf0

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"
)

// The decoder's own unit tests — hand-encoded rows, make-up codes, malformed
// input — live with the decoder in internal/ccitt. What stays here is the part
// that needs a Document: the /DecodeParms plumbing and the real-world samples.

// TestExtractCCITTImage runs the full ExtractImages path on an image XObject
// whose codec is CCITTFaxDecode, exercising the /DecodeParms plumbing and the
// hand-off to samplesToImage.
func TestExtractCCITTImage(t *testing.T) {
	st := imageXObject(8, 1, 1, "DeviceGray", "CCITTFaxDecode", []byte{0x26, 0xAE})
	parms := &Dictionary{}
	parms.Set("K", Integer(-1))
	parms.Set("Columns", Integer(8))
	parms.Set("Rows", Integer(1))
	st.Dict.Set("DecodeParms", parms)

	doc := imageDoc(map[string]*Stream{"Im0": st})
	imgs := doc.ExtractImages()
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	img := imgs[0]
	if !img.Decoded {
		t.Fatalf("CCITT image not decoded: %s", img.Note)
	}
	g, ok := img.Image.(*image.Gray)
	if !ok {
		t.Fatalf("image is %T, want *image.Gray", img.Image)
	}
	// Left four pixels black, right four white.
	for x := 0; x < 8; x++ {
		want := uint8(0)
		if x >= 4 {
			want = 255
		}
		if got := g.GrayAt(x, 0).Y; got != want {
			t.Errorf("pixel %d = %d, want %d", x, got, want)
		}
	}
}

// TestCCITTRealFiles decodes the real-world CCITTFaxDecode sample PDFs (run
// `make ccitt` to fetch them) and asserts each Group 4 image decodes to a
// correctly-sized bilevel picture with genuine content (both colours present).
// The veraPDF corpus contains no CCITT images, so these external samples are the
// decoder's real-world oracle.
func TestCCITTRealFiles(t *testing.T) {
	dir := "testdata/ccitt"
	entries, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if err != nil || len(entries) == 0 {
		t.Skip("no CCITT sample PDFs; run `make ccitt`")
	}
	total := 0
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Errorf("%s: read: %v", filepath.Base(path), err)
			continue
		}
		found := 0
		for _, img := range doc.ExtractImages() {
			if img.Filter != "CCITTFaxDecode" {
				continue
			}
			found++
			total++
			if !img.Decoded {
				t.Errorf("%s obj %d: not decoded: %s", filepath.Base(path), img.ObjNum, img.Note)
				continue
			}
			g, ok := img.Image.(*image.Gray)
			if !ok {
				t.Errorf("%s obj %d: image is %T, want *image.Gray", filepath.Base(path), img.ObjNum, img.Image)
				continue
			}
			if b := g.Bounds(); b.Dx() != img.Width || b.Dy() != img.Height {
				t.Errorf("%s obj %d: decoded %dx%d, want %dx%d", filepath.Base(path), img.ObjNum, b.Dx(), b.Dy(), img.Width, img.Height)
			}
			black, white := 0, 0
			for _, p := range g.Pix {
				if p == 0 {
					black++
				} else {
					white++
				}
			}
			if black == 0 || white == 0 {
				t.Errorf("%s obj %d: image is a single colour (black=%d white=%d) — likely a decode error", filepath.Base(path), img.ObjNum, black, white)
			}
		}
		t.Logf("%s: %d CCITT image(s) decoded", filepath.Base(path), found)
	}
	if total == 0 {
		t.Error("sample PDFs present but no CCITT images extracted")
	}
}
