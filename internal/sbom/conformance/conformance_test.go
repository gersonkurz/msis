package conformance

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// This package is the guard B, C and E will rely on instead of restating the rules. A guard
// that accepts everything is worse than none, because it looks like coverage. So every rule is
// checked twice: once against a document that obeys it, and once against a document that
// breaks exactly that rule and nothing else.

func schemaDir() string { return filepath.Join("..", "testdata", "cyclonedx") }

// good is a minimal document that satisfies every rule. Each test below mutates one thing.
func good() map[string]any {
	return map[string]any{
		"bomFormat":    "CycloneDX",
		"specVersion":  "1.6",
		"serialNumber": "urn:uuid:1b2ca6b9-6b1d-4a4a-9f2a-2c2a0c7d9a11",
		"version":      1,
		"metadata": map[string]any{
			"timestamp": "2026-09-21T00:00:00Z",
			"supplier":  map[string]any{"name": "Acme"},
			"tools": map[string]any{
				"components": []any{map[string]any{"type": "application", "name": "msis", "version": "1.0"}},
			},
			"component": map[string]any{
				"type": "application", "bom-ref": "ns/product", "name": "Product", "version": "1.0",
				"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("a", 64)}},
			},
		},
		"components": []any{
			payload("ns/file/a", "a.dll", "b"),
		},
		"dependencies": []any{
			map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
		},
		"compositions": []any{
			map[string]any{"aggregate": "incomplete", "assemblies": []any{"ns/product"}},
			map[string]any{
				"aggregate": "unknown",
				// Both fields: how completely the component is described, and how
				// completely its dependency graph is known. They are not the same claim.
				"assemblies":   []any{"ns/file/a"},
				"dependencies": []any{"ns/file/a"},
			},
		},
	}
}

// payload builds an installed component. The role matters: a Binary-table stream is executed
// during installation and never installed, so it must not satisfy an expectation about payload.
func payload(ref, name, fill string) map[string]any {
	return map[string]any{
		"type": "file", "bom-ref": ref, "name": name,
		"hashes":     []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat(fill, 64)}},
		"properties": []any{map[string]any{"name": "msis:role", "value": "payload"}},
	}
}

// stream builds a Binary-table component - present in the document, but not installed payload.
func stream(ref, name, fill string) map[string]any {
	return map[string]any{
		"type": "file", "bom-ref": ref, "name": name,
		"hashes":     []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat(fill, 64)}},
		"properties": []any{map[string]any{"name": "msis:role", "value": "binary-stream"}},
	}
}

func check(t *testing.T, doc map[string]any, want Expected) []error {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return Check(schemaDir(), data, want)
}

func TestACleanDocumentPasses(t *testing.T) {
	if problems := check(t, good(), Expected{
		PayloadNames:         []string{"a.dll"},
		IdentifiedComponents: []string{"ns/product"},
	}); len(problems) != 0 {
		t.Errorf("a conforming document was rejected: %v", problems)
	}
}

