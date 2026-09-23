package images

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// twoImageView is a one-page document drawing two 1×1 DeviceGray images,
// objects 10 and 11.
func twoImageView() core.View {
	objs := map[int]*object.IndirectObject{}
	set := func(n int, v object.Object) object.IndirectRef {
		objs[n] = &object.IndirectObject{Number: n, Value: v}
		return object.IndirectRef{Number: n}
	}
	im0 := set(10, imageXObject(1, 1, 8, "DeviceGray", "", []byte{0x40}))
	im1 := set(11, imageXObject(1, 1, 8, "DeviceGray", "", []byte{0x80}))
	xobj := object.NewDictionary(object.Entry{Key: "Im0", Value: im0}, object.Entry{Key: "Im1", Value: im1})
	res := object.NewDictionary(object.Entry{Key: "XObject", Value: xobj})
	page := set(3, object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("Page")}, object.Entry{Key: "Resources", Value: res}))
	pages := set(2, object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Pages")},
		object.Entry{Key: "Kids", Value: object.Array{page}},
		object.Entry{Key: "Count", Value: object.Integer(1)}))
	cat := set(1, object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("Catalog")}, object.Entry{Key: "Pages", Value: pages}))
	return core.View{
		Objects: objs,
		Trailer: object.NewDictionary(object.Entry{Key: "Root", Value: cat}),
		Limits:  core.DefaultLimits(),
	}
}

// TestOnePanickingImageDoesNotAbortTheWalk is audit 2026-09-22 C54. The
// images package promised "Note, not panic; one bad image does not abort the
// walk", and had no recover: every decode crash unwound into the caller. The
// fault is planted, because every crash extraction is known to have had is
// now fixed where it happened; the boundary is for the next one.
func TestOnePanickingImageDoesNotAbortTheWalk(t *testing.T) {
	extractImageHook = func(num int) {
		if num == 10 {
			panic("planted fault")
		}
	}
	defer func() { extractImageHook = nil }()

	var got []ExtractedImage
	walk(twoImageView(), func(im ExtractedImage) bool {
		got = append(got, im)
		return true
	})
	if len(got) != 2 {
		t.Fatalf("walk yielded %d images, want 2", len(got))
	}
	byNum := map[int]ExtractedImage{}
	for _, im := range got {
		byNum[im.ObjNum] = im
	}
	bad, good := byNum[10], byNum[11]
	if bad.Decoded || !strings.Contains(bad.Note, "internal error") || !strings.Contains(bad.Note, "planted fault") || len(bad.Encoded) != 1 {
		t.Errorf("the failing image: decoded=%v Note=%q Encoded=%v", bad.Decoded, bad.Note, bad.Encoded)
	}
	if !good.Decoded {
		t.Errorf("the image after it was not decoded: %q", good.Note)
	}
}

// The boundary is around the decode only. A panic in the caller's own loop
// body is the caller's, and must reach the caller.
func TestAPanicInTheCallersLoopPropagates(t *testing.T) {
	defer func() {
		if r := recover(); r != "caller's" {
			t.Errorf("recovered %v, want the caller's own panic", r)
		}
	}()
	walk(twoImageView(), func(ExtractedImage) bool { panic("caller's") })
	t.Error("Walk returned after its yield panicked")
}
