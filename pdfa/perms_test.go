package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// The /Perms rules (ISO 19005-2/-3/-4, 6.1.12), which had no test.
//
// Both halves are here because both are ways of getting the same answer wrong:
// the key whitelist reports what it should not carry, and the DocMDP check
// reports the deprecated digest keys in a signature reference dictionary.

// TestPermsAllowsOnlyUR3AndDocMDP.
func TestPermsAllowsOnlyUR3AndDocMDP(t *testing.T) {
	perms := &object.Dictionary{}
	perms.Set("DocMDP", object.IndirectRef{Number: 3})
	perms.Set("UR3", object.IndirectRef{Number: 4})
	perms.Set("Whatever", object.IndirectRef{Number: 5})

	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Perms", perms)

	trailer := object.Dictionary{}
	trailer.Set("Root", object.IndirectRef{Number: 1})
	doc := mkView(map[int]*object.IndirectObject{
		1: {Number: 1, Value: catalog},
	}, trailer)

	errs := checkPermsDict(doc, PDFA2b)
	if !hasMessage(errs, "/Whatever") {
		t.Errorf("a /Perms key that is neither /UR3 nor /DocMDP was not reported: %v", errs)
	}
	for _, allowed := range []string{"/UR3", "/DocMDP"} {
		if hasMessage(errs, "forbidden key "+allowed) {
			t.Errorf("%s is permitted and was reported: %v", allowed, errs)
		}
	}

	// And PDF/A-1 has no /Perms rules at all.
	if errs := checkPermsDict(doc, PDFA1b); len(errs) != 0 {
		t.Errorf("PDF/A-1b reported %d /Perms violations, want none: %v", len(errs), errs)
	}
}

// TestPermsDocMDPRejectsDeprecatedDigestKeys, in both the shape the rule is
// usually written in and the one it usually is not.
//
// The direct case is the point. A signature reference dictionary is normally
// reached through a chain of indirect references, and the code carried two
// fallbacks for when the array and the dictionaries were written out in place
// instead. Both were unreachable — Resolve returns a non-reference as it stands
// — and one of them reinstated a nil dictionary the loop then dereferenced. The
// fallbacks are gone; this pins the behaviour they were supposed to provide.
func TestPermsDocMDPRejectsDeprecatedDigestKeys(t *testing.T) {
	mkRef := func(key string) *object.Dictionary {
		d := &object.Dictionary{}
		d.Set("Type", object.Name("SigRef"))
		d.Set("TransformMethod", object.Name("DocMDP"))
		d.Set(object.Name(key), object.Integer(1))
		return d
	}

	for _, tc := range []struct {
		name string
		// build returns the /DocMDP signature dictionary and any objects it
		// refers to.
		build func() (*object.Dictionary, map[int]*object.IndirectObject)
	}{
		{
			name: "indirect throughout",
			build: func() (*object.Dictionary, map[int]*object.IndirectObject) {
				sig := &object.Dictionary{}
				sig.Set("Reference", object.IndirectRef{Number: 10})
				return sig, map[int]*object.IndirectObject{
					10: {Number: 10, Value: object.Array{object.IndirectRef{Number: 11}}},
					11: {Number: 11, Value: mkRef("DigestMethod")},
				}
			},
		},
		{
			name: "a direct array of direct dictionaries",
			build: func() (*object.Dictionary, map[int]*object.IndirectObject) {
				sig := &object.Dictionary{}
				sig.Set("Reference", object.Array{mkRef("DigestMethod")})
				return sig, nil
			},
		},
		{
			name: "a direct array of indirect dictionaries",
			build: func() (*object.Dictionary, map[int]*object.IndirectObject) {
				sig := &object.Dictionary{}
				sig.Set("Reference", object.Array{object.IndirectRef{Number: 11}})
				return sig, map[int]*object.IndirectObject{
					11: {Number: 11, Value: mkRef("DigestMethod")},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig, objs := tc.build()
			if objs == nil {
				objs = map[int]*object.IndirectObject{}
			}
			perms := &object.Dictionary{}
			perms.Set("DocMDP", sig)
			catalog := &object.Dictionary{}
			catalog.Set("Type", object.Name("Catalog"))
			catalog.Set("Perms", perms)
			objs[1] = &object.IndirectObject{Number: 1, Value: catalog}
			trailer := object.Dictionary{}
			trailer.Set("Root", object.IndirectRef{Number: 1})

			errs := checkPermsDict(mkView(objs, trailer), PDFA2b)
			if !hasMessage(errs, "DigestMethod") {
				t.Errorf("the deprecated /DigestMethod was not reported: %v", errs)
			}
		})
	}

	// All three deprecated keys, and only those.
	for _, key := range []string{"DigestLocation", "DigestMethod", "DigestValue"} {
		sig := &object.Dictionary{}
		sig.Set("Reference", object.Array{mkRef(key)})
		perms := &object.Dictionary{}
		perms.Set("DocMDP", sig)
		catalog := &object.Dictionary{}
		catalog.Set("Type", object.Name("Catalog"))
		catalog.Set("Perms", perms)
		trailer := object.Dictionary{}
		trailer.Set("Root", object.IndirectRef{Number: 1})
		doc := mkView(map[int]*object.IndirectObject{1: {Number: 1, Value: catalog}}, trailer)
		if !hasMessage(checkPermsDict(doc, PDFA2b), key) {
			t.Errorf("/%s was not reported", key)
		}
	}

	// A signature reference dictionary carrying none of them is clean.
	clean := &object.Dictionary{}
	clean.Set("Type", object.Name("SigRef"))
	clean.Set("TransformMethod", object.Name("DocMDP"))
	sig := &object.Dictionary{}
	sig.Set("Reference", object.Array{clean})
	perms := &object.Dictionary{}
	perms.Set("DocMDP", sig)
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Perms", perms)
	trailer := object.Dictionary{}
	trailer.Set("Root", object.IndirectRef{Number: 1})
	doc := mkView(map[int]*object.IndirectObject{1: {Number: 1, Value: catalog}}, trailer)
	if errs := checkPermsDict(doc, PDFA2b); len(errs) != 0 {
		t.Errorf("a conforming /Perms reported %d violations: %v", len(errs), errs)
	}
}

// TestPermsSurvivesAnArrayEntryThatIsNotADictionary.
//
// One of the removed fallbacks turned a non-dictionary entry into a nil
// dictionary and carried on into the key checks, which is a dereference of nil
// on input a document is free to contain.
func TestPermsSurvivesAnArrayEntryThatIsNotADictionary(t *testing.T) {
	good := &object.Dictionary{}
	good.Set("DigestValue", object.Integer(1))
	sig := &object.Dictionary{}
	sig.Set("Reference", object.Array{
		object.Integer(7),              // not a dictionary
		object.IndirectRef{Number: 99}, // resolves to nothing
		(*object.Dictionary)(nil),      // a typed nil
		object.Name("neither"),         // nor this
		good,                           // and one that is
	})
	perms := &object.Dictionary{}
	perms.Set("DocMDP", sig)
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Perms", perms)
	trailer := object.Dictionary{}
	trailer.Set("Root", object.IndirectRef{Number: 1})
	doc := mkView(map[int]*object.IndirectObject{1: {Number: 1, Value: catalog}}, trailer)

	errs := checkPermsDict(doc, PDFA2b) // must not panic
	if !hasMessage(errs, "DigestValue") {
		t.Errorf("the entries that are not dictionaries stopped the one that is "+
			"from being checked: %v", errs)
	}
}