// Each case breaks one rule. The test requires the failure to be reported AND to mention the
// thing that is wrong, so a check that fails for an unrelated reason does not count as passing.
func TestEveryRuleCatchesItsViolation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		want    Expected
		mustSay string
	}{
		{
			name: "a purl on a component whose identity was not determined",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				comps[0].(map[string]any)["purl"] = "pkg:nuget/Guessed@1.0"
			},
			want:    Expected{IdentifiedComponents: []string{"ns/product"}},
			mustSay: "identity was not determined",
		},
		{
			name: "a payload component with no SHA-256",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				delete(comps[0].(map[string]any), "hashes")
			},
			mustSay: "no SHA-256",
		},
		{
			name:   "a payload the artifact has but the document omits",
			mutate: func(d map[string]any) {},
			want:   Expected{PayloadNames: []string{"a.dll", "absent.dll"}},
			// The check that cannot be made from the document alone.
			mustSay: "missing from the document",
		},
		{
			name: "a dangling dependency reference",
			mutate: func(d map[string]any) {
				deps := d["dependencies"].([]any)
				deps[0].(map[string]any)["dependsOn"] = []any{"ns/file/nonexistent"}
			},
			mustSay: "not a component",
		},
		{
			name: "a composition naming a component that is not there",
			mutate: func(d map[string]any) {
				comps := d["compositions"].([]any)
				comps[1].(map[string]any)["assemblies"] = []any{"ns/file/ghost"}
			},
			mustSay: "not a component",
		},
		{
			name: "a duplicate bom-ref",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				dup := map[string]any{
					"type": "file", "bom-ref": "ns/file/a", "name": "b.dll",
					"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("c", 64)}},
				}
				d["components"] = append(comps, dup)
				d["compositions"].([]any)[1].(map[string]any)["assemblies"] = []any{"ns/file/a"}
			},
			mustSay: "used more than once",
		},
		{
			name: "an empty dependsOn with no declaration either way",
			mutate: func(d map[string]any) {
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
					map[string]any{"ref": "ns/file/a", "dependsOn": []any{}},
				}
				// Remove the composition that would have said "unknown".
				d["compositions"] = []any{
					map[string]any{"aggregate": "incomplete", "assemblies": []any{"ns/product"}},
				}
			},
			mustSay: "no composition says otherwise",
		},
		{
			name: "no compositions at all",
			mutate: func(d map[string]any) {
				delete(d, "compositions")
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
				}
			},
			mustSay: "how complete it is",
		},
		{
			name: "a composition that names nothing, so its declaration would have to cascade",
			mutate: func(d map[string]any) {
				// Both fields emptied. A composition carrying only dependencies is
				// legitimate - it declares dependency completeness and nothing else - so
				// emptying assemblies alone is not the violation.
				c := d["compositions"].([]any)[1].(map[string]any)
				c["assemblies"] = []any{}
				c["dependencies"] = []any{}
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
					map[string]any{"ref": "ns/file/a", "dependsOn": []any{"ns/product"}},
				}
			},
			mustSay: "does not cascade",
		},
		{
			name:    "no timestamp",
			mutate:  func(d map[string]any) { delete(d["metadata"].(map[string]any), "timestamp") },
			mustSay: "timestamp",
		},
		{
			name:    "no supplier",
			mutate:  func(d map[string]any) { delete(d["metadata"].(map[string]any), "supplier") },
			mustSay: "supplier",
		},
		{
			name:    "no tools",
			mutate:  func(d map[string]any) { delete(d["metadata"].(map[string]any), "tools") },
			mustSay: "author",
		},
		{
			name: "the subject has no digest, so a BOM-Link could not be checked",
			mutate: func(d map[string]any) {
				delete(d["metadata"].(map[string]any)["component"].(map[string]any), "hashes")
			},
			mustSay: "BOM-Link",
		},
		{
			name: "components out of order",
			mutate: func(d map[string]any) {
				first := map[string]any{
					"type": "file", "bom-ref": "ns/file/z", "name": "z.dll",
					"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("d", 64)}},
				}
				d["components"] = []any{first, d["components"].([]any)[0]}
				d["dependencies"].([]any)[0].(map[string]any)["dependsOn"] = []any{"ns/file/a", "ns/file/z"}
				d["compositions"].([]any)[1].(map[string]any)["assemblies"] = []any{"ns/file/a", "ns/file/z"}
			},
			mustSay: "not sorted",
		},
		{
			name:    "not valid CycloneDX at all",
			mutate:  func(d map[string]any) { d["bomFormat"] = "Homemade" },
			mustSay: "schema",
		},
		{
			// Two features can each contribute their own config.xml. Collapsing names into a
			// set let the document drop one and still pass.
			name: "one of two same-named payloads dropped",
			mutate: func(d map[string]any) {
				// The artifact holds two; the document keeps one.
				d["compositions"].([]any)[1].(map[string]any)["assemblies"] = []any{"ns/file/a"}
			},
			want:    Expected{PayloadNames: []string{"a.dll", "a.dll"}},
			mustSay: "the document has 1",
		},
		{
			// A Binary stream is executed, not installed. It must not stand in for payload.
			name: "a Binary stream offered in place of installed payload",
			mutate: func(d map[string]any) {
				d["components"] = []any{stream("ns/binary/a", "a.dll", "b")}
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/binary/a"}},
				}
				d["compositions"] = []any{
					map[string]any{"aggregate": "incomplete", "assemblies": []any{"ns/product"}},
					map[string]any{"aggregate": "unknown",
						"assemblies": []any{"ns/binary/a"}, "dependencies": []any{"ns/binary/a"}},
				}
			},
			want:    Expected{PayloadNames: []string{"a.dll"}},
			mustSay: "missing from the document",
		},
		{
			// Saying both "depends on nothing" and "we do not know" is a contradiction, and
			// used to pass because any composition mention counted as cover.
			name: "an empty dependsOn on a component a composition calls unknown",
			mutate: func(d map[string]any) {
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
					map[string]any{"ref": "ns/file/a", "dependsOn": []any{}},
				}
			},
			mustSay: "contradicts itself",
		},
		{
			// Neither a dependency entry nor a declaration: the document simply does not say.
			name: "a component whose dependencies are never mentioned at all",
			mutate: func(d map[string]any) {
				d["compositions"].([]any)[1].(map[string]any)["dependencies"] = []any{}
			},
			mustSay: "or that it is unknown",
		},
		{
			// compositions[].dependencies names components too, and they have to resolve.
			// Checking only assemblies let a composition declare the dependency completeness
			// of something that is not in the document.
			name: "a composition declaring the dependencies of something that is not there",
			mutate: func(d map[string]any) {
				c := d["compositions"].([]any)[1].(map[string]any)
				c["dependencies"] = []any{"ns/file/a", "ns/file/ghost"}
			},
			mustSay: "in dependencies, which is not a component",
		},
		{
			// A component's graph cannot be two degrees of known at once...
			name: "conflicting declarations, unknown then complete",
			mutate: func(d map[string]any) {
				d["compositions"] = append(d["compositions"].([]any),
					map[string]any{"aggregate": "complete", "dependencies": []any{"ns/file/a"}})
			},
			mustSay: "conflicting dependency-completeness declarations",
		},
		{
			// ...and the order they appear in must not decide whether that is noticed. A
			// last-wins map made this pair behave differently from the pair above.
			name: "conflicting declarations, complete then unknown",
			mutate: func(d map[string]any) {
				comps := d["compositions"].([]any)
				d["compositions"] = []any{
					comps[0],
					map[string]any{"aggregate": "complete", "dependencies": []any{"ns/file/a"}},
					comps[1],
				}
			},
			mustSay: "conflicting dependency-completeness declarations",
		},
		{
			// An empty dependsOn is consistent only with "the graph is fully known".
			name: "known-empty claimed while a composition calls the graph incomplete",
			mutate: func(d map[string]any) {
				d["compositions"].([]any)[1].(map[string]any)["aggregate"] = "incomplete"
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
					map[string]any{"ref": "ns/file/a", "dependsOn": []any{}},
				}
			},
			mustSay: "contradicts itself",
		},
		{
			// The exemption for bytes that are not in the artifact is two-sided. If a ref
			// declared unhashable turns out to carry a SHA-256, the expectation and the
			// artifact disagree - and a one-sided check would let a stale exemption sit
			// there forever, ready to excuse a digest the emitter drops later.
			name:    "a component declared unhashable that does carry a SHA-256",
			mutate:  func(d map[string]any) {},
			want:    Expected{UnhashableComponents: []string{"ns/file/a"}},
			mustSay: "declared unhashable but carries a SHA-256",
		},
		{
			// Admitted as unhashable, but with no digest at all: nothing about it can be
			// verified, which is the state the exemption is NOT for.
			name: "an unhashable component with no digest of any kind",
			mutate: func(d map[string]any) {
				delete(d["components"].([]any)[0].(map[string]any), "hashes")
			},
			want:    Expected{UnhashableComponents: []string{"ns/file/a"}},
			mustSay: "carries no digest at all",
		},
		{
			// Admitted, and carrying the digest the artifact records - but silent about why
			// there is no SHA-256. A reader would have to guess whether it was omitted or
			// forgotten.
			name: "an unhashable component that does not say why",
			mutate: func(d map[string]any) {
				c := d["components"].([]any)[0].(map[string]any)
				c["hashes"] = []any{
					map[string]any{"alg": "SHA-512", "content": strings.Repeat("b", 128)},
				}
			},
			want:    Expected{UnhashableComponents: []string{"ns/file/a"}},
			mustSay: "does not say why",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := good()
			c.mutate(doc)
			problems := check(t, doc, c.want)
			if len(problems) == 0 {
				t.Fatalf("the violation was not caught")
			}
			var joined []string
			for _, p := range problems {
				joined = append(joined, p.Error())
			}
			all := strings.Join(joined, "\n")
			if !strings.Contains(all, c.mustSay) {
				t.Errorf("caught something, but not this rule.\n  want a message mentioning %q\n  got:\n%s",
					c.mustSay, all)
			}
		})
	}
}

