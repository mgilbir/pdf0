//go:build !dictcopycheck

package object

// noCopy is the zero-sized marker Dictionary carries. In a normal build it is
// an empty struct and costs nothing. Under the dictcopycheck build tag it is
// the version in nocopy_check.go, which go vet's copylocks analyzer recognises;
// see the Dictionary copy rules in the package documentation.
type noCopy struct{}
