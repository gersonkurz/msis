package conformance

import (
	"encoding/json"
	"strings"
	"testing"
)

// This package is the guard B, C and E will rely on instead of restating the rules. A guard
// that accepts everything is worse than none, because it looks like coverage. So every rule is
// checked twice: once against a document that obeys it, and once against a document that
// breaks exactly that rule and nothing else.

// good is a minimal document that satisfies every rule. Each test below mutates one thing.
func good() map[string]any {
	return map[string]any{
		"bomFormat":    "CycloneDX",
		"specVersion":  "1.6",
		"serialNumber": "urn:uuid:1b2ca6b9-6b1d-4a4a-9f2a-2c2a0c7d9a11",
		"version":      1,
		"metadata": map[string]any{
			"timestamp":  "2026-09-21T00:00:00Z",
			"lifecycles": []any{map[string]any{"phase": "post-build"}},
			"supplier":   map[string]any{"name": "Acme"},
			"tools": map[string]any{
				"components": []any{map[string]any{"type": "application", "name": "msis", "version": "1.0",
					"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("c", 64)}}}},
			},
			"component": map[string]any{
				"type": "application", "bom-ref": "ns/product", "name": "Product", "version": "1.0",
				"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat("a", 64)},
					map[string]any{"alg": "SHA-512", "content": strings.Repeat("a", 128)}},
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
		"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat(fill, 64)},
			map[string]any{"alg": "SHA-512", "content": strings.Repeat(fill, 128)}},
		"properties": []any{map[string]any{"name": "msis:role", "value": "payload"}},
	}
}

// supplied builds a component imported from a document someone handed msis (#36). It carries no
// digest on purpose: msis never held those bytes, so there was never a hash for it to publish.
func supplied(ref, name, from string) map[string]any {
	return map[string]any{
		"type": "library", "bom-ref": ref, "name": name,
		"properties": []any{map[string]any{"name": "msis:supplied.from", "value": from}},
	}
}

// withSupplied adds an imported component to a document, with the dependency statement every
// component needs, so a case can then break exactly one thing.
func withSupplied(d map[string]any, c map[string]any) map[string]any {
	d["components"] = append(d["components"].([]any), c)
	d["dependencies"] = append(d["dependencies"].([]any),
		map[string]any{"ref": c["bom-ref"], "dependsOn": []any{}})
	d["compositions"] = append(d["compositions"].([]any),
		map[string]any{"aggregate": "complete", "dependencies": []any{c["bom-ref"]}})
	return d
}

// stream builds a Binary-table component - present in the document, but not installed payload.
func stream(ref, name, fill string) map[string]any {
	return map[string]any{
		"type": "file", "bom-ref": ref, "name": name,
		"hashes": []any{map[string]any{"alg": "SHA-256", "content": strings.Repeat(fill, 64)},
			map[string]any{"alg": "SHA-512", "content": strings.Repeat(fill, 128)}},
		"properties": []any{map[string]any{"name": "msis:role", "value": "binary-stream"}},
	}
}

func check(t *testing.T, doc map[string]any, want Expected) []error {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return Check(data, want)
}

// NTIA's "or explicitly unknown" (#59): a supplier and version the artifact does not record are
// accepted when the subject states them unknown.
func TestStatedUnknownNTIAElementsAreAccepted(t *testing.T) {
	d := good()
	m := d["metadata"].(map[string]any)
	delete(m, "supplier")
	c := m["component"].(map[string]any)
	delete(c, "version")
	c["properties"] = []any{
		map[string]any{"name": "msis:ntia.unknown", "value": "supplier: the bundle records no Publisher"},
		map[string]any{"name": "msis:ntia.unknown", "value": "version: the bundle records no Version"},
	}
	if problems := check(t, d, Expected{PayloadNames: []string{"a.dll"}}); len(problems) != 0 {
		t.Errorf("stated unknowns were rejected: %v", problems)
	}
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
			// Two-sided, like every other expectation here: a merged document holding a
			// component nobody supplied means the merge invented one, and an expectation
			// read out of the document under test would never notice.
			name: "a supplied component nobody supplied",
			mutate: func(d map[string]any) {
				withSupplied(d, supplied("ns/supplied/x/ghost", "ghost", "x.cdx.json"))
			},
			want:    Expected{SuppliedComponentNames: nil},
			mustSay: "nothing supplied them",
		},
		{
			name: "a supplied component the merge dropped",
			mutate: func(d map[string]any) {
				withSupplied(d, supplied("ns/supplied/x/left", "left", "x.cdx.json"))
			},
			want:    Expected{SuppliedComponentNames: []string{"left", "right"}},
			mustSay: `contributes 1 component(s) named "right"`,
		},
		{
			// The marking that excuses a missing digest cannot be attached to something msis
			// packaged itself - that would launder the one rule #29 admits no exception to.
			name: "installed payload marked as supplied",
			mutate: func(d map[string]any) {
				c := payload("ns/file/laundered", "laundered.dll", "d")
				delete(c, "hashes")
				c["properties"] = append(c["properties"].([]any),
					map[string]any{"name": "msis:supplied.from", "value": "x.cdx.json"})
				withSupplied(d, c)
			},
			want:    Expected{SuppliedComponentNames: []string{"laundered.dll"}},
			mustSay: "as installed payload",
		},
		{
			// Nesting must not hide a component from any rule.
			name: "a nested component with no SHA-256",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				child := payload("ns/file/nested", "nested.dll", "e")
				delete(child, "hashes")
				comps[0].(map[string]any)["components"] = []any{child}
			},
			mustSay: `"ns/file/nested" has no SHA-256`,
		},
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
			// Reference integrity is not only about dependencies and compositions. A
			// reference held INSIDE a component - here the crypto one the schema defines -
			// pointing at something no longer in the document makes the graph unfollowable
			// just the same, and nothing outside this rule would notice.
			name: "a dangling reference inside a component",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				comps[0].(map[string]any)["cryptoProperties"] = map[string]any{
					"assetType": "certificate",
					"certificateProperties": map[string]any{
						"signatureAlgorithmRef": "ns/file/gone",
					},
				}
			},
			mustSay: `references "ns/file/gone"`,
		},
		{
			// "urn:" is not the test. The schema says only that a local ref SHOULD NOT
			// start with "urn:cdx:", so urn:uuid: is an ordinary local ref - and exempting
			// every URN would let a dangling one through the one rule that would see it.
			name: "a dangling reference that happens to be a URN",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				comps[0].(map[string]any)["cryptoProperties"] = map[string]any{
					"assetType": "certificate",
					"certificateProperties": map[string]any{
						"signatureAlgorithmRef": "urn:uuid:11111111-1111-4111-8111-111111111111",
					},
				}
			},
			mustSay: `references "urn:uuid:11111111`,
		},
		{
			// A BOM-Link addresses another document on purpose, so it is not expected to
			// resolve here - flagging it would make every linked document unconformant.
			name: "a BOM-Link inside a component, which is not a local reference",
			mutate: func(d map[string]any) {
				comps := d["components"].([]any)
				comps[0].(map[string]any)["evidence"] = map[string]any{
					"identity": []any{map[string]any{
						"field": "purl", "confidence": 1.0,
						"tools": []any{"urn:cdx:3e671687-395b-41f5-a30f-a58921a69b79/1#scanner"},
					}},
				}
			},
			mustSay: "", // nothing: this document is correct
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
			name: "no version, not stated unknown (#59)",
			mutate: func(d map[string]any) {
				delete(d["metadata"].(map[string]any)["component"].(map[string]any), "version")
			},
			mustSay: "version",
		},
		{
			name: "no supplier, and the unknown statement is about a DIFFERENT element",
			mutate: func(d map[string]any) {
				m := d["metadata"].(map[string]any)
				delete(m, "supplier")
				m["component"].(map[string]any)["properties"] = []any{
					map[string]any{"name": "msis:ntia.unknown", "value": "version: the package records no ProductVersion"}}
			},
			mustSay: "supplier",
		},
		{
			name: "a payload with a SHA-256 but no SHA-512 (#63)",
			mutate: func(d map[string]any) {
				c := d["components"].([]any)[0].(map[string]any)
				c["hashes"] = c["hashes"].([]any)[:1]
			},
			mustSay: "no SHA-512",
		},
		{
			name: "a subject with no SHA-512 (#63)",
			mutate: func(d map[string]any) {
				c := d["metadata"].(map[string]any)["component"].(map[string]any)
				c["hashes"] = c["hashes"].([]any)[:1]
			},
			mustSay: "subject component has no SHA-512",
		},
		{
			name: "a BSI property with a value BSI does not define (#63)",
			mutate: func(d map[string]any) {
				c := d["components"].([]any)[0].(map[string]any)
				c["properties"] = []any{
					map[string]any{"name": "bsi:component:executable", "value": "yes"},
					map[string]any{"name": "msis:role", "value": "payload"},
				}
			},
			mustSay: "does not define",
		},
		{
			name:    "no generation context (#62)",
			mutate:  func(d map[string]any) { delete(d["metadata"].(map[string]any), "lifecycles") },
			mustSay: "generation context",
		},
		{
			name:    "no tools",
			mutate:  func(d map[string]any) { delete(d["metadata"].(map[string]any), "tools") },
			mustSay: "author",
		},
		{
			name: "msis is named without its own digest (#58)",
			mutate: func(d map[string]any) {
				tools := d["metadata"].(map[string]any)["tools"].(map[string]any)["components"].([]any)
				delete(tools[0].(map[string]any), "hashes")
			},
			mustSay: "D7",
		},
		{
			name: "the tools do not name msis at all",
			mutate: func(d map[string]any) {
				tools := d["metadata"].(map[string]any)["tools"].(map[string]any)["components"].([]any)
				tools[0].(map[string]any)["name"] = "some-scanner"
			},
			mustSay: "D7",
		},
		{
			name: "the subject has no digest, so a BOM-Link could not be checked",
			mutate: func(d map[string]any) {
				delete(d["metadata"].(map[string]any)["component"].(map[string]any), "hashes")
			},
			mustSay: "BOM-Link",
		},
		{
			name: "dependencies out of order (#60)",
			mutate: func(d map[string]any) {
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
					map[string]any{"ref": "ns/file/a", "dependsOn": []any{}},
				}
			},
			mustSay: "dependencies are not sorted",
		},
		{
			name: "a dependsOn out of order",
			mutate: func(d map[string]any) {
				d["components"] = append(d["components"].([]any), payload("ns/file/b", "b.dll", "c"))
				d["dependencies"] = []any{
					map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/b", "ns/file/a"}},
				}
			},
			mustSay: "dependsOn or provides",
		},
		{
			name: "compositions that tie on assemblies, out of order by dependencies",
			mutate: func(d map[string]any) {
				d["compositions"] = []any{
					map[string]any{"aggregate": "incomplete", "assemblies": []any{"ns/product"}},
					map[string]any{"aggregate": "unknown", "dependencies": []any{"ns/product"}},
					map[string]any{"aggregate": "unknown", "dependencies": []any{"ns/file/a"}},
					map[string]any{"aggregate": "unknown", "assemblies": []any{"ns/file/a"}},
				}
			},
			mustSay: "compositions are not sorted",
		},
		{
			name: "a tool's properties out of order",
			mutate: func(d map[string]any) {
				tools := d["metadata"].(map[string]any)["tools"].(map[string]any)["components"].([]any)
				tools[0].(map[string]any)["properties"] = []any{
					map[string]any{"name": "z", "value": "1"},
					map[string]any{"name": "a", "value": "1"},
				}
			},
			mustSay: "tool",
		},
		{
			name: "a component's properties out of order",
			mutate: func(d map[string]any) {
				c := d["components"].([]any)[0].(map[string]any)
				c["properties"] = []any{
					map[string]any{"name": "msis:role", "value": "payload"},
					map[string]any{"name": "msis:installTarget", "value": "[INSTALLDIR]a.dll"},
				}
			},
			mustSay: "properties of",
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
			// The detected-runtime exemption is two-sided for the same reason as the
			// unhashable one: a ref declared to distribute nothing, that turns out to
			// carry a digest, means the expectation and the build disagree - and a stale
			// list would sit there ready to excuse a digest dropped later.
			name:    "a component declared as distributing nothing that does carry a SHA-256",
			mutate:  func(d map[string]any) {},
			want:    Expected{DetectedComponents: []string{"ns/file/a"}},
			mustSay: "declared as distributing nothing but carries a SHA-256",
		},
		{
			// Admitted as distributing nothing, but silent about why it has no digest.
			name: "a detected component that does not say why it has no digest",
			mutate: func(d map[string]any) {
				delete(d["components"].([]any)[0].(map[string]any), "hashes")
			},
			want:    Expected{DetectedComponents: []string{"ns/file/a"}},
			mustSay: "does not say why it has no digest",
		},
		{
			// The exemption for bytes that are not in the artifact is two-sided. If a ref
			// declared unhashable turns out to carry a SHA-256, the expectation and the
			// artifact disagree - and a one-sided check would let a stale exemption sit
			// there forever, ready to excuse a digest the emitter drops later.
			name:    "a component declared unhashable that does carry a SHA-256",
			mutate:  func(d map[string]any) {},
			want:    Expected{UnhashableComponents: []string{"ns/file/a"}},
			mustSay: "declared as distributing nothing but carries a SHA-256",
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
			// A case may assert the opposite: that a document NOT breaking the rule passes.
			if c.mustSay == "" {
				if len(problems) != 0 {
					t.Fatalf("a conforming document was rejected: %v", problems)
				}
				return
			}
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
	if err := ValidateSchema(ok); err != nil {
		t.Errorf("a valid document was rejected: %v", err)
	}
	// The schema requires bomFormat and specVersion; `version` defaults to 1 and is not
	// required, which is worth knowing before writing an assertion about it.
	bad := []byte(`{"bomFormat":"CycloneDX"}`)
	if err := ValidateSchema(bad); err == nil {
		t.Error("a document missing specVersion was accepted")
	}
}

