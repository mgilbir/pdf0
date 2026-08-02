package facturx

import (
	"testing"
)

func TestOrderXProfiles(t *testing.T) {
	for _, p := range []string{"BASIC", "COMFORT", "EXTENDED", "basic", "Extended"} {
		if _, ok := orderXProfileFor(p); !ok {
			t.Errorf("%q should be an Order-X profile", p)
		}
	}
	if _, ok := orderXProfileFor("EN 16931"); ok {
		t.Error("EN 16931 is an invoice profile, not Order-X")
	}
}
