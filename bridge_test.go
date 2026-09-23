package pdf0

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/bridge"
)

// TestBridgeEntriesInstalledAndResolved: once this package is initialised,
// every entry point a public package keeps unexported (audit 2026-09-22 C163)
// has been installed by its owner and resolved, at its declared type, by the
// package that calls it. A missing install or a type mismatch panics during
// initialisation, before this test runs; what is left to catch is an entry
// nothing resolves.
func TestBridgeEntriesInstalledAndResolved(t *testing.T) {
	for _, e := range bridge.Entries() {
		if !e.Installed() {
			t.Errorf("%s: not installed by its package", e.Name())
		}
		if !e.Resolved() {
			t.Errorf("%s: installed, but nothing resolves it", e.Name())
		}
	}
}
