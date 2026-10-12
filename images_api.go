package pdf0

import (
	"context"
	"iter"

	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/core"
)

// The image-extraction API. Everything it calls lives in the images package and
// reads the document through a core.View; the functions here are the boundary
// that starts the run, builds that view and hands it down.

// ExtractImages returns every image XObject drawn from the document's pages, each
// decoded when the codec is one this package handles. Form XObjects are followed
// into their own resources, so images nested inside forms are found too.
//
// It has no error result, so a short slice cannot say it is short. When the
// run's work budget (WithMaxWork) runs out, the images reached before it are
// returned and the rest are silently missing; a Locked document returns none.
// ExtractImagesContext returns the same images and an error saying why the
// rest are missing.
//
// Every decoded image is held in the returned slice at once; on a large scan
// document that is unbounded memory. ImagesContext iterates lazily, with at
// most one decoded image live at a time.
//
// Deprecated: Use ExtractImagesContext, which reports a stop, or
// ImagesContext. ExtractImages is ExtractImagesContext with
// context.Background() and the error discarded.
func (d *Document) ExtractImages() []images.ExtractedImage {
	out, _ := extractImages(d, core.Canceler{})
	return out
}

// ExtractImagesContext is ExtractImages with cancellation, and with an error
// that says when the result is short.
//
// It returns the images extracted before the stop and an error saying why:
// one wrapping ctx.Err() for a cancelled or expired context, one matching
// ErrResourceLimit when the work budget (WithMaxWork) ran out. Both, for the
// reason ExtractTextContext returns both: extraction has no finding channel,
// so a short slice returned bare would be indistinguishable from a document
// with fewer images. The error is nil exactly when every image was reached.
// An image that was reached but not decoded — an unsupported codec, corrupt
// data, over the image-pixel budget — is in the slice with Decoded false and
// a Note saying why; that is not an error.
//
// A Locked document returns no images and an error wrapping LockReason: its
// image data is ciphertext, and handing it back as each image's encoded bytes
// would present it as data.
//
// Cancellation is checked between images, so it takes effect after at most one
// image decode. A single very large image is therefore not interruptible; that
// residual is bounded by the codec budgets rather than by the context.
func (d *Document) ExtractImagesContext(ctx context.Context) ([]images.ExtractedImage, error) {
	return extractImages(d, core.NewCanceler(ctx))
}

func extractImages(d *Document, cancel core.Canceler) ([]images.ExtractedImage, error) {
	var out []images.ExtractedImage
	for im, err := range walkImages(d, cancel) {
		if err != nil {
			return out, err
		}
		out = append(out, im)
	}
	return out, nil
}

// Images returns an iterator over the image XObjects drawn from the document's
// pages, in the same order ExtractImages reports them. Each image is decoded
// only as it is yielded, so — unlike ExtractImages, which materializes every
// decoded image at once — iteration keeps at most one decoded image live at a
// time (unless the caller retains them), and breaking out of the loop skips
// the remaining decode work entirely.
//
// Like ExtractImages, it cannot say it stopped short: when the work budget
// (WithMaxWork) runs out the iteration simply ends, and a Locked document
// yields nothing. ImagesContext is the same iteration with a final error that
// says so; use it wherever a missing image matters.
func (d *Document) Images() iter.Seq[images.ExtractedImage] {
	return func(yield func(images.ExtractedImage) bool) {
		for im, err := range walkImages(d, core.Canceler{}) {
			if err != nil || !yield(im) {
				return
			}
		}
	}
}

// ImagesContext is Images with cancellation and an error. It yields each image
// with a nil error, in Images' order and as lazily. If the iteration stops
// before every image was reached, it then yields once more, a zero
// ExtractedImage with a non-nil error, and ends: the error wraps ctx.Err() for
// a cancelled or expired context, matches ErrResourceLimit when the work budget
// (WithMaxWork) ran out, and wraps LockReason for a Locked document, which
// yields no images. A complete iteration yields no error, and breaking out of
// the loop is the caller's own stop, after which nothing more is yielded.
//
//	for img, err := range doc.ImagesContext(ctx) {
//		if err != nil {
//			return err // the images before it are all that were reached
//		}
//		use(img)
//	}
//
// An image that was reached but not decoded is yielded with Decoded false and
// a Note saying why, as from ExtractImagesContext; that is not an error.
// Cancellation is checked between images, as there.
//
// It is an iter.Seq2 with the error as its second value, rather than an
// iter.Seq with an Err method to call afterwards, so that the error cannot be
// left unread by accident: a range loop that names it must use it.
func (d *Document) ImagesContext(ctx context.Context) iter.Seq2[images.ExtractedImage, error] {
	return walkImages(d, core.NewCanceler(ctx))
}

// walkImages is the one image iteration every entry point above shares. Each
// range over it begins its own run under cancel — a shallow copy of d with a
// per-run cache, as the validators use (beginRunCancel) — and yields each image
// with a nil error and, when the walk did not reach every image, a final zero
// image with the error that says why.
func walkImages(d *Document, cancel core.Canceler) iter.Seq2[images.ExtractedImage, error] {
	return func(yield func(images.ExtractedImage, error) bool) {
		if d.Locked() {
			yield(images.ExtractedImage{}, d.lockedError("extracting images"))
			return
		}
		// The run's canceler carries its work meter, so a spent budget stops the
		// walk as a cancelled context does and StopErr names the budget.
		rd := beginRunCancel(d, cancel)
		stop := rd.canceler()
		caller := false // the caller broke out of its loop
		walkImagesRun(rd, func(im images.ExtractedImage) bool {
			// Yield the image first, then stop: it is already decoded, and
			// throwing away finished work is not what cancellation is for.
			if !yield(im, nil) {
				caller = true
				return false
			}
			return !stop.Stopped()
		})
		if caller {
			return // yielding again after a break would panic
		}
		if err := stop.StopErr("extracting images"); err != nil {
			yield(images.ExtractedImage{}, markLimit(err))
		}
	}
}

// walkImagesRun walks the images of a document a run has been begun on.
func walkImagesRun(rd *Document, yield func(images.ExtractedImage) bool) {
	// Install a per-run cache on a shallow copy, as the validators do: a tint
	// transform evaluates per pixel, and without the cache each evaluation
	// re-decoded the function stream (and re-parsed a type-4 program) — a
	// sub-megabyte image took minutes (sweep #13). The cache also carries the
	// cancellation signal, so the traversals it shares with the validators stop
	// on the same terms.
	//
	// This is the boundary: everything below it reads the document through a
	// view and never names Document.
	//
	// It is also the outermost boundary for the run's work meter: a walk the
	// meter stops ends here, with each image it had reached already yielded
	// (an image the meter stopped mid-decode carries a Note saying so).
	core.Contain(func() { imagesWalk(rd.view(), yield) })
}
