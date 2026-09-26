package lint

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDocCodeSpansResolve checks that what the documentation names in
// backticks exists: every `path/to/file.go` (and its :line, if given), every
// `pkg.Name` and `Type.Member`, every `TestName` or `FuzzName`, and every
// unexported identifier (`camelCase`) or call (`Name(...)`). The package split
// moved nineteen root files and renamed about twenty-five identifiers, and the
// contributor docs kept describing the flat layout for weeks, because
// check-links resolves links and nothing resolved code (audit 2026-09-22
// C108).
//
// Names resolve against the module's source, test files and every build tag
// included, and against the source of the modules the docs point into
// (forme, formalis, golittlecms, gopenjpeg) — a doc may say where a thing
// went. A path resolves against the repository root, the document's own
// directory, or one of those modules' roots. A bare file name must be at the
// root: `pdfa.go` is not `pdfa/pdfa.go`, and the README's file table said the
// first when it meant the second.
//
// Documents under historicalDocs describe the tree as it was, and are not
// checked. Words that look like Go identifiers but name something else — an
// X.509 key usage, a CFF operator, an XMP property — are in notGoNames, each
// with what it is.
func TestDocCodeSpansResolve(t *testing.T) {
	idx := loadDeclIndex(t)
	if err := loadModulePackageNames(); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, rel := range markdownFiles(t) {
		if isHistorical(rel) {
			continue
		}
		for _, s := range readMarkdown(t, rel).spans {
			ok, applies, why := idx.resolveSpan(rel, s.text)
			if !applies {
				continue
			}
			checked++
			if !ok {
				t.Errorf("%s:%d: `%s` %s", s.file, s.line, s.text, why)
			}
		}
	}
	if checked < 500 {
		t.Fatalf("resolved only %d code spans; the scan is looking in the wrong place", checked)
	}
	t.Logf("resolved %d code spans", checked)
}

// notGoNames are identifier-shaped words the docs put in backticks that are
// not Go names in this module or its siblings. Each says what it is.
var notGoNames = map[string]string{
	// X.509 (RFC 5280) key usages and extended key usages, by their ASN.1
	// names; Go spells them x509.KeyUsageDigitalSignature and so on.
	"digitalSignature":    "RFC 5280 key usage",
	"contentCommitment":   "RFC 5280 key usage (formerly nonRepudiation)",
	"serverAuth":          "RFC 5280 extended key usage",
	"emailProtection":     "RFC 5280 extended key usage",
	"anyExtendedKeyUsage": "RFC 5280 extended key usage",
	"nextUpdate":          "RFC 5280 CRL field / RFC 6960 OCSP field",
	"ETSI.CAdES.detached": "a PDF signature /SubFilter value",
	// Glyph-name conventions (Adobe Glyph List specification).
	"uniXXXX": "AGL glyph-name form",
	"uXXXX":   "AGL glyph-name form",
	// XMP (ISO 16684-1) and PDF/A extension-schema vocabulary.
	"xmpTPg":        "XMP namespace prefix (xmp Paged-Text)",
	"pdfaExtension": "PDF/A extension schema namespace prefix",
	"pdfaSchema":    "PDF/A extension schema namespace prefix",
	"pdfaProperty":  "PDF/A extension schema namespace prefix",
	"pdfaType":      "PDF/A extension schema namespace prefix",
	"pdfaField":     "PDF/A extension schema namespace prefix",
	"namespaceURI":  "PDF/A extension schema field (pdfaSchema:namespaceURI)",
	// Placeholders standing for any validator in a signature pattern.
	"ValidateX(doc *Document, …)":                             "placeholder for any validator's signature",
	"ValidateXContext(ctx context.Context, doc *Document, …)": "placeholder for any validator's Context signature",
	// JBIG2 (ITU-T T.88) decoding-procedure and context names.
	"IADH": "JBIG2 integer decoding procedure", "IADW": "JBIG2 integer decoding procedure",
	"IAEX": "JBIG2 integer decoding procedure", "IAID": "JBIG2 symbol ID decoding procedure",
	"SBREFINE": "JBIG2 text region flag", "SDREFAGG": "JBIG2 symbol dictionary flag",
	"SDHUFF": "JBIG2 symbol dictionary flag", "SBHUFF": "JBIG2 text region flag",
	// ISO 32000-2 Table 225, the /SigFlags bits, by the spec's names.
	"SignaturesExist": "a /SigFlags bit", "AppendOnly": "a /SigFlags bit",
	// Placeholders for the numbered field names the signer generates.
	"SignatureN": "a field-name pattern", "TimestampN": "a field-name pattern",
	// The Go runtime's environment variable.
	"GOMEMLIMIT": "Go runtime environment variable",
	// CMS (RFC 5652), ESS (RFC 5035) and OCSP (RFC 6960) ASN.1 field names.
	"messageDigest": "CMS signed attribute", "signerInfos": "CMS SignedData field",
	"digestAlgorithms": "CMS SignedData field", "issuerAndSerialNumber": "CMS SignerIdentifier choice",
	"issuerSerial": "ESS ESSCertIDv2 field", "issuerKeyHash": "OCSP CertID field",
	// ICC.1 (ISO 15076-1) header field and data type names.
	"dataColourSpace": "ICC header field", "uInt32Number": "ICC basic type", "dateTimeNumber": "ICC basic type",
	// TrueType glyf table fields (OpenType spec).
	"numberOfContours": "glyf header field", "glyphIndex": "glyf composite component field",
	// Operand names in ISO 32000's colour-space array syntax.
	"alternateSpace": "Separation/DeviceN array operand", "altSpace": "Separation/DeviceN array operand",
	"tintTransform": "Separation/DeviceN array operand", "tintTransform1": "Separation/DeviceN array operand",
	"underlyingCS": "Pattern colour-space array operand",
	// Terms of ISO 32000-2's security-handler algorithms.
	"sAlT": "Algorithm 1's salt bytes", "userValSalt": "Algorithm 8's User Validation Salt",
	"userKeySalt": "Algorithm 8's User Key Salt",
	// veraPDF validation-profile object properties, quoted from rule tests.
	"containsEF": "veraPDF profile property", "hasParentFormulaOrMathML": "veraPDF profile property",
	// The word itself, in the checks that look for such words.
	"camelCase": "the word",
	// Pseudo-code for a key derivation step (ISO 32000-2 Algorithm 2).
	"MD5(pad ‖ /ID[0])": "pseudo-code for a hash input",
}

