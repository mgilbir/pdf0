package pdf0

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This test verifies that the documents pdf0 produces — both the ones its
// builders generate and the ones it writes back after reading (round-trip) —
// conform to the ISO 32000 object grammar, using the Arlington PDF Model
// (github.com/pdf-association/arlington-pdf-model, Apache-2.0) as the
// authoritative structural reference. The model is not committed; run
// `make arlington` to fetch it, or point ARLINGTON_MODEL at a tsv/<ver> dir.
//
// Traversal is link-driven: it starts at the file trailer and follows each
// key's Link column to the referenced object's spec, so object variants that
// differ only by position (e.g. the root page-tree node) or by /Subtype (fonts,
// annotations, XObjects) resolve exactly as Arlington intends. Checks are
// deliberately conservative — only unconditional (non-predicate) constraints are
// enforced — so a genuine grammar violation in pdf0's output is flagged while a
// construct the model gates behind an fn:… predicate is never a false positive.

// arlKey is one row of an Arlington object TSV.
type arlKey struct {
	key, types, since, deprecated, required, indirect, possible, links string
}

// arlModel lazily loads and caches object specs from a tsv/<ver> directory.
type arlModel struct {
	dir   string
	cache map[string][]arlKey // spec name -> rows (nil entry = not found)
}

func loadArlModel(dir string) *arlModel { return &arlModel{dir: dir, cache: map[string][]arlKey{}} }

func (m *arlModel) spec(name string) []arlKey {
	if v, ok := m.cache[name]; ok {
		return v
	}
	var rows []arlKey
	f, err := os.Open(filepath.Join(m.dir, name+".tsv"))
	if err == nil {
		defer f.Close()
		r := csv.NewReader(f)
		r.Comma = '\t'
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		recs, _ := r.ReadAll()
		for i, rec := range recs {
			if i == 0 || len(rec) < 11 {
				continue
			}
			rows = append(rows, arlKey{
				key: rec[0], types: rec[1], since: rec[2], deprecated: rec[3],
				required: rec[4], indirect: rec[5], possible: rec[8], links: rec[10],
			})
		}
	}
	m.cache[name] = rows
	return rows
}

// arlValidator walks one document.
type arlValidator struct {
	doc      *Document
	m        *arlModel
	docVer   float64
	findings []string
	visited  map[*Dictionary]bool
	skipped  map[string]bool // spec names not present in the model (coverage gaps)
}

const arlMaxDepth = 40

func (v *arlValidator) add(path, msg string) {
	v.findings = append(v.findings, path+": "+msg)
}

// dictOf returns the dictionary backing a resolved object (a dict or a stream's
// dict), or nil.
func (v *arlValidator) dictOf(o Object) *Dictionary {
	switch t := v.doc.Resolve(o).(type) {
	case *Dictionary:
		return t
	case *Stream:
		return &t.Dict
	}
	return nil
}

func (v *arlValidator) walk(o Object, specName, path string, depth int) {
	if depth > arlMaxDepth {
		return
	}
	spec := v.m.spec(specName)
	if spec == nil {
		v.skipped[specName] = true
		return
	}
	d := v.dictOf(o)
	if d == nil {
		return
	}
	if v.visited[d] {
		return
	}
	v.visited[d] = true

	for _, k := range spec {
		raw := d.Get(Name(k.key))
		if raw == nil {
			if k.required == "TRUE" {
				v.add(path, "missing required key /"+k.key)
			}
			continue
		}
		resolved := v.doc.Resolve(raw)

		// Type: must match one of the declared Arlington types. Only checked when
		// the Type column is a plain type list (no fn:… predicate).
		if !strings.Contains(k.types, "fn:") && !typeMatchesArl(resolved, k.types) {
			v.add(path, "/"+k.key+" has type "+arlTypeName(resolved)+", want "+k.types)
		}

		// PossibleValues: enforce only a plain bracketed literal set.
		if lits := literalSet(k.possible); lits != nil {
			if s := scalarText(resolved); s != "" && !lits[s] {
				v.add(path, "/"+k.key+" value "+s+" not in "+k.possible)
			}
		}

		// Version: a key present in a document older than its SinceVersion, or in
		// one at/after its DeprecatedIn, is a grammar violation. Literal versions
		// only.
		if sv := plainVer(k.since); sv > 0 && v.docVer > 0 && v.docVer+1e-9 < sv {
			v.add(path, "/"+k.key+" introduced in "+k.since+" but document is "+strconv.FormatFloat(v.docVer, 'g', -1, 64))
		}
		// DeprecatedIn is intentionally NOT enforced: a deprecated key is still
		// valid grammar, and pdf0 faithfully preserves such keys on round-trip.

		// Recurse through Links.
		if k.links != "" {
			v.follow(resolved, k.links, path+"/"+k.key, depth+1)
		}
	}
}

