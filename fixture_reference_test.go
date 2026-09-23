package pdf0

import "github.com/mgilbir/pdf0/object"

// reference makes objects part of the document. A validator judges what the
// document reaches from its trailer (core.View.ReachableDicts), so an object
// dropped into the table with nothing pointing at it is an orphan, which is
// deliberately not judged (audit 2026-09-22 C83). The references hang from a
// trailer entry no rule reads, which keeps a fixture about the rule it tests;
// validators_reachable_test.go builds real structure to test reachability
// itself.
func reference(d *Document, nums ...int) {
	arr, _ := d.Trailer.Get("PDF0TestFixture").(object.Array)
	for _, n := range nums {
		arr = append(arr, object.IndirectRef{Number: n})
	}
	d.Trailer.Set("PDF0TestFixture", arr)
}
