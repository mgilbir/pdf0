package render

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// The layout engine against an external oracle: the W3C Web Platform Tests.
//
// # Why these, and why they are an oracle
//
// A CSS reftest is a pair of documents with the assertion *these two render
// identically*. The pair and the claim come from the CSS Working Group, so a
// disagreement is evidence about pdf0 rather than a restatement of pdf0's own
// reading — which is the distinction ADR 0003 records this repository learning
// twice, the hard way.
//
// They are better than an ordinary expectation file for a reason worth stating:
// reftests are *constructed* so that the two documents reach the same rendering
// by different mechanisms. One uses a margin where the other uses a padding, one
// an inline-block where the other a float. So an engine bug usually moves one
// document and not the other, and shows up as a difference rather than as two
// matching wrong answers.
//
// No browser is involved. pdf0 renders both and compares.
//
// # What is compared, and why not the fragment tree
//
// §7.1 suggested comparing fragment trees. That turns out to be the wrong value,
// and for exactly the reason the reftests are good: the two documents have
// *different* structure on purpose, so their fragment trees differ even when
// they render identically. Comparing them would fail every test.
//
// The display list is the right one. It is what the two documents have in
// common — the marks on the page, with the structure that produced them gone —
// and it is a real value precisely so that something can attach here.
//
// # Vacuous passes, and the ratchet
//
// §7.1 names the trap: a reftest passes vacuously when the engine ignores a
// property in both documents. Two blank pages match. So a pass only counts
// towards the ratchet when *neither* document raised an unsupported finding —
// that is the companion signal §6.3 was designed to provide, and it is why the
// baseline below is of clean passes rather than of passes.
//
// That check was not enough on its own, and finding out why is the most useful
// thing this file has done so far. Planted layout defects — broken inheritance,
// display:none ignored, halved border widths — moved the clean-pass count by
// *nothing at all*. The reason was that the suite's .xht tests wrap their CSS in
// a CDATA section, which this engine handed to the CSS parser as a rule; the
// stylesheet then produced no declarations, so nothing could be reported
// unsupported, so every one of those tests looked clean while rendering two
// unstyled documents that matched trivially.
//
// The counting was working and it was counting nothing. Two things now guard it:
// the wrapper is stripped, and a document that paints nothing at all cannot
// count as a clean pass however quiet it was.
//
// # How much this oracle proves
//
// A reftest compares two documents rendered by the *same* engine, so in
// principle it can only see a fault that moves one of them and not the other. A
// uniform error — every border half as wide — shifts both sides equally and
// passes. That is the standing objection to reftests as a guard, and it was
// measured rather than assumed: against the earlier marks-based comparison, of
// eight planted layout defects this oracle caught two, and halved borders,
// broken inheritance, an ignored display:none and a missing min-height all went
// through.
//
// Resolving occlusion changed that more than expected. Re-measured against five
// planted defects, all five are now caught, halved borders among them — from
// 1569 clean passes down to 1029. The reason is that the reference documents in
// this suite are not built symmetrically with the tests: they draw the expected
// result directly, often with a plain image or a single filled box, so a fault in
// the engine's box model moves the test and leaves the reference standing. The
// marks-based comparison could not see that, because the pair disagreed about red
// rectangles before it ever got to the geometry.
//
// It remains a secondary guard. The planted-defect tests next door assert
// absolute numbers against the specification's own arithmetic, and they are what
// catches a fault that is uniform across both documents. What the reftests add is
// the class those cannot reach: an interaction between two features that no one
// thought to write a test for.
//
// The ratchet's value grows with the engine. Every layout feature that lands
// moves tests out of the "something unsupported" bucket and into this one, and
// each one that arrives brings its sensitivity with it.

const wptEnv = "WPT_TESTS"