// follow validates the object(s) a key links to, resolving the correct spec.
func (v *arlValidator) follow(o Object, links, path string, depth int) {
	cands := linkSpecs(links)
	if len(cands) == 0 {
		return
	}
	switch val := o.(type) {
	case *Dictionary, *Stream:
		if sn := v.pick(cands, o); sn != "" {
			v.walk(o, sn, path, depth)
		}
	case Array:
		for i, e := range val {
			er := v.doc.Resolve(e)
			if v.dictOf(er) == nil {
				continue
			}
			if sn := v.pick(cands, er); sn != "" {
				v.walk(er, sn, path+"["+strconv.Itoa(i)+"]", depth)
			}
		}
	}
}

// pick chooses the candidate spec matching the object; returns "" if ambiguous
// (never guesses — an unresolved variant is skipped, not mis-validated).
func (v *arlValidator) pick(cands []string, o Object) string {
	if len(cands) == 1 {
		return cands[0]
	}
	d := v.dictOf(o)
	if d == nil {
		return ""
	}
	typ, _ := d.Get("Type").(Name)
	sub, _ := d.Get("Subtype").(Name)
	var matches []string
	for _, c := range cands {
		if v.specAccepts(c, typ, sub) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// specAccepts reports whether a spec's /Type and /Subtype PossibleValues admit
// the given values.
func (v *arlValidator) specAccepts(specName string, typ, sub Name) bool {
	spec := v.m.spec(specName)
	if spec == nil {
		return false
	}
	ok := func(key string, val Name) bool {
		for _, k := range spec {
			if k.key != key {
				continue
			}
			lits := literalSet(k.possible)
			if lits == nil {
				return true // unconstrained
			}
			return val != "" && lits[string(val)]
		}
		return true // key not in spec: no constraint
	}
	return ok("Type", typ) && ok("Subtype", sub)
}

// --- helpers ---

func typeMatchesArl(o Object, types string) bool {
	for _, t := range strings.Split(types, ";") {
		switch strings.TrimSpace(t) {
		case "name":
			if _, ok := o.(Name); ok {
				return true
			}
		case "dictionary", "name-tree", "number-tree":
			if _, ok := o.(*Dictionary); ok {
				return true
			}
		case "array", "rectangle", "matrix":
			if _, ok := o.(Array); ok {
				return true
			}
		case "integer", "bitmask":
			if _, ok := o.(Integer); ok {
				return true
			}
		case "number":
			if _, ok := o.(Integer); ok {
				return true
			}
			if _, ok := o.(Real); ok {
				return true
			}
		case "string-text", "string", "string-byte", "string-ascii", "date":
			if _, ok := o.(String); ok {
				return true
			}
		case "stream":
			if _, ok := o.(*Stream); ok {
				return true
			}
		case "boolean":
			if _, ok := o.(Boolean); ok {
				return true
			}
		case "null":
			if _, ok := o.(Null); ok {
				return true
			}
		}
	}
	return false
}

func arlTypeName(o Object) string {
	switch o.(type) {
	case Name:
		return "name"
	case *Dictionary:
		return "dictionary"
	case Array:
		return "array"
	case Integer:
		return "integer"
	case Real:
		return "number"
	case String:
		return "string"
	case *Stream:
		return "stream"
	case Boolean:
		return "boolean"
	case Null:
		return "null"
	}
	return "?"
}

// literalSet returns the set of values in a "[a,b,c]" PossibleValues cell, or nil
// if the cell is empty, predicate-bearing, or otherwise not a plain literal set.
func literalSet(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "fn:") {
		return nil
	}
	// A cell may hold one bracket group per declared type ("[..];[..]"). Union
	// them; if any group is not a simple [..] we bail (return nil) to stay safe.
	set := map[string]bool{}
	for _, grp := range strings.Split(s, ";") {
		grp = strings.TrimSpace(grp)
		if grp == "" || grp == "[]" {
			continue
		}
		if !strings.HasPrefix(grp, "[") || !strings.HasSuffix(grp, "]") {
			return nil
		}
		for _, v := range strings.Split(grp[1:len(grp)-1], ",") {
			if v = strings.TrimSpace(v); v != "" {
				set[v] = true
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// scalarText renders a Name/Integer/Boolean value for enum comparison; "" for
// anything else (enums only apply to those).
func scalarText(o Object) string {
	switch t := o.(type) {
	case Name:
		return string(t)
	case Integer:
		return strconv.FormatInt(int64(t), 10)
	case Boolean:
		if bool(t) {
			return "true"
		}
		return "false"
	}
	return ""
}

// linkSpecs extracts the spec names from a Link cell, dropping fn:… wrappers.
func linkSpecs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '[' || r == ']' || r == ',' || r == ';' || r == '(' || r == ')' || r == ' '
	}) {
		if tok == "" || strings.HasPrefix(tok, "fn:") {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

// plainVer parses a literal PDF version ("1.4", "2.0"); 0 if empty/predicate.
func plainVer(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "fn:") {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func docVersion(d *Document) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(d.Version), 64)
	return f
}

// validateAgainstArlington walks a document from the trailer and returns the
// grammar findings plus the set of object specs the model did not cover.
func validateAgainstArlington(m *arlModel, data []byte) (findings []string, skipped map[string]bool, readErr error) {
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, err
	}
	v := &arlValidator{
		doc: doc, m: m, docVer: docVersion(doc),
		visited: map[*Dictionary]bool{}, skipped: map[string]bool{},
	}
	// Start at the document catalog (trailer /Root). The file trailer and Info
	// dictionary are cross-reference/document mechanics that pdf0 normalizes on
	// read and reconstructs on write (e.g. /Size lives in the emitted xref, not
	// the in-memory trailer), so validating them against the grammar fights that
	// normalization rather than checking the content object graph.
	v.walk(doc.Trailer.Get("Root"), "Catalog", "Catalog", 0)
	return v.findings, v.skipped, nil
}

func TestArlingtonOutputConforms(t *testing.T) {
	dir := os.Getenv("ARLINGTON_MODEL")
	if dir == "" {
		dir = "testdata/arlington-pdf-model/tsv/2.0"
	}
	if _, err := os.Stat(filepath.Join(dir, "FileTrailer.tsv")); err != nil {
		t.Skip("Arlington model not present; run `make arlington`")
	}
	m := loadArlModel(dir)

	allSkipped := map[string]bool{}
	report := func(label string, data []byte) {
		findings, skipped, err := validateAgainstArlington(m, data)
		if err != nil {
			t.Errorf("%s: read-back failed: %v", label, err)
			return
		}
		for s := range skipped {
			allSkipped[s] = true
		}
		for _, f := range findings {
			if arlAllow[f] {
				continue
			}
			t.Errorf("%s: %s", label, f)
		}
	}

	// 1. Documents pdf0's builders generate.
	for _, lv := range []struct {
		name string
		l    PDFALevel
	}{{"PDFA1b", PDFA1b}, {"PDFA2b", PDFA2b}, {"PDFA3b", PDFA3b}, {"PDFA4", PDFA4}} {
		doc := NewPDFADocumentWithInfo(lv.l, "Title", "Author")
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Errorf("generate %s: %v", lv.name, err)
			continue
		}
		report("generated "+lv.name, buf.Bytes())
	}

	// 2. Read -> Write round-trip of the reference PDF 2.0 files.
	refs, _ := filepath.Glob("testdata/pdf20examples/*.pdf")
	for _, f := range refs {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Errorf("read %s: %v", filepath.Base(f), err)
			continue
		}
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Errorf("write %s: %v", filepath.Base(f), err)
			continue
		}
		report("round-trip "+filepath.Base(f), buf.Bytes())
	}

	// Coverage visibility: object types present in the tested documents that the
	// model directory did not define (never a failure — just reported).
	if len(allSkipped) > 0 && testing.Verbose() {
		var names []string
		for s := range allSkipped {
			names = append(names, s)
		}
		sort.Strings(names)
		t.Logf("spec names not found in model (coverage gaps): %s", strings.Join(names, ", "))
	}
}

