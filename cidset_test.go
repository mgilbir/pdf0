package pdf0

import (
	"github.com/mgilbir/pdf0/internal/core"
	"testing"
	"time"
)

// TestCIDSetMembership pins the bit semantics of core.CIDSet.has / core.CIDSet.empty,
// which must match the previous map-of-set-bits exactly (MSB-first within each
// byte, CID i at byte i/8).
func TestCIDSetMembership(t *testing.T) {
	// 0xA0 = 1010_0000 -> CIDs 0 and 2; 0x01 = 0000_0001 -> CID 15.
	cs := core.CIDSet([]byte{0xA0, 0x01})
	want := map[int]bool{0: true, 2: true, 15: true}
	for cid := 0; cid < 24; cid++ {
		if cs.Has(cid) != want[cid] {
			t.Errorf("has(%d) = %v, want %v", cid, cs.Has(cid), want[cid])
		}
	}
	if cs.Has(1000) {
		t.Error("out-of-range CID must not be present")
	}
	if cs.Has(-1) {
		t.Error("negative CID must not be present")
	}
	if cs.Empty() {
		t.Error("a set with bits should not be empty")
	}
	if !core.CIDSet(nil).Empty() {
		t.Error("a nil set is empty")
	}
	if !core.CIDSet([]byte{0, 0, 0}).Empty() {
		t.Error("an all-zero set is empty")
	}
}

// TestCIDSetMatchesBitmap confirms core.CIDSet.has agrees with the direct bit test
// for every bit across a byte range — the equivalence to the old bitmap.
func TestCIDSetMatchesBitmap(t *testing.T) {
	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i*131 + 7) // arbitrary spread of bit patterns
	}
	cs := core.CIDSet(data)
	anySet := false
	for i, b := range data {
		for bit := 0; bit < 8; bit++ {
			want := b&(0x80>>bit) != 0
			anySet = anySet || want
			if cs.Has(i*8+bit) != want {
				t.Fatalf("has(%d) = %v, want %v", i*8+bit, cs.Has(i*8+bit), want)
			}
		}
	}
	if cs.Empty() != !anySet {
		t.Errorf("empty() = %v, want %v", cs.Empty(), !anySet)
	}
}

// TestCIDSetLargeNoBlowup guards the DoS this replaced: a large CIDSet must be
// cheap. Materialising a map of every set bit turned a 64 MB CIDSet into ~70s of
// validation; direct membership testing is O(1) per lookup regardless of size.
func TestCIDSetLargeNoBlowup(t *testing.T) {
	const n = 16 << 20 // 16 MiB = 128M bits, all present
	data := make([]byte, n)
	for i := range data {
		data[i] = 0xFF
	}
	cs := core.CIDSet(data)
	start := time.Now()
	// Membership tests across the whole range plus an emptiness scan.
	for cid := 0; cid < n*8; cid += 997 {
		if !cs.Has(cid) {
			t.Fatalf("CID %d should be present", cid)
		}
	}
	if cs.Empty() {
		t.Fatal("a full set must not be empty")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("large-CIDSet membership took %v; expected well under a second", d)
	}
}
