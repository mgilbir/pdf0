package ccitt

import (
	"bytes"
	"testing"
)

// The reference row used throughout: 8 pixels, 4 black then 4 white. In the PDF
// image convention (0 = black, 1 = white) that packs to 0x0F.
const ccittRowBBBBWWWW = 0x0F

// TestCCITTGroup4Row decodes a hand-encoded Group 4 (K<0) single row.
//
// Encoding of BBBBWWWW against the imaginary all-white reference line:
//
//	Horizontal  001
//	white run 0 00110101
//	black run 4 011
//	V0          1
//
// = 001 00110101 011 1, byte-padded to {0x26, 0xAE}.
func TestCCITTGroup4Row(t *testing.T) {
	got, err := Decode([]byte{0x26, 0xAE}, NewParams(-1, 8, 1, false))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := []byte{ccittRowBBBBWWWW}; !bytes.Equal(got, want) {
		t.Fatalf("row = %08b, want %08b", got, want)
	}
}

// TestCCITTGroup4TwoRows decodes two identical rows; the second is coded entirely
// with vertical (V0) modes relative to the first, exercising the reference line.
//
// Row 1 = 001 00110101 011 1 (as above); row 2 = 1 1 1 (three V0). Concatenated
// and byte-padded: {0x26, 0xAF, 0xC0}.
func TestCCITTGroup4TwoRows(t *testing.T) {
	got, err := Decode([]byte{0x26, 0xAF, 0xC0}, NewParams(-1, 8, 2, false))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []byte{ccittRowBBBBWWWW, ccittRowBBBBWWWW}
	if !bytes.Equal(got, want) {
		t.Fatalf("rows = % 08b, want % 08b", got, want)
	}
}

// TestCCITTGroup3OneD decodes a hand-encoded Group 3 1-D (K=0) row.
//
//	white run 0 00110101
//	black run 4 011
//	white run 4 1011
//
// = 00110101 011 1011, byte-padded to {0x35, 0x76}.
func TestCCITTGroup3OneD(t *testing.T) {
	got, err := Decode([]byte{0x35, 0x76}, NewParams(0, 8, 1, false))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := []byte{ccittRowBBBBWWWW}; !bytes.Equal(got, want) {
		t.Fatalf("row = %08b, want %08b", got, want)
	}
}

// TestCCITTWideMakeup exercises a make-up code: a run longer than 63 pixels.
// A 128-pixel all-black row in Group 3 1-D is: white run 0 (00110101), black
// make-up 128 (000011001000), black terminating 0 (0000110111).
func TestCCITTWideMakeup(t *testing.T) {
	bits := "00110101" + "000011001000" + "0000110111"
	data := bitsToBytes(bits)
	got, err := Decode(data, NewParams(0, 128, 1, false))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := make([]byte, 16) // 128 black pixels = all 0 bits
	if !bytes.Equal(got, want) {
		t.Fatalf("128-black row = % x, want all zero", got)
	}
}

// TestCCITTMalformed rejects garbage rather than looping or panicking.
func TestCCITTMalformed(t *testing.T) {
	// A lone 0 bit run is not a complete code; decoding a full row from it must
	// fail cleanly.
	if _, err := Decode([]byte{0x00}, NewParams(-1, 1728, 1, false)); err == nil {
		t.Fatal("expected an error on truncated data")
	}
}

func bitsToBytes(bits string) []byte {
	for len(bits)%8 != 0 {
		bits += "0"
	}
	out := make([]byte, len(bits)/8)
	for i := 0; i < len(bits); i++ {
		if bits[i] == '1' {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}
