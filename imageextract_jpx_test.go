package pdf0

import (
	"image"
	"testing"

	"github.com/mgilbir/gopenjpeg"
)

// jpxComp builds a w×h test component whose samples are all v.
func jpxComp(w, h int, v int32, alpha uint16) gopenjpeg.Component {
	data := make([]int32, w*h)
	for i := range data {
		data[i] = v
	}
	return gopenjpeg.Component{
		Dx: 1, Dy: 1, W: uint32(w), H: uint32(h), Prec: 8, Alpha: alpha, Data: data,
	}
}

// TestJPXTwoComponentImage: real-world 2-component (grayscale + opacity) JPEG
// 2000 files panicked jpxComponentsToImage with "index out of range [2] with
// length 2" (Common Crawl sweep #13). They must render as gray + alpha.
func TestJPXTwoComponentImage(t *testing.T) {
	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, 2, 2, []gopenjpeg.Component{
		jpxComp(2, 2, 200, 0),
		jpxComp(2, 2, 128, 1),
	})
	got := jpxComponentsToImage(img)
	if got == nil {
		t.Fatal("2-component image not rendered")
	}
	rgba, ok := got.(*image.NRGBA)
	if !ok {
		t.Fatalf("got %T, want *image.NRGBA", got)
	}
	if r, g, b, a := rgba.Pix[0], rgba.Pix[1], rgba.Pix[2], rgba.Pix[3]; r != 200 || g != 200 || b != 200 || a != 128 {
		t.Errorf("pixel (0,0) = %d,%d,%d,%d; want 200,200,200,128", r, g, b, a)
	}
}

// TestJPXShortComponentData: a component whose Data slice is shorter than W×H
// (a damaged codestream) must read as 0, not panic.
func TestJPXShortComponentData(t *testing.T) {
	short := jpxComp(4, 4, 50, 0)
	short.Data = short.Data[:3]
	empty := jpxComp(4, 4, 50, 0)
	empty.W, empty.H, empty.Data = 0, 0, nil
	for _, comps := range [][]gopenjpeg.Component{
		{short},
		{jpxComp(4, 4, 10, 0), short},
		{jpxComp(4, 4, 10, 0), jpxComp(4, 4, 20, 0), short},
		{empty, empty, empty},
	} {
		if img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceUnknown, 0, 0, 4, 4, comps); len(comps) > 0 {
			_ = jpxComponentsToImage(img) // must not panic
		}
	}
}
