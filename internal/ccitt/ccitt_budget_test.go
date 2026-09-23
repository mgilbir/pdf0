package ccitt

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
)

// testBudget is the budget the codec tests decode under: the extraction
// default, so a test that passes here passes through ExtractImages.
const testBudget = 1 << 26

// Every 1 bit of Group 4 data is a V0 code, and against an all-white reference
// line one V0 code is a whole all-white row. v0Flood is n bytes of them: 8n
// rows for n bytes, whatever the width.
func v0Flood(n int) []byte { return bytes.Repeat([]byte{0xFF}, n) }

// TestCCITTOutputIsHeldToTheBudget is audit 2026-09-22 C12: 128 KiB of 0xFF
// with /Columns 2^20 is a million rows of 128 KiB each. The old decoder capped
// the row count at 2^20 and nothing else, so the output was bounded only at a
// terabyte. Each case asserts the budget error, not merely survival.
func TestCCITTOutputIsHeldToTheBudget(t *testing.T) {
	t.Run("rows unknown", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
			_, err := Decode(v0Flood(128<<10), NewParams(-1, 1<<20, 0, false, testBudget))
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("Decode = %v, want ErrBudget", err)
			}
		})
	})
	t.Run("rows declared", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
			// Refused before a single row is decoded: the data is too short to
			// make even one, so any other error means the decode was attempted.
			for _, rows := range []int{1 << 20, math.MaxInt} {
				_, err := Decode(nil, NewParams(-1, 1<<20, rows, false, testBudget))
				if !errors.Is(err, ErrBudget) {
					t.Fatalf("rows=%d: Decode = %v, want ErrBudget", rows, err)
				}
			}
		})
	})
}

// The budget is exact, not approximate: an image of exactly the budget decodes,
// one row more is refused, and a stream that ends exactly at the budget is not
// mistaken for one that runs past it.
func TestCCITTBudgetBoundary(t *testing.T) {
	const cols, rows = 16, 24 // 3 bytes of V0 flood
	budget := int64(cols * rows)

	out, err := Decode(v0Flood(3), NewParams(-1, cols, 0, false, budget))
	if err != nil {
		t.Fatalf("an image of exactly the budget: %v", err)
	}
	if want := rows * 2; len(out) != want {
		t.Fatalf("decoded %d bytes, want %d", len(out), want)
	}
	if _, err := Decode(v0Flood(3), NewParams(-1, cols, 0, false, budget-1)); !errors.Is(err, ErrBudget) {
		t.Fatalf("one pixel short of the budget: %v, want ErrBudget", err)
	}
	if _, err := Decode(v0Flood(3), NewParams(-1, cols, rows, false, budget)); err != nil {
		t.Fatalf("declared rows at the budget: %v", err)
	}
	if _, err := Decode(v0Flood(3), NewParams(-1, cols, rows+1, false, budget)); !errors.Is(err, ErrBudget) {
		t.Fatalf("declared rows past the budget: %v, want ErrBudget", err)
	}
	if _, err := Decode(v0Flood(3), NewParams(-1, cols, 0, false, 0)); !errors.Is(err, ErrBudget) {
		t.Fatalf("a zero budget decodes nothing: %v, want ErrBudget", err)
	}
}
