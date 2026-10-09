package htmlpdf

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
)

// TestATransformGroupIsRefused: a transform no rectangle can say is drawn by
// layout through a TransformGroup only when the caller asks for one, and this
// backend does not draw one yet, so it refuses the document rather than leave
// the box off the page. Without the option, layout draws the box untransformed
// and says so, and the page renders.
func TestATransformGroupIsRefused(t *testing.T) {
	set := notoSansSet(t)
	in := Input{
		HTML:  `<div style="margin:100px; width:100px; height:50px; background:red; transform: rotate(30deg)"></div>`,
		Fonts: set,
	}
	if _, err := Render(in, Options{}); err != nil {
		t.Fatalf("without TransformGroups: %v", err)
	}

	_, err := Render(in, Options{TransformGroups: true})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("with TransformGroups: rendered (%v); want a refusal", err)
	}
	found := false
	for _, f := range refused.Findings {
		if f.Rule == RuleUndrawable && strings.Contains(f.Message, "TransformGroups") {
			found = true
		}
	}
	if !found {
		t.Errorf("refused with %+v; want %s naming TransformGroups", refused.Findings, RuleUndrawable)
	}

	// A quarter turn needs no group, so asking for groups changes nothing.
	in.HTML = strings.Replace(in.HTML, "30deg", "90deg", 1)
	if _, err := Render(in, Options{TransformGroups: true}); err != nil {
		t.Errorf("a quarter turn with TransformGroups: %v", err)
	}

	in.HTML = strings.Replace(in.HTML, "90deg", "30deg", 1)
	in.Policy = layout.Policy{RuleUndrawable: layout.Warn}
	if _, err := Render(in, Options{TransformGroups: true}); err != nil {
		t.Errorf("under a Warn policy: %v", err)
	}
}
