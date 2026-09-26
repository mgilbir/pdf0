// Package lint holds repository-wide static checks that go vet does not
// make, run as ordinary tests so CI enforces them. It has no API: every check
// is a test in this package, and each one type-checks the module's own source
// with the standard library alone (go/types over the compiler's export data,
// located with `go list -export`), so a check can ask what type an expression
// has rather than guess from its spelling.
//
// A check here earns its place by catching a class of bug that has already
// happened and that review keeps missing. Each test's comment names the
// incident.
package lint
