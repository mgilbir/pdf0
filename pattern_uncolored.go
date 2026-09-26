package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// What an uncoloured tiling pattern's cell may not use.
//
// ISO 32000-2 8.6.8 lists what a reader ignores in the content stream of an
// uncoloured tiling pattern "and all other content streams invoked from within"
// it: the colour operators, ri and sh; the colour-related entries of a gs
// graphics state; and every image painting operator unless it paints a stencil
// mask (8.7.3.3). Ignored is not an error — the reader skips the operator and
// carries on — so a cell that uses one is a file that silently draws something
// other than what its producer wrote. AddTilingPattern refuses it here, where
// the mistake can still be attributed.

// uncoloredIgnoredOps are the operators 8.6.8 lists. The Builder records the
// first one it wrote itself (ColorOperator); this set is for the streams of
// the forms a cell draws, which were not built here.
var uncoloredIgnoredOps = map[string]bool{
	"CS": true, "cs": true, "SC": true, "SCN": true, "sc": true, "scn": true,
	"G": true, "g": true, "RG": true, "rg": true, "K": true, "k": true,
	"ri": true, "sh": true,
}

// uncoloredIgnoredGS are the graphics-state entries 8.6.8 lists.
var uncoloredIgnoredGS = []object.Name{"TR", "TR2", "BG", "BG2", "UCR", "UCR2", "HT", "UseBlackPtComp"}

// maxUncoloredFormDepth bounds how deep the check follows forms that draw
// forms. A deeper chain is refused rather than trusted.
const maxUncoloredFormDepth = content.MaxNestingDepth

// errUncolored words every refusal the same way: what was used, and that a
// reader ignores it there.
func errUncolored(what string) error {
	return fmt.Errorf("pdf0: an uncoloured pattern takes its colour from where it is painted, "+
		"and a reader ignores %s in its cell and in anything the cell draws (ISO 32000-2 8.6.8, 8.7.3.3)", what)
}

// checkUncoloredCell refuses an uncoloured pattern's cell that uses something a
// reader ignores there: an operator the Builder recorded, a graphics state with
// a colour-related entry, an image that is not a stencil mask, or a form whose
// own content does any of these.
func (d *Document) checkUncoloredCell(p TilingPattern) error {
	if op := p.Content.ColorOperator(); op != "" {
		return errUncolored("the " + op + " operator")
	}
	used := p.Content.Resources()
	for _, name := range used.ExtGStates {
		if err := d.checkUncoloredExtGState(name, p.ExtGStates[name]); err != nil {
			return err
		}
	}
	seen := map[*object.Stream]bool{}
	for _, name := range used.XObjects {
		if err := d.checkUncoloredXObject(name, p.XObjects[name], 0, seen); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) checkUncoloredExtGState(name object.Name, gs object.Object) error {
	dict := d.ResolveDict(gs)
	if dict == nil {
		return nil // an undefined name is the resource check's to report
	}
	for _, key := range uncoloredIgnoredGS {
		if dict.Has(key) {
			return errUncolored(fmt.Sprintf("the %v entry of the graphics state %v", key, name))
		}
	}
	return nil
}

// checkUncoloredXObject checks what a Do in the cell paints. An image must be a
// stencil mask, which designates where the pattern's colour goes rather than
// stating one; a form is checked through its own content.
func (d *Document) checkUncoloredXObject(name object.Name, xo object.Object, depth int, seen map[*object.Stream]bool) error {
	stream, ok := d.Resolve(xo).(*object.Stream)
	if !ok || seen[stream] {
		return nil // an undefined name is the resource check's; a cycle is walked once
	}
	seen[stream] = true
	subtype, _ := d.Resolve(stream.Dict.Get("Subtype")).(object.Name)
	switch subtype {
	case "Image":
		if mask, _ := d.Resolve(stream.Dict.Get("ImageMask")).(object.Boolean); !mask {
			return errUncolored(fmt.Sprintf("the image %v, which is not a stencil mask (/ImageMask true),", name))
		}
	case "Form":
		if depth >= maxUncoloredFormDepth {
			return fmt.Errorf("pdf0: the uncoloured pattern's cell draws forms nested more than %d deep; "+
				"what they paint cannot be checked", maxUncoloredFormDepth)
		}
		return d.checkUncoloredForm(name, stream, depth+1, seen)
	}
	return nil
}

// checkUncoloredForm scans a form's content for the operators an uncoloured
// cell ignores, following its gs and Do names through its own resources. It
// reads the content with the shared content lexer, whose inline-image hook
// reports each inline image whole: one that is not a stencil mask (/IM true)
// is an image a reader ignores there too. A form built with AddForm cannot
// contain one — the Builder has no inline-image operator — but a form carried
// over from a read document can.
func (d *Document) checkUncoloredForm(name object.Name, form *object.Stream, depth int, seen map[*object.Stream]bool) error {
	data, err := d.StreamData(form)
	if err != nil {
		return fmt.Errorf("pdf0: the uncoloured pattern's cell draws the form %v, whose content cannot be read "+
			"to check that it sets no colour: %w", name, err)
	}
	res := d.ResolveDict(form.Dict.Get("Resources"))
	sub := func(kind object.Name) *object.Dictionary {
		if res == nil {
			return nil
		}
		return d.ResolveDict(res.Get(kind))
	}
	var operand object.Name // the name operand just before an operator
	lx := core.NewContentLexer(core.Canceler{}, data)
	var tok core.ContentTok
	for lx.Next(&tok) {
		switch tok.Kind {
		case core.ContentName:
			operand = object.Name(tok.Name())
			continue
		case core.ContentInlineImage:
			if !inlineImageIsMask(tok.Params) {
				return errUncolored(fmt.Sprintf("an inline image (in the form %v) that is not a stencil mask (/IM true),", name))
			}
			operand = ""
			continue
		case core.ContentDictStart:
			lx.SkipDict(&tok)
			operand = ""
			continue
		case core.ContentOperator:
		default:
			operand = ""
			continue
		}
		op := string(tok.Raw)
		var failure error
		switch {
		case uncoloredIgnoredOps[op]:
			failure = errUncolored(fmt.Sprintf("the %s operator (in the form %v)", op, name))
		case op == "gs" && operand != "":
			if gs := sub("ExtGState"); gs != nil {
				failure = d.checkUncoloredExtGState(operand, gs.Get(operand))
			}
		case op == "Do" && operand != "":
			if xo := sub("XObject"); xo != nil {
				failure = d.checkUncoloredXObject(operand, xo.Get(operand), depth, seen)
			}
		}
		if failure != nil {
			return failure
		}
		operand = ""
	}
	return nil
}

// inlineImageIsMask reports whether an inline image's parameters declare it a
// stencil mask: /IM (or /ImageMask) true.
func inlineImageIsMask(params []byte) bool {
	for _, p := range core.ParseInlineImageParams(params) {
		if (p.Key == "IM" || p.Key == "ImageMask") && !p.Array && len(p.Value) == 1 && p.Value[0].Is("true") {
			return true
		}
	}
	return false
}
