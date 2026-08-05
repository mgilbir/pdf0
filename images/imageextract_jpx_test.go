package images

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

// TestJPXTwoComponentImage: real-world 2-component (grey + extra channel)
// JPEG 2000 files panicked jpxComponentsToImage with "index out of range [2]
// with length 2" (Common Crawl sweep #13). Their handling follows the specs:
// /SMaskInData (ISO 32000-2, Table 87) governs whether the opacity channel is
// used — 0 (the default, and what the sweep files carry) means encoded
// soft-mask information shall be ignored, so the image renders grey from the
// colour channel alone; 1 renders it into NRGBA alpha; 2 means premultiplied
// colour, Go's RGBA representation.
func TestJPXTwoComponentImage(t *testing.T) {
	build := func() *gopenjpeg.Image {
		return gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, 2, 2, []gopenjpeg.Component{
			jpxComp(2, 2, 200, 0),
			jpxComp(2, 2, 128, 0),
		})
	}

	// SMaskInData 0: the extra channel is ignored; grey only.
	got := jpxComponentsToImage(build(), 0)
	g, ok := got.(*image.Gray)
	if !ok {
		t.Fatalf("SMaskInData 0: got %T, want *image.Gray (opacity ignored)", got)
	}
	if g.Pix[0] != 200 {
		t.Errorf("SMaskInData 0: pixel = %d, want 200", g.Pix[0])
	}

	// SMaskInData 1: the extra channel is the soft mask; grey + alpha.
	got = jpxComponentsToImage(build(), 1)
	n, ok := got.(*image.NRGBA)
	if !ok {
		t.Fatalf("SMaskInData 1: got %T, want *image.NRGBA", got)
	}
	if r, gr, b, a := n.Pix[0], n.Pix[1], n.Pix[2], n.Pix[3]; r != 200 || gr != 200 || b != 200 || a != 128 {
		t.Errorf("SMaskInData 1: pixel = %d,%d,%d,%d; want 200,200,200,128", r, gr, b, a)
	}

	// SMaskInData 2: colour premultiplied with opacity — Go's RGBA type.
	got = jpxComponentsToImage(build(), 2)
	p, ok := got.(*image.RGBA)
	if !ok {
		t.Fatalf("SMaskInData 2: got %T, want *image.RGBA (premultiplied)", got)
	}
	if r, a := p.Pix[0], p.Pix[3]; r != 200 || a != 128 {
		t.Errorf("SMaskInData 2: premultiplied pixel r=%d a=%d; want 200,128", r, a)
	}
}

// TestJPXCdefFlaggedAlpha: a cdef-flagged opacity channel (ISO 15444-1,
// surfaced as Component.Alpha) is honoured wherever it sits, and the colour
// channels are the unflagged ones.
func TestJPXCdefFlaggedAlpha(t *testing.T) {
	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, 2, 2, []gopenjpeg.Component{
		jpxComp(2, 2, 77, 1), // flagged opacity, deliberately first
		jpxComp(2, 2, 200, 0),
	})
	got := jpxComponentsToImage(img, 1)
	n, ok := got.(*image.NRGBA)
	if !ok {
		t.Fatalf("got %T, want *image.NRGBA", got)
	}
	if r, a := n.Pix[0], n.Pix[3]; r != 200 || a != 77 {
		t.Errorf("pixel r=%d a=%d; want colour 200 from the unflagged channel, alpha 77 from the flagged one", r, a)
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
		for _, smid := range []int{0, 1, 2} {
			img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceUnknown, 0, 0, 4, 4, comps)
			_ = jpxComponentsToImage(img, smid) // must not panic
		}
	}
}
