// Package images reads images out of a PDF and writes images into one. Reach
// for it to pull the pictures out of a document, or to put a Go image.Image or
// a JPEG on a page you are building.
//
// Reading starts in the root package, because it walks a *pdf0.Document:
// pdf0.Document.ImagesContext yields each image XObject the pages draw, as an
// ExtractedImage, lazily and with at most one decoded image held at a time,
// and ends with an error if it stopped early (a cancelled context, the work
// budget, a Locked document). pdf0.Document.ExtractImagesContext returns them
// all at once. Samples under the general-purpose filters (Flate, LZW and the
// rest), DCTDecode (JPEG), CCITTFaxDecode, JBIG2Decode and JPXDecode (JPEG
// 2000) are decoded, and /Mask and /SMask transparency is applied. An image
// that cannot be decoded is not an error: its ExtractedImage has Decoded
// false, the encoded bytes, and a Note saying why.
//
//	for img, err := range doc.ImagesContext(ctx) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(img.ObjNum, img.Width, img.Height, img.Filter, img.Decoded)
//	}
//
// Writing is this package's own: Embed writes an image.Image losslessly,
// EmbedJPEG stores JPEG bytes as they are, and EmbedStencil writes a one-bit
// mask painted in the current fill colour. Each adds an image XObject to an
// Allocator, which *pdf0.Document is, and returns the reference a page names
// it by in pdf0.Page.XObjects; the page's content.Builder draws it with Draw.
// MaxPixels bounds what Embed and EmbedStencil will write, and it is also the
// default extraction budget (pdf0.WithMaxImagePixels), so extraction reads
// back anything they write.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/images.md covers both
// directions, the codecs and the budgets.
package images
