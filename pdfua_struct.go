package pdf0

// Structure-element nesting constraints from the veraPDF PDF/UA-1 profile
// (clause 7.2). allowedParents maps a child type to the parent types that may
// contain it; allowedChildren maps a parent type to the only child types it may
// contain. Types are compared after resolving through the structure tree's
// /RoleMap.
var uaAllowedParents = map[Name][]Name{
	"LBody": {"LI"},
	"LI":    {"L"},
	"TBody": {"Table"},
	"THead": {"Table"},
	"TFoot": {"Table"},
	"TD":    {"TR"},
	"TH":    {"TR"},
	"TR":    {"Table", "THead", "TBody", "TFoot"},
	"TOCI":  {"TOC"},
}

var uaAllowedChildren = map[Name]map[Name]bool{
	"LI":    {"Lbl": true, "LBody": true},
	"L":     {"L": true, "LI": true, "Caption": true},
	"TBody": {"TR": true},
	"THead": {"TR": true},
	"TFoot": {"TR": true},
	"TR":    {"TH": true, "TD": true},
	"Table": {"TR": true, "THead": true, "TBody": true, "TFoot": true, "Caption": true},
	"TOC":   {"TOC": true, "TOCI": true, "Caption": true},
}

// standardStructType resolves a structure element's type through /RoleMap to a
// standard type, or returns the element's own /S (which the role-map check
// flags if non-standard).
func (d *Document) standardStructType(elem *Dictionary, roleMap *Dictionary) Name {
	s, _ := elem.Get("S").(Name)
	if standardStructTypes[s] || roleMap == nil {
		return s
	}
	if to, _ := d.Resolve(roleMap.Get(s)).(Name); standardStructTypes[to] {
		return to
	}
	return s
}

// checkUAStructNesting enforces the structure-element parent/child constraints
// (tables, lists, table of contents) from the PDF/UA profile.
func (d *Document) checkUAStructNesting(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))

	var v []UAViolation
	seen := map[int]bool{}
	var walk func(node Object, parentType Name)
	walk = func(node Object, parentType Name) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid, parentType)
				}
			}
			return
		}
		// Only structure elements (those with an /S type) participate.
		if _, hasS := elem.Get("S").(Name); !hasS {
			return
		}
		t := d.standardStructType(elem, roleMap)

		// Parent constraint.
		if parents, ok := uaAllowedParents[t]; ok && !containsName(parents, parentType) {
			v = append(v, UAViolation{"7.2", "<" + string(t) + "> element must be contained in a " + orList(parents) + " element, not <" + string(parentType) + ">", 0})
		}

		// Child constraint: check each structure-element child's type.
		if allowed, ok := uaAllowedChildren[t]; ok {
			for _, ct := range d.childStructTypes(elem, roleMap) {
				if !allowed[ct] {
					v = append(v, UAViolation{"7.2", "<" + string(t) + "> element must not contain a <" + string(ct) + "> element", 0})
				}
			}
		}

		for _, kid := range d.structKids(elem) {
			walk(kid, t)
		}
	}
	walk(root.Get("K"), "")
	return v
}

// structKids returns the /K children of an element as a slice of objects.
func (d *Document) structKids(elem *Dictionary) []Object {
	k := elem.Get("K")
	if k == nil {
		return nil
	}
	if arr, ok := d.Resolve(k).(Array); ok {
		return []Object(arr)
	}
	return []Object{k}
}

// checkUATableListStructure enforces the well-formedness rules for Table, List
// (L) and table-of-contents (TOC) containers that go beyond simple parent/child
// typing (UA profile / ISO 32000-1 14.8.4.3): at most one Caption/THead/TFoot,
// a THead or TFoot requires a TBody, and a Caption must sit in the permitted
// position (first-or-last for a Table, first for a List or TOC).
func (d *Document) checkUATableListStructure(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	var v []UAViolation
	seen := map[int]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		t := d.standardStructType(elem, roleMap)
		kids := d.childStructTypes(elem, roleMap)
		switch t {
		case "Table":
			v = append(v, tableStructErrors(kids)...)
		case "L":
			if n := countName(kids, "Caption"); n > 1 {
				v = append(v, UAViolation{"7.2", "list (L) has more than one Caption", 0})
			} else if n == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, UAViolation{"7.2", "list (L) Caption must be the first child", 0})
			}
		case "TOC":
			if n := countName(kids, "Caption"); n > 1 {
				v = append(v, UAViolation{"7.2", "table of contents (TOC) has more than one Caption", 0})
			} else if n == 1 && firstIndexName(kids, "Caption") != 0 {
				v = append(v, UAViolation{"7.2", "table of contents (TOC) Caption must be the first child", 0})
			}
		}
		for _, kid := range d.structKids(elem) {
			walk(kid)
		}
	}
	walk(root.Get("K"))
	return v
}

