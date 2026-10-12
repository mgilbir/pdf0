package pdf0

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// How the extractors and StreamData say they stopped short: a resource limit,
// a Locked document, or content pdf0 declined to decode. Each of these used to
// be silent somewhere — an empty or short result with a nil error, or no error
// channel at all — which a caller cannot tell from a document that simply has
// less in it (docs audit 2026-10-11 D5, D6, D37).

// textAndImagePage is one page showing "Hello" in Helvetica and drawing one
// 2×2 gray image.
func textAndImagePage() []byte {
	objs := rawPage("BT /F1 12 Tf 10 10 Td (Hello) Tj ET q 2 0 0 2 0 0 cm /Im0 Do Q",
		"<</Font<</F1 5 0 R>>/XObject<</Im0 6 0 R>>>>")
	objs = append(objs,
		rawObj{dict: "<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>>"},
		rawObj{dict: "<</Type/XObject/Subtype/Image/Width 2/Height 2/BitsPerComponent 8/ColorSpace/DeviceGray/Filter/FlateDecode>>", stream: zlibBytes([]byte{0, 64, 128, 255})},
	)
	return buildRawPDF(objs)
}

// lockedTextAndImagePage is textAndImagePage encrypted under a user password,
// read without one: a Locked document.
func lockedTextAndImagePage(t *testing.T) *Document {
	t.Helper()
	doc := readRaw(t, textAndImagePage())
	if err := doc.SetEncryption("user", "owner"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	locked := readRaw(t, buf.Bytes())
	if !locked.Locked() {
		t.Fatal("the document read without its password is not Locked")
	}
	return locked
}

// collectImagesContext drains ImagesContext, returning the images and every
// error it yielded.
func collectImagesContext(ctx context.Context, d *Document) (ims []images.ExtractedImage, errs []error) {
	for im, err := range d.ImagesContext(ctx) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ims = append(ims, im)
	}
	return ims, errs
}

// TestImagesContextReportsAStop is D5. Images and ExtractImages have no error
// channel, so a run the work budget stops yields fewer images with nothing to
// say so; ImagesContext yields the stop as a final error.
func TestImagesContextReportsAStop(t *testing.T) {
	t.Run("budget spent", func(t *testing.T) {
		doc := readRaw(t, textAndImagePage(), WithMaxWork(1))
		ims, errs := collectImagesContext(context.Background(), doc)
		if len(ims) != 0 || len(errs) != 1 {
			t.Fatalf("got %d images and errors %v; want none and exactly one error", len(ims), errs)
		}
		if !errors.Is(errs[0], ErrResourceLimit) || !errors.Is(errs[0], core.ErrWorkLimit) {
			t.Errorf("error %v does not match ErrResourceLimit and the work meter", errs[0])
		}
		// The silent forms are unchanged: they truncate, as documented.
		if n := len(doc.ExtractImages()); n != 0 {
			t.Errorf("ExtractImages = %d images under a spent budget, want 0", n)
		}
		n := 0
		for range doc.Images() {
			n++
		}
		if n != 0 {
			t.Errorf("Images yielded %d images under a spent budget, want 0", n)
		}
	})
	t.Run("complete", func(t *testing.T) {
		doc := readRaw(t, textAndImagePage())
		ims, errs := collectImagesContext(context.Background(), doc)
		if len(errs) != 0 {
			t.Fatalf("errors %v from a complete extraction", errs)
		}
		eager := doc.ExtractImages()
		if len(ims) != 1 || len(eager) != 1 || ims[0].ObjNum != eager[0].ObjNum || !ims[0].Decoded {
			t.Errorf("ImagesContext = %+v, ExtractImages = %+v; want the same one decoded image", ims, eager)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, errs := collectImagesContext(ctx, readRaw(t, textAndImagePage()))
		if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) || errors.Is(errs[0], ErrResourceLimit) {
			t.Errorf("errors %v; want one wrapping context.Canceled and not ErrResourceLimit", errs)
		}
	})
	t.Run("break", func(t *testing.T) {
		// Breaking out is the caller's stop: nothing more is yielded, and
		// yielding after a break would panic.
		doc := buildTwoImageDoc("{ pop 0 0 1 }", 4, 4)
		n := 0
		for _, err := range doc.ImagesContext(context.Background()) {
			if err != nil {
				t.Fatal(err)
			}
			n++
			break
		}
		if n != 1 {
			t.Errorf("n = %d", n)
		}
	})
}

