package pdf0

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/pdfa"
)

// lockedPDFA3 is a PDF/A-3b document encrypted and read back without its
// password: Locked, its strings and streams still ciphertext.
func lockedPDFA3(t *testing.T) *Document {
	t.Helper()
	d := mustPDFADoc(t, pdfa.PDFA3b)
	if err := d.SetEncryption("secret", "owner"); err != nil {
		t.Fatalf("SetEncryption: %v", err)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	locked, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !locked.Locked() {
		t.Fatal("the fixture is not Locked")
	}
	return locked
}

// TestMetadataWritersRefuseALockedDocument: SetDocumentInfo, EmbedFacturX and
// EmbedOrderX add objects written in the clear. On a Locked document Write
// passes everything through under the original /Encrypt, so a reader would
// "decrypt" those objects into noise. They refuse, and add nothing.
func TestMetadataWritersRefuseALockedDocument(t *testing.T) {
	ops := map[string]func(d *Document) error{
		"SetDocumentInfo": func(d *Document) error { return d.SetDocumentInfo(DocumentInfo{Title: "T"}) },
		"EmbedFacturX": func(d *Document) error {
			return EmbedFacturX(d, []byte(ciiForProfile(formalis.ProfileBasic)), formalis.ProfileBasic, "")
		},
		"EmbedOrderX": func(d *Document) error {
			return EmbedOrderX(d, []byte(`<rsm:SCRDMCCBDACIOMessageStructure xmlns:rsm="urn:x"/>`), "COMFORT", "ORDER", "")
		},
	}
	for name, op := range ops {
		// Twice: with the file's own (encrypted) metadata, and with a
		// plaintext packet, as a file written with /EncryptMetadata false
		// carries. The second is the case where the encrypted packet's failure
		// to parse cannot refuse the edit by accident.
		for _, plainXMP := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/plaintext-metadata=%v", name, plainXMP), func(t *testing.T) {
				d := lockedPDFA3(t)
				if plainXMP {
					packet, err := GenerateXMPMetadata(pdfa.PDFA3b, "", "")
					if err != nil {
						t.Fatal(err)
					}
					replaceMetadata(d, packet)
				}
				checkRefused(t, name, d, op)
			})
		}
	}
}

func checkRefused(t *testing.T, name string, d *Document, op func(*Document) error) {
	t.Helper()
	before := len(d.Objects)
	info := d.Trailer.Get("Info")
	err := op(d)
	if err == nil || !strings.Contains(err.Error(), "Locked") {
		t.Fatalf("%s on a Locked document = %v, want a refusal naming Locked", name, err)
	}
	if len(d.Objects) != before {
		t.Errorf("%s was refused but added %d objects", name, len(d.Objects)-before)
	}
	if d.Trailer.Get("Info") != info {
		t.Errorf("%s was refused but replaced the Info dictionary", name)
	}
}

// TestMetadataWritersRefuseANilDocument: a nil document is an error, not a
// panic.
func TestMetadataWritersRefuseANilDocument(t *testing.T) {
	var d *Document
	for name, err := range map[string]error{
		"SetDocumentInfo": d.SetDocumentInfo(DocumentInfo{Title: "T"}),
		"EmbedFacturX":    EmbedFacturX(nil, []byte("<a/>"), formalis.ProfileBasic, ""),
		"EmbedOrderX":     EmbedOrderX(nil, []byte("<a/>"), "COMFORT", "ORDER", ""),
	} {
		if !errors.Is(err, errNilDocument) {
			t.Errorf("%s(nil) = %v, want errNilDocument", name, err)
		}
	}
}
