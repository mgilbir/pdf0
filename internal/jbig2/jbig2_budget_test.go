package jbig2

import "testing"

// TestNewJBBitmapBudget pins the single-allocation choke point: a normal bitmap
// allocates, an over-budget one panics with the recoverable sentinel rather than
// attempting a multi-gigabyte make (audit C2).
func TestNewJBBitmapBudget(t *testing.T) {
	if b := newJBBitmap(100, 100, 0); b == nil || len(b.pix) != 10000 {
		t.Fatalf("newJBBitmap(100,100) = %v", b)
	}
	defer func() {
		if r := recover(); r != errJBIG2Budget {
			t.Fatalf("over-budget newJBBitmap: recovered %v, want errJBIG2Budget", r)
		}
	}()
	_ = newJBBitmap(1<<14, 1<<14, 0) // 2^28 pixels > maxJBIG2Pixels (2^26)
	t.Fatal("newJBBitmap did not panic on an over-budget allocation")
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
