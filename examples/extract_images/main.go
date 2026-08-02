// Command extract_images builds a small PDF holding two image XObjects, writes
// it, reads it back, and walks the images with the lazy Images iterator.
//
// It needs no test data: the images are generated in-process. Run it with
//
//	go run ./examples/extract_images
package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

const imgW, imgH = 8, 8

// rgbSamples returns 8-bit DeviceRGB samples for an imgW x imgH gradient: red
// rises along x, green along y, blue is constant. PDF packs samples row by row,
// each row starting on a byte boundary — at 8 bits per component that is just
// w*3 bytes per row.
func rgbSamples() []byte {
	data := make([]byte, 0, imgW*imgH*3)
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			data = append(data, byte(x*255/(imgW-1)), byte(y*255/(imgH-1)), 0x40)
		}
	}
	return data
}

// graySamples returns 8-bit DeviceGray samples for an imgW x imgH ramp.
func graySamples() []byte {
	data := make([]byte, 0, imgW*imgH)
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			data = append(data, byte((x+y)*255/(imgW+imgH-2)))
		}
	}
	return data
}

// flate compresses data the way a real producer would store image samples.
func flate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// imageXObject builds an image XObject stream. filter is "" for raw samples.
func imageXObject(cs object.Name, filter object.Name, data []byte) *object.Stream {
	d := object.Dictionary{}
	d.Set("Type", object.Name("XObject"))
	d.Set("Subtype", object.Name("Image"))
	d.Set("Width", object.Integer(imgW))
	d.Set("Height", object.Integer(imgH))
	d.Set("BitsPerComponent", object.Integer(8))
	d.Set("ColorSpace", cs)
	if filter != "" {
		d.Set("Filter", filter)
	}
	return &object.Stream{Dict: d, Data: data}
}

// buildDoc assembles a one-page document whose content stream draws both images.
func buildDoc() *pdf.Document {
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", object.IndirectRef{Number: 2})

	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(200), object.Integer(200)})
	page.Set("Contents", object.IndirectRef{Number: 4})
	page.Set("Resources", object.IndirectRef{Number: 5})

	// Both images are painted with Do, each scaled to a 64pt square.
	content := []byte("q 64 0 0 64 20 110 cm /ImRGB Do Q\nq 64 0 0 64 20 20 cm /ImGray Do Q\n")
	contentStream := &object.Stream{Dict: object.Dictionary{}, Data: content}

	xobjects := &object.Dictionary{}
	xobjects.Set("ImRGB", object.IndirectRef{Number: 6})
	xobjects.Set("ImGray", object.IndirectRef{Number: 7})
	resources := &object.Dictionary{}
	resources.Set("XObject", xobjects)

	// One Flate-compressed colour image and one uncompressed grayscale image, so
	// the walk reports two different /Filter values.
	rgbImage := imageXObject("DeviceRGB", "FlateDecode", flate(rgbSamples()))
	grayImage := imageXObject("DeviceGray", "", graySamples())

	return &pdf.Document{
		Version: "2.0",
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: catalog},
			2: {Number: 2, Value: pages},
			3: {Number: 3, Value: page},
			4: {Number: 4, Value: contentStream},
			5: {Number: 5, Value: resources},
			6: {Number: 6, Value: rgbImage},
			7: {Number: 7, Value: grayImage},
		},
		Trailer: object.Dictionary{
			Keys:   []object.Name{"Root"},
			Values: []object.Object{object.IndirectRef{Number: 1}},
		},
	}
}

func main() {
	// 1. Build and serialize the document.
	var buf bytes.Buffer
	if err := buildDoc().Write(&buf); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d bytes of PDF in memory\n\n", buf.Len())

	// 2. Read it back — from here on nothing knows how the file was produced.
	doc, err := pdf.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}

	// 3. Walk the images lazily. Images decodes one image at a time, so a large
	//    scan document never holds more than one decoded image in memory —
	//    unlike ExtractImages, which returns every decoded image at once.
	seen := map[string]bool{}
	count := 0
	for img := range doc.Images() {
		count++
		filter := img.Filter
		if filter == "" {
			filter = "(none)"
		}
		fmt.Printf("object %d: %dx%d %d bpc, colour space %s, filter %s\n",
			img.ObjNum, img.Width, img.Height, img.BitsPerComponent, img.ColorSpace, filter)
		if img.Decoded {
			b := img.Image.Bounds()
			r, g, b2, a := img.Image.At(0, 0).RGBA()
			fmt.Printf("  decoded: %T %dx%d, pixel(0,0) = rgba(%d,%d,%d,%d)\n",
				img.Image, b.Dx(), b.Dy(), r>>8, g>>8, b2>>8, a>>8)
		} else {
			// Not decoded: Encoded holds the bytes this package could not turn
			// into pixels, and Note says why.
			fmt.Printf("  not decoded (%d raw bytes): %s\n", len(img.Encoded), img.Note)
		}
		seen[img.ColorSpace] = true
	}

	// 4. Fail loudly, so this doubles as a CI guard on the extraction path.
	if count != 2 || !seen["DeviceRGB"] || !seen["DeviceGray"] {
		fmt.Fprintf(os.Stderr, "\nexpected 2 images (DeviceRGB and DeviceGray), got %d: %v\n", count, seen)
		os.Exit(1)
	}
	fmt.Printf("\nfound %d image XObjects, both decoded\n", count)
}
