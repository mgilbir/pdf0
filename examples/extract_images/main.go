// Command extract_images builds a small PDF holding two image XObjects, writes
// it, reads it back, and walks the images with the lazy Images iterator.
//
// The building half uses the writer this module provides — images.Embed for the
// XObjects, content.Builder for the drawing, AddPage for the page — so it also
// shows the two directions meeting: whatever is embedded here is what the walk
// below reports.
//
// It needs no test data: the images are generated in-process. Run it with
//
//	go run ./examples/extract_images
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"os"

	pdf "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
)

const imgW, imgH = 8, 8

// rgbImage is a colour ramp: every pixel differs from its neighbours, so an
// image written or read in the wrong order is obvious.
func rgbImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 255 / (imgW - 1)),
				G: uint8(y * 255 / (imgH - 1)),
				B: 128,
				A: 255,
			})
		}
	}
	return img
}

// grayImage is a diagonal ramp, written as DeviceGray because its type says so.
func grayImage() image.Image {
	img := image.NewGray(image.Rect(0, 0, imgW, imgH))
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8((x + y) * 255 / (imgW + imgH - 2))})
		}
	}
	return img
}

// buildDoc assembles a one-page document drawing both images.
func buildDoc() (*pdf.Document, error) {
	doc := &pdf.Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}}

	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	pagesRef := doc.Add(pages)
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", doc.Add(catalog))

	rgbRef, err := images.Embed(doc, rgbImage())
	if err != nil {
		return nil, err
	}
	grayRef, err := images.Embed(doc, grayImage())
	if err != nil {
		return nil, err
	}

	// An image XObject draws into the unit square, so the matrix is its size on
	// the page: each is scaled to a 64pt square.
	var page content.Builder
	page.Save().Concat(64, 0, 0, 64, 20, 110).Draw("ImRGB").Restore()
	page.Save().Concat(64, 0, 0, 64, 20, 20).Draw("ImGray").Restore()

	_, err = doc.AddPage(pdf.Page{
		Width: 200, Height: 200,
		Content:  &page,
		XObjects: map[object.Name]object.Object{"ImRGB": rgbRef, "ImGray": grayRef},
	})
	return doc, err
}

func main() {
	// 1. Build and serialize the document.
	doc0, err := buildDoc()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	if err := doc0.Write(&buf); err != nil {
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
