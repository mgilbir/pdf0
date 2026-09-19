package core

import (
	"bytes"
	"compress/zlib"
	"io"
	"sync"
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

// TestPooledFlateIsConcurrencySafe. A pool is shared, and a document with many
// streams is the case this exists for; a writer handed to two goroutines would
// interleave their output.
func TestPooledFlateIsConcurrencySafe(t *testing.T) {
	const n = 64
	in := bytes.Repeat([]byte("concurrent content stream\n"), 200)
	want := FlateEncode(in)

	var wg sync.WaitGroup
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = FlateEncode(in)
		}(i)
	}
	wg.Wait()
	for i, got := range out {
		if !bytes.Equal(got, want) {
			t.Fatalf("goroutine %d produced different bytes (%d vs %d)", i, len(got), len(want))
		}
	}
}
