package render

import "testing"

// The font-relative length units, which are the ones that need something layout
// knows and the cascade does not.
//
// Each width below is derived from a metric rather than recorded from a run: the
// standard faces are fixed, so "0" in Courier advances 600/1000 of the size and
// the arithmetic can be read in the test. A number copied out of a previous run
// would pass equally well against a wrong implementation.

func TestRemResolvesAgainstTheRoot(t *testing.T) {
	// The whole point of rem is that it does not compound as elements nest. This
	// engine passed the box's own font size as the root's, which made rem an
	// exact synonym for em — invisible until a document set a font size on
	// anything, and then wrong by whatever factor it set.
	root := layoutOf(t, 600, `<div><div id="probe"></div></div>`,
		`html { font-size: 16px }
		 div { font-size: 40px }
		 #probe { width: 2rem; height: 10px }`)
	if got := find(t, root, "probe").BorderRect.W.Px(); got != 32 {
		t.Errorf("2rem inside a 40px element resolved to %gpx, want 32 "+
			"(twice the 16px root size); 80 means it is being read as em", got)
	}
}

func TestRemAndEmDiffer(t *testing.T) {
	// Pinning the distinction directly, since an implementation that confuses
	// the two agrees with the test above whenever the root and the element
	// happen to match.
	root := layoutOf(t, 600, `<div><span id="r"></span><span id="e"></span></div>`,
		`html { font-size: 16px }
		 div { font-size: 32px }
		 span { display: block; height: 10px }
		 #r { width: 1rem } #e { width: 1em }`)
	rem := find(t, root, "r").BorderRect.W.Px()
	em := find(t, root, "e").BorderRect.W.Px()
	if rem != 16 || em != 32 {
		t.Errorf("1rem = %gpx and 1em = %gpx under a 32px element; want 16 and 32", rem, em)
	}
}

func TestChIsTheAdvanceOfZero(t *testing.T) {
	// Courier is monospaced at 600/1000, so "0" at 20px advances 12px.
	root := layoutOf(t, 600, `<div id="probe"></div>`,
		`#probe { font-family: Courier; font-size: 20px; width: 10ch; height: 10px }`)
	if got := find(t, root, "probe").BorderRect.W.Px(); got != 120 {
		t.Errorf("10ch at 20px Courier resolved to %gpx, want 120 (10 x 0.6 x 20)", got)
	}
}

func TestChFollowsTheFace(t *testing.T) {
	// Two boxes at the same size in different faces. This is the case the
	// memoization key has to separate: the parsed length is cached, and a key
	// that held only the size would let whichever box was laid out first decide
	// "10ch" for the other. A document that mixes a proportional and a
	// monospaced font is the common case, not an exotic one.
	root := layoutOf(t, 600,
		`<div id="mono"></div><div id="prop"></div>`,
		`div { font-size: 20px; width: 10ch; height: 10px }
		 #mono { font-family: Courier }
		 #prop { font-family: Helvetica }`)
	mono := find(t, root, "mono").BorderRect.W.Px()
	prop := find(t, root, "prop").BorderRect.W.Px()
	if mono != 120 {
		t.Errorf("10ch in Courier resolved to %gpx, want 120", mono)
	}
	// Helvetica's digits are 556/1000, so 10ch is 111.2px. The exact value
	// matters less than that it is not Courier's.
	if prop == mono {
		t.Errorf("10ch resolved to %gpx in both Courier and Helvetica; "+
			"the cached length is being shared across faces", prop)
	}
	if want := 111.2; prop < want-0.1 || prop > want+0.1 {
		t.Errorf("10ch in Helvetica resolved to %gpx, want %g (10 x 0.556 x 20)", prop, want)
	}
}

func TestExIsHalfAnEm(t *testing.T) {
	// The face layer carries no x-height, and CSS Values §5.1.2 specifies half
	// an em for exactly that case — so this is the specified answer, not a
	// stand-in for one. If a real x-height ever becomes available this test
	// should change with it.
	root := layoutOf(t, 600, `<div id="probe"></div>`,
		`#probe { font-size: 20px; width: 4ex; height: 10px }`)
	if got := find(t, root, "probe").BorderRect.W.Px(); got != 40 {
		t.Errorf("4ex at 20px resolved to %gpx, want 40", got)
	}
}