// TestExtractorsOnALockedDocument is D6. A Locked document's streams are
// ciphertext. ExtractText returned "" and a nil error — "every page's text is
// in the result" — and the image extractors returned the ciphertext as each
// image's encoded bytes, with a note blaming the file.
func TestExtractorsOnALockedDocument(t *testing.T) {
	doc := lockedTextAndImagePage(t)
	isLock := func(t *testing.T, what string, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, ErrWrongPassword) || errors.Is(err, ErrResourceLimit) {
			t.Errorf("%s: err = %v; want one wrapping LockReason (ErrWrongPassword)", what, err)
		}
	}

	text, err := doc.ExtractText()
	isLock(t, "ExtractText", err)
	if text != "" {
		t.Errorf("ExtractText = %q from ciphertext", text)
	}
	text, err = doc.ExtractTextContext(context.Background())
	isLock(t, "ExtractTextContext", err)
	if text != "" {
		t.Errorf("ExtractTextContext = %q from ciphertext", text)
	}
	text, err = doc.ExtractPageText(doc.PageList()[0])
	isLock(t, "ExtractPageText", err)
	var pe *PageTextError
	if !errors.As(err, &pe) || pe.Page != 0 || text != "" {
		t.Errorf("ExtractPageText = %q, %v; want \"\" and a *PageTextError with Page 0", text, err)
	}

	ims, err := doc.ExtractImagesContext(context.Background())
	isLock(t, "ExtractImagesContext", err)
	if len(ims) != 0 {
		t.Errorf("ExtractImagesContext returned %d images of ciphertext", len(ims))
	}
	ims, errs := collectImagesContext(context.Background(), doc)
	if len(ims) != 0 || len(errs) != 1 {
		t.Fatalf("ImagesContext yielded %d images and errors %v; want only one error", len(ims), errs)
	}
	isLock(t, "ImagesContext", errs[0])
	if n := len(doc.ExtractImages()); n != 0 {
		t.Errorf("ExtractImages returned %d images of ciphertext", n)
	}
	for range doc.Images() {
		t.Error("Images yielded an image of ciphertext")
	}

	var content *object.Stream
	for _, iobj := range doc.Objects {
		if s, ok := iobj.Value.(*object.Stream); ok {
			content = s
			break
		}
	}
	_, err = doc.StreamData(content)
	isLock(t, "StreamData", err)
}

// TestExtractionLimitErrorsMatchErrResourceLimit is D37: a caller tells a
// resource limit from every other stop with errors.Is, not by matching the
// message.
func TestExtractionLimitErrorsMatchErrResourceLimit(t *testing.T) {
	file := textAndImagePage()
	spent := readRaw(t, file, WithMaxWork(1))
	if _, err := spent.ExtractText(); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("ExtractText under a spent budget: %v", err)
	}
	if _, err := spent.ExtractTextContext(context.Background()); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("ExtractTextContext under a spent budget: %v", err)
	}
	if _, err := spent.ExtractPageText(spent.PageList()[0]); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("ExtractPageText under a spent budget: %v", err)
	}
	if _, err := spent.ExtractImagesContext(context.Background()); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("ExtractImagesContext under a spent budget: %v", err)
	}

	small := readRaw(t, file, WithMaxDecodedStreamBytes(2))
	content, _ := small.Resolve(small.PageList()[0].Get("Contents")).(*object.Stream)
	if _, err := small.StreamData(content); !errors.Is(err, ErrResourceLimit) || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("StreamData over the decoded-stream limit: %v", err)
	}
	if _, err := small.StreamDataContext(context.Background(), content); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("StreamDataContext over the decoded-stream limit: %v", err)
	}

	// A cancellation is not a resource limit.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	whole := readRaw(t, file)
	if _, err := whole.ExtractTextContext(ctx); err == nil || errors.Is(err, ErrResourceLimit) {
		t.Errorf("ExtractTextContext cancelled: %v", err)
	}
	if _, err := whole.ExtractImagesContext(ctx); err == nil || errors.Is(err, ErrResourceLimit) {
		t.Errorf("ExtractImagesContext cancelled: %v", err)
	}
	if _, err := whole.ExtractText(); err != nil {
		t.Errorf("ExtractText of the whole document: %v", err)
	}
}

