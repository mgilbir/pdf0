package pdf0

import (
	"strings"
	"testing"
)

// TestCodecPrefixFilterSaysWhy: the general-purpose stages in front of a
// CCITTFaxDecode or JBIG2Decode image decode through View.DecodeStages, so an
// image whose prefix pdf0 declined says so — "unsupported" — rather than
// falling into the same "could not be reversed" as corrupt data. A corrupt
// prefix says "malformed".
func TestCodecPrefixFilterSaysWhy(t *testing.T) {
	for _, c := range []struct{ codec, prefix, want string }{
		{"CCITTFaxDecode", "/NotAFilter", "(unsupported)"},
		{"JBIG2Decode", "/NotAFilter", "(unsupported)"},
		{"CCITTFaxDecode", "/FlateDecode", "(malformed)"},
		{"JBIG2Decode", "/FlateDecode", "(malformed)"},
	} {
		file := buildRawPDF(onePage(
			rawObj{dict: "<<>>", stream: []byte("q 10 0 0 10 0 0 cm /Im0 Do Q")},
			"<</XObject<</Im0 5 0 R>>>>",
			rawObj{dict: "<</Type/XObject/Subtype/Image/Width 8/Height 8/ColorSpace/DeviceGray/BitsPerComponent 1/Filter[" + c.prefix + "/" + c.codec + "]>>", stream: []byte("not what the filter wants")},
		))
		imgs := readRaw(t, file).ExtractImages()
		if len(imgs) != 1 {
			t.Fatalf("%s after %s: %d images, want 1", c.codec, c.prefix, len(imgs))
		}
		if imgs[0].Decoded || !strings.Contains(imgs[0].Note, c.want) {
			t.Errorf("%s after %s: decoded=%v note %q, want it undecoded and saying %s", c.codec, c.prefix, imgs[0].Decoded, imgs[0].Note, c.want)
		}
	}
}
