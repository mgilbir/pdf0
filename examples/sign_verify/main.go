// Command sign_verify signs a one-page document with a self-signed certificate
// generated in-process and verifies it, showing the verdict a caller must
// actually read: DocumentUnmodified (Valid AND CoversWholeDocument) plus
// TrustedChain from VerifySignaturesWithRoots.
//
// It then modifies the signed file with an incremental update — the attack
// Valid alone does not catch — and shows Valid staying true while
// DocumentUnmodified goes false.
//
// Nothing is read from or written to disk and no network is used. The program
// exits non-zero if any verdict differs from what it expects, so it works as a
// CI guard. See docs/signing.md.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"os"
	"time"

	pdf "github.com/mgilbir/pdf0"
)

func main() {
	// A self-signed signer. In production this is your real certificate and a
	// key from a file, a PKCS#11 token or an HSM — anything implementing
	// crypto.Signer. pdf0 signs with SHA-256 and supports RSA and ECDSA keys.
	cert, key, err := selfSignedSigner("pdf0 example signer")
	if err != nil {
		fail("generating the signing certificate: %v", err)
	}

	// Sign. WriteSigned adds the signature field, computes the /ByteRange over
	// everything except the /Contents window and fills in a detached CMS
	// (PAdES B-B: /SubFilter /ETSI.CAdES.detached).
	var signedBuf bytes.Buffer
	if err := newDocument().WriteSigned(&signedBuf, cert, key); err != nil {
		fail("WriteSigned: %v", err)
	}
	signed := signedBuf.Bytes()
	fmt.Printf("signed %d bytes\n", len(signed))

	// Verify. Pass the EXACT bytes of the file: the digest is recomputed over
	// them, not over the parsed object model.
	roots := x509.NewCertPool()
	roots.AddCert(cert) // trust anchor; with a real CA, load its root here
	doc, err := pdf.Read(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		fail("re-reading the signed document: %v", err)
	}
	results := doc.VerifySignaturesWithRoots(signed, roots)
	if len(results) != 1 {
		fail("expected 1 signature, got %d", len(results))
	}
	r := results[0]
	report("as signed", r)

	// This is the verdict to branch on. Valid alone is NOT enough, and without
	// VerifySignaturesWithRoots the signer is never checked against any root.
	if !r.DocumentUnmodified() || !r.TrustedChain {
		fail("the freshly signed document should be unmodified and trusted (err=%v chainErr=%v)", r.Err, r.ChainErr)
	}

	// Now modify the signed file the way an attacker would: an incremental
	// update that leaves the signed byte range byte-for-byte intact and appends
	// a changed page. The signature still verifies — only CoversWholeDocument
	// tells you the rendered document is no longer the one that was signed.
	altered, err := incrementallyAlter(signed)
	if err != nil {
		fail("building the incremental update: %v", err)
	}
	doc2, err := pdf.Read(bytes.NewReader(altered), int64(len(altered)))
	if err != nil {
		fail("re-reading the altered document: %v", err)
	}
	res2 := doc2.VerifySignaturesWithRoots(altered, roots)
	if len(res2) != 1 {
		fail("expected 1 signature after the update, got %d", len(res2))
	}
	a := res2[0]
	report("after a post-signing incremental update", a)

	if !a.Valid {
		fail("the signed range is untouched, so Valid should still be true")
	}
	if a.CoversWholeDocument || a.DocumentUnmodified() {
		fail("an incremental update must make CoversWholeDocument (and DocumentUnmodified) false")
	}

	fmt.Println("\nOK: DocumentUnmodified() rejected the altered file that Valid alone accepted.")
}

// report prints the fields of a SignatureResult that a caller should look at.
func report(label string, r pdf.SignatureResult) {
	fmt.Printf("\n%s:\n", label)
	fmt.Printf("  signer               %s\n", r.SignerCommonName)
	fmt.Printf("  Valid                %v  (the signed byte range verifies)\n", r.Valid)
	fmt.Printf("  CoversWholeDocument  %v  (only the signature bytes are outside the range)\n", r.CoversWholeDocument)
	fmt.Printf("  DocumentUnmodified() %v  <- the verdict to branch on\n", r.DocumentUnmodified())
	fmt.Printf("  TrustedChain         %v  (only ever set by VerifySignaturesWithRoots)\n", r.TrustedChain)
	fmt.Printf("  Revocation           %v\n", r.Revocation.Status)
	if r.Err != nil {
		fmt.Printf("  Err                  %v\n", r.Err)
	}
	if r.ChainErr != nil {
		fmt.Printf("  ChainErr             %v\n", r.ChainErr)
	}
}

// newDocument builds a one-page document to sign.
//
// The page deliberately carries no /Contents entry: signing locates the
// signature placeholder by the first literal "/Contents" in the serialized
// output, so a page content stream (or an already-signed file) currently makes
// WriteSigned fail with "/ByteRange placeholder not found". See the limitations
// section of docs/signing.md.
func newDocument() *pdf.Document {
	catalog := &pdf.Dictionary{}
	catalog.Set("Type", pdf.Name("Catalog"))
	catalog.Set("Pages", pdf.IndirectRef{Number: 2})

	pages := &pdf.Dictionary{}
	pages.Set("Type", pdf.Name("Pages"))
	pages.Set("Kids", pdf.Array{pdf.IndirectRef{Number: 3}})
	pages.Set("Count", pdf.Integer(1))

	page := &pdf.Dictionary{}
	page.Set("Type", pdf.Name("Page"))
	page.Set("Parent", pdf.IndirectRef{Number: 2})
	page.Set("MediaBox", pdf.Array{pdf.Integer(0), pdf.Integer(0), pdf.Integer(612), pdf.Integer(792)})

	return &pdf.Document{
		Version: "2.0",
		Objects: map[int]*pdf.IndirectObject{
			1: {Number: 1, Value: catalog},
			2: {Number: 2, Value: pages},
			3: {Number: 3, Value: page},
		},
		Trailer: pdf.Dictionary{
			Keys:   []pdf.Name{"Root"},
			Values: []pdf.Object{pdf.IndirectRef{Number: 1}},
		},
	}
}

// incrementallyAlter appends an incremental update that replaces the page — the
// original bytes, and therefore the signature over them, are preserved exactly.
func incrementallyAlter(original []byte) ([]byte, error) {
	doc, err := pdf.Read(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		return nil, err
	}
	page, ok := doc.Objects[3].Value.(*pdf.Dictionary)
	if !ok {
		return nil, fmt.Errorf("object 3 is not the page dictionary")
	}
	// Halve the page box: a visible change to what the document renders.
	page.Set("MediaBox", pdf.Array{pdf.Integer(0), pdf.Integer(0), pdf.Integer(306), pdf.Integer(396)})

	var buf bytes.Buffer
	if err := doc.WriteIncremental(&buf, original, []int{3}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// selfSignedSigner generates an in-memory ECDSA key and a self-signed
// certificate usable both as the signer and as its own trust anchor.
func selfSignedSigner(commonName string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sign_verify: "+format+"\n", args...)
	os.Exit(1)
}
