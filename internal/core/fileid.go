package core

import (
	"crypto/rand"

	"github.com/mgilbir/pdf0/object"
)

// RandomFileID makes one half of the two-part file identifier of ISO 32000-2
// 14.4.
//
// Random rather than derived from the moment of creation. The identifier's job
// is to distinguish this file from every other, including one made a
// microsecond later by the same program, and a clock cannot promise that —
// two documents built inside one tick would share an identifier, and on a
// platform with a coarse clock that is not a remote possibility.
//
// It lives here because both document builders need it and neither can import
// the other. The PDF/A builder used to hash time.Now() with MD5 instead, which
// was a second answer to a question that already had one.
//
// Since Go 1.24 the system random source cannot fail without the runtime
// ending, so there is no error to handle.
func RandomFileID() object.String {
	var b [16]byte
	rand.Read(b[:])
	return object.String{Value: b[:], IsHex: true}
}
