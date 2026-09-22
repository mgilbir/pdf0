package crypt

import (
	"errors"
	"fmt"
	"math"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Every value the handler reads from the /Encrypt dictionary passes through
// this file before it is used. The dictionary is attacker-controlled input:
// /Length once sliced a 16-byte MD5 digest (audit 2026-09-22 C59), /P was
// silently wrapped to 32 bits, an unknown /CFM made half the document
// "Identity", and a V4 file with no top-level /Length got a 40-bit key. The
// answer to each is the same, and it is given here once: a value the standard
// security handler of ISO 32000-2 7.6 does not define yields a *reason*, the
// document stays Locked with that reason recorded, and nothing downstream ever
// sees an unvalidated number. Read never fails because of /Encrypt.
//
// Tolerances are deliberate and few: /O, /U, /OE, /UE and /Perms may be longer
// than the spec's fixed lengths (Acrobat writes 127-byte /O and /U at R6) and
// only their defined prefix is used; a V4 crypt filter's /Length is accepted in
// bytes (as ISO 32000-2 Table 25 specifies for the standard handler) or in bits
// (as most producers write it), since the two ranges do not overlap; an indirect
// value is resolved rather than refused. None of these can change what a
// password unlocks: a wrong key fails the /U check, and the document is Locked.

// The reasons a document is left Locked. Open's error wraps exactly one.
var (
	// ErrWrongPassword: the supplied password (empty for Read) is neither the
	// user nor the owner password.
	ErrWrongPassword = errors.New("the password is neither the user nor the owner password")
	// ErrUnsupported: the file uses a security handler, revision or crypt
	// filter method the standard security handler here does not implement.
	ErrUnsupported = errors.New("unsupported encryption")
	// ErrMalformed: the /Encrypt dictionary violates ISO 32000-2 7.6.
	ErrMalformed = errors.New("malformed /Encrypt dictionary")
)

// ErrPermsMismatch is a warning, not a lock: the /Perms entry decrypts and
// carries the "adb" marker, so the file key is proven, but the permissions or
// the EncryptMetadata flag inside it differ from /P or /EncryptMetadata
// (ISO 32000-2 Algorithm 13: they "should match"). The document decrypts; the
// dictionary's /P is not authenticated. pdf0 enforces no permissions, so the
// consequence is limited to consumers that read /P.
var ErrPermsMismatch = errors.New("/Perms does not match the /Encrypt dictionary")

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, a...))
}

func unsupported(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, a...))
}

// params is the validated content of an /Encrypt dictionary.
type params struct {
	v, r            int
	keyLen          int    // bytes: 5–16 for R2–R4, 32 for R6
	o, u            []byte // 32 bytes (R2–R4) or 48 (R6)
	oe, ue, perms   []byte // R6 only: 32, 32, 16 bytes
	p               int32
	encryptMetadata bool
	id              []byte // first /ID element; empty when absent (R2–R4 only use it)

	stm, str, eff method                 // resolved StmF, StrF, EFF
	effErr        error                  // why EFF cannot be applied (eff is then invalid)
	filters       map[object.Name]method // every /CF entry, for per-stream /Crypt filters

	encNum int          // object number of the /Encrypt dictionary, or -1 if direct
	exempt map[int]bool // objects holding /Encrypt's own values (never encrypted)
}

