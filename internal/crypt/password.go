package crypt

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/pdfdoc"
	"github.com/mgilbir/pdf0/internal/saslprep"
)

// Password preparation (ISO 32000-2 7.6.4.1, 7.6.4.3.2 step a, 7.6.4.3.3 steps
// a–b). A password is Unicode text; the handler hashes bytes, so every revision
// defines how the text becomes bytes, and a reader and a writer that disagree
// produce a file whose password "does not work":
//
//   - Revision 6: SASLprep (RFC 4013), then UTF-8, then the first 127 bytes.
//   - Revisions 2–4: PDFDocEncoding, then the first 32 bytes, padded with
//     PasswordPad.
//
// pdf0 hashed the raw UTF-8 bytes at every revision, with no truncation (audit
// 2026-09-22 C58): a 200-byte password it set was rejected by poppler, and the
// 127-byte form every conforming producer derives did not open pdf0's files.
//
// Setting a password (the write side, R6 only — pdf0 produces no other
// revision) is strict: a password SASLprep prohibits is refused, since no
// conforming reader could reproduce its bytes. Checking a password (the read
// side) prepares it as a SASLprep query and tries the spec's form first; it
// then also tries the raw UTF-8 bytes when they differ, because producers that
// skip SASLprep exist (pdf0 itself, before this change) and a file they wrote
// is still opened by exactly the password its author typed. The
// extra candidate cannot weaken anything: each candidate must still reproduce
// the /U or /O hash.

// maxR6Password is the byte length Algorithm 2.A step b truncates to.
const maxR6Password = 127

// PrepareR6Password prepares a password being set for revision 6: SASLprep as
// a stored string, UTF-8, truncated to 127 bytes. The truncation is the spec's
// and every reader applies it, so only the first 127 bytes of a longer
// password protect the file.
func PrepareR6Password(password string) ([]byte, error) {
	s, err := saslprep.Prepare(password, true)
	if err != nil {
		return nil, fmt.Errorf("password cannot be used with AES-256 encryption (ISO 32000-2 requires SASLprep): %w", err)
	}
	return truncate([]byte(s), maxR6Password), nil
}

// r6Candidates are the byte strings a password being checked at revision 6
// may have been hashed as: the SASLprep query form, then the raw UTF-8 form.
func r6Candidates(password string) [][]byte {
	var out [][]byte
	if s, err := saslprep.Prepare(password, false); err == nil {
		out = append(out, truncate([]byte(s), maxR6Password))
	}
	return appendDistinct(out, truncate([]byte(password), maxR6Password))
}

// r4Candidates are the padded 32-byte forms a password being checked at
// revisions 2–4 may have been hashed as: PDFDocEncoding (the spec's form),
// then the raw UTF-8 bytes, which is what pdf0 and other producers that do not
// transcode have always used. The two agree for ASCII.
func r4Candidates(password string) [][]byte {
	var out [][]byte
	if b, ok := pdfdoc.Encode(password); ok {
		out = append(out, PadBytes(b))
	}
	return appendDistinct(out, PadBytes([]byte(password)))
}

func appendDistinct(list [][]byte, b []byte) [][]byte {
	for _, have := range list {
		if string(have) == string(b) {
			return list
		}
	}
	return append(list, b)
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// PadPassword pads (or truncates) a password to the 32-byte field used by
// revisions 2–4 (ISO 32000-1 Algorithm 2, step a), encoding it in
// PDFDocEncoding when it can be, and as UTF-8 bytes otherwise.
func PadPassword(password string) []byte {
	return r4Candidates(password)[0]
}

// PadBytes pads (or truncates) already-encoded password bytes to 32 bytes.
func PadBytes(password []byte) []byte {
	out := make([]byte, 32)
	n := copy(out, password)
	copy(out[n:], PasswordPad)
	return out
}
