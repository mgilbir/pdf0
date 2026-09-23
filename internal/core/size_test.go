package core

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCheckImage(t *testing.T) {
	def := DefaultLimits()
	for _, c := range []struct {
		name       string
		w, h, comp int64
		ok         bool
	}{
		{"an ordinary scan", 4960, 7016, 1, true},
		{"exactly the budget", 1 << 13, 1 << 13, 4, true},
		{"one row over", 1 << 13, 1<<13 + 1, 1, false},
		{"a product that wraps", 1 << 60, 2, 1, false},
		{"MaxInt64 squared", math.MaxInt64, math.MaxInt64, 1, false},
		{"a negative dimension", -1, 10, 1, false},
		// The sample budget: four components per pixel of the pixel budget.
		{"CMYK at the budget", 1 << 13, 1 << 13, 4, true},
		{"five components at the budget", 1 << 13, 1 << 13, 5, false},
		{"32 components within the samples", 1 << 10, 1 << 13, 32, true},
		{"32 components past the samples", 1 << 11, 1<<13 + 1, 32, false},
	} {
		err := def.CheckImage(c.w, c.h, c.comp)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckImage(%d, %d, %d) = %v, want ok=%v", c.name, c.w, c.h, c.comp, err, c.ok)
		}
		var le *LimitError
		if err != nil && (!errors.As(err, &le) || le.Guard != GuardImagePixels) {
			t.Errorf("%s: %v is not an image-pixels LimitError", c.name, err)
		}
	}
}

func TestLimitErrorSaysWhoseBound(t *testing.T) {
	if err := DefaultLimits().CheckImage(1<<14, 1<<14, 1); !strings.Contains(err.Error(), "pdf0's default") {
		t.Errorf("default bound: %v", err)
	}
	custom := Limits{ImagePixels: 100}.WithDefaults()
	err := custom.CheckImage(11, 10, 1)
	if err == nil || !strings.Contains(err.Error(), "needs 110 pixels, over the bound of 100 (configured by the caller)") {
		t.Errorf("configured bound: %v", err)
	}
	if err := custom.ImageBudgetError("the fax data"); !strings.Contains(err.Error(), "the fax data exceeds the bound of 100 pixels (configured by the caller)") {
		t.Errorf("unknown size: %v", err)
	}
	// A budget so large the sample bound would overflow saturates instead.
	huge := Limits{ImagePixels: math.MaxInt64}.WithDefaults()
	if err := huge.CheckImage(1<<20, 1<<20, 64); err != nil {
		t.Errorf("a saturated sample budget refused %v", err)
	}
}
