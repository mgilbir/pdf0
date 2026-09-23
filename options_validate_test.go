package pdf0

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mustResolveLimits is resolveLimits for a test whose options are valid.
func mustResolveLimits(opts []Option) core.Limits {
	l, err := resolveLimits(opts)
	if err != nil {
		panic(err)
	}
	return l
}

// everyLimitOption builds each limit option with the value n (truncated to the
// option's type), so a test can ask the same question of all of them and a new
// option cannot be added without being asked it.
func everyLimitOption(n int64) map[string]Option {
	return map[string]Option{
		"WithMaxDecodedStreamBytes":  WithMaxDecodedStreamBytes(int(n)),
		"WithMaxDecodedContentBytes": WithMaxDecodedContentBytes(n),
		"WithMaxObjectStreamBytes":   WithMaxObjectStreamBytes(n),
		"WithMaxContentStreamBytes":  WithMaxContentStreamBytes(int(n)),
		"WithMaxICCProfileBytes":     WithMaxICCProfileBytes(int(n)),
		"WithMaxXMPPacketBytes":      WithMaxXMPPacketBytes(int(n)),
		"WithMaxCIDRangeSpan":        WithMaxCIDRangeSpan(int(n)),
		"WithMaxRoleMapSteps":        WithMaxRoleMapSteps(int(n)),
		"WithMaxTableGridFills":      WithMaxTableGridFills(n),
		"WithMaxPostScriptSteps":     WithMaxPostScriptSteps(int(n)),
		"WithMaxCmapWork":            WithMaxCmapWork(int(n)),
		"WithMaxImagePixels":         WithMaxImagePixels(n),
	}
}

// TestLimitOptionsRejectZeroAndNegative: a limit of 0 or less has no meaning a
// caller could rely on — 0 used to mean "the default" silently and -1 made
// every stream fail — so every option refuses it, at Read, with an error that
// names the option (audit 2026-09-22 C48).
func TestLimitOptionsRejectZeroAndNegative(t *testing.T) {
	file := minimalContentPDF(t, []byte("BT /F1 12 Tf (Hello) Tj ET"), false)
	for _, n := range []int64{0, -1, math.MinInt64} {
		for name, opt := range everyLimitOption(n) {
			doc, err := Read(bytes.NewReader(file), int64(len(file)), opt)
			if err == nil || doc != nil {
				t.Errorf("%s(%d): Read = (%v, %v), want an error and no document", name, n, doc != nil, err)
				continue
			}
			if !errors.Is(err, ErrInvalidOption) || !strings.Contains(err.Error(), name) {
				t.Errorf("%s(%d): error %q does not wrap ErrInvalidOption and name the option", name, n, err)
			}
		}
	}
	// ParseXRefStream is the other entry point that takes options.
	if _, err := ParseXRefStream(object.NewStream(object.NewDictionary(), nil), WithMaxDecodedStreamBytes(0)); !errors.Is(err, ErrInvalidOption) {
		t.Errorf("ParseXRefStream(WithMaxDecodedStreamBytes(0)) = %v, want ErrInvalidOption", err)
	}
}

// TestLimitOptionsHonourTheMaximum: math.MaxInt is the natural way to write
// "no practical limit", and it must mean that. The decoders read one byte past
// the cap to tell "exactly the cap" from "over it", and int64(MaxInt)+1 wrapped
// negative, so io.LimitReader read nothing and every stream decoded to empty
// with no error: a document with text looked empty (audit 2026-09-22 C48).
func TestLimitOptionsHonourTheMaximum(t *testing.T) {
	const text = "BT /F1 12 Tf (Hello) Tj ET"
	for _, flate := range []bool{true, false} {
		file := minimalContentPDF(t, []byte(text), flate)
		var opts []Option
		for _, opt := range everyLimitOption(math.MaxInt64) {
			opts = append(opts, opt)
		}
		doc, err := Read(bytes.NewReader(file), int64(len(file)), opts...)
		if err != nil {
			t.Fatalf("flate=%v: Read with every option at its maximum: %v", flate, err)
		}
		st := contentStreamOf(t, doc)
		got, err := doc.StreamData(st)
		if err != nil || string(got) != text {
			t.Errorf("flate=%v: StreamData = (%q, %v), want (%q, nil)", flate, got, err, text)
		}
	}
}