// ValidateSchema is what ticket D reuses on its own, so it has to work standalone.
func TestValidateSchemaStandalone(t *testing.T) {
	ok := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`)
	if err := ValidateSchema(schemaDir(), ok); err != nil {
		t.Errorf("a valid document was rejected: %v", err)
	}
	// The schema requires bomFormat and specVersion; `version` defaults to 1 and is not
	// required, which is worth knowing before writing an assertion about it.
	bad := []byte(`{"bomFormat":"CycloneDX"}`)
	if err := ValidateSchema(schemaDir(), bad); err == nil {
		t.Error("a document missing specVersion was accepted")
	}
}

// A schema directory that is missing must fail loudly. Silently skipping validation would make
// every emitter's conformance test pass for the wrong reason.
func TestMissingSchemaIsAnError(t *testing.T) {
	err := ValidateSchema(filepath.Join("testdata", "nonexistent"), []byte(`{}`))
	if err == nil {
		t.Fatal("a missing schema directory must fail, not skip validation")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error = %v, want it to say the vendored schema is incomplete", err)
	}
}

// Known-empty is a legitimate state and must not be swept up with the contradictions: a
// component whose dependency graph really is empty, together with a composition saying that
// graph is completely known, is a correct document and has to keep passing.
func TestKnownEmptyDependenciesAreAccepted(t *testing.T) {
	d := good()
	d["dependencies"] = []any{
		map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
		map[string]any{"ref": "ns/file/a", "dependsOn": []any{}},
	}
	d["compositions"] = []any{
		map[string]any{"aggregate": "incomplete", "assemblies": []any{"ns/product"}},
		map[string]any{"aggregate": "unknown", "assemblies": []any{"ns/file/a"}},
		map[string]any{"aggregate": "complete", "dependencies": []any{"ns/file/a"}},
	}
	if problems := check(t, d, Expected{}); len(problems) != 0 {
		t.Errorf("a component with a genuinely empty, fully known dependency graph was "+
			"rejected: %v", problems)
	}
}

// The exemption must also ACCEPT the state it exists for, or it is just another refusal: a
// component whose bytes are not in the artifact, carrying the digest the artifact records and
// saying why there is no SHA-256, is a correct document.
func TestAProperlyDeclaredUnhashableComponentIsAccepted(t *testing.T) {
	d := good()
	c := d["components"].([]any)[0].(map[string]any)
	c["hashes"] = []any{map[string]any{"alg": "SHA-512", "content": strings.Repeat("b", 128)}}
	c["properties"] = []any{
		map[string]any{"name": "msis:role", "value": "payload"},
		map[string]any{"name": "msis:payload.unavailable",
			"value": "the engine downloads it at install time"},
	}

	want := Expected{UnhashableComponents: []string{"ns/file/a"}}
	if problems := check(t, d, want); len(problems) != 0 {
		t.Errorf("a correctly declared unhashable component was rejected: %v", problems)
	}
}