// parseParams validates the trailer's /Encrypt dictionary.
func parseParams(doc core.View, encObj object.Object) (*params, error) {
	p := &params{encNum: -1, encryptMetadata: true}
	if ref, ok := encObj.(object.IndirectRef); ok {
		p.encNum = ref.Number
	}
	enc := doc.ResolveDict(encObj)
	if enc == nil {
		return nil, malformed("/Encrypt does not resolve to a dictionary")
	}
	// The spec requires the dictionary's values to be direct; an indirect one
	// is tolerated, and its object is exempt from the decrypt and encrypt walks
	// (see encryptValueObjects).
	resolve := doc.Resolve
	p.exempt = encryptValueObjects(doc)

	switch f := resolve(enc.Get("Filter")).(type) {
	case object.Name:
		if f != "Standard" {
			return nil, unsupported("/Filter /%s: only the standard security handler is implemented", f)
		}
	case nil:
		return nil, malformed("/Filter is missing")
	default:
		return nil, malformed("/Filter is a %T, not a name", f)
	}

	v, err := requiredInt(resolve, enc, "V")
	if err != nil {
		return nil, err
	}
	r, err := requiredInt(resolve, enc, "R")
	if err != nil {
		return nil, err
	}
	if v < 0 || v > 5 || r < 2 || r > 6 {
		return nil, unsupported("/V %d /R %d", v, r)
	}
	p.v, p.r = int(v), int(r)
	switch {
	case p.v == 0 || p.v == 3:
		return nil, unsupported("/V %d is an undocumented or unpublished algorithm", p.v)
	case p.v == 5 && p.r == 5:
		return nil, unsupported("/R 5 is a deprecated proprietary extension (ISO 32000-2 Table 21)")
	case p.v == 1 && (p.r == 2 || p.r == 3),
		p.v == 2 && p.r == 3,
		p.v == 4 && p.r == 4,
		p.v == 5 && p.r == 6:
		// the pairs ISO 32000-2 Table 21 defines
	default:
		return nil, malformed("/R %d is not the revision for /V %d (ISO 32000-2 Table 21)", p.r, p.v)
	}

	pv, err := requiredInt(resolve, enc, "P")
	if err != nil {
		return nil, err
	}
	// /P is "an unsigned 32-bit quantity" (Table 21); producers write it signed
	// (-4) or unsigned (4294967292). Anything outside both readings is not a
	// 32-bit value, and wrapping it would change the key it feeds.
	if pv < math.MinInt32 || pv > math.MaxUint32 {
		return nil, malformed("/P %d is not a 32-bit value", pv)
	}
	p.p = int32(uint32(pv))

	if p.v >= 4 {
		switch em := resolve(enc.Get("EncryptMetadata")).(type) {
		case nil:
		case object.Boolean:
			p.encryptMetadata = bool(em)
		default:
			return nil, malformed("/EncryptMetadata is a %T, not a boolean", em)
		}
	}

	oLen := 32
	if p.r == 6 {
		oLen = 48
	}
	if p.o, err = requiredBytes(resolve, enc, "O", oLen); err != nil {
		return nil, err
	}
	if p.u, err = requiredBytes(resolve, enc, "U", oLen); err != nil {
		return nil, err
	}
	if p.r == 6 {
		if p.oe, err = requiredBytes(resolve, enc, "OE", 32); err != nil {
			return nil, err
		}
		if p.ue, err = requiredBytes(resolve, enc, "UE", 32); err != nil {
			return nil, err
		}
		if p.perms, err = requiredBytes(resolve, enc, "Perms", 16); err != nil {
			return nil, err
		}
	}

	if err := p.resolveFilters(doc, enc, resolve); err != nil {
		return nil, err
	}
	if err := p.resolveKeyLen(doc, enc, resolve); err != nil {
		return nil, err
	}

	if p.r <= 4 {
		// The /ID is an input to Algorithm 2. A missing one is a defect of the
		// trailer, not of /Encrypt: derive with an empty identifier, as other
		// readers do. A wrong guess cannot unlock anything — it fails the /U
		// check and the document is Locked with ErrWrongPassword.
		if ids, ok := doc.Resolve(doc.Trailer.Get("ID")).(object.Array); ok && len(ids) > 0 {
			if s, ok := doc.Resolve(ids[0]).(object.String); ok {
				p.id = s.Value
			}
		}
	}
	return p, nil
}