// tableStructErrors reports the Table-container well-formedness violations for a
// table's ordered child-type list.
func tableStructErrors(kids []Name) []UAViolation {
	var v []UAViolation
	captions := countName(kids, "Caption")
	theads := countName(kids, "THead")
	tfoots := countName(kids, "TFoot")
	tbodies := countName(kids, "TBody")
	if captions > 1 {
		v = append(v, UAViolation{"7.2", "table has more than one Caption", 0})
	}
	if theads > 1 {
		v = append(v, UAViolation{"7.2", "table has more than one THead", 0})
	}
	if tfoots > 1 {
		v = append(v, UAViolation{"7.2", "table has more than one TFoot", 0})
	}
	if (theads > 0 || tfoots > 0) && tbodies == 0 {
		v = append(v, UAViolation{"7.2", "table has a THead or TFoot but no TBody", 0})
	}
	if captions == 1 {
		i := firstIndexName(kids, "Caption")
		if i != 0 && i != len(kids)-1 {
			v = append(v, UAViolation{"7.2", "table Caption must be the first or last child", 0})
		}
	}
	return v
}

func countName(names []Name, want Name) int {
	n := 0
	for _, x := range names {
		if x == want {
			n++
		}
	}
	return n
}

func firstIndexName(names []Name, want Name) int {
	for i, x := range names {
		if x == want {
			return i
		}
	}
	return -1
}

// childStructTypes returns the resolved standard types of an element's
// structure-element children (ignoring marked-content and object references).
func (d *Document) childStructTypes(elem *Dictionary, roleMap *Dictionary) []Name {
	var out []Name
	for _, kid := range d.structKids(elem) {
		child := d.ResolveDict(kid)
		if child == nil {
			continue
		}
		if _, hasS := child.Get("S").(Name); !hasS {
			continue
		}
		out = append(out, d.standardStructType(child, roleMap))
	}
	return out
}

func containsName(names []Name, n Name) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

func orList(names []Name) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			if i == len(names)-1 {
				s += " or "
			} else {
				s += ", "
			}
		}
		s += "<" + string(n) + ">"
	}
	return s
}

// walkStructElems invokes fn for every structure element (with an /S type) in
// the tree, passing its role-map-resolved standard type.
func (d *Document) walkStructElems(cat *Dictionary, fn func(elem *Dictionary, stdType Name)) {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	seen := map[int]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		if _, hasS := elem.Get("S").(Name); hasS {
			fn(elem, d.standardStructType(elem, roleMap))
		}
		for _, kid := range d.structKids(elem) {
			walk(kid)
		}
	}
	walk(root.Get("K"))
}

// checkUAHeaderVersion: PDF/UA-1 is defined against PDF 1.7, so the header must
// declare a 1.n version.
func (d *Document) checkUAHeaderVersion() []UAViolation {
	if len(d.Version) >= 2 && d.Version[0] == '1' && d.Version[1] == '.' {
		return nil
	}
	return []UAViolation{{"6.1", "PDF/UA-1 requires a PDF 1.x header, got " + d.Version, 0}}
}

// checkUASuspects: a MarkInfo /Suspects value of true means the tagging may be
// unreliable and is not permitted.
func (d *Document) checkUASuspects(cat *Dictionary) []UAViolation {
	if mark := d.ResolveDict(cat.Get("MarkInfo")); mark != nil && d.isTrue(mark.Get("Suspects")) {
		return []UAViolation{{"7.1", "/MarkInfo /Suspects must not be true", 0}}
	}
	return nil
}

// checkUAStrongWeak: a document must be either strongly structured (H1–H6) or
// weakly structured (H), not both (7.4.4).
func (d *Document) checkUAStrongWeak(cat *Dictionary) []UAViolation {
	var hasH, hasHn bool
	d.walkStructElems(cat, func(_ *Dictionary, t Name) {
		switch {
		case t == "H":
			hasH = true
		case len(t) == 2 && t[0] == 'H' && t[1] >= '1' && t[1] <= '6':
			hasHn = true
		}
	})
	if hasH && hasHn {
		return []UAViolation{{"7.4.4", "document mixes <H> and <H1>–<H6> headings; it must be either strongly or weakly structured", 0}}
	}
	return nil
}

// checkUANotes: every Note structure element must carry a unique /ID (7.9).
func (d *Document) checkUANotes(cat *Dictionary) []UAViolation {
	var v []UAViolation
	ids := map[string]bool{}
	d.walkStructElems(cat, func(elem *Dictionary, t Name) {
		if t != "Note" {
			return
		}
		id, _ := d.Resolve(elem.Get("ID")).(String)
		if len(id.Value) == 0 {
			v = append(v, UAViolation{"7.9", "<Note> structure element has no /ID", 0})
			return
		}
		if ids[string(id.Value)] {
			v = append(v, UAViolation{"7.9", "<Note> structure elements share a non-unique /ID", 0})
		}
		ids[string(id.Value)] = true
	})
	return v
}
