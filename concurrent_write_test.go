package pdf0

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"

	"sort"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Writing documents concurrently must produce the same bytes as writing them
// one at a time.
//
// The compressors behind every stream are pooled and shared across goroutines,
// which is the kind of sharing that produces a file that is subtly wrong rather
// than one that fails: a stream carrying a neighbour's window still decompresses
// to *something*. internal/core tests the pool directly; this is the same
// question asked of the thing a caller actually does.
func TestDocumentsWrittenConcurrentlyAreIdenticalToSerialOnes(t *testing.T) {
	const n = 48

	// Build a document and return the bytes of every compressed stream in it,
	// in object order.
	//
	// The compressed streams rather than the whole file, because Skeleton
	// derives the trailer /ID from time.Now() — two builds of the same document
	// differ by 32 bytes of hex whatever the compressors do, and comparing
	// files would measure that instead.
	//
	// Building rather than writing, because that is where the compression
	// happens: AddPage compresses the content stream, and Write of an
	// already-built document does almost none. A first attempt at this test
	// built serially and wrote concurrently, passed, and went on passing with a
	// single shared compressor planted — it was not exercising the pool at all.
	streamsOf := func(i int) [][]byte {
		doc := mustPDFADoc(t, pdfa.PDFA2b)
		var b content.Builder
		for r := 0; r < 3+i%7; r++ {
			b.Rect(float64(10+r), float64(10+i%40), float64(20+i%30), 30).Fill()
		}
		if _, err := doc.AddPage(Page{Width: 200, Height: 200, Content: &b}); err != nil {
			t.Errorf("building document %d: %v", i, err)
			return nil
		}
		nums := make([]int, 0, len(doc.Objects))
		for num := range doc.Objects {
			nums = append(nums, num)
		}
		sort.Ints(nums)
		var out [][]byte
		for _, num := range nums {
			st, ok := doc.Objects[num].Value.(*object.Stream)
			if !ok {
				continue
			}
			if f, _ := st.Dict.Get("Filter").(object.Name); f != "FlateDecode" {
				continue
			}
			out = append(out, append([]byte(nil), st.Data...))
		}
		return out
	}

	// The reference, one at a time.
	want := make([][][]byte, n)
	for i := range want {
		want[i] = streamsOf(i)
	}
	if len(want[0]) == 0 {
		t.Fatal("no compressed streams in a built document; this test would assert nothing")
	}
	if bytes.Equal(bytes.Join(want[0], nil), bytes.Join(want[1], nil)) {
		t.Fatal("two documents compressed to the same bytes, so a mix-up between " +
			"them would be invisible here")
	}

	got := make([][][]byte, n)
	var wg sync.WaitGroup
	var next atomic.Int64
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				got[i] = streamsOf(i)
			}
		}()
	}
	wg.Wait()

	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("document %d: %d compressed streams under load, %d serially",
				i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			if !bytes.Equal(got[i][j], want[i][j]) {
				t.Fatalf("document %d stream %d differs when built concurrently: "+
					"%d bytes vs %d serially — something is shared between goroutines "+
					"that should not be", i, j, len(got[i][j]), len(want[i][j]))
			}
		}
	}
}