// The schema has to be COMPLETE, not merely present. It used to be a directory the caller
// named, and a missing one had to fail loudly rather than skip validation; embedding removes
// that failure mode but not this one - an embed pattern still matches whatever is there, so a
// renamed or dropped file would leave a $ref unresolvable and a constraint unchecked.
func TestTheVendoredSchemaIsComplete(t *testing.T) {
	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	// bom-1.6 references the other two by absolute URL; without them the compile fails
	// rather than silently skipping what they constrain.
	for _, name := range []string{"bom-1.6.schema.json", "jsf-0.82.schema.json", "spdx.schema.json"} {
		if !got[name] {
			t.Errorf("the vendored schema is missing %s", name)
		}
	}

	// And it really compiles, so a resolvable-looking but broken chain is caught here
	// rather than as a mysteriously passing conformance run.
	if _, err := compile(); err != nil {
		t.Fatalf("the vendored schema does not compile: %v", err)
	}
}

// Known-empty is a legitimate state and must not be swept up with the contradictions: a
// component whose dependency graph really is empty, together with a composition saying that
// graph is completely known, is a correct document and has to keep passing.
func TestKnownEmptyDependenciesAreAccepted(t *testing.T) {
	d := good()
	d["dependencies"] = []any{
		map[string]any{"ref": "ns/file/a", "dependsOn": []any{}},
		map[string]any{"ref": "ns/product", "dependsOn": []any{"ns/file/a"}},
	}
	d["compositions"] = []any{
		map[string]any{"aggregate": "complete", "dependencies": []any{"ns/file/a"}},
		map[string]any{"aggregate": "incomplete", "assemblies": []any{"ns/product"}},
		map[string]any{"aggregate": "unknown", "assemblies": []any{"ns/file/a"}},
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
		map[string]any{"name": "msis:payload.unavailable",
			"value": "the engine downloads it at install time"},
		map[string]any{"name": "msis:role", "value": "payload"},
	}

	want := Expected{UnhashableComponents: []string{"ns/file/a"}}
	if problems := check(t, d, want); len(problems) != 0 {
		t.Errorf("a correctly declared unhashable component was rejected: %v", problems)
	}
}

// The reference fields come from the schema, not from a list somebody maintains. This checks
// that the derivation actually finds them - a silent empty result would make the merge namespace
// nothing at all while every test that only nests components still passed.
func TestReferenceFieldNamesComeFromTheSchema(t *testing.T) {
	got := ReferenceFieldNames()
	// One per shape the schema uses: a plain ref, an array of refs, a ref buried in
	// cryptoProperties, and one in a part of the spec msis has no types for at all.
	for _, want := range []string{
		"dependsOn", "provides", "parent", "signatureAlgorithmRef", "subjectPublicKeyRef",
		"algorithmRef", "claims", "requirements", "assemblies", "ref",
	} {
		if !got[want] {
			t.Errorf("%q is a component reference in the schema but was not derived", want)
		}
	}
	// bom-ref DEFINES a reference rather than using one, and the caller treats the two
	// differently - including it would make a definition look like a use.
	if got["bom-ref"] {
		t.Error("bom-ref must not be listed as a reference-bearing field")
	}
	// A field that is plainly not a reference must not be swept in.
	for _, unwanted := range []string{"name", "version", "purl", "content", "url"} {
		if got[unwanted] {
			t.Errorf("%q is not a component reference", unwanted)
		}
	}
}
