//go:build devtools

// Command genicc writes the sRGB ICC profiles that pdfa embeds.
//
// The profiles are a deterministic function of fixed constants — golittlecms
// builds them from a built-in sRGB definition and serialises to memory — so
// they are generated once and committed rather than rebuilt per document. That
// removes the only operation in document creation that could fail, and with it
// the panic that used to stand in for handling the failure.
//
// TestTheEmbeddedProfilesAreWhatTheEngineBuilds rebuilds both through
// golittlecms and compares, so the committed bytes cannot quietly drift from
// what the colour engine would produce.
//
//	go run -tags devtools ./internal/cmd/genicc
package main

import (
	"fmt"
	"os"
	"path/filepath"

	lcms2 "github.com/mgilbir/golittlecms"
)

func main() {
	for _, p := range []struct {
		version float64
		file    string
	}{
		{2.1, "pdfa/icc/srgb-v2_1.icc"},
		{4.3, "pdfa/icc/srgb-v4_3.icc"},
	} {
		prof, err := lcms2.Create_sRGBProfile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "genicc: create sRGB profile: %v\n", err)
			os.Exit(1)
		}
		prof.SetProfileVersion(p.version)
		data, err := prof.SaveProfileToMem()
		if err != nil {
			fmt.Fprintf(os.Stderr, "genicc: serialise v%.1f: %v\n", p.version, err)
			os.Exit(1)
		}
		if err := os.MkdirAll(filepath.Dir(p.file), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "genicc: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(p.file, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "genicc: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("genicc: %s (%d bytes, ICC v%.1f)\n", p.file, len(data), p.version)
	}
}
