package syntax

import (
	"fmt"
	"sync"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestParsedLargeDictConcurrentGet is the C45 guard (audit 2026-09-22) at the
// layer where it lives: a dictionary the parser produced, with more keys than
// the size at which lookups switch to an index, read from several goroutines
// at once. Reads must be pure. Run under -race; before the fix the first Get
// built the index lazily and -race reported a DATA RACE on it.
func TestParsedLargeDictConcurrentGet(t *testing.T) {
	const n = 70
	obj, err := NewParser(largeDictSource(n)).ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	d, ok := obj.(*object.Dictionary)
	if !ok {
		t.Fatalf("parsed %T, want *object.Dictionary", obj)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				if got := d.Get(object.Name(fmt.Sprintf("K%02d", i))); got != object.Integer(i) {
					errs <- fmt.Sprintf("Get(K%02d) = %v, want %d", i, got, i)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
