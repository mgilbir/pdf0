package pdf0

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/hostile"
)

// Every image codec, and every buffer sized from an image's geometry, is held to
// one pixel budget (audit 2026-09-22 C11, C12 and their siblings). Each input
// below allocated gigabytes, or had no bound at all, before it was: the raw
// sample path trusted /Width and /Height once the data fit them, the fax
// decoder capped rows and not row × width, image/jpeg and the JPEG 2000 codec
// allocate from their own headers, and a soft mask has geometry of its own.
//
// Each case asserts the outcome the budget promises — the image is not
// decoded, its Note names the image-pixels guard, and the walk goes on — not
// merely that the process survived.
func TestExtractImagesHoldsEveryCodecToThePixelBudget(t *testing.T) {
	for _, name := range []string{
		"image-dim-2^60",
		"image-22000-square",
		"jpeg-header-65500",
		"jpx-header-60000",
		"ccitt-v0-flood",
	} {
		t.Run(name, func(t *testing.T) {
			hostile.Run(t, hostile.Limits{MaxRSS: 384 << 20, Timeout: time.Minute}, func(t *testing.T) {
				imgs := readHostile(t, name).ExtractImages()
				if len(imgs) != 1 {
					t.Fatalf("got %d images, want 1", len(imgs))
				}
				im := imgs[0]
				if im.Decoded || im.Image != nil {
					t.Fatalf("an image over the budget was decoded: %+v", im)
				}
				if !strings.Contains(im.Note, "resource limit reached (image-pixels)") || !strings.Contains(im.Note, "pdf0's default") {
					t.Fatalf("Note = %q, want the image-pixels budget at its default", im.Note)
				}
				if len(im.Encoded) == 0 {
					t.Error("a refused image keeps its encoded bytes")
				}
			})
		})
	}
}

// A soft mask over the budget leaves the image decoded, unmasked, and says so.
func TestExtractImagesSoftMaskOverBudget(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 384 << 20, Timeout: time.Minute}, func(t *testing.T) {
		imgs := readHostile(t, "smask-22000-square").ExtractImages()
		if len(imgs) != 1 {
			t.Fatalf("got %d images, want 1", len(imgs))
		}
		im := imgs[0]
		if !im.Decoded {
			t.Fatalf("the image itself fits the budget and should decode: %q", im.Note)
		}
		if !strings.Contains(im.Note, "/SMask was not applied") || !strings.Contains(im.Note, "(image-pixels)") {
			t.Fatalf("Note = %q, want the soft mask's budget refusal", im.Note)
		}
		if _, _, _, a := im.Image.At(0, 0).RGBA(); a != 0xFFFF {
			t.Errorf("the unapplied mask changed the alpha: %#x", a)
		}
	})
}

// The budget is the caller's to move, in both directions, and the Note says
// whose bound it was.
func TestWithMaxImagePixels(t *testing.T) {
	pdf := rawImagePage("<</Type/XObject/Subtype/Image/Width 20/Height 20/BitsPerComponent 8/ColorSpace/DeviceGray>>", make([]byte, 400))
	extract := func(opts ...Option) images.ExtractedImage {
		t.Helper()
		doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)), opts...)
		if err != nil {
			t.Fatal(err)
		}
		imgs := doc.ExtractImages()
		if len(imgs) != 1 {
			t.Fatalf("got %d images", len(imgs))
		}
		return imgs[0]
	}
	if im := extract(); !im.Decoded {
		t.Fatalf("default budget: %q", im.Note)
	}
	if im := extract(WithMaxImagePixels(400)); !im.Decoded {
		t.Fatalf("a budget of exactly the image: %q", im.Note)
	}
	im := extract(WithMaxImagePixels(399))
	if im.Decoded || !strings.Contains(im.Note, "(image-pixels)") || !strings.Contains(im.Note, "configured by the caller") {
		t.Fatalf("one pixel under: decoded=%v Note=%q", im.Decoded, im.Note)
	}
}
