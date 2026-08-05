package pdf0

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestDictObjNumCacheConsistency verifies the cached reverse index returns the
// same object numbers as the linear scan, and -1 for an unknown dictionary.
func TestDictObjNumCacheConsistency(t *testing.T) {
	doc := &Document{Objects: map[int]*object.IndirectObject{}}
	dicts := map[int]*object.Dictionary{}
	for i := 1; i <= 200; i++ {
		d := &object.Dictionary{}
		d.Set("N", object.Integer(i))
		doc.Objects[i] = &object.IndirectObject{Number: i, Value: d}
		dicts[i] = d
	}
	// Without a cache: linear scan.
	for i, d := range dicts {
		if got := doc.view().DictObjNum(d); got != i {
			t.Fatalf("uncached dictObjNum = %d, want %d", got, i)
		}
	}
	// With a cache: reverse index. Must agree.
	doc.valCache = newValidationCache(core.Canceler{})
	for i, d := range dicts {
		if got := doc.view().DictObjNum(d); got != i {
			t.Fatalf("cached dictObjNum = %d, want %d", got, i)
		}
	}
	// An unknown dictionary yields -1 under both paths.
	stray := &object.Dictionary{}
	if got := doc.view().DictObjNum(stray); got != -1 {
		t.Errorf("cached dictObjNum(unknown) = %d, want -1", got)
	}
}
