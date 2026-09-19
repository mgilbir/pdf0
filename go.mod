module github.com/mgilbir/pdf0

go 1.26

require github.com/mgilbir/formalis v0.3.1

require github.com/mgilbir/gopenjpeg v0.1.1

require github.com/mgilbir/golittlecms v0.0.0-20260727161601-f6af7cfe1556

require github.com/mgilbir/forme v0.3.0

// v0.3.0 was published from a commit that no longer exists. Its tree was
// correct — the module zip a consumer downloads never contained anything it
// should not — but the history behind it carried 2 MB of generated PDFs
// committed by accident, and removing them rewrote every commit from that
// point, including the one the tag named.
//
// The proxy keeps what it has cached, so the version cannot be withdrawn by
// deleting the tag. This is what withdrawal amounts to: the version is excluded
// from selection, so `go get @latest` and every upgrade skip it, and `go list
// -m -u` reports it as retracted with this text as the reason. An explicit
// `go get @v0.3.0` still resolves — Go honours a pin it is asked for — so this
// is a strong recommendation, not a block.
//
// v0.3.1 is v0.3.0's tree, from a history that is clean.
retract v0.3.0