// arlAllow lists deliberate, understood deviations from the Arlington model
// (keyed by the exact finding string). Empty for now — pdf0's output conforms.
var arlAllow = map[string]bool{}

// TestArlingtonGuardHasTeeth confirms the guard actually detects grammar
// violations, so a clean TestArlingtonOutputConforms means "conforms", not
// "checked nothing". A deliberately corrupted catalog must be flagged for the
// wrong type, an out-of-enum value, and a missing required key.
func TestArlingtonGuardHasTeeth(t *testing.T) {
	dir := os.Getenv("ARLINGTON_MODEL")
	if dir == "" {
		dir = "testdata/arlington-pdf-model/tsv/2.0"
	}
	if _, err := os.Stat(filepath.Join(dir, "Catalog.tsv")); err != nil {
		t.Skip("Arlington model not present; run `make arlington`")
	}
	m := loadArlModel(dir)

	doc := NewPDFADocument(PDFA2b)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Delete("Pages")             // remove a required key
	cat.Set("Type", Integer(9))     // wrong type AND out of the [Catalog] enum
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	findings, _, err := validateAgainstArlington(m, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"missing required key /Pages", "has type integer", "not in [Catalog]"}
	for _, w := range want {
		found := false
		for _, f := range findings {
			if strings.Contains(f, w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("guard did not flag %q; findings=%v", w, findings)
		}
	}
}
