package render

import (
	"strings"
	"testing"
)

// List markers, and the number they count to.
//
// Markers used to count an item's position among its siblings, which is right
// for a plain list and wrong for every document that says otherwise — and wrong
// quietly, because the list is still numbered, just not with the numbers the
// author asked for. Each case below is one a sibling count gets wrong.

// markers returns the marker text of every list item, in document order.
func markers(t *testing.T, htmlSrc, cssSrc string) []string {
	t.Helper()
	root := layoutOf(t, 600, htmlSrc, cssSrc)
	var out []string
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f == nil {
			return
		}
		if f.Marker != nil {
			out = append(out, f.Marker.Text)
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

func want(t *testing.T, got []string, expect ...string) {
	t.Helper()
	if len(got) != len(expect) {
		t.Fatalf("got %d markers %v, want %d %v", len(got), got, len(expect), expect)
	}
	for i := range expect {
		if got[i] != expect[i] {
			t.Errorf("marker %d is %q, want %q (all: %v)", i, got[i], expect[i], got)
		}
	}
}

func TestPlainListStillCountsFromOne(t *testing.T) {
	want(t, markers(t, `<ol><li>a</li><li>b</li><li>c</li></ol>`, ``), "1.", "2.", "3.")
}

func TestListStartAttribute(t *testing.T) {
	// <ol start="5"> numbers 5, 6, 7. A sibling count gives 1, 2, 3.
	want(t, markers(t, `<ol start="5"><li>a</li><li>b</li><li>c</li></ol>`, ``),
		"5.", "6.", "7.")
}

func TestListItemValueAttribute(t *testing.T) {
	// <li value="3"> sets the counter, and the items after it carry on from
	// there rather than resuming their sibling positions.
	want(t, markers(t, `<ol><li>a</li><li value="7">b</li><li>c</li></ol>`, ``),
		"1.", "7.", "8.")
}

func TestListCounterCanBeResetInCSS(t *testing.T) {
	// The marker reads the same counter a stylesheet can set, which is what
	// makes "list-item" the name CSS Lists reserves rather than an internal.
	want(t, markers(t, `<ol><li>a</li><li>b</li></ol>`,
		`ol { counter-reset: list-item 10 }`), "11.", "12.")
}

func TestNestedListsCountSeparately(t *testing.T) {
	// Each list resets the counter, so the inner one starts again while the
	// outer carries on — and the outer's second item is 2, not 4.
	want(t, markers(t,
		`<ol><li>a<ol><li>x</li><li>y</li></ol></li><li>b</li></ol>`, ``),
		"1.", "1.", "2.", "2.")
}

func TestItemsNeedNotBeSiblings(t *testing.T) {
	// A list item wrapped in something else is still a list item, and the count
	// runs through it. Counting siblings restarts at each wrapper, which is the
	// case that makes the old approach visibly wrong rather than merely
	// incomplete.
	want(t, markers(t,
		`<ol><li>a</li><div><li>b</li></div><li>c</li></ol>`, ``),
		"1.", "2.", "3.")
}

func TestNegativeAndZeroStart(t *testing.T) {
	// start="0" and a negative one are legal and are not the same as absent.
	want(t, markers(t, `<ol start="0"><li>a</li><li>b</li></ol>`, ``), "0.", "1.")
	want(t, markers(t, `<ol start="-2"><li>a</li><li>b</li></ol>`, ``), "-2.", "-1.")
}

func TestMalformedStartIsIgnored(t *testing.T) {
	// An attribute that is not an integer leaves the stylesheet's answer
	// standing, which is the same as it not being there — and must not be read
	// as a partial number.
	for _, bad := range []string{"", "abc", "3px", "1.5", "٣", strings.Repeat("9", 40)} {
		got := markers(t, `<ol start="`+bad+`"><li>a</li></ol>`, ``)
		want(t, got, "1.")
	}
}
