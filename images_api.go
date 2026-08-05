package pdf0

import (
	"context"
	"iter"

	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/core"
)

// The image-extraction API. Everything it calls lives in the images package and
// reads the document through a core.View; the four functions here are the
// boundary that starts the run, builds that view and hands it down.

// ExtractImages returns every image XObject drawn from the document's pages, each
// decoded when the codec is one this package handles. Form XObjects are followed
// into their own resources, so images nested inside forms are found too.
//
// Every decoded image is held in the returned slice at once; on a large scan
// document that is unbounded memory. Use Images to iterate lazily with at most
// one decoded image live at a time.
func (d *Document) ExtractImages() []images.ExtractedImage {
	out, _ := extractImages(d, core.Canceler{})
	return out
}

// ExtractImagesContext is ExtractImages with cancellation.
//
// It returns the images extracted before the cancellation and an error wrapping
// ctx.Err(), for the reason ExtractTextContext does: extraction has no finding
// channel, so a short slice returned bare would be indistinguishable from a
// document with fewer images. The error is nil exactly when every image was
// reached.
//
// Cancellation is checked between images, so it takes effect after at most one
// image decode. A single very large image is therefore not interruptible; that
// residual is bounded by the codec budgets rather than by the context. See
// cancel.go.
//
// There is deliberately no context variant of Images: an iterator is already
// cancellable by breaking out of the range loop, and because each image is
// decoded only as it is yielded, breaking after image N skips exactly the work
// a context checked between images would have skipped.
func (d *Document) ExtractImagesContext(ctx context.Context) ([]images.ExtractedImage, error) {
	return extractImages(d, core.NewCanceler(ctx))
}

func extractImages(d *Document, cancel core.Canceler) ([]images.ExtractedImage, error) {
	var out []images.ExtractedImage
	walkImagesCancel(d, cancel, func(im images.ExtractedImage) bool {
		// Keep the image first, then stop: it is already decoded, and throwing
		// away finished work is not what cancellation is for.
		out = append(out, im)
		return !cancel.Stopped()
	})
	return out, cancel.StopErr("extracting images")
}

// Images returns an iterator over the image XObjects drawn from the document's
// pages, in the same order ExtractImages reports them. Each image is decoded
// only as it is yielded, so — unlike ExtractImages, which materializes every
// decoded image at once — iteration keeps at most one decoded image live at a
// time (unless the caller retains them), and breaking out of the loop skips
// the remaining decode work entirely.
func (d *Document) Images() iter.Seq[images.ExtractedImage] {
	return func(yield func(images.ExtractedImage) bool) { walkImages(d, yield) }
}

// walkImages drives the image traversal, calling yield for each image until it
// returns false.
func walkImages(d *Document, yield func(images.ExtractedImage) bool) {
	walkImagesCancel(d, core.Canceler{}, yield)
}

func walkImagesCancel(d *Document, cancel core.Canceler, yield func(images.ExtractedImage) bool) {
	// Install a per-run cache on a shallow copy, as the validators do: a tint
	// transform evaluates per pixel, and without the cache each evaluation
	// re-decoded the function stream (and re-parsed a type-4 program) — a
	// sub-megabyte image took minutes (sweep #13). The cache also carries the
	// cancellation signal, so the traversals it shares with the validators stop
	// on the same terms.
	//
	// This is the boundary: everything below it reads the document through a
	// view and never names Document.
	images.Walk(beginRunCancel(d, cancel).view(), yield)
}
