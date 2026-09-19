package jbig2

import (
	"errors"
	"testing"
)

// TestNewJBBitmapBudget pins the single-allocation choke point: a normal bitmap
// allocates, an over-budget one returns errJBIG2Budget rather than attempting a
// multi-gigabyte make (audit C2).
//
// It used to panic and be recovered at the Decode boundary. The bound is the
// same; what changed is that it is now carried out through every caller as an
// error, because a library should not panic.
func TestNewJBBitmapBudget(t *testing.T) {
	b, err := newJBBitmap(100, 100, 0)
	if err != nil || b == nil || len(b.pix) != 10000 {
		t.Fatalf("newJBBitmap(100,100) = %v, %v", b, err)
	}

	// 2^28 pixels > maxJBIG2Pixels (2^26).
	over, err := newJBBitmap(1<<14, 1<<14, 0)
	if !errors.Is(err, errJBIG2Budget) {
		t.Fatalf("over-budget newJBBitmap: err = %v, want errJBIG2Budget", err)
	}
	if over != nil {
		t.Error("a refused allocation came back with a bitmap beside the error")
	}

	// A negative dimension is refused the same way rather than panicking in
	// make.
	if _, err := newJBBitmap(-1, 10, 0); !errors.Is(err, errJBIG2Budget) {
		t.Errorf("a negative width gave %v, want errJBIG2Budget", err)
	}
}

// TestNothingInTheDecoderPanics is the rule this package now keeps.
//
// The budget used to be signalled by a panic recovered at the Decode boundary,
// which meant every allocation site was one missing recover away from taking
// the process down. There is no recover left, so a panic from anywhere in the
// decoder reaches the caller — this walks the adversarial shapes that reach the
// allocation paths and asserts none of them does.
func TestNothingInTheDecoderPanics(t *testing.T) {
	region := func(w, h uint32, typ byte) []byte {
		return []byte{
			0, 0, 0, 0, typ, 0, 1, 0, 0, 0, 0x12,
			byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w),
			byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h),
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		}
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"a generic region larger than the budget", region(1<<20, 1<<20, 0x26)},
		{"a zero-sized region", region(0, 0, 0x26)},
		{"a width that fills the field", region(0xFFFFFFFF, 1, 0x26)},
		{"a halftone region larger than the budget", region(1<<20, 1<<20, 0x2A)},
		{"a refinement region larger than the budget", region(1<<20, 1<<20, 0x2E)},
		{"a text region larger than the budget", region(1<<20, 1<<20, 0x06)},
		{"a truncated segment header", []byte{0, 0, 0, 0, 0x26}},
		{"nothing at all", []byte{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the decoder panicked: %v", r)
				}
			}()
			// The result does not matter; not panicking does.
			_, _ = Decode(nil, tc.data, 8, 8)
			_, _ = Decode(tc.data, tc.data, 1<<10, 1<<10)
		})
	}
}

// TestReserveBoundsAggregate pins both the per-allocation and the stream-wide
// cumulative budgets that bound retained region/dictionary bitmaps (audit C2).
func TestReserveBoundsAggregate(t *testing.T) {
	// A single area over the per-bitmap cap is refused.
	d := &jbig2Decoder{}
	if err := d.reserve(1<<14, 1<<13); err == nil { // 2^27 > 2^26
		t.Fatal("reserve accepted a single over-cap area")
	}

	// Small areas accumulate until the stream total (2^28) is exceeded. Each is
	// 2^25 pixels, so exactly maxJBIG2TotalPixels/2^25 fit before rejection.
	d = &jbig2Decoder{}
	want := maxJBIG2TotalPixels / (1 << 25)
	ok := 0
	for i := 0; i < want+50; i++ {
		if d.reserve(1<<13, 1<<12) == nil { // 2^25 each
			ok++
		}
	}
	if ok != want {
		t.Fatalf("aggregate budget accepted %d areas of 2^25, want %d", ok, want)
	}
}

// TestDecodeJBIG2RejectsHugeRegion is the end-to-end C2 guard: a tiny segment
// declaring a 2^40-pixel generic region must be rejected promptly (via the area
// reservation), not drive a terabyte allocation or an unbounded decode loop.
func TestDecodeJBIG2RejectsHugeRegion(t *testing.T) {
	seg := []byte{
		0x00, 0x00, 0x00, 0x00, // segment number 0
		0x26,                   // flags: type 38 (immediate generic region)
		0x00,                   // referred-to count 0
		0x01,                   // page association
		0x00, 0x00, 0x00, 0x12, // data length 18
		// region information field (7.4.1):
		0x00, 0x10, 0x00, 0x00, // width  = 1<<20
		0x00, 0x10, 0x00, 0x00, // height = 1<<20
		0x00, 0x00, 0x00, 0x00, // x
		0x00, 0x00, 0x00, 0x00, // y
		0x00, // external combination op flags
		0x00, // generic region flags (arithmetic, template 0)
	}
	if _, err := Decode(nil, seg, 8, 8); err == nil {
		t.Fatal("Decode accepted a generic region declaring a 2^40-pixel bitmap")
	}
}
