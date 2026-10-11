package pdf0

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Reading a stream's contents.
//
// Every stream in a PDF is stored encoded — Flate, LZW, one of the image
// codecs, or several in sequence — and Stream.Data holds those encoded bytes,
// because that is what round-tripping a file faithfully requires. Until now
// nothing exported turned them back into content: the validators, the text
// extractor and the image extractor each decoded internally and a caller who
// wanted a stream for its own reasons had no way in.

// StreamData decodes a stream's contents through its filter chain.
//
// It is the counterpart to reaching into Stream.Data, which holds the *encoded*
// bytes. A stream with no filters decodes to itself, so this is always the
// right way to ask what a stream says.
//
// The document's resource limits apply. A stream that decompresses to more than
// WithMaxDecodedStreamBytes is refused rather than expanded, with an error
// matching ErrResourceLimit — decompression bombs are the reason that ceiling
// exists, and a caller reading a stream out of an untrusted file wants it
// enforced here as much as anywhere.
//
// An encrypted document whose contents could not be decrypted returns an error
// rather than ciphertext: handing back bytes that look like data but are not is
// worse than saying so. For a Locked document the error wraps LockReason; for
// one of the streams DecryptFailures lists, it names the object.
func (d *Document) StreamData(s *object.Stream) ([]byte, error) {
	return d.streamData(d.canceler(), s)
}

// StreamDataContext is StreamData under a context, for a stream large enough
// that decoding it is worth being able to abandon.
func (d *Document) StreamDataContext(ctx context.Context, s *object.Stream) ([]byte, error) {
	return d.streamData(core.NewCanceler(ctx), s)
}

func (d *Document) streamData(cancel core.Canceler, s *object.Stream) ([]byte, error) {
	if s == nil {
		return nil, errors.New("pdf0: cannot decode a nil stream")
	}
	if d.Locked() {
		return nil, d.lockedError("cannot decode the stream")
	}
	for _, num := range d.decryptFailures {
		if iobj, ok := d.Objects[num]; ok && iobj.Value == object.Object(s) {
			return nil, fmt.Errorf("pdf0: stream object %d did not decrypt under the document's key, so its data is unrecoverable", num)
		}
	}
	data, err := core.DecodeStreamData(cancel, s, d.lim(), d.Resolve)
	return data, markLimit(err)
}