// TestExtractTextReportsDeclinedContent: a page whose content pdf0 declined to
// decode — over a size limit, in a filter it does not implement — was left out
// with a nil error, so "the error is nil exactly when every page's text is in
// the result" did not hold.
func TestExtractTextReportsDeclinedContent(t *testing.T) {
	helv := rawObj{dict: "<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>"}
	page := func(content string, extra ...rawObj) []byte {
		res := "<</Font<</F1 5 0 R>>>>"
		if len(extra) > 1 {
			res = "<</Font<</F1 5 0 R>>/XObject<</X0 6 0 R>>>>"
		}
		return buildRawPDF(append(rawPage(content, res), extra...))
	}
	toUnicode := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n1 begincodespacerange <00> <FF> endcodespacerange\n1 beginbfchar <41> <0042> endbfchar\nendcmap\n"
	cases := []struct {
		name  string
		file  []byte
		opts  []Option
		limit bool   // the error matches ErrResourceLimit
		guard string // the error names it
	}{
		{"page content over the decoded-stream limit", page("BT /F1 12 Tf (Hello) Tj ET", helv), []Option{WithMaxDecodedStreamBytes(8)}, true, core.GuardDecodedStream},
		{"page content over the content-stream limit", page("BT /F1 12 Tf (Hello) Tj ET", helv), []Option{WithMaxContentStreamBytes(8)}, true, core.GuardContentStream},
		{"form content over the decoded-stream limit", page("/X0 Do", helv,
			rawObj{dict: "<</Type/XObject/Subtype/Form/BBox[0 0 1 1]/Filter/FlateDecode>>", stream: zlibBytes([]byte("BT /F1 12 Tf (Hello from a form) Tj ET"))}),
			[]Option{WithMaxDecodedStreamBytes(16)}, true, core.GuardDecodedStream},
		{"form content past the run's decoded-content total", page("/X0 Do", helv,
			rawObj{dict: "<</Type/XObject/Subtype/Form/BBox[0 0 1 1]>>", stream: []byte("BT /F1 12 Tf (Hello from a form) Tj ET")}),
			[]Option{WithMaxDecodedContentBytes(1)}, true, core.GuardContentTotal},
		{"ToUnicode over the decoded-stream limit", page("BT /F1 1 Tf (A) Tj ET",
			rawObj{dict: "<</Type/Font/Subtype/Type1/BaseFont/Helvetica/ToUnicode 6 0 R>>"},
			rawObj{dict: "<</Filter/FlateDecode>>", stream: zlibBytes([]byte(toUnicode))}),
			[]Option{WithMaxDecodedStreamBytes(64)}, true, core.GuardDecodedStream},
		{"page content in a filter pdf0 does not decode", buildRawPDF(append(rawPage("", "<</Font<</F1 5 0 R>>>>")[:3],
			rawObj{dict: "<</Filter/DCTDecode>>", stream: []byte("BT /F1 12 Tf (Hello) Tj ET")}, helv)), nil, false, core.GuardUnsupportedFilter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := readRaw(t, c.file, c.opts...)
			text, err := doc.ExtractText()
			var pe *PageTextError
			if !errors.As(err, &pe) || pe.Page != 1 {
				t.Fatalf("ExtractText = %q, %v; want page 1 reported", text, err)
			}
			if errors.Is(err, ErrResourceLimit) != c.limit || !strings.Contains(err.Error(), "("+c.guard+")") {
				t.Errorf("err = %v; want ErrResourceLimit %v and the guard %s named", err, c.limit, c.guard)
			}
			if text != "" {
				t.Errorf("the page's partial text was returned: %q", text)
			}
			if _, err := doc.ExtractPageText(doc.PageList()[0]); !errors.As(err, &pe) || pe.Page != 0 || errors.Is(err, ErrResourceLimit) != c.limit {
				t.Errorf("ExtractPageText: %v", err)
			}
		})
	}

	// A page whose content is malformed has no text pdf0 could recover, and
	// that is not reported: the result is the text the file carries.
	t.Run("malformed content is not an error", func(t *testing.T) {
		objs := rawPage("", "<</Font<</F1 5 0 R>>>>")[:3]
		doc := readRaw(t, buildRawPDF(append(objs, rawObj{dict: "<</Filter/FlateDecode>>", stream: []byte("not zlib")}, helv)))
		if text, err := doc.ExtractText(); text != "" || err != nil {
			t.Errorf("ExtractText = %q, %v", text, err)
		}
	})
	// Two pages sharing one declined stream are both reported, though the
	// trip is recorded once and the page text is memoised.
	t.Run("shared content", func(t *testing.T) {
		objs := []rawObj{
			{dict: "<</Type/Catalog/Pages 2 0 R>>"},
			{dict: "<</Type/Pages/Kids[3 0 R 6 0 R]/Count 2>>"},
			{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>"},
			{dict: "<</Filter/FlateDecode>>", stream: zlibBytes([]byte("BT /F1 12 Tf (Hello) Tj ET"))},
			helv,
			{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>"},
		}
		doc := readRaw(t, buildRawPDF(objs), WithMaxDecodedStreamBytes(8))
		_, err := doc.ExtractText()
		if err == nil {
			t.Fatal("ExtractText: nil error")
		}
		for _, n := range []int{1, 2} {
			if !strings.Contains(err.Error(), "text of page "+string(rune('0'+n))+" not extracted") {
				t.Errorf("page %d not reported: %v", n, err)
			}
		}
	})
}
