module github.com/mgilbir/pdf0

go 1.26

require github.com/mgilbir/formalis v0.3.1

require github.com/mgilbir/gopenjpeg v0.1.1

require github.com/mgilbir/golittlecms v0.0.0-20260727161601-f6af7cfe1556

require github.com/mgilbir/forme v0.3.0

require golang.org/x/text v0.40.0

// v0.3.0 was published from a commit that no longer exists. Its tree was
// correct — the module zip a consumer downloads never contained anything it
// should not — but the history behind it carried 2 MB of generated PDFs
// committed by accident, and removing them rewrote every commit from that
// point, including the one the tag named.
//
// The proxy keeps what it has cached, so a version cannot be withdrawn by
// deleting its tag. Retraction is what withdrawal amounts to: the version is
// excluded from selection, so `go get @latest` and every upgrade skip it, and
// `go list -m -u` reports it as retracted with this text as the reason. An
// explicit `go get @v0.3.0` still resolves — Go honours a pin it is asked for —
// so this is a strong recommendation, not a block.
retract v0.3.0

// v0.2.0 and v0.3.1 shipped three compiled Linux executables — text,
// simple_pdf and genuse — committed to the repository root by accident. In
// v0.3.1 they are 19,479,407 of 23,924,465 bytes: 81% of the module was ELF
// for one architecture, and none of it was reachable Go code.
//
// They are out of the history now, which moved every commit from 3 August
// onward and left both tags naming commits that no longer exist. v0.3.2 is the
// same code without them, and the module is about 4.4 MB instead of 24.
retract [v0.2.0, v0.3.1]
