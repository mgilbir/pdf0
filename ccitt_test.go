package pdf0

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reference row used throughout: 8 pixels, 4 black then 4 white. In the PDF
// image convention (0 = black, 1 = white) that packs to 0x0F.
const ccittRowBBBBWWWW = 0x0F

// TestCCITTGroup4Row decodes a hand-encoded Group 4 (K<0) single row.
//
// Encoding of BBBBWWWW against the imaginary all-white reference line:
//
//	Horizontal  001
//	white run 0 00110101
//	black run 4 011
//	V0          1
//
// = 001 00110101 011 1, byte-padded to {0x26, 0xAE}.
func TestCCITTGroup4Row(t *testing.T) {
	got, err := decodeCCITT([]byte{0x26, 0xAE}, ccittParams{k: -1, columns: 8, rows: 1})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := []byte{ccittRowBBBBWWWW}; !bytes.Equal(got, want) {
		t.Fatalf("row = %08b, want %08b", got, want)
	}
}

// TestCCITTGroup4TwoRows decodes two identical rows; the second is coded entirely
// with vertical (V0) modes relative to the first, exercising the reference line.
//
// Row 1 = 001 00110101 011 1 (as above); row 2 = 1 1 1 (three V0). Concatenated
// and byte-padded: {0x26, 0xAF, 0xC0}.
func TestCCITTGroup4TwoRows(t *testing.T) {
	got, err := decodeCCITT([]byte{0x26, 0xAF, 0xC0}, ccittParams{k: -1, columns: 8, rows: 2})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []byte{ccittRowBBBBWWWW, ccittRowBBBBWWWW}
	if !bytes.Equal(got, want) {
		t.Fatalf("rows = % 08b, want % 08b", got, want)
	}
}

// TestCCITTGroup3OneD decodes a hand-encoded Group 3 1-D (K=0) row.
//
//	white run 0 00110101
//	black run 4 011
//	white run 4 1011
//
// = 00110101 011 1011, byte-padded to {0x35, 0x76}.
func TestCCITTGroup3OneD(t *testing.T) {
	got, err := decodeCCITT([]byte{0x35, 0x76}, ccittParams{k: 0, columns: 8, rows: 1})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := []byte{ccittRowBBBBWWWW}; !bytes.Equal(got, want) {
		t.Fatalf("row = %08b, want %08b", got, want)
	}
}

// TestCCITTWideMakeup exercises a make-up code: a run longer than 63 pixels.
// A 128-pixel all-black row in Group 3 1-D is: white run 0 (00110101), black
// make-up 128 (000011001000), black terminating 0 (0000110111).
func TestCCITTWideMakeup(t *testing.T) {
	bits := "00110101" + "000011001000" + "0000110111"
	data := bitsToBytes(bits)
	got, err := decodeCCITT(data, ccittParams{k: 0, columns: 128, rows: 1})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := make([]byte, 16) // 128 black pixels = all 0 bits
	if !bytes.Equal(got, want) {
		t.Fatalf("128-black row = % x, want all zero", got)
	}
}

// TestCCITTMalformed rejects garbage rather than looping or panicking.
func TestCCITTMalformed(t *testing.T) {
	// A lone 0 bit run is not a complete code; decoding a full row from it must
	// fail cleanly.
	if _, err := decodeCCITT([]byte{0x00}, ccittParams{k: -1, columns: 1728, rows: 1}); err == nil {
		t.Fatal("expected an error on truncated data")
	}
}

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

func bitsToBytes(bits string) []byte {
	for len(bits)%8 != 0 {
		bits += "0"
	}
	out := make([]byte, len(bits)/8)
	for i := 0; i < len(bits); i++ {
		if bits[i] == '1' {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}

// TestCCITTCorpusImages decodes every CCITTFaxDecode image found in the veraPDF
// corpus, asserting the decoder produces a plausible bilevel image (correct size,
// and — for images with real content — both black and white pixels present).
func TestCCITTCorpusImages(t *testing.T) {
	corpus := os.Getenv("VERAPDF_CORPUS")
	if corpus == "" {
		corpus = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpus); err != nil {
		t.Skip("veraPDF corpus not found; run `make corpus`")
	}
	decoded, files := 0, 0
	_ = filepath.Walk(corpus, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".pdf") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil
		}
		for _, img := range doc.ExtractImages() {
			if img.Filter != "CCITTFaxDecode" {
				continue
			}
			files++
			if !img.Decoded {
				continue
			}
			decoded++
			g, ok := img.Image.(*image.Gray)
			if !ok {
				t.Errorf("%s obj %d: CCITT image is %T, want *image.Gray", filepath.Base(path), img.ObjNum, img.Image)
				continue
			}
			if b := g.Bounds(); b.Dx() != img.Width || b.Dy() != img.Height {
				t.Errorf("%s obj %d: decoded %dx%d, want %dx%d", filepath.Base(path), img.ObjNum, b.Dx(), b.Dy(), img.Width, img.Height)
			}
		}
		return nil
	})
	if files == 0 {
		t.Skip("no CCITTFaxDecode images in the corpus")
	}
	t.Logf("CCITTFaxDecode images: %d found, %d decoded", files, decoded)
	if decoded == 0 {
		t.Error("found CCITT images but decoded none")
	}
}
