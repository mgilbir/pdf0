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

// This test uses the Arlington PDF Model (github.com/pdf-association/
// arlington-pdf-model, Apache-2.0) as an EXTERNAL oracle for the shape of PDF
// objects, to check that pdf0's parser and serializer represent objects
// FAITHFULLY: right value types, keys present, structure intact. The existing
// round-trip tests compare pdf0's model against itself (self-consistent, not
// necessarily correct); Arlington is ground truth from outside pdf0, so it
// catches structural mis-parsing a self-referential check cannot — e.g. reading
// a page's /MediaBox array as a string would round-trip cleanly yet violate the
// grammar. The model is not committed; run `make arlington` to fetch it.
//
// Traversal is link-driven from the catalog: each key's Link column resolves the
// referenced object's spec, so variants (page-tree root, font/annotation/action
// subtypes) are validated against the right rules. It handles array specs,
// inheritable keys (a page may inherit /Resources, /MediaBox from an ancestor),
// and disambiguates by /Type, /Subtype and /S. Checks are conservative — only
// unconditional constraints (required=TRUE, literal type/PossibleValues) are
// enforced; predicate-gated rules and version-appropriateness (not a parser
// concern) are skipped — so a finding means a genuine structural discrepancy.

// --- Arlington model ---

type arlRow struct {
	key, types, required, inheritable, possible, links string
}

type arlModel struct {
	dir   string
	cache map[string][]arlRow
}

func loadArlModel(dir string) *arlModel { return &arlModel{dir, map[string][]arlRow{}} }

func (m *arlModel) spec(name string) []arlRow {
	if v, ok := m.cache[name]; ok {
		return v
	}
	var rows []arlRow
	if f, err := os.Open(filepath.Join(m.dir, name+".tsv")); err == nil {
		r := csv.NewReader(f)
		r.Comma = '\t'
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		recs, _ := r.ReadAll()
		f.Close()
		for i, c := range recs {
			if i == 0 || len(c) < 11 {
				continue
			}
			rows = append(rows, arlRow{key: c[0], types: c[1], required: c[4], inheritable: c[6], possible: c[8], links: c[10]})
		}
	}
	m.cache[name] = rows
	return rows
}

// --- validator ---

type arlValidator struct {
	doc      *Document
	m        *arlModel
	findings []string
	visited  map[*Dictionary]bool
}

const arlMaxDepth = 50

func (v *arlValidator) add(path, msg string) { v.findings = append(v.findings, path+": "+msg) }

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

func arlTypeMatch(o Object, types string) bool {
	for _, t := range strings.Split(types, ";") {
		switch strings.TrimSpace(t) {
		case "name":
			if _, k := o.(Name); k {
				return true
			}
		case "dictionary", "name-tree", "number-tree":
			if _, k := o.(*Dictionary); k {
				return true
			}
		case "array", "rectangle", "matrix":
			if _, k := o.(Array); k {
				return true
			}
		case "integer", "bitmask":
			if _, k := o.(Integer); k {
				return true
			}
		case "number":
			if _, k := o.(Integer); k {
				return true
			}
			if _, k := o.(Real); k {
				return true
			}
		case "string-text", "string", "string-byte", "string-ascii", "date":
			if _, k := o.(String); k {
				return true
			}
		case "stream":
			if _, k := o.(*Stream); k {
				return true
			}
		case "boolean":
			if _, k := o.(Boolean); k {
				return true
			}
		case "null":
			if _, k := o.(Null); k {
				return true
			}
		}
	}
	return false
}

