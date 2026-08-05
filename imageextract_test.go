package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// imageXObject makes an image XObject dictionary + stream.
func imageXObject(w, h, bpc int, cs, filter string, data []byte) *object.Stream {
	d := object.Dictionary{}
	d.Set("Type", object.Name("XObject"))
	d.Set("Subtype", object.Name("Image"))
	d.Set("Width", object.Integer(w))
	d.Set("Height", object.Integer(h))
	d.Set("BitsPerComponent", object.Integer(bpc))
	if cs != "" {
		d.Set("ColorSpace", object.Name(cs))
	}
	if filter != "" {
		d.Set("Filter", object.Name(filter))
	}
	return &object.Stream{Dict: d, Data: data}
}

func imageDoc(images map[string]*object.Stream) *Document {
	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "2.0"}
	set := func(n int, v object.Object) { d.Objects[n] = &object.IndirectObject{Number: n, Value: v} }
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	set(1, cat)
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	set(2, pages)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	res := &object.Dictionary{}
	xo := &object.Dictionary{}
	num := 10
	for name, st := range images {
		xo.Set(object.Name(name), object.IndirectRef{Number: num})
		set(num, st)
		num++
	}
	res.Set("XObject", xo)
	page.Set("Resources", res)
	set(3, page)
	d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

func TestExtractImages(t *testing.T) {
	// A JPEG image (DCTDecode) decoded via image/jpeg.
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.SetGray(x, y, color.Gray{Y: byte(x * 32)})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	d := imageDoc(map[string]*object.Stream{
		"Jpeg": imageXObject(8, 8, 8, "DeviceGray", "DCTDecode", jb.Bytes()),
		"Gray": imageXObject(2, 2, 8, "DeviceGray", "", []byte{0, 64, 128, 255}),
		"RGB":  imageXObject(1, 2, 8, "DeviceRGB", "", []byte{255, 0, 0, 0, 255, 0}),
		"Fax":  imageXObject(16, 16, 1, "", "CCITTFaxDecode", []byte{0x26, 0x01, 0xff}),
	})

	imgs := d.ExtractImages()
	if len(imgs) != 4 {
		t.Fatalf("expected 4 images, got %d", len(imgs))
	}
	var jpg, ccitt, decoded int
	for _, im := range imgs {
		if im.Decoded {
			decoded++
		}
		switch im.Filter {
		case "DCTDecode":
			jpg++
			if !im.Decoded || im.Image.Bounds().Dx() != 8 {
				t.Errorf("JPEG image not decoded correctly: %+v", im)
			}
		case "CCITTFaxDecode":
			ccitt++
			if im.Decoded || im.Image != nil || len(im.Encoded) == 0 || im.Note == "" {
				t.Errorf("CCITT image should be undecoded with raw bytes and a note: %+v", im)
			}
		}
	}
	if jpg != 1 || ccitt != 1 {
		t.Errorf("expected one JPEG and one CCITT image; got jpg=%d ccitt=%d", jpg, ccitt)
	}
	if decoded != 3 { // JPEG + raw gray + raw RGB; not CCITT
		t.Errorf("expected 3 decoded images, got %d", decoded)
	}
}
