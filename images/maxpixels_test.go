package images

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
)

// TestMaxPixelsIsTheExtractionDefault holds the encoder's bound to the
// extraction budget's default. MaxPixels is written as a literal so godoc
// shows a number rather than the name of an internal constant no reader can
// follow; this test is what keeps the two from drifting apart.
func TestMaxPixelsIsTheExtractionDefault(t *testing.T) {
	if MaxPixels != core.DefaultMaxImagePixels {
		t.Fatalf("images.MaxPixels = %d, core.DefaultMaxImagePixels = %d: an image this package writes must be one extraction reads back by default", MaxPixels, core.DefaultMaxImagePixels)
	}
}
