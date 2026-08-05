package jbig2

import "testing"

// The decoder's behaviour on real files is exercised from the root package,
// through ExtractImages, because those tests need a Document. What belongs here
// is what calls the decoder directly.

// TestJBIG2Malformed rejects garbage without panicking.
func TestJBIG2Malformed(t *testing.T) {
	if _, err := Decode(nil, []byte{0, 0, 0, 0, 0x30, 0x00, 0x01}, 8, 8); err == nil {
		t.Error("expected an error on malformed JBIG2 data")
	}
}

// TestJBIG2ShortMMRDoesNotPanic pins the one place where ignoring a truncation
// was not a wrong finding but a crash: ccitt.Decode stops early and returns a
// nil error when its data runs out, and decodeGenericMMR indexed the short
// result as if it held every row. The resulting slice-bounds panic is not
// errJBIG2Budget, so Decode's recover re-raised it and it escaped
// ExtractImages to the caller.
//
// Before the fix this failed with:
//
//	decodeGenericMMR panicked on a short CCITT decode: runtime error: index
//	out of range [0] with length 0
func TestJBIG2ShortMMRDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("decodeGenericMMR panicked on a short CCITT decode: %v", r)
		}
	}()
	// In Group 4 a single 1 bit is a V0 code, which against an all-white
	// reference line completes one all-white row. Sixteen of them decode
	// sixteen rows of a region declared to be 64 rows tall, and ccitt.Decode
	// returns that short result with a nil error.
	if _, err := decodeGenericMMR([]byte{0xFF, 0xFF}, 64, 64); err == nil {
		t.Error("a 16-row decode of a 64-row region must be reported as a failure, not returned as an image")
	}
}