// resolveFilters resolves /StmF, /StrF, /EFF and every /CF entry to a method.
// Below V4 both strings and streams are RC4 and /CF means nothing.
func (p *params) resolveFilters(doc core.View, enc *object.Dictionary, resolve func(object.Object) object.Object) error {
	p.filters = map[object.Name]method{}
	if p.v < 4 {
		p.stm, p.str, p.eff = RC4, RC4, RC4
		return nil
	}
	var cf *object.Dictionary
	switch c := resolve(enc.Get("CF")).(type) {
	case nil:
	case *object.Dictionary:
		cf = c
	default:
		return malformed("/CF is a %T, not a dictionary", c)
	}
	if cf != nil {
		for i, name := range cf.Keys {
			if name == "Identity" {
				continue // a standard name: its /CF entry "shall be ignored" (Table 20)
			}
			m, err := p.filterMethod(name, resolve(cf.Values[i]), resolve)
			if err != nil {
				m = invalid
			}
			p.filters[name] = m
		}
	}
	lookup := func(key object.Name) (method, error) {
		var name object.Name
		switch n := resolve(enc.Get(key)).(type) {
		case nil:
			return None, nil // Default value: Identity (Table 20)
		case object.Name:
			name = n
		default:
			return invalid, malformed("/%s is a %T, not a name", key, n)
		}
		if name == "Identity" {
			return None, nil
		}
		if cf == nil {
			return invalid, malformed("/%s names crypt filter /%s but there is no /CF dictionary", key, name)
		}
		entry := cf.Get(name)
		if entry == nil {
			return invalid, malformed("/%s names crypt filter /%s, which /CF does not define", key, name)
		}
		return p.filterMethod(name, resolve(entry), resolve)
	}
	var err error
	if p.stm, err = lookup("StmF"); err != nil {
		return err
	}
	if p.str, err = lookup("StrF"); err != nil {
		return err
	}
	// EFF defaults to StmF (Table 20). An EFF the handler cannot apply locks
	// only the embedded files, not the document: they are reported as not
	// decrypted when they are met (see streamMethod).
	if enc.Get("EFF") == nil {
		p.eff = p.stm
	} else if p.eff, p.effErr = lookup("EFF"); p.effErr != nil {
		p.eff = invalid
	}
	return nil
}

// filterMethod validates one crypt filter dictionary (ISO 32000-2 Table 25).
func (p *params) filterMethod(name object.Name, entry object.Object, resolve func(object.Object) object.Object) (method, error) {
	fd, ok := entry.(*object.Dictionary)
	if !ok {
		return invalid, malformed("crypt filter /%s is a %T, not a dictionary", name, entry)
	}
	var cfm object.Name = "None" // Default value: None
	switch c := resolve(fd.Get("CFM")).(type) {
	case nil:
	case object.Name:
		cfm = c
	default:
		return invalid, malformed("crypt filter /%s: /CFM is a %T, not a name", name, c)
	}
	switch {
	case p.v == 4 && cfm == "V2":
		return RC4, nil
	case p.v == 4 && cfm == "AESV2":
		return AESV2, nil
	case p.v == 5 && cfm == "AESV3":
		return AESV3, nil
	case cfm == "None":
		// "The application shall not decrypt data but shall direct the input
		// stream to the security handler for decryption" — i.e. a handler
		// other than this one.
		return invalid, unsupported("crypt filter /%s has /CFM /None: its data is decrypted by a handler other than the standard one", name)
	}
	return invalid, unsupported("crypt filter /%s: /CFM /%s is not defined for /V %d (ISO 32000-2 7.6.4.1: V2 or AESV2 at revision 4, AESV3 at revision 6)", name, cfm, p.v)
}

