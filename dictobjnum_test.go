package pdf0

import "testing"

// TestDictObjNumCacheConsistency verifies the cached reverse index returns the
// same object numbers as the linear scan, and -1 for an unknown dictionary.
func TestDictObjNumCacheConsistency(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	dicts := map[int]*Dictionary{}
	for i := 1; i <= 200; i++ {
		d := &Dictionary{}
		d.Set("N", Integer(i))
		doc.Objects[i] = &IndirectObject{Number: i, Value: d}
		dicts[i] = d
	}
	// Without a cache: linear scan.
	for i, d := range dicts {
		if got := doc.dictObjNum(d); got != i {
			t.Fatalf("uncached dictObjNum = %d, want %d", got, i)
		}
	}
	// With a cache: reverse index. Must agree.
	doc.valCache = &validationCache{}
	for i, d := range dicts {
		if got := doc.dictObjNum(d); got != i {
			t.Fatalf("cached dictObjNum = %d, want %d", got, i)
		}
	}
	// An unknown dictionary yields -1 under both paths.
	stray := &Dictionary{}
	if got := doc.dictObjNum(stray); got != -1 {
		t.Errorf("cached dictObjNum(unknown) = %d, want -1", got)
	}
}