// wptCleanPassBaseline is the number of reftests that pass with nothing
// unsupported reported in either document.
//
// It is a ratchet: it may rise and must never be lowered to make a red test
// green. A drop means a layout regression, and the failing names are printed.
//
// Replaced elements are worth recording here, because that number went *down*
// before it went up. Loading images unmasked 172 passes that had been counting
// nothing: the suite's references draw their expected picture with an <img>, so
// before images loaded those documents painted only their instruction paragraph
// — and so did the tests beside them, whose own square was drawn with a display
// value this engine does not implement. Two documents agreeing on a paragraph is
// not evidence about layout, and the "paints nothing at all" guard could not see
// it because both painted the paragraph.
//
// So a seventh of the baseline at the time was measuring the absence of a
// feature. That is the same lesson the CDATA wrapper taught above and the
// occlusion-blind comparison taught below, and it is the one this file keeps
// having to relearn: a pass is only evidence if something could have made it
// fail.
//
// The text properties — text-decoration, text-transform, box-sizing, visibility,
// text-indent, letter-spacing and word-spacing — took it from 1899 to 1916, and
// the headline understates them: failures went from 2017 to 1995, and most of
// the tests that stopped failing moved into the *vacuous* bucket rather than
// this one, where they wait on something else that is still unimplemented. That
// is the ratchet working as designed — a test counts here only once nothing in
// either document is missing.
//
// §17.6.2's collapsing border model took it from 1916 to 1998, and that number
// is two effects that are worth keeping apart, because together they overstate
// the layout work. Measured separately, by running the new layout with the old
// "collapse is not implemented" finding still in place:
//
//   - the *layout* moved 59 tests out of failing, 1995 to 1936, and every one of
//     them into the vacuous bucket, which stayed at 1916 clean;
//   - *removing the finding* then moved 82 tests from vacuous to clean, which is
//     honest reporting catching up with the engine rather than anything new
//     being drawn.
//
// Twelve tests went the other way, and they are worth recording as the clearest
// example this file has of what a vacuous pass is. The border-collapse-dynamic-*
// tests are marked "flags: dom" and change a border with a script; nothing here
// runs scripts, so they cannot pass. They *were* passing, because neither
// document drew a row border at all and two blank grids match. Drawing the
// borders made them fail honestly. The harness skips the "dom" flag now, for the
// reason given where the flags are read.
//
// Bidirectional text took it from 1916 to 2045, and that number is two effects
// worth keeping apart because they are different claims. They were measured
// separately, by running the suite with the properties reported as implemented
// and every layout effect of them switched off:
//
//   - Deleting the admissions for "direction" and "unicode-bidi", and the
//     unsupported-script finding for the right-to-left scripts, moved 113 tests
//     out of the tainted bucket without changing a pixel. Failures did not move
//     at all — 1995 before, 1995 after.
//   - Implementing the properties then moved the pixels: 16 more clean passes,
//     and failures from 1995 to 1979.
//
// The first number is the larger and the second is the one that is about layout.
// The reason the second is small is worth recording: the standard fourteen faces
// are the default font set and have no Hebrew or Arabic glyph, so a document with
// right-to-left *text* in it is tainted by glyph-missing whatever the ordering
// does. What the 16 are is documents where "direction: rtl" moved Latin content —
// the alignment, the over-constrained margins, and the order of the runs.
//
// Background images took it from 2578 to 2736, and once again the number is two
// effects that have to be kept apart. They were measured separately, by running
// the finished engine with every painting effect of the seven new properties
// switched off and the old padding-box background colour restored — so the
// reporting is the new one and the ink is the old one:
//
//   - the *reporting* alone moved 99 tests from tainted to clean, 2578 to 2677,
//     without changing a pixel: failures did not move at all, 2293 before and
//     2293 after. That is the unsupported-property finding for background-image
//     going away, which is honest reporting catching up with the engine.
//   - *painting* the backgrounds then moved 59 more into the clean bucket, 2677
//     to 2736, and took failures from 2293 to 2227.
//
// The first number is the larger and the second is the one about layout. 66
// failures became passes and only 59 of them were clean, which is the usual
// shape: a test that stopped failing on its background often still has something
// else in it this engine does not do.
// Rectangle glyphs took it from 2578 to 2923, and not one line of layout changed
// to do it — this is the third time this file has recorded the same lesson, and
// the largest instance of it. The comparison was calling a run of Ahem a piece of
// text and the identical black square beside it a fill, and ruling that a
// difference. Failures fell from 2293 to 1930, and every one of the cases dumped
// and read by hand was already correct to the layout unit. So the whole of this
// number is honest reporting catching up with an engine that was already right,
// and none of it is new ink on the page. See blockglyph_test.go.
//
// One test went the other way and is worth naming, because it is the same shape
// as the border-collapse dozen above. lists/list-item-dynamic-color.html sets a
// red marker and turns it green from a script; nothing here runs scripts, so it
// cannot pass. It *was* passing, because the comparison could not see the square
// at all. Drawing it made the test fail honestly. It carries no flags metadata,
// so the harness has no way to skip it.
//
// An inline box's own horizontal margin, border and padding took it from 2923 to
// 2973, and that number *is* about layout: failures fell from 1930 to 1878, and
// the two directions were counted rather than netted. 62 tests stopped failing
// and 10 started, every one of the ten understood:
//
//   - Nine are §8.6's bidi box model, where the box's inset is on the wrong side
//     of a "direction: rtl" inline. insetItems says what implementing it would
//     take and why swapping the sides on the direction property is not it.
//   - One, linebox/split-inline-borders, fails because its *reference* uses
//     border-inline-end and padding-inline-end, which this engine does not
//     implement and does report. The test document is now right and the
//     reference is not.
//
// How the fault was found is worth recording, because nothing was looking for
// it. The css/CSS2/text directory's letter-spacing and word-spacing families —
// 34 tests — check their property by drawing the same picture twice, once with
// the property and once with an equivalent margin on an inline box. The engine
// had letter-spacing and word-spacing exactly right and no inline margin at all,
// so a third of that directory failed and read as a spacing fault.
//
// §4.1.2's trailing white space took it from 3132 to 3143, and this time the two
// effects do not need separating because there is only one: not a finding was
// added or removed, so the whole of the movement is layout. Failures fell from
// 1812 to 1780, counted in both directions — 32 tests stopped failing and none
// started — and the 21 that are not in this number went to the vacuous bucket,
// where they wait on something else the engine does not do.
//
// What moved is worth naming, because the largest directory left in the suite is
// css-text/white-space and none of the three was the thing it looked like:
//
//   - break-spaces was breaking after the *word* rather than after the space it
//     is named for, so a line took one word too many. The rule is that the wrap
//     opportunity is after every preserved space and nowhere else, which makes a
//     space part of the unit before it.
//   - §4.1's "other space separators" — the Zs category less U+0020 and U+00A0 —
//     were ordinary text. An ideographic space at the end of a line hangs like
//     any other space; it was stretching the box that held it.
//   - a preserved space at the end of the *last* line hangs only conditionally,
//     which means it counts when it fits. This engine hung it unconditionally
//     and a unit test asserted that it should, which is the second time this
//     repository has found a test pinning a bug rather than a rule.
//
// Block layout and floats took it from 3132 to 3254, and for the fourth time in
// this file the larger half of the number is not about layout. The two were
// measured separately, by running the finished engine with the harness's
// resource resolver put back the way it was:
//
//   - the *harness* accounts for 89 of it, 3132 to 3221, and failures from 1812
//     to 1724 — 100 tests stopped failing and one started. It is the note on
//     newSuiteResolver: the suite keeps its shared references in
//     css/CSS2/reference/ and its images in css/CSS2/support/, so every one of
//     those references writes "../support/black96x96.png" and every one was
//     refused by a resolver rooted at the reference's own directory. 149 tests
//     share ref-filled-black-96px-square.xht alone, and each was failing
//     because the *reference* drew six words of alt text that the test document
//     had no counterpart for. Not one pixel of engine output changed.
//   - the *layout* accounts for the other 33, 3221 to 3254, and failures from
//     1724 to 1691. 33 tests stopped failing and none started, counted by name
//     over the whole suite rather than netted.
//
// The one test that started failing is worth naming for the usual reason:
// inline-svg-100-percent-in-body's reference can now load the SVG it names, and
// this engine does not render SVG. It was passing because neither document drew
// the picture, and it fails honestly now.
//
// What the 33 are: §8.3.1's rule that a float between two blocks does not stop
// their margins collapsing, which was defeated in every real document by the
// newline after the tag (see wrapInlines); §8.3.1's clearance separating a
// margin from its parent's; §4.2's rule that a declaration with an illegal
// value is dropped rather than clamped, which the sizing properties needed; and
// §9.5's rule that a box establishing a formatting context may not overlap a
// float.
//
// An inline box's own background and border took it from 3447 to 3480, and the
// two effects that have to be separated this time are not reporting and layout —
// not a finding changed — but a feature and a bug it *exposed*. Each was measured
// on its own, against the same base:
//
//   - painting an inline box's background and border, alone: clean passes 3447 to
//     3448, failures 1470 to 1454. 30 tests stopped failing and 14 started. Only
//     one of the 30 reached the clean bucket; the rest went to the vacuous one,
//     295 to 310, where they wait on something else the engine does not do.
//   - the illegal-value fix, alone: clean passes 3447 to 3465, failures 1470 to
//     1452, 18 tests, none the other way. "border-top-width: -1pt" is an illegal
//     value, so CSS 2.1 §4.2 drops the declaration and the initial "medium"
//     stands; layout clamped the negative to zero instead and drew no border at
//     all. The four border widths joined the sizes and the paddings in
//     nonNegative.
//   - together: 3480 clean and 1422 failures, counted by name over the whole
//     suite — 48 tests stopped failing and none started. The two do not add,
//     because 14 of the 48 are the tests that need both.
//
// Those 14 are worth recording for how the bug stayed hidden. css/CSS2/borders
// checks each width property by drawing two rules on a <span>, once with the
// illegal value and once with "medium", and asserting they are the same
// thickness. With no inline border painted the test drew nothing, the reference
// drew nothing, and 14 tests passed on two blank pages — while the same clamp sat
// in every block box in the engine, where the other 18 could see it.
//
// What the painting itself moved is three families, and all three are the shape
// the feature was chosen for — a reference that draws its expected picture with a
// background on a <span>: 16 in css-text/white-space, where a blue span marks
// where a hanging space went; 7 in css/CSS2/text; and 4 in css/CSS2/linebox,
// which is the directory that tests §8.4's rule that an inline box's vertical
// padding and border bleed over the lines around them without moving any of them.
// §10.5's percentage height took it from 3551 to 3610, and for once the two
// effects do not need separating: not a finding was added or removed, so the
// whole of the movement is layout. Failures fell from 1349 to 1290, counted by
// name over the whole suite rather than netted — 59 tests stopped failing and
// none started.
//
// What it was is worth recording, because the headline was wrong again and this
// time the failing document was the *reference*. The four largest directories
// left in the suite are §9's and §10's, and grouping their 365 failures by the
// shape of the display-list difference rather than by filename put 55 of them on
// one line of arithmetic: a percentage height was refused whatever it was a
// percentage of. The suite's references say "html, body, div { height: 100% }"
// and then position a background against the bottom of that box, which is how a
// reftest puts an expected picture at the bottom of the page — so the *test*
// document drew its green square at the bottom, correctly, and the reference
// drew its own at the top of a box as tall as one line of text.
//
// The condition §10.5 attaches to the rule is real and is still enforced; what
// was wrong was reading the condition as the rule. See percentheight_test.go.
// A wrap opportunity around an atomic inline took it from 3610 to 3624, and
// again the whole of it is layout: no finding moved, failures fell from 1290 to
// 1276, and counted by name 15 tests stopped failing and one started.
//
// It is the same lesson as the percentage height above and was found the same
// way. Eleven failures in css/CSS2/positioning shared one display-list
// signature, and in every one of them the document laying out wrongly was the
// reference: it draws a two-row expected picture as two full-width images with
// no space between them, and this engine set them side by side because an atomic
// inline offered no break opportunity of its own. UAX #14's LB20 gives one on
// both sides, and LB7 takes back the one a following space would have taken.
//
// The test that started failing is understood and is about the page rather than
// the engine. css/CSS2/values/units-002 needs a line 700px wide and this page's
// content box is 626.52; both documents used to overflow that on one line and
// agree, and now both wrap — at different line heights, because one line holds
// 250px text and the other 200px images. The suite is written for an 800px
// viewport, and this is the first test to notice.
// Seeing through a patterned picture took it from 3624 to 3721, and none of it
// is layout. This is the fifth time this file has recorded a large block of
// failures that were about the comparison rather than about the engine, and it
// is the largest since the rectangle glyphs: 99 tests stopped failing, two
// started, and not one line of the engine changed.
//
// What it was: the suite draws with two kinds of picture. A solid swatch, which
// the uniform-colour equivalence already handled — and a *pattern*, three or
// five solid bands with a fully transparent region in it, which is how a test
// says "nothing of mine should show here, and the page behind should". Compared
// as one opaque mark keyed by its file, such a picture hid whatever the document
// had drawn behind it, so a test drawing a green box under a striped overlay
// differed from a reference drawing the same green box and two red bars. Twelve
// of the css/CSS2/margin-padding-clear margin-collapse family are exactly that,
// and every one had geometry correct to the layout unit.
//
// The decomposition is exact and picture_test.go argues why. What is worth
// recording here is the shape of the investigation rather than the fix, because
// it is the same shape as the two commits before it: the four largest failing
// directories were grouped by the *display-list difference* rather than by
// filename, and the three clusters that came out of it were a percentage height,
// a wrap opportunity, and this — of which only the first two were about layout.
//
// The two that started failing are honest and are named for the usual reason.
// background-root-008 and -009 tile a 17px pattern down the left edge, and the
// test and its reference now disagree by one pixel about where the tiling
// starts: 19 against 18. That is a real difference in where this engine anchors
// a background propagated from the body to the canvas, it was there before, and
// the old comparison could not see it because both documents drew the same
// opaque unknown. It is reported rather than fixed here.
//
// §17.5.2.1's two width rules took it from 3551 to 3618, and this time both
// numbers are about layout: not a finding was added or removed, and the two were
// counted by name over the whole suite and in both directions.
//
//   - A first-row cell's declared width is a content width, so the column it
//     settles has to hold that cell's padding and border too. 59 tests, 3551 to
//     3610, none the other way, and every one of the 59 landed in this bucket
//     rather than the vacuous one.
//   - §17.6.1's divergence, where a <table>'s width is its border box and a
//     "display: table" div's is its content box: another 8, 3610 to 3618, again
//     none the other way.
//
// Joining abutting runs in the comparison took it from 3618 to 3644, and none of
// that is layout: not a pixel of engine output changed. Failures fell from 1282
// to 1255 — 27 tests stopped failing, none started, and one of the 27 went to the
// vacuous bucket rather than this one.
//
// It is the fifth time this file has recorded a block of failures that were about
// the harness, and the reason these were invisible is that the two documents were
// already drawing the same glyphs in the same places. The comparison matched them
// run against run, so a reference setting "a bc d" as one line disagreed with a
// test setting "a b" and "c d" as two table cells that touch — over a boundary
// that exists in the display list and not on the page. See joinRuns.
//
// The two remaining ways a table's own width comes out wrong took it from 3644 to
// 3663, both layout and both measured on their own, by name over the whole suite
// and in both directions. No test started failing at either step.
//
//   - §17.5.2.1's last sentence, that the table is the greater of its declared
//     width and the sum of its columns: 2 tests, 3644 to 3646. Small, and worth
//     the note for what the failure looked like — nothing was clipped, the
//     columns were laid out at the widths the author gave them and the table's
//     own box was the smaller number, so the cells hung out of the side of their
//     own table and what was behind it showed through.
//   - §17.4's percentage, which is of the *wrapper's* containing block: 18 tests,
//     3646 to 3663, failures 1253 to 1235. The wrapper shrank to fit the table's
//     content and the percentage then resolved against that, so "width: 80%"
//     meant eighty per cent of the widest word in the table. Eleven of the 18 are
//     css/CSS2/floats-clear's float-applies-to family, which floats a
//     "display: table" at a percentage width and is not a table test at all.
//
// Generated content and lists took it from 3551 to 3642, and the headline is the
// sixth time this file has recorded the same lesson and the clearest instance of
// it. The two directories held 162 failures between them and the obvious reading
// was counters and quotes; 97 of the 162 raised no finding at all, and the first
// one dumped was §10.8.1. A ::before with "font-size: 30px" had its baseline
// 11.66px too high — and so did a plain <span> at 30px in a 16px div, which is
// the moment the framing changed. Nothing about it was generated content: the
// engine stacked only the *atomic* inlines when it built a line box, so a run of
// text in an inline box of a different size contributed nothing to the line's
// height, and the tests are simply full of pseudo-elements with a size on them.
//
// The four changes, measured separately and counted by name over the whole
// suite rather than netted:
//
//   - §10.8.1's leading for text runs: 3551 to 3616, failures 1349 to 1284. 66
//     tests stopped failing and one started. 50 of the 66 are in lists and only
//     9 in generated-content, which is the clearest evidence that the cluster
//     was never about the feature it was filed under.
//   - §12.4.1's counter scope: 3616 to 3619. A pseudo-element is a *child* of its
//     element, so a counter its ::before creates nests inside the element's own
//     rather than overwriting it; an element with "display: none" cannot count,
//     and neither can a pseudo-element that generates no box.
//   - §12.3's quotes: 3619 to 3635. Split, because there was a finding to remove
//     as well as marks to draw: admitting the property alone moved *nothing*, 0
//     tests either way, because every test that declares "quotes" also uses the
//     keywords and stayed tainted by the content finding; removing that finding
//     with the marks still suppressed moved 1 test to clean and no failures;
//     drawing the marks moved the other 15 and took 15 tests out of failing. So
//     unusually for this file the ink is almost the whole of it.
//   - §12.5.1's inside markers: 3635 to 3642. A marker with "list-style-position:
//     inside" is the first inline box in the item rather than a mark beside it,
//     which is what makes an *empty* list item one line tall and show its
//     background — the shape a dozen of the "does this property apply to a list
//     item" tests are built on.
//
// Over the four: 98 tests stopped failing and one started, 1349 to 1252. The one
// is visudet/line-height-205, and it is the familiar shape. It asserts that a
// line box set in two downloadable faces is the union of the two, which needs
// @font-face; nothing loaded one at the time, so the test could not pass. It
// *was* passing because neither of its two divs grew, and now the div whose
// spans name the missing families is 2.25px taller than the one that names them
// together — the union of two faces with different baselines, which is §10.8.1
// working. It fails honestly.
//
// It still fails, and the reason has narrowed rather than gone: @font-face is
// implemented now and this test's two faces are `format('woff')`, a compressed
// container this engine does not unwrap. So the fonts are refused before the
// read and reported, which is the honest answer and not a passing one.
//
// Floats, clearance and margins took it from 3924 to 3957, and this time the
// reporting and the ink need no separating: the vacuous count is 320 before and
// 320 after, so not one test moved buckets for a reason other than a mark
// changing place. 33 tests stopped failing, 968 to 935, and *none* started —
// each of the five changes was measured on its own against a per-test dump and
// counted in both directions.
//
// What the failures turned out to be is the part worth recording, because the
// heading was wrong again. Of the 159 failures across floats, floats-clear and
// margin-padding-clear, only 27 came from the three rules the directories are
// named for; the rest are still there. The five that moved:
//
//   - <table width> was not a presentational hint. Eighteen of the dbaron float
//     tests declare their table's width as an attribute, so the table came out
//     at its content's width and the whole document was laid out at half scale.
//     Five tests, and not a line of float code.
//   - §9.5's non-overlap rule was asked at a single y. A box is a rectangle and
//     a float whose top is below the box's top still overlaps it, which is the
//     subject of twelve tests that say so in their titles. Six of them.
//   - The same rule was asked as "is the band wide enough" rather than as "do
//     these two rectangles meet", which cannot see a float outside the
//     containing block, and it counted the box's margins against a rule that is
//     about its border box. Five more.
//   - Line boxes had the same single-y fault, which is the other six of the
//     twelve, plus one on a zero-height float.
//   - "height: 0" was read as a barrier a margin cannot cross. §8.3.1 asks for
//     "zero or 'auto'" where a box's own two margins meet and for "'auto'"
//     where a parent's meets its last child's, and one condition was doing for
//     both. Three tests, two of them nowhere near a float.
//   - And one that is not a layout rule at all: "background: url(x) left -1em"
//     parsed as no position, because the keyword grabbed the length greedily and
//     the result was then refused. Seven tests, four of them in the backgrounds
//     directory. Removing the *finding* alone moved nothing in either direction
//     — those tests were failing, not tainted — so the whole of the seven is the
//     image landing where the stylesheet put it.
//
// What did not move is worth naming too. css/CSS2/floats-clear went from 65
// failures to 64. Almost all of what is left there is one missing behaviour: a
// float's position depends on the margins that collapse around the block it sits
// in, so a float followed by a large margin is pulled down with it — and
// clearance on the box carrying that margin has to put the float back. This
// engine places a float where the flow has reached when it meets it and never
// moves it again, which is right until a later sibling's margin collapses
// through. adjoining-float-before-clearance and the four
// adjoining-float-nested-forced-clearance tests are all this one thing.
// §11.1's clipping took it from 3924 to 3979, and the three effects were
// measured separately because the headline named the wrong one of them. The
// directory this was filed under is css/CSS2/visufx, whose 47 failures read as
// "overflow"; 44 of them are the "clip" property and three are overflow.
//
//   - registering "clip" so that it stops being reported unsupported moved
//     *nothing*: 3924 clean and 968 failures before and after. Unusually for
//     this file the reporting half is zero, and the reason is that every one of
//     those tests was failing rather than passing vacuously — a red square that
//     should have been clipped away was on the page, so no amount of honest
//     reporting could move it.
//   - overflow clipping: 3924 to 3935 clean, 968 to 957 failures. 11 tests
//     stopped failing and none started, counted by name over the whole suite.
//     Only one of the 11 is in visufx; the rest are in normal-flow, floats,
//     text and margin-padding-clear, which is the usual shape — a test that
//     needs a clip is rarely filed under the property.
//   - the clip property: 3935 to 3979 clean, 957 to 914 failures, 44 tests and
//     one the other way.
//
// The one is visufx/clip-001, and it is the same shape as the border-collapse
// dozen and lists/list-item-dynamic-color above. It sets "clip" from a script
// and asserts the result; nothing here runs scripts, so it cannot pass. It
// *was* passing because the property did nothing, and drawing the clip made it
// fail honestly. It does carry the "dom" flag this harness skips — but written
// as <meta content="dom" name="flags"/>, with the attributes in the order
// flagsRe does not match. 142 documents in the suite write them that way, about
// forty of them with a flag that would be skipped, so correcting the expression
// is a measurement of its own rather than a tidy-up, and it is reported here
// rather than taken.
//
// Buried text took it from 3979 to 4007, and this is the sixth time this file
// has recorded a block of failures that were about the comparison rather than
// about the engine. Not a line of layout changed, failures fell from 914 to
// 886, and 28 tests stopped failing with none the other way.
//
// It is the abspos-overflow family, and it is worth recording for what it
// turned out not to be. Twelve tests in css/CSS2/positioning are written about
// §11.1.1's rule that an absolutely positioned box escapes an ancestor's
// overflow, ten of them were failing, and the rule was implemented — but
// dumping their display lists showed every one of the ten already correct to
// the layout unit. What they have in common is a red "FAIL" that an opaque
// green box is painted over: this comparison resolved occlusion between
// *fills* and never between a fill and a run of text, so it was counting
// letters that neither document shows. Eighteen more tests elsewhere are the
// same shape.
//
// The rule that fixes it is exact and narrow — a single opaque mark painted
// after the run and containing the whole of its ink — and picture_test.go says
// what it deliberately does not do. That the *clipping* work above sits between
// this note and the tests it was sent to fix is the whole lesson: the brief
// said those tests needed §11.1.1, and they needed the oracle to be able to see
// a covered word.
// §10.8.1 on a run of text and §8.6's bidi box model took it from 3924 to 3953,
// and for once the number needs no separating at all: not a finding was added or
// removed, so the whole of it is layout. Failures fell from 968 to 939, counted
// by name over the whole suite and in both directions — 29 tests stopped failing
// and none started. The two were measured on their own against the same base.
//
//   - vertical-align on an inline box's *text*: 3924 to 3947, 23 tests. The
//     property was read for atomic inlines only, so a "vertical-align: 96px" on
//     a <span> moved nothing at all and a <sup> sat on the line of type. Eighteen
//     of the 23 are one family — linebox's vertical-align-007 through -104, which
//     check a length or a percentage in each of nine units.
//   - §8.6's bidi box model: 3947 to 3953, 6 tests, all of bidi-text's
//     bidi-box-model family.
//
// Neither headline was the first thing tried, and every intermediate answer is
// worth recording, because each was found by a test rather than by argument.
//
// The first vertical-align attempt aligned each *item* rather than each aligned
// subtree, and cost three tests while gaining 23. Both faults behind them are
// real and both are now implemented:
//
//   - §10.8.1's "top" and "bottom" move a box together with everything
//     baseline-aligned inside it. Applied per run, a "vertical-align: top" span
//     holding two sizes of text had the smaller run pulled up out of the words it
//     belongs with — which is precisely what anonymous-inline-inherit-001
//     asserts must not happen.
//   - §16.3.1 draws a decoration across the whole of the box that *declared* it,
//     "without paying any attention to" the descendants it crosses, so moving the
//     run moved the underline with it. text-decoration-va-length-001 sets three
//     spans at three alignments under one overlining div and asserts one straight
//     line; it got three stepped ones.
//
// §8.6 went through three readings before it stopped costing something:
//
//   - swapping the two insets on the *first* content item's level cost
//     css-text/white-space's tab-bidi-001, whose outer span holds a right-to-left
//     <bdo> and then left-to-right text. A box whose content is not right-to-left
//     throughout has ends that need not be at its visual edges at all, and two
//     items cannot say where they are; the engine leaves such a box alone now.
//   - giving an inset the *paragraph's* base level, which is what rule L1 gives a
//     line's leading separators, cost css/CSS2/text's bidi-span-003: a
//     purple-bordered span of Latin in a "direction: rtl" div had its opening
//     border thrown to the far end of the line, so a border drawn round one word
//     enclosed two. An inset takes the lowest level anything inside its box
//     reached instead, which is the level the box's own edges sit at.
//
// The reading that §8.6 keys on the direction property is the one insetItems
// recorded as nine clean passes worse, and it is still wrong. §8.6 is physical on
// both sides of its rule: the leftmost generated box carries the left inset
// whichever direction is declared, and the direction decides only which *line* of
// a box broken across several. What had to be found was not the property but
// whether the algorithm reversed the content, and that is a resolved embedding
// level.
// The last sixteen came from css-text/white-space, and it is worth recording
// which half was engine and which was apparatus, because the two are routinely
// conflated in this file's history.
//
//   - eleven are §4.1.2's hang landing on the wrong side of a right-to-left
//     line. Rule L1 puts the trailing spaces at the paragraph's level, so they
//     are drawn *before* the first word, and the alignment was computed as
//     though they still followed the last — every dir=rtl pre-wrap line was
//     pushed right by the width of a space that hangs. That is layout, and it is
//     the whole of the dir=rtl half of pre-wrap-align-*.
//   - five are the comparison's own: text marks were paired by their place in a
//     sorted list, and the sort key was an x that nearlyAt's tolerance exists to
//     forgive, so a sixty-fourth of a pixel could reverse two marks in one list
//     and not in the other. That is apparatus, and it moved no pixel. The note
//     above num() had recorded the symptom on twelve ws-break-spaces-applies-to
//     tests and diagnosed it as needing a positional tolerance; the tolerance
//     arrived and the sort key did not follow it. Seven of the twelve are still
//     failing, on a table width and not on this.
//
// §9's and §10's remaining families took it from 4072 to 4100, and this time the
// two effects need no separating because there is only one: the vacuous count is
// 319 before and 319 after, so not a finding moved and the whole of it is
// layout. Failures fell from 821 to 793, counted by name over the whole suite —
// 28 tests stopped failing and none started.
//
// The diagnosis is worth more than the fix and is the seventh time this file has
// recorded the same shape. The three directories the work was aimed at —
// positioning, normal-flow and box-display — held 195 failures, and 78 of them
// cannot be fixed by any amount of layout work:
//
//   - 45 draw their picture with inline SVG, which this engine does not render.
//     The absolute-replaced-width family is 25 of them, and it is the largest
//     single family of failures in the three directories.
//   - 33 are flagged "dom" and change the page with a script. They are run only
//     because flagsRe wants the attributes in the order name-then-content and
//     these write content-then-name — the expression fault recorded above,
//     measured here at 33 tests in three directories.
//
// Of the 117 that were left, five rules moved 28. None of the five is what the
// directory names suggest, and four are one section away from where the tests
// are filed:
//
//   - a child's own min-width and max-width were left out of what it contributes
//     to the width a float or an absolutely positioned box shrinks to fit, so a
//     box holding a child at "max-width: 4em" came out at the width of the eight
//     ems of text inside that child, and the page showed through the difference.
//     10 tests, four of them not in these directories at all — they are in
//     floats-clear, where a float's shrink-to-fit is the same calculation.
//   - §10.3.7's right-to-left half: "set 'right' to the static position" and
//     "set 'margin-right' to zero and solve for 'margin-left'". 8 tests. The
//     static position needed a second number rather than a change of sign — see
//     absCandidate.staticEnd.
//   - §10.4's clamp reaching through a box into its descendants: the used height
//     is what a percentage height resolves against, so a maximum that cuts the
//     box down has to cut it down before its content is laid out. 2 tests.
//   - §E.2's step 4 is over the *non-inline-level* descendants, so an
//     inline-block's background belongs with the line it sits on and not with
//     the block backgrounds. Painted in tree order it went underneath a later
//     sibling's background instead of over it. 6 tests.
//   - §10.4's constraint table divides by the ratio of the tentative used width
//     and height, and "height: 0" with an auto width makes that pair 0 by 0. The
//     intrinsic ratio stands in for it. 2 tests.
//
// One more change is in the same commit and moved *nothing*: a percentage width
// in an intrinsic measurement was being resolved against a basis of nought,
// which makes "width: 50%" mean "width: 0" rather than the "as though auto" CSS
// Sizing asks for. Zero tests either way over the whole suite, so its evidence
// is a unit test written for it rather than anything here — worth recording
// because intrinsic.go's own comment had claimed the correct behaviour for as
// long as the code had done the other thing.
// §4.1.2's tab threshold — "if this distance is less than 0.5ch, then the
// subsequent tab stop is used instead" — took it from 4116 to 4119, and the
// three are the whole of the effect: failures went from 742 to 739 and the
// vacuous bucket did not move, so nothing here changed category without also
// changing a page. They are tab-stop-threshold-002, -004 and -006, one for each
// of pre, pre-wrap and break-spaces, and their references are the same document.
//
// It is a small number for a rule worth writing down anyway, because the tests
// it moved are the only three in the suite that can tell the two readings apart
// and the difference they measure is not small: a tab a tenth of a character
// before its stop advanced a tenth of a character, so the column an author wrote
// it to make was not a column at all.
// "width: min-content" and "width: max-content" took it from 4119 to 4124, and
// the two effects have to be kept apart because together they overstate the
// layout work. Failures went from 739 to 732, so *seven* pages changed and came
// out right. Of those seven, four had nothing else unsupported in either
// document and count here; the other three still carry a missing ogham glyph, so
// they moved into the vacuous bucket and wait there. The fifth clean pass is the
// taint coming off: one test was already passing and its finding was the only
// thing keeping it out of this count. Nothing about that page changed.
//
// Two of the seven needed a second fix that moves nothing at all on its own —
// the trailing white space §4.1.2 removes at a line edge was taken off the
// preferred width and not off the minimum, which is invisible until something
// asks for the minimum by name. Measured separately: the trim alone leaves all
// three numbers exactly where they were, and the keyword alone *breaks* a test
// that was passing. They are only a pair, which is why the trim's evidence is a
// unit test written for it rather than anything here.
// §4.1.2's conditional hang, read per character rather than per sequence, took
// it from 4124 to 4126, and only one of those two is a page. Failures went from
// 732 to 731: white-space-pre-wrap-trailing-spaces-001, whose reference centres
// a letter where a full line puts it and not where a line of five characters
// does. The second clean pass is text-align-white-space-001, which was passing
// already and lost its "text-align: justify is not implemented" finding — its
// lines now come out exactly full, and a full line is one justification would
// not have moved, so the finding no longer applies. Nothing about that page
// changed; see alignLine for why the report sits inside the slack test.
//
// The clamp inside the change moves nothing here at all and is not decoration:
// it decides how far a sequence hangs, which is invisible until the hang is at
// the *start* edge of a right-to-left line. Its evidence is a unit test.
//
// Generated content, the margin-collapsing rules and the counter grammar took it
// from 4116 to 4158, and the run that did most of the work was not about
// generated content at all. In order of size, and each measured on its own:
//
//   - +14, the user-agent stylesheet underlining every <a> rather than every
//     link. An empty <a> is where the suite hangs its pseudo-elements, and every
//     one came out blue against a reference that drew neither the colour nor the
//     line.
//   - +9, a string value that could not survive the cascade's own round trip: a
//     newline in "content: 'a\Ab'" was written back raw, did not tokenize, and
//     was reported as a value this engine cannot produce.
//   - +10, §8.3.1's "margins of the root element's box do not collapse".
//   - +3, white-space processing on generated content, which this engine used to
//     say deliberately did not apply.
//   - +4, §8.3.1's rule read over the whole run of adjoining margins rather than
//     folded two at a time. The two are not the same arithmetic once a negative
//     is in the run, and the walk was folding.
//   - +1 each, the order a collapsed-through run leaves a box by, and §12.2's
//     grammar for the counter functions.
//
// The +17 that takes it to 4175 is not the engine at all and is recorded
// separately for that reason: it came from trimRunSpace in picture_test.go, which stopped the
// comparison treating white space *inside* a run as something that decides where
// the run ends. Eighteen pairs drew the same glyphs in the same places and were
// told apart by which call the space was batched into. Nothing rendered
// differently after it than before.
//
// Form controls took it from 4185 to 4192, and this time the split between
// reporting and layout is the other way round from every entry above: the
// reporting is worth *one* test and the ink is worth the rest. Measured by name
// over the whole suite and in both directions — 9 tests stopped failing and 2
// started, 671 to 665.
//
//   - the *reporting* is one test, floats-wrap-bfc-outside-001, which was
//     already passing and was tainted by nothing but a dropped <button>. No
//     failure moved with it.
//   - the *layout* is the other 6 of the net 7. Seven tests stopped failing
//     outright: five are css/CSS2/generated-content's form:before family, which
//     hangs an attr() on a <form> and draws it in a bordered box; one is the
//     same shape on a <label>; and one is normal-flow/blocks-026, which needs an
//     <input> to be a box at all.
//
// The headline was wrong again, and this is the eighth time this file has
// recorded that. "67 failures involve a form control" was a grep over the
// suite's source; grouped by what the pair actually needs, 29 of the 671
// failures mention one of these tags and only 19 of those could be moved by
// laying a control out. The rest divide into three groups worth naming:
//
//   - twelve are textarea tests in css-text/white-space, and ten of them are
//     blocked on something else entirely: they put the control's styling in a
//     <link rel=stylesheet>, and this engine follows no links, so the test
//     document is styled by the user-agent sheet while its reference carries a
//     <style> with everything in it. No amount of control work reaches them.
//   - the remaining two, textarea-pre-wrap-012 and -013, are a §4.1.2 question
//     and not a control one: their reference hangs a preserved space before a
//     forced break, and this engine hangs it only conditionally there, so the
//     line is aligned one character out. A <div> with the same content behaves
//     identically, which is what says it is not about the control. Reported
//     rather than taken.
//   - seven are <object>, and every one of them is unreachable: five draw their
//     picture with SVG, one embeds an HTML document, and one needs an object to
//     decode a PNG. Rendering the fallback content — which is what HTML says an
//     object whose data cannot be used represents — moved none of them, and is
//     in this change for what it does to a real document rather than for what it
//     does here.
//
// The two that started failing are understood and both are honest:
//
//   - white-space-pre-001.xht loses a leading newline it used to keep. HTML's
//     tree builder drops the line feed immediately after a <pre> start tag and
//     this engine now does too, which is right for HTML and wrong for this file:
//     it is XHTML, where there is no such rule, and its reference is drawn for a
//     <pre> seven lines tall. The harness already adapts these documents in one
//     place (see cdataRe); this is a second place where reading XHTML as HTML
//     shows, and it is reported rather than worked around. Measured on its own,
//     the newline rule is worth exactly this: −1 clean, +1 failure, no other test
//     either way.
//   - textarea-always-preserves-spaces-001.tentative was passing because neither
//     document drew a textarea. Both draw one now and they differ, because the
//     test overrides "white-space" on the control and the reference does not. The
//     behaviour it asserts — that a textarea's preserved white space cannot be
//     overridden by an author — is why the file is named tentative; it is not in
//     HTML's rendering section yet and is not implemented here.
//
// The +19 that takes it to 4204 is §8.3.1, §9.5 and §17.2, and the three are
// worth keeping apart because two of them are much larger than they look from
// here. Counted in both directions over the six directories they were found in —
// normal-flow, floats, floats-clear, positioning, abspos and margin-padding-clear
// — nothing at all moved the wrong way in any of them:
//
//   - §9.5.2's closing sentence about a cleared box whose own margins collapse
//     took six, all in floats-clear.
//   - §9.5's "line boxes created next to the float are shortened" took twelve,
//     from a float met part-way along a line, with §10.3.7's static position
//     folded in because the first exposed the second. Fourteen tests moved,
//     across four of the six directories.
//   - §17.2's column group that generates its own columns took one.
//
// Following a <link rel=stylesheet> moved this number by *nothing*, and that
// result is the whole point of recording it, because the estimate it was
// undertaken on was that a sixth of the remaining failures were waiting on it.
//
// 162 of the 665 failures did link a stylesheet. 152 of them linked exactly one
// file, /fonts/ahem.css, whose entire content is an @font-face for Ahem — a face
// this harness handed the engine directly at the time, so loading it changed not
// one pixel. (It does not hand it over any more; the engine loads that sheet and
// that font itself. See the note below on @font-face.)
// Ten linked a sheet with rules in it, all ten the same file, and nine of those
// ten stopped failing. Counted by name over the whole suite and in both
// directions: 9 tests moved, every one of them from failing to the vacuous
// bucket, and none the other way. Clean passes did not move at all.
//
// The reporting effect and the layout effect are separate here as they always
// are, and this time the reporting effect is the enormous one and it is a *cost*
// rather than a gain. A linked stylesheet that cannot be loaded is now reported,
// which is what makes a document that lost its styles distinguishable from one
// that never had any — and 1224 of the pairs in this suite link ahem.css. Left
// alone, every one of them would be tainted by that report and this number would
// fall from 4192 to 3205, measured, for a suite whose pages were all rendering
// correctly. Measured again with the harness resolving the path the way the WPT
// server does, so the sheet really loaded: 3205 again, because the cascade then
// reports the @font-face it does not apply.
//
// @font-face itself then moved this number by *nothing*, and that is the whole
// point of recording it: the two adaptations above — the stripped link and the
// harness's hand-built Ahem — came out in the same change, and the suite landed
// on the same 4211 it was on before. Not the same total: the same *documents*.
// The per-test dump is byte-identical to the one before, all 5177 lines of it,
// so no test moved in either direction.
//
// That is the result the work was for, and on its own it would be consistent
// with having changed nothing at all, so the load-bearing measurement is the
// other one. With @font-face in place and the strip put back — so a document
// can no longer ask for Ahem and nothing hands it over either — clean passes
// fall from 4211 to 3383: 565 tests go from clean to failing and 263 from clean
// to vacuous, none the other way. Those 828 are what the document's own
// @font-face is now carrying, and what the two adaptations were carrying before
// it.
//
// The two effects stay separated. Removing the strip from the *old* engine, with
// no @font-face to answer the link, was measured at 4211 to 3222, and every one
// of those 989 moved from clean to vacuous with not one test changing from
// passing to failing: a pure reporting cost, which is exactly what the strip
// existed to avoid. What replaced it removes the cost by removing its cause.
const wptCleanPassBaseline = 4405

