//go:build dictcopycheck

package object

// noCopy, under the dictcopycheck tag, has the Lock and Unlock methods that go
// vet's copylocks analyzer looks for (the same device as sync's own noCopy),
// so every copy of a Dictionary — and of a Stream or any other struct holding
// one by value — is reported. scripts/check-dict-copies.sh runs that analysis.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
