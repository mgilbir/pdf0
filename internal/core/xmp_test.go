package core

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestXMPMalformedReason: the reason a metadata packet is not well-formed is
// the XML parser's, naming the line, and asking for it first parses the
// packet — the answer does not depend on a reader having asked XMPPacketOf
// before. A packet that is well-formed, or a stream whose data does not decode
// (XMPMalformed, but no XML was read), has no reason.
func TestXMPMalformedReason(t *testing.T) {
	for _, tc := range []struct {
		name       string
		st         *object.Stream
		status     XMPStatus
		wantReason string
	}{
		{"control character", streamWith([]byte("<x:xmpmeta xmlns:x=\"adobe:ns:meta/\">\n\x01</x:xmpmeta>")), XMPMalformed,
			"XML syntax error on line 2: illegal character code U+0001"},
		{"well-formed", streamWith([]byte("<x:xmpmeta xmlns:x=\"adobe:ns:meta/\"/>")), XMPParsed, ""},
		{"data does not decode", streamWith([]byte("not zlib"), object.Entry{Key: "Filter", Value: object.Name("FlateDecode")}), XMPMalformed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := viewOf(map[int]*object.IndirectObject{}, DefaultLimits())
			if got := v.XMPMalformedReason(tc.st, 7); got != tc.wantReason {
				t.Errorf("XMPMalformedReason = %q, want %q", got, tc.wantReason)
			}
			if _, status := v.XMPPacketOf(tc.st, 7); status != tc.status {
				t.Errorf("XMPPacketOf status = %v, want %v", status, tc.status)
			}
		})
	}
}