// linkRe finds the reference link that makes a document a reftest.
var linkRe = regexp.MustCompile(`(?i)<link\s+[^>]*rel\s*=\s*["']?(match|mismatch)["']?[^>]*>`)

// hrefRe pulls the href out of such a link.
var hrefRe = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)

// The metadata that marks a test as needing something automation cannot give it.
//
// It is read as a tag with two attributes rather than as one pattern, because a
// pattern has to fix their order and HTML does not. The expression this replaced
// demanded name before content, and 142 documents in the suite write them the
// other way round — 35 of them carrying a flag this harness means to honour, so
// 35 tests it had decided not to run were running and failing.
//
// That is the shape of harness bug worth being slow about: it cost nothing
// visible, because a test that should not run and fails looks exactly like a
// test that should run and fails.
var (
	metaTagRe   = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	flagsNameRe = regexp.MustCompile(`(?is)\bname\s*=\s*["']?flags["']?[\s/>]`)
	contentRe   = regexp.MustCompile(`(?is)\bcontent\s*=\s*["']([^"']*)["']`)
)

// flagsOf returns the flags a document declares, in either attribute order.
func flagsOf(src string) []string {
	for _, tag := range metaTagRe.FindAllString(src, -1) {
		if !flagsNameRe.MatchString(tag) {
			continue
		}
		if m := contentRe.FindStringSubmatch(tag); m != nil {
			return strings.Fields(m[1])
		}
	}
	return nil
}

func wptDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(wptEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make test-wpt`) to check layout against the Web Platform Tests", wptEnv)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s=%s: %v", wptEnv, dir, err)
	}
	return dir
}

// reftest is one pair.
type reftest struct {
	name string
	test string // path to the test document
	ref  string // path to the reference
	// mismatch reverses the assertion: the two must render *differently*.
	mismatch bool
}

// findReftests walks the suite for pairs.
func findReftests(t *testing.T, root string) []reftest {
	t.Helper()
	var out []reftest

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext != ".html" && ext != ".xht" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := string(data)

		link := linkRe.FindStringSubmatch(src)
		if link == nil {
			return nil
		}
		href := hrefRe.FindStringSubmatch(link[0])
		if href == nil {
			return nil
		}
		// A reference that lives outside the sparse checkout is not a failure,
		// it is a file that was not fetched.
		ref := filepath.Join(filepath.Dir(path), href[1])
		if _, err := os.Stat(ref); err != nil {
			return nil
		}

		// Tests needing something automation cannot give are not run: a person,
		// an animation, a script, a user stylesheet, a font this checkout has no
		// way to get.
		//
		// "ahem" used to be on that list and is not any more. Ahem is a test font
		// whose glyphs are all one-em squares, which is what makes a layout
		// assertion expressible at all — "font: 20px Ahem" with four characters is
		// an 80x20 block and a test can say so. The suite ships it, so the reason
		// for skipping was that the harness did not hand it to the engine, not
		// that it could not. It does now, and running those 962 tests found 325
		// that pass cleanly and 619 that fail. The failure count is the honest
		// cost of having stopped skipping them.
		//
		// "dom" was added after the collapsing-border work made twelve of them
		// fail. They change the page with a script and then assert the result;
		// this engine runs no scripts and is not going to, because a PDF page is
		// settled before it is written. There are 197 of them, contributing 175
		// failures and 22 vacuous passes and no clean passes at all — so skipping
		// them costs nothing that was evidence, and stops the failure count
		// carrying tests that cannot be fixed. Those twelve are the clearest
		// illustration in this file of what a vacuous pass is: they passed while
		// neither document drew a border, and drawing the borders made them fail.
		for _, f := range flagsOf(src) {
			switch strings.ToLower(f) {
			case "animated", "interact", "paged", "userstyle", "font", "dom":
				return nil
			}
		}

		name, _ := filepath.Rel(root, path)
		out = append(out, reftest{
			name: filepath.ToSlash(name), test: path, ref: ref,
			mismatch: strings.EqualFold(link[1], "mismatch"),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// cdataRe matches the CDATA wrapper an XHTML document puts around its CSS.
//
// The suite's older tests are .xht, which a browser parses as XML — and XML
// strips a CDATA section before the CSS is ever seen. This engine reads HTML,
// where <style> is raw text and the wrapper would be handed to the CSS parser
// as though it were a rule.
//
// Stripping it here rather than in the engine is deliberate: the engine reads
// HTML and should not learn XML syntax to suit a test suite. This is the harness
// adapting the input, and it is the sort of adjustment that has to be visible.
//
// It was found by the vacuous-pass check failing to do its job — see the note on
// clean passes below.
var cdataRe = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)

// emptyElementRe matches an XML empty-element tag: "<div/>", "<td class='x'/>".
//
// The attribute part is spelled out rather than written [^>]* so that a ">"
// inside a quoted attribute value does not end the match early.
var emptyElementRe = regexp.MustCompile(
	`<([a-zA-Z][a-zA-Z0-9]*)((?:[^>"']|"[^"]*"|'[^']*')*?)\s*/>`)

// xhtmlVoid is the set an empty-element tag says nothing extra about, because
// HTML has no end tag for them either.
var xhtmlVoid = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// expandEmptyElements rewrites XML empty-element tags into a start tag and an
// end tag, which is what they mean.
//
// The suite's .xht documents are XHTML and a browser parses them as XML, where
// "<div/>" is an empty div. This engine has an HTML parser, and HTML has no
// self-closing syntax outside the void elements — it reads the same tag as an
// open <div> and puts everything after it inside. Neither reading is a bug: they
// are two languages, and the file extension says which one the document is in.
//
// So the harness says it too, in the same place and for the same reason it
// unwraps CDATA. These are XHTML documents being handed to an HTML parser, and
// where the two languages disagree about what the markup *means* it is the
// harness's business to translate rather than the engine's to guess — the note
// on the tree-builder change above calls this the second place where reading
// XHTML as HTML shows, and this is it.
//
// Without it the numbers-units references, written "<div/><div/>", are read as
// one div inside another and draw a single square where the test draws two.
// Twelve tests across values, floats-clear, generated-content and positioning
// turn on it, and none goes the other way.
//
// Only .xht is rewritten. An .html document in the suite is HTML, and its
// "<div/>" means what HTML says it means.
func expandEmptyElements(src string) string {
	return emptyElementRe.ReplaceAllStringFunc(src, func(tag string) string {
		m := emptyElementRe.FindStringSubmatch(tag)
		if xhtmlVoid[strings.ToLower(m[1])] {
			return tag
		}
		return "<" + m[1] + m[2] + "></" + m[1] + ">"
	})
}

// pageClip is the area a rendering is compared over.
//
// It stands in for the viewport a browser would have shown the reftest in: a
// mark outside it is not part of the picture, exactly as content scrolled off
// the page is not. Absolutely positioned boxes land outside it routinely.
func pageClip() Rect {
	sz := wptViewport()
	return Rect{W: sz.W, H: sz.H}
}

// wptViewport is the viewport the suite was written against.
//
// The suite is run by browsers in a window 800 pixels wide, and a good number of
// its documents are laid out to that number — units-002 sets two 250px Ahem
// glyphs and an image on one line and needs 642 of them, which A4's content box
// does not have. Rendering the suite into an A4 page is standing in for a
// browser viewport with the wrong one, and a test that wrapped only because the
// page was narrow is not evidence about the engine.
//
// Measured over the whole suite the difference is one test either way — nothing
// regressed, units-002 gained — which says the suite is very largely
// width-insensitive and that this is a fidelity fix rather than a lever. It is
// made for the fidelity.
//
// The height stays A4's rather than becoming 600. The suite's 600 is a *window*
// height and its references are compared over the whole scrollable page, so the
// faithful stand-in for it is a page tall enough not to paginate, which is what
// A4's already is. Six hundred would cut documents in half and would be copying
// the number rather than the meaning.
func wptViewport() Size {
	return Size{W: picPx(800), H: A4.Content().H}
}

// renderForCompare lays a document out and returns its display list together
// with whether anything unsupported was reported.
//
// The ops are returned rather than a canonical string because the comparison
// resolves occlusion, and occlusion depends on paint order — see picture_test.go.
func renderForCompare(root, file string) (ops []Op, clean bool, err error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, false, err
	}
	src := cdataRe.ReplaceAllString(string(data), "$1")
	if ext := strings.ToLower(filepath.Ext(file)); ext == ".xht" || ext == ".xhtml" {
		src = expandEmptyElements(src)
	}

	// The suite's documents refer to real files beside them — most of the
	// references draw their expected picture with
	// "<img src=support/blue15x15.png width=5 height=96>" — so the harness
	// hands the engine a resolver. That is the caller opting in, which is the
	// only way an image is ever loaded: the engine's own default is to load
	// nothing, and nothing here changes it.
	//
	// The resolver is rooted at the *checkout* and resolves each reference
	// against the directory the document was read from, which is what a browser
	// does and is not what an earlier version of this harness did. Rooting at
	// the document's own directory looked like the stricter choice and was
	// measurably the wrong one: the suite keeps its shared references in
	// css/CSS2/reference/ and its images in css/CSS2/support/, so every one of
	// those references writes "../support/black96x96.png" and every one of them
	// was refused. 149 tests share ref-filled-black-96px-square.xht alone, and
	// each was failing because the *reference* drew six words of alt text that
	// the test document had no counterpart for. That is the fourth time this
	// file has recorded a large block of failures that were about the harness
	// rather than about the engine; see the note on the ratchet.
	//
	// Containment is still real and still enforced by os.Root: a reference that
	// leaves the checkout is refused, and the engine's own DirResolver policy —
	// no scheme, no absolute path, no escape — is unchanged. What the harness
	// adds is the document-relative join a browser performs, and it performs it
	// here rather than in the engine because the engine deliberately has no
	// notion of a base URL.
	res, err := newSuiteResolver(root, filepath.Dir(file))
	if err != nil {
		return nil, false, err
	}
	defer res.Close()

	built := Build(Input{HTML: src, Resources: res, Fonts: fontSetForWPT()})

	// The faces the document itself brought, which for a quarter of this suite
	// is Ahem: 1665 of its documents link /fonts/ahem.css, whose whole content
	// is an @font-face for it. The engine loads that through the same resolver
	// as everything else, so what is registered here is a face that arrived
	// from the document rather than one this harness handed over.
	//
	// The registration is undone before the next document, because a face is
	// per document — a thousand documents' Ahems left in a package-level map
	// would be a thousand parsed fonts nothing can free.
	defer unregisterBlockFonts(registerDocumentBlockFonts(built, res))

	rec := NewRecorder(nil)
	laid := Layout(built.Root, wptViewport(), built.Fonts, rec)

	clean = true
	for _, f := range built.Findings {
		if f.Unsupported() {
			clean = false
		}
	}
	for _, f := range rec.Findings() {
		if f.Unsupported() {
			clean = false
		}
	}

	ops = Paint(laid)
	// A document that paints nothing cannot be evidence of anything. Two blank
	// pages match, which is the purest form of the vacuous pass §7.1 warns
	// about, and no amount of finding-counting detects it.
	if normaliseOps(ops) == "" {
		clean = false
	}
	// A run set in a face whose glyphs are filled rectangles is those
	// rectangles, and has to reach the comparison as such — a quarter of this
	// suite draws its expected square with Ahem on one side and a background
	// colour on the other. See blockglyph_test.go for the rule and for what it
	// refuses.
	return blockFills(ops), clean, nil
}

// suiteResolver serves a suite document the files it refers to, from anywhere
// inside the checkout and from nowhere else.
//
// It is the harness's stand-in for a browser's base URL: the engine is handed
// the reference exactly as the document wrote it, and the engine has no idea
// which directory the document came from, so somebody has to join the two. A
// browser does it when it resolves the URL. Doing it here keeps the engine's
// resolver contract — a relative path with no scheme and no escape — exactly as
// it is for every other caller.
//
// The join is done on the *undecoded* reference and the result is cleaned, so a
// ".." that stays inside the checkout is resolved away and one that leaves it is
// refused here. A ".." that is per-cent-escaped survives the clean untouched and
// is then refused by the engine's own resourcePath after it decodes, which is
// the second of the two mechanisms and the reason this does not rest on either
// alone.
type suiteResolver struct {
	dir string // the document's directory, relative to the checkout, slash-separated
	res *DirResolver
}

func newSuiteResolver(root, dir string) (*suiteResolver, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("%s is not inside the checkout at %s", dir, root)
	}
	res, err := NewDirResolver(root)
	if err != nil {
		return nil, err
	}
	return &suiteResolver{dir: rel, res: res}, nil
}

func (s *suiteResolver) Close() error { return s.res.Close() }

func (s *suiteResolver) Resolve(ref string) ([]byte, error) {
	// The refusals that must happen before anything is joined, because joining
	// a directory onto "http://example/x" would turn a scheme into a plausible
	// relative path and lose the reason for the refusal.
	trimmed := strings.TrimSpace(ref)
	if scheme, ok := schemeOf(trimmed); ok {
		return nil, fmt.Errorf("render: %q names the %q scheme; this engine resolves no URLs", ref, scheme)
	}
	// A leading "/" in this suite is not a filesystem path, it is the root of
	// the server the suite is served from — which is the checkout. Resolving it
	// as such is the other half of the base-URL job a browser does, and it is
	// what the 1665 documents linking "/fonts/ahem.css" are asking for.
	//
	// This is the harness standing in for the server and is not a loosening of
	// anything: the engine's own DirResolver still refuses an absolute path
	// outright, and everything below still goes through it rooted at the
	// checkout. What arrives at the engine is a relative path inside it.
	base := s.dir
	if strings.HasPrefix(trimmed, "/") {
		base, trimmed = ".", strings.TrimPrefix(trimmed, "/")
		if trimmed == "" {
			return nil, fmt.Errorf("render: %q names the server root, which is not a file", ref)
		}
	}
	if strings.HasPrefix(trimmed, `\`) {
		return nil, fmt.Errorf("render: %q is an absolute path", ref)
	}
	// A reference that climbs out of the checkout is refused, and it is refused
	// by the engine's own resolver rather than here. path.Clean resolves away
	// every ".." that stays inside and can only leave one at the *front*, so a
	// joined path that escapes is exactly a joined path beginning with "..",
	// which is what resourcePath already will not take.
	//
	// A second check here was written first and then deleted: planting the
	// escape with it removed changed nothing, because it could never be the
	// rule that decided. A guard that cannot decide anything reads as defence
	// and is decoration, and this file has enough of that history already.
	return s.res.Resolve(path.Clean(path.Join(base, filepath.ToSlash(trimmed))))
}

// registerDocumentBlockFonts gives the rectangle-glyph comparison the faces the
// document loaded for itself, and returns them so they can be taken out again.
//
// Without it, Ahem arriving from a document rather than from the harness would
// be compared as text on one side of a reftest and as a fill on the other, which
// is the failure blockglyph_test.go exists to prevent and which no single test
// names — it is worth about 345 clean passes.
//
// The rectangle table is cached by the reference that produced it, because it is
// a function of the font *file* and the file is the same one for every document
// that links ahem.css. What is not shared is the face: each document parses its
// own, which is what a face records glyphs for.
func registerDocumentBlockFonts(built Built, res ResourceResolver) []*fonts.Face {
	var set *documentFonts
	switch f := built.Fonts.(type) {
	case *documentFonts:
		set = f
	case fallbackDocumentFonts:
		set = f.documentFonts
	default:
		return nil
	}
	var added []*fonts.Face
	for _, df := range set.faces {
		if _, have := blockFonts[df.face]; have {
			continue
		}
		bf, ok := suiteBlockFonts[df.ref]
		if !ok {
			data, err := res.Resolve(df.ref)
			if err == nil {
				bf, _ = newBlockFont(data)
			}
			suiteBlockFonts[df.ref] = bf
		}
		if bf == nil {
			continue
		}
		blockFonts[df.face] = bf
		added = append(added, df.face)
	}
	return added
}

func unregisterBlockFonts(faces []*fonts.Face) {
	for _, f := range faces {
		delete(blockFonts, f)
	}
}

// suiteBlockFonts caches a rectangle table per font file, so that the outlines
// of Ahem are read once rather than once per document that links it. A nil
// entry is a file already found to have no rectangles, which is as worth
// remembering as one that has.
var suiteBlockFonts = map[string]*blockFont{}

// normaliseOps renders a display list as comparable text.
//
// This is no longer what decides whether two renderings agree — pictureEqual is,
// because it can see that one mark covers another and this cannot. What is left
// for it is the blank-page check above, where only the presence of marks matters
// and their order does not.
//
// It is worth recording why the sorted form was wrong as a comparison, since the
// reasoning that justified it sounded solid: sorting loses the ability to detect
// a wrong painting order, and painting order only matters where marks overlap,
// so a test that depended on it would be testing z-ordering rather than layout.
// The last step is the false one. Nearly every CSS 2.1 test paints a red box and
// covers it with a green one, and you pass by showing no red — which is an
// overlap, and which no comparison of unordered marks can ever satisfy, because
// the test has a red rectangle in it and the reference does not.
func normaliseOps(ops []Op) string {
	lines := make([]string, 0, len(ops))
	for _, op := range ops {
		switch v := op.(type) {
		case FillRect:
			if v.Rect.Empty() || v.Color.A == 0 {
				// A mark that paints nothing is not a difference. Two documents
				// may reach an empty box by different routes.
				continue
			}
			lines = append(lines, fmt.Sprintf("fill %s %s", rectKey(v.Rect), v.Color))
		case DrawText:
			if strings.TrimSpace(v.Text) == "" {
				// A space marks no paper. It is drawn for the sake of text
				// extraction, and two documents may legitimately have different
				// numbers of them between the same visible glyphs.
				continue
			}
			lines = append(lines, fmt.Sprintf("text %q at %s,%s size %s",
				v.Text, num(v.At.X), num(v.At.Y), num(v.Size)))
		case DrawImage:
			if v.Rect.Empty() {
				continue
			}
			// A picture of one colour, drawn over a rectangle, puts exactly the
			// same ink on exactly the same paper as a fill of that colour over
			// that rectangle. Saying so here is not a concession to make tests
			// pass — it is what makes this comparison a faithful proxy for the
			// thing a reftest actually asserts, which is that the two documents
			// *render* identically.
			//
			// It matters because of how the suite is written. Its references
			// draw their expected picture with a solid PNG —
			// "<img src=black96x96.png width=96 height=96>" — while the test
			// draws the same square with a background colour or a border. A
			// comparison that could not equate the two would rule that every
			// one of those pairs differs, which is a statement about the
			// comparison and not about the engine.
			//
			// The check is a real one and not an assumption: every pixel is
			// read, and an image with two colours in it, or any transparency at
			// all, is compared as an image. TestWPTOracleHasTeeth plants both.
			if c, ok := uniformColor(v.Image); ok {
				lines = append(lines, fmt.Sprintf("fill %s %s", rectKey(v.Rect), c))
				continue
			}
			// Otherwise the source rather than the pixels: two documents
			// drawing the same file draw the same key, and comparing decoded
			// images pixel by pixel would make this a rasterizer.
			lines = append(lines, fmt.Sprintf("image %s %s", v.Key, rectKey(v.Rect)))
		case TileImage:
			// Only the blank-page check reads this, so a tiling contributes one
			// line however many tiles it puts down: what matters here is that
			// something was painted at all.
			for _, f := range tiledFills(v) {
				if f.img != "" {
					lines = append(lines, fmt.Sprintf("image %s %s", f.img, rectKey(f.r)))
					continue
				}
				lines = append(lines, fmt.Sprintf("fill %s %s", rectKey(f.r), f.c))
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// uniformColours memoizes the scan below, which is over every pixel of every
// image in four thousand documents rendered twice.
var uniformColours = map[image.Image]struct {
	colour style.RGBA
	ok     bool
}{}

// uniformColor reports whether every pixel of an image is the same opaque
// colour, and what that colour is.
//
// Opaque is required rather than merely uniform: a picture that is half
// transparent black does not put the same ink on the page as a fill of black,
// and treating the two as equal would hide exactly the kind of difference this
// comparison exists to find.
func uniformColor(img image.Image) (style.RGBA, bool) {
	if img == nil {
		return style.RGBA{}, false
	}
	if got, ok := uniformColours[img]; ok {
		return got.colour, got.ok
	}
	colour, ok := scanUniform(img)
	uniformColours[img] = struct {
		colour style.RGBA
		ok     bool
	}{colour, ok}
	return colour, ok
}

func scanUniform(img image.Image) (style.RGBA, bool) {
	b := img.Bounds()
	if b.Empty() {
		return style.RGBA{}, false
	}
	r0, g0, b0, a0 := img.At(b.Min.X, b.Min.Y).RGBA()
	if a0 != 0xFFFF {
		return style.RGBA{}, false
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if r != r0 || g != g0 || bl != b0 || a != a0 {
				return style.RGBA{}, false
			}
		}
	}
	// The same 0-255 scale style.ParseColor produces, so a fill written by a
	// stylesheet and a fill derived from a pixel are the same string.
	return style.RGBA{
		R: float64(r0 >> 8), G: float64(g0 >> 8), B: float64(b0 >> 8), A: 1,
	}, true
}

func rectKey(r Rect) string {
	return num(r.X) + "," + num(r.Y) + " " + num(r.W) + "x" + num(r.H)
}

// num renders a length to a hundredth of a pixel.
//
// Layout units are exact, so two identical renderings agree to the unit — but
// the two documents of a reftest arrive at their geometry by different
// arithmetic, and a rounding of a third of a pixel in one and not the other is
// not a rendering difference. A hundredth is far below anything visible and far
// above the noise.
//
// The last clause is not true and it is worth writing down where it is false
// rather than leaving it to be rediscovered. A layout unit is a 64th of a pixel,
// which is 0.0156 — *coarser* than the hundredth this rounds to — so two
// documents can differ by an amount the engine cannot represent and still be
// told apart here. It happens where one document sets a run of spaces as one run
// and the other sets them as several: each advance is rounded to the unit as it
// is measured, so three separate spaces of 19.2px come to 57.609375 and one run
// of three comes to 57.59375, one unit apart.
//
// It is measurable rather than theoretical. The twelve
// white-space/ws-break-spaces-applies-to tests fail on exactly this and on
// nothing else: every fill matches, every visible glyph matches, and one "8" is
// a 64th of a pixel to the right of the other because break-spaces makes each
// space a run of its own. Fixing it means comparing text positions with a
// tolerance rather than by a rounded string, and choosing that tolerance is a
// decision about the oracle — the error accumulates with the number of runs, so
// no fixed epsilon is principled — which is why it was reported rather than
// taken.
func num(u style.Unit) string {
	return strconv.FormatFloat(float64(int(u.Px()*100+0.5))/100, 'f', -1, 64)
}

func TestWPTReftests(t *testing.T) {
	root := wptDir(t)
	tests := findReftests(t, root)
	if len(tests) == 0 {
		t.Fatalf("no reftests found under %s; is the sparse checkout set?", root)
	}

	var cleanPass, vacuousPass, fail, broke int
	var failed []string

	for _, rt := range tests {
		got, gotClean, err := renderForCompare(root, rt.test)
		if err != nil {
			broke++
			continue
		}
		want, wantClean, err := renderForCompare(root, rt.ref)
		if err != nil {
			broke++
			continue
		}

		same := pictureEqual(got, want, pageClip())
		passed := same != rt.mismatch

		switch {
		case !passed:
			fail++
			if len(failed) < 20 {
				failed = append(failed, rt.name)
			}
		case gotClean && wantClean:
			cleanPass++
		default:
			// A pass where something was unsupported in either document. It may
			// be real and it may be two blank pages agreeing, and nothing here
			// can tell which — so it does not count.
			vacuousPass++
		}
	}

	t.Logf("%d reftests: %d passed cleanly, %d passed with something unsupported, "+
		"%d failed, %d could not be read",
		len(tests), cleanPass, vacuousPass, fail, broke)
	if len(failed) > 0 {
		t.Logf("first failures: %s", strings.Join(failed, ", "))
	}

	if cleanPass < wptCleanPassBaseline {
		t.Errorf("%d reftests pass cleanly, below the baseline of %d — this is a "+
			"layout regression, and the baseline is not to be lowered to make it green",
			cleanPass, wptCleanPassBaseline)
	}
	if cleanPass > wptCleanPassBaseline {
		t.Logf("the clean-pass baseline can be raised from %d to %d",
			wptCleanPassBaseline, cleanPass)
	}
}

// TestWPTOracleHasTeeth is the check on the check, on the model of
// TestArlingtonOracleHasTeeth and TestCSSOracleHasTeeth.
//
// An oracle whose comparison accepts everything is worse than no oracle, because
// it reads as coverage. This plants the differences a layout fault produces — a
// box in the wrong place, of the wrong size, of the wrong colour, and text at
// the wrong position — and requires the comparison to see each.
func TestWPTOracleHasTeeth(t *testing.T) {
	red := style.RGBA{R: 255, A: 1}
	blue := style.RGBA{B: 255, A: 1}
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }

	base := []Op{FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red}}

	cases := []struct {
		name  string
		ops   []Op
		equal bool
	}{
		{"identical", []Op{FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red}}, true},
		{"moved", []Op{FillRect{Rect: Rect{u(11), u(20), u(100), u(50)}, Color: red}}, false},
		{"resized", []Op{FillRect{Rect: Rect{u(10), u(20), u(101), u(50)}, Color: red}}, false},
		{"recoloured", []Op{FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: blue}}, false},
		{"missing", nil, false},
		// Painting the same opaque rectangle twice produces the same page. The
		// marks-based comparison called this a difference, which was over-strict
		// in the direction that costs real passes: a reference is free to reach
		// its picture with a different number of marks than the test.
		{"doubled", []Op{
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
		}, true},
		// Covering the mark completely with another colour is a different page,
		// and is the case the whole suite turns on.
		{"covered", []Op{
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: blue},
		}, false},
	}
	clip := pageClip()
	for _, tc := range cases {
		if got := pictureEqual(tc.ops, base, clip); got != tc.equal {
			t.Errorf("%s: the comparison said equal=%v, want %v", tc.name, got, tc.equal)
		}
	}

	// A difference far below a pixel is not a rendering difference, and treating
	// it as one would fail every test on arithmetic noise.
	near := []Op{FillRect{Rect: Rect{u(10.0001), u(20), u(100), u(50)}, Color: red}}
	if !pictureEqual(near, base, clip) {
		t.Error("a difference of a ten-thousandth of a pixel was treated as a difference")
	}

	// The uniform-image equivalence, which is the one place this comparison
	// says two different ops are the same mark. It has to be exactly as strict
	// as that claim: a solid opaque picture is a fill of its colour, and
	// anything else is not.
	fill4x4 := func(f func(x, y int) color.NRGBA) image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				img.SetNRGBA(x, y, f(x, y))
			}
		}
		return img
	}
	solid := fill4x4(func(int, int) color.NRGBA { return color.NRGBA{R: 255, A: 255} })
	speckled := fill4x4(func(x, y int) color.NRGBA {
		if x == 3 && y == 3 {
			// One pixel of a different colour, which is a picture and not a
			// rectangle however uniform the rest of it is.
			return color.NRGBA{G: 255, A: 255}
		}
		return color.NRGBA{R: 255, A: 255}
	})
	// Half-transparent full red, which *premultiplies* to exactly the opaque
	// dark red below. That is the whole point of this one: a test using a
	// translucent colour that premultiplied to something else would be caught
	// by the colour comparison and would pass with the opacity check deleted,
	// which was found by planting exactly that.
	translucent := fill4x4(func(int, int) color.NRGBA { return color.NRGBA{R: 255, A: 128} })
	darkRed := style.RGBA{R: 128, A: 1}

	rect := Rect{u(10), u(20), u(100), u(50)}
	if !pictureEqual([]Op{DrawImage{Rect: rect, Image: solid, Key: "k"}}, base, clip) {
		t.Error("a solid red image over the same rectangle did not compare equal to a red fill")
	}
	if pictureEqual([]Op{DrawImage{Rect: rect, Image: speckled, Key: "k"}}, base, clip) {
		t.Error("an image with a pixel of another colour compared equal to a plain fill")
	}
	dark := []Op{FillRect{Rect: rect, Color: darkRed}}
	if pictureEqual([]Op{DrawImage{Rect: rect, Image: translucent, Key: "k"}}, dark, clip) {
		t.Error("a half-transparent image compared equal to the opaque fill it premultiplies to")
	}
	// Two different pictures at the same place are still different. The images
	// here are diagonals, which is deliberate: a diagonal changes along both
	// axes at once, so it is not a set of uniform rectangles and bandsOf refuses
	// it — which is what leaves the key as the thing being compared, and is the
	// path this case is for. A pair of *patterned* images is covered next door
	// in TestImageBandsAreExact, where the answer is different and is meant to
	// be.
	diagonal := func(c color.NRGBA) image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				if x == y {
					img.SetNRGBA(x, y, c)
					continue
				}
				img.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
		return img
	}
	if bandsOf(diagonal(color.NRGBA{R: 255, A: 255})) != nil {
		t.Error("a diagonal was decomposed into uniform rectangles; it is not made of them")
	}
	if pictureEqual(
		[]Op{DrawImage{Rect: rect, Image: diagonal(color.NRGBA{R: 255, A: 255}), Key: "one"}},
		[]Op{DrawImage{Rect: rect, Image: diagonal(color.NRGBA{B: 255, A: 255}), Key: "two"}}, clip) {
		t.Error("two different image sources compared equal")
	}

	// Marks that do not overlap may be emitted in either order, since the two
	// documents of a reftest paint from different structures.
	a := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(1), u(1)}, Color: red},
		FillRect{Rect: Rect{u(5), u(5), u(1), u(1)}, Color: blue},
	}
	b := []Op{a[1], a[0]}
	if !pictureEqual(a, b, clip) {
		t.Error("the same marks in a different order compared unequal")
	}
}

// TestSuiteResolverReachesTheSupportDirectory pins what the harness's resolver
// is for and what it still refuses.
//
// It is a test about the harness rather than about the engine, and it earns its
// place because the thing it fixes was invisible for a long time: the suite
// keeps its shared references in one directory and its images in another, so
// every one of those references writes "../support/x.png". A resolver rooted at
// the document's own directory refused all of them, the reference drew its alt
// text instead of its picture, and 149 tests failed on six words the test
// document had no counterpart for.
//
// The refusals are the other half and are the reason this is not simply "open
// anything": a scheme is still a URL, an absolute path is still the filesystem,
// and a reference that climbs out of the checkout is still refused — by
// os.Root, which resolves each component at the system-call level, and by the
// join here, which is checked so that the containment does not rest on one
// mechanism.
func TestSuiteResolverReachesTheSupportDirectory(t *testing.T) {
	root := t.TempDir()
	mkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("suite/reference")
	mkdir("suite/support")
	write("suite/support/black.png", "pretend png")
	write("suite/reference/beside.txt", "beside")
	// Outside the checkout entirely, which is what the containment is about.
	write("secret.txt", "not for the suite")

	// The two files below are what make the scheme and absolute-path refusals
	// *decidable*. Joining a directory onto a reference turns "/etc/passwd"
	// into "reference/etc/passwd" and "http://host/x" into
	// "reference/http:/host/x" — both perfectly good relative paths — so
	// without a file at the other end the refusal cannot be told apart from a
	// missing file, and a test that plants the bug watches it pass.
	//
	// That is not a hypothetical: both checks were planted and neither moved
	// this test until these existed.
	mkdir("suite/reference/etc")
	write("suite/reference/etc/passwd", "reachable by a mangled absolute path")
	mkdir("suite/reference/http:/example.invalid")
	write("suite/reference/http:/example.invalid/x", "reachable by a mangled URL")

	res, err := newSuiteResolver(filepath.Join(root, "suite"),
		filepath.Join(root, "suite", "reference"))
	if err != nil {
		t.Fatalf("rooting the resolver: %v", err)
	}
	defer res.Close()

	if got, err := res.Resolve("../support/black.png"); err != nil {
		t.Errorf("a reference to the suite's support directory was refused: %v", err)
	} else if string(got) != "pretend png" {
		t.Errorf("read %q", got)
	}
	if _, err := res.Resolve("beside.txt"); err != nil {
		t.Errorf("a reference beside the document was refused: %v", err)
	}

	for _, ref := range []string{
		"../../secret.txt",            // out of the checkout
		"../../../../../etc/passwd",   // and well out of it
		"/etc/passwd",                 // absolute
		"http://example.invalid/x",    // a URL
		"file:///etc/passwd",          // a URL by another spelling
		"..%2f..%2fsecret.txt",        // escaped, so the join cannot see it
		"../support/../../secret.txt", // climbing after descending
	} {
		if _, err := res.Resolve(ref); err == nil {
			t.Errorf("%q was resolved; it must be refused", ref)
		}
	}
}

// TestWPTFindsRealPairs pins that the walker recognises reftests, so a run
// reporting zero failures because it found zero tests fails here instead.
func TestWPTFindsRealPairs(t *testing.T) {
	root := wptDir(t)
	tests := findReftests(t, root)
	if len(tests) < 50 {
		t.Errorf("found %d reftests; the sparse checkout should hold hundreds", len(tests))
	}
	for _, rt := range tests[:min(5, len(tests))] {
		if rt.test == "" || rt.ref == "" {
			t.Errorf("%s has an empty side", rt.name)
		}
		if rt.test == rt.ref {
			t.Errorf("%s compares a document with itself", rt.name)
		}
	}
}

// TestSuiteFontFaceLoadsAhem is what replaced the harness's Ahem shortcut, and
// it asserts the whole of the path that replaced it.
//
// Until this branch the harness handed the engine Ahem by filename and stripped
// the `<link rel="stylesheet" href="/fonts/ahem.css">` out of every document
// that asked for it, because the engine had no @font-face and the report would
// have tainted 1665 documents whose pages were rendering correctly. Both are
// gone. What has to be true instead is a chain of five things, and the reason
// this test walks all five rather than asserting the metric at the end is that
// four of them can fail while the fifth still looks right — a fallback face with
// the wrong metrics still measures *something*.
//
// So: the suite's own document is read, unmodified; the link is followed; the
// sheet's @font-face is loaded through the same resolver; the face that arrives
// is the real Ahem, which is checked by the metric the font exists for — every
// glyph exactly one em wide; and the rectangle table is found for it, which is
// what a quarter of this suite's comparisons depend on.
func TestSuiteFontFaceLoadsAhem(t *testing.T) {
	root := wptDir(t)

	// A real document from the suite rather than a hand-written one, because
	// the claim is about what the suite's documents do.
	doc := filepath.Join(root, "css", "CSS2", "text", "white-space-processing-001.xht")
	data, err := os.ReadFile(doc)
	if err != nil {
		t.Skipf("no such document in this checkout: %v", err)
	}
	src := cdataRe.ReplaceAllString(string(data), "$1")
	if !strings.Contains(src, "/fonts/ahem.css") {
		t.Skipf("%s no longer links the Ahem stylesheet", doc)
	}

	res, err := newSuiteResolver(root, filepath.Dir(doc))
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	defer res.Close()

	built := Build(Input{HTML: src, Resources: res, Fonts: fontSetForWPT()})

	face, ok := built.Fonts.Face("Ahem", false, false)
	if !ok || face == nil {
		t.Fatalf("the document links /fonts/ahem.css and the engine has no Ahem; findings: %v",
			built.Findings)
	}
	if _, standard := fontSetForWPT().Face("Ahem", false, false); standard {
		t.Fatal("the harness font set answers for Ahem, so this test cannot tell " +
			"a face loaded from the document from one handed over")
	}

	// The metric the font exists for: every glyph is one em, so four characters
	// at 20px are exactly 80px. A fallback face is not.
	if got := face.Measure("XXXX", 20); got < 79.99 || got > 80.01 {
		t.Errorf("four characters of 20px Ahem measure %v, want 80 — the face that "+
			"arrived is not Ahem", got)
	}

	// And it reaches the rectangle-glyph comparison, which is where about 345
	// of this suite's clean passes live.
	added := registerDocumentBlockFonts(built, res)
	defer unregisterBlockFonts(added)
	if blockFonts[face] == nil {
		t.Error("the face the document loaded has no rectangle table, so every Ahem " +
			"run in the suite would be compared as text against a fill")
	}

	// Nothing about loading it is reported as unsupported. That is the half the
	// stripped link was hiding, and it is what lets those documents count as
	// clean passes.
	for _, f := range built.Findings {
		if f.Unsupported() && strings.Contains(strings.ToLower(f.Message), "font") {
			t.Errorf("loading the document's own font reported %s: %s", f.Rule, f.Message)
		}
	}
}

// TestSuiteResolverServesTheServerRoot pins the other half of the base-URL job
// this harness does for the engine.
//
// The suite is served over HTTP and its documents write "/fonts/ahem.css" for
// the root of that server, which is the checkout. Resolving it is the harness's
// business and not the engine's — the engine still refuses an absolute path
// outright, which the second half of this checks.
func TestSuiteResolverServesTheServerRoot(t *testing.T) {
	root := wptDir(t)
	res, err := newSuiteResolver(root, filepath.Join(root, "css", "CSS2", "text"))
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	defer res.Close()

	if _, err := res.Resolve("/fonts/ahem.css"); err != nil {
		t.Errorf("the harness did not serve the server root: %v", err)
	}
	// And it is still contained. "/" is the checkout, not the filesystem.
	if _, err := res.Resolve("/../../../etc/passwd"); err == nil {
		t.Error("a server-root reference climbed out of the checkout")
	}
	if _, err := res.Resolve("/"); err == nil {
		t.Error("the server root itself was served as a file")
	}

	// The engine's own resolver is unchanged: an absolute path is refused.
	plain, err := NewDirResolver(root)
	if err != nil {
		t.Fatalf("DirResolver: %v", err)
	}
	defer plain.Close()
	if _, err := plain.Resolve("/fonts/ahem.css"); err == nil {
		t.Error("DirResolver took an absolute path; the harness's join must not " +
			"have loosened the engine's policy")
	}
}
