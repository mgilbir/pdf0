package core

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// The pooled compressor has to produce exactly what a fresh one would.
//
// FlateEncode'"'"'s output goes into a PDF stream, and the byte-level write tests
// compare whole documents. A pooled writer that emitted even slightly different
// bytes — a stale window, a different level — would change every document this
// library produces.
func TestPooledFlateMatchesAFreshWriter(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		[]byte("q 1 0 0 1 0 0 cm /Im0 Do Q"),
		bytes.Repeat([]byte("BT /F1 12 Tf (hello) Tj ET\n"), 400),
		bytes.Repeat([]byte{0}, 1<<16),
		func() []byte { // incompressible
			b := make([]byte, 4096)
			for i := range b {
				b[i] = byte(i*7 + i/3)
			}
			return b
		}(),
	}

	fresh := func(data []byte) []byte {
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	for i, in := range inputs {
		// Twice each, so the second call is served by the pool and has to match
		// the first — a writer returned dirty would show up here.
		for pass := 0; pass < 2; pass++ {
			got := FlateEncode(in)
			if want := fresh(in); !bytes.Equal(got, want) {
				t.Errorf("input %d pass %d: pooled output differs from a fresh writer "+
					"(%d bytes vs %d)", i, pass, len(got), len(want))
			}
		}
	}
}

// TestPooledFlateRoundTrips, because matching a fresh writer is only half of it:
// the bytes have to decompress back to what went in.
func TestPooledFlateRoundTrips(t *testing.T) {
	for _, in := range [][]byte{
		[]byte("stream contents"),
		bytes.Repeat([]byte("abc"), 10000),
		{},
	} {
		zr, err := zlib.NewReader(bytes.NewReader(FlateEncode(in)))
		if err != nil {
			t.Fatalf("the compressed stream does not open: %v", err)
		}
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("reading it back: %v", err)
		}
		if !bytes.Equal(got, in) {
			t.Errorf("round trip gave %d bytes, want %d", len(got), len(in))
		}
	}
}

// TestPooledFlateDoesNotCrossContaminate is the test that matters for a pooled
// compressor, and it is deliberately not the easy version of it.
//
// Compressing the *same* bytes in every goroutine proves almost nothing: two
// goroutines sharing a writer would still be handling identical data, and a
// leaked window from a previous stream of the same content is invisible. So
// every goroutine and every iteration here compresses payload nobody else has,
// and each result is decompressed and compared against the bytes that goroutine
// actually put in.
//
// That is the assertion: not that the output looks plausible, but that the
// stream carries back exactly its own input. State leaking from another
// goroutine's stream — a shared window, a half-flushed block, a dictionary from
// the previous user of the writer — cannot survive it.
func TestPooledFlateDoesNotCrossContaminate(t *testing.T) {
	const (
		workers = 32
		rounds  = 60
	)

	// distinct builds payload no other (worker, round) pair produces, at a
	// range of sizes that straddles the deflate window so that a leaked window
	// would have something to leak.
	distinct := func(worker, round int) []byte {
		size := 1 + (worker*7+round*13)%(1<<16)
		b := make([]byte, size)
		seed := byte(worker*31 + round*17)
		for i := range b {
			switch (worker + round) % 3 {
			case 0: // highly compressible, so the window matters
				b[i] = seed
			case 1: // structured, like a content stream
				b[i] = "0123456789abcdef"[(i+int(seed))%16]
			default: // incompressible
				b[i] = byte(i*i + i*int(seed) + round)
			}
		}
		return b
	}

	var wg sync.WaitGroup
	fail := make(chan string, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				in := distinct(w, r)
				enc := FlateEncode(in)

				zr, err := zlib.NewReader(bytes.NewReader(enc))
				if err != nil {
					fail <- fmt.Sprintf("worker %d round %d: the stream does not open: %v", w, r, err)
					return
				}
				got, err := io.ReadAll(zr)
				zr.Close()
				if err != nil {
					fail <- fmt.Sprintf("worker %d round %d: reading back: %v", w, r, err)
					return
				}
				if !bytes.Equal(got, in) {
					fail <- fmt.Sprintf("worker %d round %d: %d bytes in, %d out, and they differ — "+
						"a stream carried something other than its own input", w, r, len(in), len(got))
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(fail)
	n := 0
	for msg := range fail {
		if n++; n <= 5 {
			t.Error(msg)
		}
	}
	if n > 5 {
		t.Errorf("... and %d more", n-5)
	}
}

// TestPooledFlateUnderLoadMatchesASerialRun goes one further: the concurrent
// output has to equal what the same inputs produce with no concurrency at all.
//
// Round-tripping proves a stream is self-consistent. This proves it is the
// *same* stream — that sharing a pool across goroutines does not change the
// bytes a document ends up containing, which is what would make two runs of the
// same program produce different files.
func TestPooledFlateUnderLoadMatchesASerialRun(t *testing.T) {
	const workers = 24
	inputs := make([][]byte, 200)
	for i := range inputs {
		inputs[i] = bytes.Repeat([]byte{byte(i), byte(i * 3), 'x'}, 1+i*37)
	}

	// The reference, computed with nothing else running.
	want := make([][]byte, len(inputs))
	for i, in := range inputs {
		want[i] = FlateEncode(in)
	}

	got := make([][]byte, len(inputs))
	var wg sync.WaitGroup
	var next atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(inputs) {
					return
				}
				got[i] = FlateEncode(inputs[i])
			}
		}()
	}
	wg.Wait()

	for i := range inputs {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("input %d: %d bytes under load, %d bytes serially — the pool "+
				"changed the output depending on what else was running",
				i, len(got[i]), len(want[i]))
		}
	}
}

// TestPooledFlateSurvivesAPoolThatKeepsNothing.
//
// sync.Pool is emptied at every GC, so under memory pressure most Gets are
// fresh writers and the pooled path is barely exercised — the opposite of the
// steady state the other tests see. This forces the mix: compress, collect,
// compress, so both a recycled and a brand-new writer are on the path.
func TestPooledFlateSurvivesAPoolThatKeepsNothing(t *testing.T) {
	in := bytes.Repeat([]byte("garbage collected between streams\n"), 500)
	want := FlateEncode(in)
	for i := 0; i < 20; i++ {
		runtime.GC()
		if got := FlateEncode(in); !bytes.Equal(got, want) {
			t.Fatalf("round %d after a GC: %d bytes, want %d", i, len(got), len(want))
		}
	}
}