var (
	goPathSpan   = regexp.MustCompile(`^([\w.-]+/)*[\w.-]*\w\.go(:\d+([-,]\d+)*)?$`)
	dirSpan      = regexp.MustCompile(`^[\w.-]+(/[\w.-]+)*/$`)
	qualSpan     = regexp.MustCompile(`^([A-Za-z_]\w*)((?:\.[A-Za-z_]\w*)+)(\(.*\))?$`)
	testSpan     = regexp.MustCompile(`^(Test|Fuzz|Benchmark|Example)\w*(/\S*)?$`)
	camelSpan    = regexp.MustCompile(`^([a-z][a-z0-9]*[A-Z]\w*)(\(.*\))?$`)
	callSpan     = regexp.MustCompile(`^([A-Z]\w*)\(.*\)$`)
	bareSpan     = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)
	fileExtEnd   = regexp.MustCompile(`\.(pdf|mod|sum|py|md|sh|tsv|txt|json|ya?ml|xml|otf|ttf|zip|html?|css|xsd|rng|icc|icm|ok|gitignore)$`)
	lineSuffixRe = regexp.MustCompile(`:(\d+(?:[-,]\d+)*)$`)
)

// resolveSpan reports whether a code span resolves. applies is false for a
// span the check has no opinion on (prose, a command, a PDF name).
func (idx *declIndex) resolveSpan(doc, s string) (ok, applies bool, why string) {
	if reason, known := notGoNames[s]; known {
		_ = reason
		return true, false, ""
	}
	switch {
	case strings.HasPrefix(s, "go ") || strings.HasPrefix(s, "make "):
		return true, false, "" // TestDocCommandsRun
	case strings.HasPrefix(s, "_") || strings.HasPrefix(s, "*"):
		return true, false, ""
	case goPathSpan.MatchString(s):
		return idx.resolvePath(doc, s)
	case dirSpan.MatchString(s):
		if strings.HasPrefix(s, "testdata/") || strings.HasPrefix(s, "spec/") || strings.HasPrefix(s, ".") {
			return true, false, "" // fetched or placed data, absent from a fresh clone
		}
		if idx.pathExists(doc, strings.TrimSuffix(s, "/")) {
			return true, true, ""
		}
		return false, true, "names a directory that does not exist"
	case testSpan.MatchString(s):
		name, _, _ := strings.Cut(s, "/")
		for _, pds := range idx.byName {
			for _, pd := range pds {
				if pd.funcs[name] {
					return true, true, ""
				}
			}
		}
		return false, true, "names a test function that does not exist"
	case qualSpan.MatchString(s):
		m := qualSpan.FindStringSubmatch(s)
		if fileExtEnd.MatchString(m[1] + m[2]) {
			return true, false, ""
		}
		return idx.resolveQualified(m[1], strings.Split(m[2][1:], "."))
	case camelSpan.MatchString(s):
		name := camelSpan.FindStringSubmatch(s)[1]
		if idx.all[name] || idx.literal[name] {
			return true, true, ""
		}
		return false, true, "is not declared anywhere in this module or the modules it points into"
	case callSpan.MatchString(s):
		name := callSpan.FindStringSubmatch(s)[1]
		if idx.all[name] {
			return true, true, ""
		}
		return false, true, "calls a function that is not declared anywhere"
	case bareSpan.MatchString(s):
		// A bare capitalised word is a Go name or a PDF name. Either way the
		// code has it: as a declaration, or in a string literal. A removed
		// option (`WithMaxCIDRangeSpan`) or a renamed method has neither.
		if idx.all[s] || idx.literal[s] || idx.makeVars[s] {
			return true, true, ""
		}
		return false, true, "is neither declared nor a string the code uses"
	}
	return true, false, ""
}

