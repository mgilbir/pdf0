package pdfa

import (
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// ISO 32000-1 Tables 63-65: CIE colour space parameter validation.
func TestCIEColorSpaceParams(t *testing.T) {
	check := func(family string, params *object.Dictionary) []Violation {
		doc := mkPDFAView(PDFA2b)
		var errs []Violation
		checkColorSpaceValue(doc, object.Array{object.Name(family), params}, 0, PDFA2b, &errs)
		return errs
	}
	wp := func(x, y, z float64) object.Array {
		return object.Array{object.Real(x), object.Real(y), object.Real(z)}
	}

	missing := &object.Dictionary{}
	if len(check("CalRGB", missing)) == 0 {
		t.Error("missing WhitePoint must be flagged")
	}
	badY := &object.Dictionary{}
	badY.Set("WhitePoint", wp(0.95, 0.9, 1.09))
	if len(check("CalGray", badY)) == 0 {
		t.Error("WhitePoint Yw != 1.0 must be flagged")
	}
	negBP := &object.Dictionary{}
	negBP.Set("WhitePoint", wp(0.95, 1.0, 1.09))
	negBP.Set("BlackPoint", object.Array{object.Real(-0.1), object.Real(0), object.Real(0)})
	if len(check("Lab", negBP)) == 0 {
		t.Error("negative BlackPoint must be flagged")
	}
	badRange := &object.Dictionary{}
	badRange.Set("WhitePoint", wp(0.95, 1.0, 1.09))
	badRange.Set("Range", object.Array{object.Integer(100), object.Integer(-100), object.Integer(-100), object.Integer(100)})
	if len(check("Lab", badRange)) == 0 {
		t.Error("Lab Range with min > max must be flagged")
	}
	good := &object.Dictionary{}
	good.Set("WhitePoint", wp(0.9505, 1.0, 1.089))
	if errs := check("CalRGB", good); len(errs) != 0 {
		t.Errorf("valid CalRGB dict must pass, got %v", errs)
	}
}

// DeviceN with spot colorants requires a Colorants dictionary at 2b+.
func TestDeviceNSpotNeedsColorants(t *testing.T) {
	doc := mkPDFAView(PDFA2b)
	var errs []Violation
	deviceN := object.Array{object.Name("DeviceN"), object.Array{object.Name("Spot1")}, object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 5}}, object.IndirectRef{Number: 5}}
	checkColorSpaceValue(doc, deviceN, 0, PDFA2b, &errs)
	found := false
	for _, e := range errs {
		if e.Message == "DeviceN color space with spot colorants must have a Colorants dictionary" {
			found = true
		}
	}
	if !found {
		t.Errorf("spot DeviceN without Colorants dict must be flagged, got %v", errs)
	}
}