func arlLitSet(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "fn:") {
		return nil
	}
	set := map[string]bool{}
	for _, g := range strings.Split(s, ";") {
		g = strings.TrimSpace(g)
		if g == "" || g == "[]" {
			continue
		}
		if !strings.HasPrefix(g, "[") || !strings.HasSuffix(g, "]") {
			return nil
		}
		for _, val := range strings.Split(g[1:len(g)-1], ",") {
			if val = strings.TrimSpace(val); val != "" {
				set[val] = true
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func arlLinkSpecs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune("[],;() ", r) }) {
		if t == "" || strings.HasPrefix(t, "fn:") || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func arlIsArraySpec(spec []arlRow) bool {
	for _, r := range spec {
		if r.key == "*" {
			return true
		}
		if _, err := strconv.Atoi(r.key); err == nil {
			return true
		}
	}
	return false
}

func (v *arlValidator) dictOf(o Object) *Dictionary {
	switch t := o.(type) {
	case *Dictionary:
		return t
	case *Stream:
		return &t.Dict
	}
	return nil
}

func (v *arlValidator) walk(o Object, specName, path string, depth int, inh map[string]bool) {
	if depth > arlMaxDepth {
		return
	}
	spec := v.m.spec(specName)
	if spec == nil {
		return
	}
	o = v.doc.Resolve(o)

	if arlIsArraySpec(spec) {
		arr, ok := o.(Array)
		if !ok {
			return
		}
		var wild *arlRow
		for i := range spec {
			if spec[i].key == "*" {
				wild = &spec[i]
			}
		}
		for i, e := range arr {
			er := v.doc.Resolve(e)
			r := wild
			for j := range spec {
				if spec[j].key == strconv.Itoa(i) {
					r = &spec[j]
				}
			}
			if r == nil {
				continue
			}
			if r.types != "" && !strings.Contains(r.types, "fn:") && !arlTypeMatch(er, r.types) {
				v.add(path, "["+strconv.Itoa(i)+"] type "+arlTypeName(er)+", want "+r.types)
			}
			if r.links != "" {
				v.follow(er, r.links, path+"["+strconv.Itoa(i)+"]", depth+1, inh)
			}
		}
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

	// Inheritable keys satisfied at or above this node, passed to children.
	child := map[string]bool{}
	for k := range inh {
		child[k] = true
	}
	for _, r := range spec {
		if r.inheritable == "TRUE" && d.Get(Name(r.key)) != nil {
			child[r.key] = true
		}
	}

	for _, r := range spec {
		raw := d.Get(Name(r.key))
		if raw == nil {
			if r.required == "TRUE" && !(r.inheritable == "TRUE" && child[r.key]) {
				v.add(path, "missing required /"+r.key)
			}
			continue
		}
		res := v.doc.Resolve(raw)
		if r.types != "" && !strings.Contains(r.types, "fn:") && !arlTypeMatch(res, r.types) {
			v.add(path, "/"+r.key+" type "+arlTypeName(res)+", want "+r.types)
		}
		if ls := arlLitSet(r.possible); ls != nil {
			if n, ok := res.(Name); ok && !ls[string(n)] {
				v.add(path, "/"+r.key+" value /"+string(n)+" not in "+r.possible)
			}
		}
		if r.links != "" {
			v.follow(res, r.links, path+"/"+r.key, depth+1, child)
		}
	}
}

func (v *arlValidator) follow(o Object, links, path string, depth int, inh map[string]bool) {
	cands := arlLinkSpecs(links)
	if len(cands) == 0 {
		return
	}
	o = v.doc.Resolve(o)
	if sn := v.pick(cands, o); sn != "" {
		v.walk(o, sn, path, depth, inh)
	}
}

// pick returns the unique candidate spec that matches the object, or "" if none
// or several do — it never guesses, so an unresolved variant is skipped rather
// than validated against the wrong rules.
func (v *arlValidator) pick(cands []string, o Object) string {
	if _, isArr := o.(Array); isArr {
		var arrs []string
		for _, c := range cands {
			if arlIsArraySpec(v.m.spec(c)) {
				arrs = append(arrs, c)
			}
		}
		if len(arrs) == 1 {
			return arrs[0]
		}
		return ""
	}
	if len(cands) == 1 {
		return cands[0]
	}
	d := v.dictOf(o)
	if d == nil {
		return ""
	}
	typ, _ := d.Get("Type").(Name)
	sub, _ := d.Get("Subtype").(Name)
	sAct, _ := d.Get("S").(Name)
	accepts := func(spec []arlRow, key string, val Name) bool {
		for _, r := range spec {
			if r.key == key {
				ls := arlLitSet(r.possible)
				if ls == nil {
					return true
				}
				return val != "" && ls[string(val)]
			}
		}
		return true
	}
	var match []string
	for _, c := range cands {
		spec := v.m.spec(c)
		if spec == nil {
			continue
		}
		if accepts(spec, "Type", typ) && accepts(spec, "Subtype", sub) && accepts(spec, "S", sAct) {
			match = append(match, c)
		}
	}
	if len(match) == 1 {
		return match[0]
	}
	return ""
}

// arlValidate walks a document from the catalog and returns its structural
// findings against the grammar.
func arlValidate(m *arlModel, data []byte) (findings []string, readErr error) {
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	v := &arlValidator{doc: doc, m: m, visited: map[*Dictionary]bool{}}
	v.walk(doc.Trailer.Get("Root"), "Catalog", "Catalog", 0, map[string]bool{})
	return v.findings, nil
}

func arlModelDir(t *testing.T) string {
	dir := os.Getenv("ARLINGTON_MODEL")
	if dir == "" {
		dir = "testdata/arlington-pdf-model/tsv/2.0"
	}
	if _, err := os.Stat(filepath.Join(dir, "Catalog.tsv")); err != nil {
		t.Skip("Arlington model not present; run `make arlington`")
	}
	return dir
}

// TestArlingtonParserFaithful checks that pdf0 represents known-conforming
// documents faithfully: the objects its builders emit, and the reference PDF 2.0
// files after a Read->Write round-trip, must all conform to the grammar. A
// finding here is a parser or serializer bug (the inputs are conformant).
func TestArlingtonParserFaithful(t *testing.T) {
	m := loadArlModel(arlModelDir(t))

	check := func(label string, data []byte) {
		findings, err := arlValidate(m, data)
		if err != nil {
			t.Errorf("%s: read-back failed: %v", label, err)
			return
		}
		for _, f := range findings {
			t.Errorf("%s: %s", label, f)
		}
	}

	// Builder output.
	for _, lv := range []struct {
		name string
		l    PDFALevel
	}{{"PDFA1b", PDFA1b}, {"PDFA2b", PDFA2b}, {"PDFA3b", PDFA3b}, {"PDFA4", PDFA4}} {
		var buf bytes.Buffer
		if err := NewPDFADocumentWithInfo(lv.l, "Title", "Author").Write(&buf); err != nil {
			t.Errorf("generate %s: %v", lv.name, err)
			continue
		}
		check("generated "+lv.name, buf.Bytes())
	}

	// Reference PDFs: parse-check and Read->Write round-trip-check.
	refs, _ := filepath.Glob("testdata/pdf20examples/*.pdf")
	for _, f := range refs {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		check("parse "+filepath.Base(f), data)
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
		check("round-trip "+filepath.Base(f), buf.Bytes())
	}
}

// arlCorpusPassBaseline bounds how many conformant (-pass-) veraPDF corpus files
// may still produce a structural finding. These are residual imprecisions in
// this oracle (a handful of inheritance / action-subtype-disambiguation edge
// cases and files that legitimately carry an off-grammar value the profile
// tolerates) — NOT parser bugs (every one was traced to the input, not to pdf0
// mangling it). It is a ratchet: if pdf0's parser ever starts mangling the
// structure of conformant files, the count rises above the baseline and this
// fails. Lower it when the oracle's edge cases are tightened.
const arlCorpusPassBaseline = 5

// TestArlingtonCorpusParserFaithful runs the structural oracle over the whole
// veraPDF corpus. On the conformant (-pass-) files it asserts the number with
// findings stays at or below the baseline (a parser regression would push it
// up). Findings on -fail- files are expected and reported for information: the
// oracle independently detects the malformations those files inject.
func TestArlingtonCorpusParserFaithful(t *testing.T) {
	m := loadArlModel(arlModelDir(t))
	corpus := os.Getenv("VERAPDF_CORPUS")
	if corpus == "" {
		corpus = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpus); os.IsNotExist(err) {
		t.Skip("veraPDF corpus not present; run `make corpus`")
	}

	var passTotal, passWith, failWith int
	passFindings := map[string]int{}
	filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".pdf") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		findings, verr := arlValidate(m, data)
		if verr != nil {
			return nil
		}
		isPass := strings.Contains(filepath.Base(p), "-pass-")
		if isPass {
			passTotal++
			if len(findings) > 0 {
				passWith++
				rel, _ := filepath.Rel(corpus, p)
				passFindings[rel] = len(findings)
			}
		} else if len(findings) > 0 {
			failWith++
		}
		return nil
	})

	t.Logf("conformant files=%d with structural finding=%d (baseline %d); malformed files with finding=%d (expected)",
		passTotal, passWith, arlCorpusPassBaseline, failWith)
	if passWith > arlCorpusPassBaseline {
		var names []string
		for n := range passFindings {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Errorf("%d conformant files produce structural findings, above baseline %d — a parser faithfulness regression. Offending files:\n  %s",
			passWith, arlCorpusPassBaseline, strings.Join(names, "\n  "))
	}
}

// TestArlingtonOracleHasTeeth confirms the oracle detects structural corruption,
// so a clean TestArlingtonParserFaithful means "faithful", not "checked nothing".
func TestArlingtonOracleHasTeeth(t *testing.T) {
	m := loadArlModel(arlModelDir(t))
	doc := NewPDFADocument(PDFA2b)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Delete("Pages")             // required-key check
	cat.Set("Type", Name("Bogus"))  // enum check: /Type not in [Catalog]
	cat.Set("Metadata", Name("no")) // type check: /Metadata must be a stream, not a name
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	findings, err := arlValidate(m, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"missing required /Pages", "/Type value /Bogus not in [Catalog]", "/Metadata type name"} {
		found := false
		for _, f := range findings {
			if strings.Contains(f, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("oracle did not flag %q; findings=%v", want, findings)
		}
	}
}
