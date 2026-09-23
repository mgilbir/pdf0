package pdf0

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
)

// Recursion over file-supplied structure in image extraction is bounded
// (audit 2026-09-22 C13, C14, and the appearance-dictionary sibling). Each
// input used to recurse until the stack overflowed — a fatal error no recover
// can catch — and the child here runs with an 8 MiB stack so that a regression
// is reported as such within a second. Each case asserts what the image
// reports, not only that the walk returned.
func TestExtractImagesBoundsRecursion(t *testing.T) {
	for _, c := range []struct {
		name, note string
	}{
		{"cs-separation-cycle", "colour space object 6 contains itself"},
		{"cs-devicen-cycle", "colour space object 6 contains itself"},
		{"cs-iccbased-cycle", "colour space object 6 contains itself"},
		{"cs-indexed-cycle", "colour space object 6 contains itself"},
		{"cs-mutual-cycle", "colour space object 6 contains itself"},
		{"cs-iccbased-chain-20", "colour space nested more than 16 deep"},
		// The tint function does not parse, so the Separation space is
		// declined like any other unusable one.
		{"ps-brace-nest", "unsupported sample layout"},
	} {
		t.Run(c.name, func(t *testing.T) {
			hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second, MaxStack: 8 << 20}, func(t *testing.T) {
				imgs := readHostile(t, c.name).ExtractImages()
				if len(imgs) != 1 {
					t.Fatalf("got %d images, want 1", len(imgs))
				}
				if imgs[0].Decoded || !strings.Contains(imgs[0].Note, c.note) {
					t.Fatalf("decoded=%v Note=%q, want %q", imgs[0].Decoded, imgs[0].Note, c.note)
				}
			})
		})
	}
	t.Run("ap-state-cycle", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second, MaxStack: 8 << 20}, func(t *testing.T) {
			imgs := readHostile(t, "ap-state-cycle").ExtractImages()
			if len(imgs) != 1 || !imgs[0].Decoded {
				t.Fatalf("got %+v, want the page's one image, decoded", imgs)
			}
		})
	})
}

// The bounds leave real nesting alone: an Indexed space over an ICCBased space
// whose /Alternate is a device space, and a chain exactly at the depth bound.
func TestExtractImagesNestedColourSpacesStillDecode(t *testing.T) {
	for name, pdf := range map[string][]byte{
		"indexed over iccbased over gray": rawImagePage("<</Type/XObject/Subtype/Image/Width 1/Height 1/BitsPerComponent 8/ColorSpace 6 0 R>>", []byte{0},
			rawObj{dict: "[/Indexed 7 0 R 0 <80>]"},
			rawObj{dict: "[/ICCBased 8 0 R]"},
			rawObj{dict: "<</N 2/Alternate/DeviceGray>>", stream: []byte{0}}),
		// Fifteen ICCBased levels and the DeviceGray name beneath them make
		// sixteen: the deepest chain the bound admits.
		"a chain at the bound": iccChainPage(15),
	} {
		doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
		if err != nil {
			t.Fatal(err)
		}
		imgs := doc.ExtractImages()
		if len(imgs) != 1 || !imgs[0].Decoded {
			t.Errorf("%s: %+v", name, imgs)
			continue
		}
		if r, _, _, _ := imgs[0].Image.At(0, 0).RGBA(); r>>8 != 0x80 {
			t.Errorf("%s: pixel red = %#x, want 0x80", name, r>>8)
		}
	}
}
