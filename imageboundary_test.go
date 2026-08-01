package pdf0

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// TestImageSubsystemReadsThroughAView guards the boundary the image subsystem
// now sits behind: below its entry points it reads the document through a
// core.View and never names Document.
//
// The point of the boundary is that it can be moved. Nothing under
// walkImagesCancel depends on the root package's central type, so the files
// below can become a package of their own whenever the question of what that
// package should export is settled. Without a guard that property decays
// silently — adding a *Document parameter to one helper compiles, passes every
// test, and quietly re-couples the subsystem.
//
// The four entry points are the boundary itself and are expected to name
// Document: they start the run and build the view.
func TestImageSubsystemReadsThroughAView(t *testing.T) {
	const (
		file    = "imageextract.go"
		pkgFile = "imagecolor.go"
	)
	files := []string{file, pkgFile, "imagemask.go", "imagejpeg.go"}

	// The boundary functions, which legitimately take or receive a *Document.
	boundary := map[string]bool{
		"ExtractImages": true, "ExtractImagesContext": true, "Images": true,
		"extractImages": true, "walkImages": true, "walkImagesCancel": true,
	}

	var offenders []string
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || boundary[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if ok && id.Name == "Document" {
					offenders = append(offenders, name+": "+fn.Name.Name)
					return false
				}
				return true
			})
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these image functions name Document below the boundary; they should take a core.View:\n  %v", offenders)
	}
}