func (idx *declIndex) resolveQualified(base string, rest []string) (ok, applies bool, why string) {
	// base is a package.
	if pds := idx.byName[base]; len(pds) > 0 {
		for _, pd := range pds {
			if !pd.top[rest[0]] {
				continue
			}
			if len(rest) == 1 || idx.hasMember([]*pkgDecls{pd}, rest[0], rest[1]) {
				return true, true, ""
			}
		}
		if len(rest) > 1 {
			return false, true, fmt.Sprintf("names %s.%s, which has no member %s", base, rest[0], rest[1])
		}
		return false, true, fmt.Sprintf("names %s, which package %s does not declare", rest[0], base)
	}
	// base is a type. A lower-case base that is not a package is read as a
	// variable the prose introduced (doc, level, r), even where an unexported
	// type happens to share its name.
	if _, isType := idx.types[base]; isType && base[0] >= 'A' && base[0] <= 'Z' {
		if idx.hasMember(nil, base, rest[0]) {
			return true, true, ""
		}
		return false, true, fmt.Sprintf("names a member %s that type %s does not have", rest[0], base)
	}
	// base is a variable the prose introduced (doc, r, level): the name
	// after the dot must at least exist somewhere.
	if idx.all[rest[0]] {
		return true, true, ""
	}
	if _, isStd := stdImports[base]; isStd {
		return false, true, fmt.Sprintf("names %s, which package %s does not declare", rest[0], base)
	}
	return false, true, fmt.Sprintf("names %s, which nothing declares", rest[0])
}

func (idx *declIndex) resolvePath(doc, s string) (ok, applies bool, why string) {
	p, lines := s, ""
	if m := lineSuffixRe.FindStringSubmatchIndex(s); m != nil {
		p, lines = s[:m[0]], s[m[2]:m[3]]
	}
	full := idx.findPath(doc, p)
	if full == "" {
		if !strings.Contains(p, "/") {
			return false, true, "is not a file at the repository root; give the path from the root"
		}
		return false, true, "names a file that does not exist"
	}
	if lines != "" {
		b, err := os.ReadFile(full)
		if err != nil {
			return false, true, err.Error()
		}
		n := bytes.Count(b, []byte("\n")) + 1
		for _, part := range regexp.MustCompile(`[-,]`).Split(lines, -1) {
			if v, _ := strconv.Atoi(part); v > n {
				return false, true, fmt.Sprintf("cites line %d of a %d-line file", v, n)
			}
		}
	}
	return true, true, ""
}

// findPath resolves a slash path cited in doc, or returns "".
func (idx *declIndex) findPath(doc, p string) string {
	fp := filepath.FromSlash(p)
	var cands []string
	if !strings.Contains(p, "/") {
		// A bare name is a root file.
		cands = append(cands, filepath.Join(idx.modRoot, fp))
	} else {
		cands = append(cands, filepath.Join(idx.modRoot, fp), filepath.Join(idx.modRoot, filepath.Dir(filepath.FromSlash(doc)), fp))
		first, after, _ := strings.Cut(p, "/")
		for i, r := range idx.roots[1:] {
			cands = append(cands, filepath.Join(r, fp))
			if first == filepath.Base(indexedModules[i]) {
				cands = append(cands, filepath.Join(r, filepath.FromSlash(after)))
			}
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func (idx *declIndex) pathExists(doc, p string) bool {
	if !strings.Contains(p, "/") {
		for _, r := range idx.roots {
			if _, err := os.Stat(filepath.Join(r, p)); err == nil {
				return true
			}
		}
		if _, err := os.Stat(filepath.Join(idx.modRoot, filepath.Dir(filepath.FromSlash(doc)), p)); err == nil {
			return true
		}
		return false
	}
	return idx.findPath(doc, p) != ""
}

// goToolOutput runs the go command and returns its standard output.
func goToolOutput(args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out), nil
}
