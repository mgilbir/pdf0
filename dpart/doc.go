// Package dpart is the document part (DPart) hierarchy of ISO 32000-2 clause
// 14.12: the findings its validator reports. A DPart tree partitions a
// document's pages into a hierarchy of parts, each of which may carry document
// part metadata; it is how a PDF/VT file marks its record boundaries. Reach
// for it to check that hierarchy on its own; pdf0.ValidatePDFVT already
// includes it.
//
// The entry point takes a *pdf0.Document, so it lives in the root package:
// pdf0.ValidateDParts (and pdf0.ValidateDPartsContext) checks a document's
// hierarchy and returns Violation values. The hierarchy is optional, so a
// document without one (no /DPartRoot in its catalog) has no findings.
//
//	for _, v := range pdf0.ValidateDParts(doc) {
//		fmt.Println(v)
//	}
//
// A finding's Rule is the ISO 32000-2 subclause it departs from, such as
// "14.12.2". The checks follow Tables 408 and 409 and the connectivity rules
// of 14.12.2 and 14.12.3: the root wiring, each node's parent link and role,
// leaf page ranges that cover every page exactly once in page-tree order, and
// the pages' links back to their parts. An empty result means none of the
// implemented checks fired. A finding under the reserved rule "limit" or
// "internal" says pdf0 could not finish a check, not that the hierarchy is
// wrong; pdf0.IsCheckerFinding tells the two apart.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/validators.md compares the
// validators and says what a result is worth.
package dpart
