// Command signpdf signs the PDF on stdin with a throwaway self-signed key and
// writes the signed file to stdout. It makes the signed fixture docs/cli.md's
// transcripts read:
//
//	go run ./cmd/pdf0/testdata/signpdf < simple.pdf > signed.pdf
//
// The key and certificate are generated in-process and discarded, so the
// signature verifies as intact but nothing trusts its signer. It lives under
// testdata because it is a fixture maker, not a tool: `go build ./...` and
// `go vet ./...` do not see it.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	"github.com/mgilbir/pdf0"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "signpdf:", err)
		os.Exit(1)
	}
}

func run() error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pdf0 fixture signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := doc.WriteSigned(&out, cert, key); err != nil {
		return err
	}
	_, err = os.Stdout.Write(out.Bytes())
	return err
}