// resolveKeyLen sets the file key length, in bytes.
func (p *params) resolveKeyLen(doc core.View, enc *object.Dictionary, resolve func(object.Object) object.Object) error {
	switch p.v {
	case 5:
		p.keyLen = 32 // Algorithm 2.A: always 256 bits; /Length is not consulted
		return nil
	case 1:
		p.keyLen = 5 // "a file encryption key length of 40 bits" (Table 20)
		return nil
	case 2:
		n, err := lengthBits(resolve, enc)
		if err != nil {
			return err
		}
		p.keyLen = n / 8
		return nil
	}
	// V4: "a file encryption key length of 128 bits" (Table 20). The length a
	// crypt filter declares is honoured when it is an RC4 filter — AESV2 is
	// always 128 bits (Table 25) — and failing that the top-level /Length, which
	// many producers write at V4 although the spec reserves it for V2 and V3.
	// A missing /Length never means 40 bits here: that was the C154 defect,
	// which gave every such AESV2 file a 5-byte key and left it Locked.
	p.keyLen = 16
	declared := 0
	cf := doc.ResolveDict(enc.Get("CF"))
	for _, key := range []object.Name{"StmF", "StrF", "EFF"} {
		name, _ := doc.Resolve(enc.Get(key)).(object.Name)
		if name == "" || name == "Identity" || cf == nil {
			continue
		}
		fd := doc.ResolveDict(cf.Get(name))
		if fd == nil {
			continue
		}
		if cfm, _ := doc.Resolve(fd.Get("CFM")).(object.Name); cfm != "V2" {
			continue // AESV2 is 128 bits whatever /Length says
		}
		lv := resolve(fd.Get("Length"))
		if lv == nil {
			continue
		}
		n, ok := lv.(object.Integer)
		if !ok {
			return malformed("crypt filter /%s: /Length is a %T, not an integer", name, lv)
		}
		var bytes int
		switch {
		case n >= 5 && n <= 16:
			bytes = int(n) // bytes, as Table 25 specifies for the standard handler
		case n >= 40 && n <= 128 && n%8 == 0:
			bytes = int(n) / 8 // bits, as producers commonly write it
		default:
			return malformed("crypt filter /%s: /Length %d is neither 5 to 16 bytes nor 40 to 128 bits", name, n)
		}
		if declared != 0 && declared != bytes {
			return malformed("crypt filters declare different key lengths (%d and %d bytes) for one file key", declared, bytes)
		}
		declared = bytes
	}
	switch {
	case declared != 0:
		if p.stm == AESV2 || p.str == AESV2 || p.eff == AESV2 {
			if declared != 16 {
				return malformed("an RC4 crypt filter declares a %d-byte key, but AESV2 needs the 16-byte file key it shares", declared)
			}
		}
		p.keyLen = declared
	case enc.Get("Length") != nil && p.stm != AESV2 && p.str != AESV2 && p.eff != AESV2:
		n, err := lengthBits(resolve, enc)
		if err != nil {
			return err
		}
		p.keyLen = n / 8
	}
	return nil
}

// lengthBits validates the top-level /Length: "a multiple of 8, in the range
// 40 to 128. Default value: 40" (ISO 32000-2 Table 20).
func lengthBits(resolve func(object.Object) object.Object, enc *object.Dictionary) (int, error) {
	switch n := resolve(enc.Get("Length")).(type) {
	case nil:
		return 40, nil
	case object.Integer:
		if n < 40 || n > 128 || n%8 != 0 {
			return 0, malformed("/Length %d is not a multiple of 8 between 40 and 128", n)
		}
		return int(n), nil
	default:
		return 0, malformed("/Length is a %T, not an integer", n)
	}
}

func requiredInt(resolve func(object.Object) object.Object, d *object.Dictionary, key object.Name) (int64, error) {
	switch n := resolve(d.Get(key)).(type) {
	case object.Integer:
		return int64(n), nil
	case nil:
		return 0, malformed("/%s is missing", key)
	default:
		return 0, malformed("/%s is a %T, not an integer", key, n)
	}
}

// requiredBytes returns the first n bytes of a required string entry.
func requiredBytes(resolve func(object.Object) object.Object, d *object.Dictionary, key object.Name, n int) ([]byte, error) {
	switch s := resolve(d.Get(key)).(type) {
	case object.String:
		if len(s.Value) < n {
			return nil, malformed("/%s is %d bytes, want %d", key, len(s.Value), n)
		}
		return s.Value[:n:n], nil
	case nil:
		return nil, malformed("/%s is missing", key)
	default:
		return nil, malformed("/%s is a %T, not a string", key, s)
	}
}
