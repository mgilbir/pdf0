package images

import (
	"math"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// TestHostileDimensionsRefused: /Width and /Height come from the file. A value
// whose products overflow int — an Integer near math.MaxInt64, or a Real that
// object.Int saturates to math.MaxInt (audit C118) — must make the sample-size
// checks fail, not wrap to a small number that passes them. Before the guard a
// saturated width wrapped the row size to 0, so a 16-byte stream "fitted" an
// image of math.MaxInt columns.
//
// This covers the size checks object.Int's saturation feeds. The per-image
// pixel budget and the codec branches are audit C11.
func TestHostileDimensionsRefused(t *testing.T) {
	data := make([]byte, 16)
	for _, w := range []object.Object{object.Integer(math.MaxInt64), object.Real(1e300), object.Integer(math.MaxInt64 / 8)} {
		for _, c := range []struct {
			cs  string
			bpc int
		}{{"DeviceGray", 1}, {"DeviceGray", 8}, {"DeviceRGB", 8}, {"DeviceGray", 16}} {
			st := imageXObject(1, 2, c.bpc, c.cs, "", data)
			st.Dict.Set("Width", w)
			var img ExtractedImage
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("width %v, %s %d bpc: panic %v", w, c.cs, c.bpc, r)
					}
				}()
				img = extractImage(core.View{Limits: core.DefaultLimits()}, st, 1)
			}()
			if img.Decoded {
				t.Errorf("width %v, %s %d bpc: decoded a %dx%d image from %d bytes", w, c.cs, c.bpc, img.Width, img.Height, len(data))
			}
		}
	}
	for _, c := range []struct {
		w, h, ncomp, bpc int
		want             bool
	}{
		{math.MaxInt, 2, 1, 8, false},
		{math.MaxInt / 8, 1, 1, 8, false},
		{2, math.MaxInt, 1, 8, false},
		{4, 4, 1, 8, true},
		{4, 5, 1, 8, false},
		{0, 1, 1, 8, false},
		{1, 1, 1, -8, false},
	} {
		if got := sampleDataFits(data, c.w, c.h, c.ncomp, c.bpc); got != c.want {
			t.Errorf("sampleDataFits(16 bytes, %d, %d, %d, %d) = %v, want %v", c.w, c.h, c.ncomp, c.bpc, got, c.want)
		}
	}
}
