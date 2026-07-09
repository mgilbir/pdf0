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
