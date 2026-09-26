package jbig2

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
)

// testBudget is the budget the codec tests decode under: the extraction
// default, so a test that passes here passes through ExtractImages.
const testBudget = 1 << 26

// segment encodes one JBIG2 segment in the embedded organisation (7.2): number,
// type, the segments it refers to (one byte each, which holds while numbers stay
// at or below 256), page 1, and the data.
func segment(number uint32, typ byte, refs []byte, data []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, number)
	b = append(b, typ, byte(len(refs))<<5)
	b = append(b, refs...)
	b = append(b, 1) // page association
	b = binary.BigEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

// patternDict is a type 16 segment of grayMax+1 patterns of pw×ph pixels,
// arithmetic-coded with no coded data: the MQ decoder supplies past-EOF bits
// for free, which is what makes the work independent of the input size.
func patternDict(pw, ph byte, grayMax uint32) []byte {
	d := []byte{0, pw, ph}
	return segment(0, 16, nil, binary.BigEndian.AppendUint32(d, grayMax))
}

// halftone is a type 22 region of rw×rh pixels over a gw×gh grid whose cells
// step by (hrx, hry)/256 pixels, referring to pattern dictionary 0.
func halftone(number uint32, rw, rh, gw, gh uint32, hrx, hry uint16) []byte {
	var d []byte
	for _, v := range []uint32{rw, rh, 0, 0} {
		d = binary.BigEndian.AppendUint32(d, v)
	}
	d = append(d, 0) // region combination operator
	d = append(d, 0) // halftone flags: arithmetic, template 0, no skip, OR
	for _, v := range []uint32{gw, gh, 0, 0} {
		d = binary.BigEndian.AppendUint32(d, v)
	}
	d = binary.BigEndian.AppendUint16(d, hrx)
	d = binary.BigEndian.AppendUint16(d, hry)
	return segment(number, 22, []byte{0}, d)
}

// TestHalftoneWorkIsCharged is audit 2026-09-22 C55. A halftone region
// reserved its own area, not the grid decode behind it: a 1×1 region over a
// 1024×1024 grid and a 65,536-pattern dictionary is 16 bit-planes of 2^20
// cells, about 1.7e7 MQ decodes, charged as one pixel — and region count is
// bounded only by the input. Each case asserts the budget error.
func TestHalftoneWorkIsCharged(t *testing.T) {
	t.Run("grid decode", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
			data := patternDict(1, 1, 65535)
			for i := uint32(1); i <= 200; i++ {
				data = append(data, halftone(i, 1, 1, 1024, 1024, 256, 0)...)
			}
			// Under a 4-megapixel budget one region's grid (17 planes of 2^20
			// cells) is already over the 2^24 of work the stream may do, so the
			// refusal comes before any decode. At the default budget it comes
			// after fifteen regions and about ten seconds; uncharged, the 200
			// regions take minutes.
			_, err := Decode(nil, data, 8, 8, 1<<22)
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("Decode = %v, want ErrBudget", err)
			}
		})
	})
	t.Run("stamping", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
			// Two 255×255 patterns, and every one of 2^20 cells stamped at
			// the origin of a 255×255 region: 6.8e10 pixel writes for one
			// region the reserve charged 65,025.
			data := patternDict(255, 255, 1)
			data = append(data, halftone(1, 255, 255, 1024, 1024, 0, 0)...)
			_, err := Decode(nil, data, 255, 255, testBudget)
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("Decode = %v, want ErrBudget", err)
			}
		})
	})
}

// A halftone that fits the budget still decodes: the grid and the stamping are
// charged, not refused.
func TestHalftoneWithinBudgetDecodes(t *testing.T) {
	data := patternDict(4, 4, 3)
	data = append(data, halftone(1, 16, 16, 4, 4, 4<<8, 0)...)
	out, err := Decode(nil, data, 16, 16, testBudget)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 2*16 {
		t.Fatalf("decoded %d bytes, want %d", len(out), 2*16)
	}
}

// blitSymbol reports the pixels it touched — the overlap with the region, not
// the symbol's area — because that is what the caller charges.
func TestBlitSymbolReportsTheClippedArea(t *testing.T) {
	region, _ := newJBBitmap(10, 10, 0)
	sym, _ := newJBBitmap(4, 4, 1)
	for _, c := range []struct {
		x, y int
		want int64
	}{
		{0, 0, 16},
		{8, 8, 4},
		{-2, 0, 8},
		{10, 0, 0},
		{-4, -4, 0},
		{-1 << 30, 1 << 30, 0},
	} {
		if got := blitSymbol(region, sym, c.x, c.y, 0); got != c.want {
			t.Errorf("blitSymbol at (%d,%d) = %d, want %d", c.x, c.y, got, c.want)
		}
	}
}
