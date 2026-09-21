package sbom

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// Composing a supplied component SBOM (#36).
//
// The question these answer is not "did the merge copy the JSON" but "does the merged document
// claim more than the two inputs support". Receiving a document establishes nothing about it, so
// every test here is about a claim NOT being made.

// suppliedDoc writes a CycloneDX document for F1's bytes. compositions is spliced in as written
// so a test can supply each of the four coverage states, including none at all.
func suppliedDoc(compositions, extra string) []byte {
	doc := `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "serialNumber": "urn:uuid:9f1c0e2a-7777-4aaa-9bbb-cccccccccccc",
  "version": 1,
  "metadata": {
    "component": {
      "type": "application",
      "bom-ref": "app",
      "name": "a.dll",
      "version": "2.1.0",
      "hashes": [{"alg": "SHA-256", "content": "` + strings.Repeat("a", 64) + `"}]
    }
  },
  "components": [
    {
      "type": "library",
      "bom-ref": "pkg:golang/example.com/left@1.2.3",
      "name": "left",
      "version": "1.2.3",
      "purl": "pkg:golang/example.com/left@1.2.3",
      "licenses": [{"license": {"id": "Apache-2.0"}}]
    },
    {
      "type": "library",
      "bom-ref": "pkg:golang/example.com/right@4.5.6",
      "name": "right",
      "version": "4.5.6",
      "purl": "pkg:golang/example.com/right@4.5.6"
    }
  ],
  "dependencies": [
    {"ref": "app", "dependsOn": ["pkg:golang/example.com/left@1.2.3"]},
    {"ref": "pkg:golang/example.com/left@1.2.3", "dependsOn": ["pkg:golang/example.com/right@4.5.6"]},
    {"ref": "pkg:golang/example.com/right@4.5.6", "dependsOn": []}
  ]` + compositions + extra + `
}`
	return []byte(doc)
}

// suppliedNames are the components the document above contributes, derived from IT rather than
// from the output - including its own subject, which becomes a component of the merged document.
var suppliedNames = []string{"a.dll", "left", "right"}

func compositionsBlock(body string) string {
	if body == "" {
		return ""
	}
	return ",\n  \"compositions\": [" + body + "]"
}

func mergedDoc(t *testing.T, supplied ...Supplied) (*Document, []byte) {
	t.Helper()
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test", Supplied: supplied})
	if err != nil {
		t.Fatalf("FromPackage: %v", err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return doc, data
}

func forF1(data []byte) Supplied {
	return Supplied{Source: "app.cdx.json", Target: "[INSTALLDIR]a.dll", FileID: "F1", Data: data}
}

// dependencyAggregateOf reports what the merged document says about one component's graph.
func dependencyAggregateOf(doc *Document, ref string) []string {
	var out []string
	for _, c := range doc.Compositions {
		for _, d := range c.Dependencies {
			if d == ref {
				out = append(out, c.Aggregate)
			}
		}
	}
	return out
}

func refWithName(doc *Document, name string) string {
	for _, c := range doc.Components {
		if c.Name == name {
			return c.BOMRef
		}
	}
	return ""
}

func dependsOnOf(doc *Document, ref string) ([]string, bool) {
	for _, d := range doc.Dependencies {
		if d.Ref == ref {
			return d.DependsOn, true
		}
	}
	return nil, false
}

// --- the four coverage states ------------------------------------------------------------------

// Only a supplied document that ASSERTS completeness may narrow the payload file's marking.
//
// Everything else leaves it UNKNOWN, which is the marking the artifact-only document already
// gave it. Not `incomplete`: the schema defines incomplete as "additional relationships exist",
// which is a claim that something IS missing, and knowing one thing that is inside the file
// establishes nothing about whether anything else is. Unknown says completeness is not known,
// which is the fact.
func TestTheFileIsOnlyCompleteWhenTheSupplierSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		// want is the marking the PAYLOAD FILE gets; supplierWant is what the supplied
		// subject's own graph is declared in the merged document.
		want         string
		supplierWant string
	}{
		{
			name:         "the supplier declares its coverage complete",
			block:        compositionsBlock(`{"aggregate": "complete", "dependencies": ["app"]}`),
			want:         aggregateComplete,
			supplierWant: aggregateComplete,
		},
		{
			name:         "the supplier declares its coverage incomplete",
			block:        compositionsBlock(`{"aggregate": "incomplete", "dependencies": ["app"]}`),
			want:         aggregateUnknown,
			supplierWant: aggregateIncomplete,
		},
		{
			name:         "the supplier declares its coverage unknown",
			block:        compositionsBlock(`{"aggregate": "unknown", "dependencies": ["app"]}`),
			want:         aggregateUnknown,
			supplierWant: aggregateUnknown,
		},
		{
			// Nothing at all: the merged document has to say so rather than leave the
			// question open, or a reader cannot tell it from a graph nobody examined.
			name:         "the supplier declares nothing at all",
			block:        "",
			want:         aggregateUnknown,
			supplierWant: aggregateUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, data := mergedDoc(t, forF1(suppliedDoc(tc.block, "")))

			// Each of the four states has to produce a document that still conforms -
			// including the one where narrowing the file's marking rewrites the blanket
			// declaration the artifact-only document gave every payload component.
			for _, p := range conformance.Check(data, conformance.Expected{
				PayloadNames:           []string{"a.dll", "b.dll"},
				IdentifiedComponents:   identifiedRefs(doc),
				SuppliedComponentNames: suppliedNames,
			}) {
				t.Errorf("conformance: %v", p)
			}

			target := refWithName(doc, "a.dll")
			if target == "" {
				t.Fatal("the payload component disappeared")
			}
			got := dependencyAggregateOf(doc, target)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("the file's dependency graph is declared %v, want exactly [%s]",
					got, tc.want)
			}

			// Whatever the supplier said about ITS subject is carried across unchanged -
			// the merge does not restate someone else's claim in its own words.
			appRef := ""
			for _, c := range doc.Components {
				if strings.Contains(c.BOMRef, "/supplied/") && c.Name == "a.dll" {
					appRef = c.BOMRef
				}
			}
			if appRef == "" {
				t.Fatal("the supplied document's own subject was not imported")
			}
			if got := dependencyAggregateOf(doc, appRef); len(got) != 1 || got[0] != tc.supplierWant {
				t.Errorf("the supplied subject's own graph is declared %v, want [%s]",
					got, tc.supplierWant)
			}
		})
	}
}

// A component known to depend on nothing is a THIRD state, distinct from unknown, and a merge
// that turned it into either of the others would corrupt a valid graph (#29).
func TestAKnownEmptyGraphSurvivesTheMerge(t *testing.T) {
	doc, data := mergedDoc(t, forF1(suppliedDoc(
		compositionsBlock(`{"aggregate": "complete", "dependencies": ["pkg:golang/example.com/right@4.5.6"]}`), "")))

	right := refWithName(doc, "right")
	on, ok := dependsOnOf(doc, right)
	if !ok {
		t.Fatal("the known-empty dependency entry was dropped; the component now looks unexamined")
	}
	if len(on) != 0 {
		t.Errorf("dependsOn = %v, want empty - the supplier said it depends on nothing", on)
	}

	// And the document still conforms: an empty dependsOn is only admissible beside a
	// declaration that the graph is fully known, which is what the supplier gave.
	for _, p := range conformance.Check(data, conformance.Expected{
		IdentifiedComponents:   identifiedRefs(doc),
		SuppliedComponentNames: suppliedNames,
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// identifiedRefs lists the refs that legitimately carry a purl: the supplier determined the
// identity of its own components, which is exactly what a supplied document is for.
func identifiedRefs(doc *Document) []string {
	var out []string
	for _, c := range doc.Components {
		if c.raw != nil && c.raw.str("purl") != "" {
			out = append(out, c.BOMRef)
		}
	}
	return out
}

// --- what the merge must not do ------------------------------------------------------------

// The supplied document describes bytes. If they are not the bytes in the package, the document
// is about a different build and attaching it would put a dependency graph on the wrong file.
func TestADigestThatDisagreesFailsTheBuild(t *testing.T) {
	wrong := strings.ReplaceAll(string(suppliedDoc("", "")),
		strings.Repeat("a", 64), strings.Repeat("9", 64))

	_, err := FromPackage(syntheticPackage(), Options{
		MsisVersion: "test", Supplied: []Supplied{forF1([]byte(wrong))}})
	if err == nil {
		t.Fatal("a document describing different bytes was merged")
	}
	for _, want := range []string{"a.dll", "different build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}

// Everything the supplier wrote is emitted, including fields msis does not model. Re-serialising
// an imported component through msis's own struct would silently drop its licences - the single
// most consequential field in a component SBOM - and the document would look complete.
func TestAnImportedComponentIsEmittedVerbatim(t *testing.T) {
	_, data := mergedDoc(t, forF1(suppliedDoc("", "")))

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var found map[string]any
	for _, item := range raw["components"].([]any) {
		c := item.(map[string]any)
		if c["name"] == "left" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("the supplied component is not in the merged document")
	}
	if found["licenses"] == nil {
		t.Error("the licences were dropped: msis does not model them, so the component has " +
			"to be emitted as its author wrote it")
	}
	if got := found["purl"]; got != "pkg:golang/example.com/left@1.2.3" {
		t.Errorf("purl = %v; the supplier's identity must survive untouched", got)
	}
}

// Two documents become one, so document-local refs have to be namespaced or they can collide.
// That is addressing, not identity: the purl above is untouched, while the ref that only ever
// meant "this component, in that file" is qualified by which file it came from.
func TestImportedRefsAreNamespacedAndRelationshipsFollow(t *testing.T) {
	doc, data := mergedDoc(t, forF1(suppliedDoc("", "")))

	left := refWithName(doc, "left")
	if !strings.Contains(left, "/supplied/app.cdx.json/") {
		t.Errorf("ref %q is not namespaced by the document it came from", left)
	}
	on, ok := dependsOnOf(doc, left)
	if !ok || len(on) != 1 || on[0] != refWithName(doc, "right") {
		t.Errorf("left dependsOn %v; the relationship did not follow the rename", on)
	}
	// The payload file is what ties the two documents together.
	target := refWithName(doc, "a.dll")
	on, ok = dependsOnOf(doc, target)
	if !ok || len(on) != 1 {
		t.Fatalf("the payload file does not depend on what the supplied document describes: %v", on)
	}
	for _, p := range conformance.Check(data, conformance.Expected{
		IdentifiedComponents:   identifiedRefs(doc),
		SuppliedComponentNames: suppliedNames,
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// A reference msis cannot resolve would be published as a dangling one, and a consumer reading
// the merged document could not tell whether the component was dropped or never existed.
func TestADanglingSuppliedReferenceIsRefused(t *testing.T) {
	broken := strings.Replace(string(suppliedDoc("", "")),
		`{"ref": "pkg:golang/example.com/right@4.5.6", "dependsOn": []}`,
		`{"ref": "pkg:golang/example.com/right@4.5.6", "dependsOn": ["pkg:golang/example.com/ghost@0.0.1"]}`, 1)

	_, err := FromPackage(syntheticPackage(), Options{
		MsisVersion: "test", Supplied: []Supplied{forF1([]byte(broken))}})
	if err == nil {
		t.Fatal("a dependency on a component the document does not define was published")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("%q does not name the unresolvable reference", err)
	}
}

// Nesting is allowed by the schema, and a nested component carries a ref like any other: it has
// to be namespaced too, or the merged document points at something that is no longer there.
func TestNestedSuppliedComponentsAreNamespacedToo(t *testing.T) {
	nested := strings.Replace(string(suppliedDoc("", "")),
		`"purl": "pkg:golang/example.com/right@4.5.6"`,
		`"purl": "pkg:golang/example.com/right@4.5.6",
      "components": [{"type": "library", "bom-ref": "vendored", "name": "vendored"}]`, 1)

	doc, data := mergedDoc(t, forF1([]byte(nested)))

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var child map[string]any
	for _, item := range raw["components"].([]any) {
		c := item.(map[string]any)
		if kids, ok := c["components"].([]any); ok && len(kids) > 0 {
			child = kids[0].(map[string]any)
		}
	}
	if child == nil {
		t.Fatal("the nested component was dropped")
	}
	if ref, _ := child["bom-ref"].(string); !strings.Contains(ref, "/supplied/") {
		t.Errorf("nested ref %q was left in the supplier's namespace", ref)
	}

	for _, p := range conformance.Check(data, conformance.Expected{
		IdentifiedComponents:   identifiedRefs(doc),
		SuppliedComponentNames: append(append([]string{}, suppliedNames...), "vendored"),
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// Two documents for one file would each declare that file's coverage, and reconciling two
// coverage claims is their authors' job, not msis's.
func TestTwoDocumentsForOneFileAreRefused(t *testing.T) {
	a := forF1(suppliedDoc("", ""))
	b := forF1(suppliedDoc("", ""))
	b.Source = "other.cdx.json"

	_, err := FromPackage(syntheticPackage(), Options{
		MsisVersion: "test", Supplied: []Supplied{a, b}})
	if err == nil {
		t.Fatal("two documents were merged onto one file")
	}
	if !strings.Contains(err.Error(), "other.cdx.json") {
		t.Errorf("%q does not name the second document", err)
	}
}

// The document has to say, in it, that merging established nothing - otherwise a reader sees a
// dependency graph under msis's name and reasonably assumes msis checked it.
func TestTheDocumentSaysWhatTheMergeDidNotEstablish(t *testing.T) {
	doc, _ := mergedDoc(t, forF1(suppliedDoc("", "")))

	var note string
	for _, p := range doc.Metadata.Properties {
		if p.Name == propSuppliedDocument {
			note = p.Value
		}
	}
	if note == "" {
		t.Fatal("the merged document does not record that anything was merged")
	}
	for _, want := range []string{"app.cdx.json", "a.dll", "establishes nothing"} {
		if !strings.Contains(note, want) {
			t.Errorf("%q does not mention %q", note, want)
		}
	}
	// And every imported component says where it came from, so a reader can tell msis's own
	// observations from someone else's assertions without reading the note.
	for _, c := range doc.Components {
		if c.raw == nil {
			continue
		}
		var marked bool
		for _, p := range c.raw.children() {
			_ = p
		}
		props, _ := c.raw["properties"].([]any)
		for _, item := range props {
			m, _ := item.(map[string]any)
			if m["name"] == propSuppliedFrom {
				marked = true
			}
		}
		if !marked {
			t.Errorf("%s does not say which document supplied it", c.BOMRef)
		}
	}
}

// #29 D6: two builds of one input produce one document, or a diff is not a release review.
func TestMergingStaysDeterministic(t *testing.T) {
	_, first := mergedDoc(t, forF1(suppliedDoc(
		compositionsBlock(`{"aggregate": "incomplete", "dependencies": ["app"]}`), "")))
	_, second := mergedDoc(t, forF1(suppliedDoc(
		compositionsBlock(`{"aggregate": "incomplete", "dependencies": ["app"]}`), "")))

	a, err := CanonicalForDiff(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalForDiff(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two merges of one input produced different documents")
	}
}

// --- round 2: what the schema says a reference is ---------------------------------------------

// `bom-ref` and `dependsOn` are not the whole story. CycloneDX 1.6 references a component from
// `signatureAlgorithmRef`, from `parent`, from `provides` and from a dozen more fields, and it
// nests components under `pedigree` as well as under `components`. A ref left in the supplier's
// namespace either dangles or collides with the host's, and a hand-written list of the fields to
// rewrite would be a copy of the schema that drifts from it.
func TestEveryKindOfReferenceIsNamespaced(t *testing.T) {
	supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "app", "name": "a.dll"}},
  "components": [
    {
      "type": "cryptographic-asset", "bom-ref": "cert", "name": "signing-cert",
      "cryptoProperties": {
        "assetType": "certificate",
        "certificateProperties": {"signatureAlgorithmRef": "alg", "subjectPublicKeyRef": "key"}
      }
    },
    {"type": "cryptographic-asset", "bom-ref": "alg", "name": "sha256-rsa"},
    {"type": "cryptographic-asset", "bom-ref": "key", "name": "public-key"},
    {
      "type": "library", "bom-ref": "forked", "name": "forked",
      "pedigree": {"ancestors": [{"type": "library", "bom-ref": "upstream", "name": "upstream"}]}
    }
  ],
  "dependencies": [
    {"ref": "app", "dependsOn": ["cert", "forked"], "provides": ["alg"]},
    {"ref": "cert", "dependsOn": ["alg", "key"]},
    {"ref": "alg", "dependsOn": []},
    {"ref": "key", "dependsOn": []},
    {"ref": "forked", "dependsOn": []}
  ]
}`)

	doc, data := mergedDoc(t, forF1(supplied))

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	// Every REFERENCE field, wherever the document puts one, has to be in the namespace -
	// and nothing else may be touched. The fixture has a component whose name and ref are
	// both "forked" for exactly that reason: rewriting the name would be rewriting identity.
	supplierRefs := map[string]bool{"app": true, "cert": true, "alg": true, "key": true,
		"forked": true, "upstream": true}
	names := 0
	var check func(node any, path string)
	check = func(node any, path string) {
		switch n := node.(type) {
		case map[string]any:
			for k, v := range n {
				str, isString := v.(string)
				switch {
				case !isString:
				case k == "bom-ref" || conformance.ReferenceFieldNames()[k]:
					if supplierRefs[str] {
						t.Errorf("%s.%s still holds the supplier's ref %q", path, k, str)
					}
				case k == "name" && supplierRefs[str]:
					names++ // identity, and it must survive untouched
				}
				check(v, path+"."+k)
			}
		case []any:
			for i, item := range n {
				check(item, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	check(raw["components"], "components")
	check(raw["dependencies"], "dependencies")
	if names != 2 {
		t.Errorf("%d names equal to a ref survived, want 2 (forked and its ancestor upstream) "+
			"- rewriting a name would be rewriting identity, not addressing", names)
	}

	// And `provides` survived: it is the other relationship a dependency can carry, and
	// decoding it through a type that does not model it would drop the edge silently.
	var provided []string
	for _, d := range doc.Dependencies {
		provided = append(provided, d.Provides...)
	}
	if len(provided) != 1 || !strings.Contains(provided[0], "/supplied/") {
		t.Errorf("provides = %v; the edge was dropped or left in the supplier's namespace", provided)
	}

	for _, p := range conformance.Check(data, conformance.Expected{
		SuppliedComponentNames: []string{"a.dll", "signing-cert", "sha256-rsa", "public-key", "forked"},
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// The same document may legitimately describe two files: identical bytes installed at two
// targets. Importing it once per target would emit every component twice under the same refs,
// which is a duplicate-ref document - and neither target is ambiguous, so refusing would be
// refusing something correct.
func TestOneDocumentForTwoFilesIsImportedOnce(t *testing.T) {
	pkg := syntheticPackage()
	pkg.Files[1].SHA256 = pkg.Files[0].SHA256 // the same bytes, installed twice

	first := forF1(suppliedDoc("", ""))
	second := first
	second.FileID, second.Target = "F2", "[INSTALLDIR]b.dll"

	doc, err := FromPackage(pkg, Options{MsisVersion: "test", Supplied: []Supplied{first, second}})
	if err != nil {
		t.Fatalf("FromPackage: %v", err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}
	for _, c := range doc.Components {
		seen[c.BOMRef]++
	}
	for ref, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times", ref, n)
		}
	}
	// Both files point at the one imported graph.
	pointing := 0
	for _, d := range doc.Dependencies {
		for _, on := range d.DependsOn {
			if strings.Contains(on, "/supplied/app.cdx.json/") && strings.Contains(d.Ref, "/file/") {
				pointing++
			}
		}
	}
	if pointing != 2 {
		t.Errorf("%d of the two files point at the supplied document", pointing)
	}

	for _, p := range conformance.Check(data, conformance.Expected{
		IdentifiedComponents:   identifiedRefs(doc),
		SuppliedComponentNames: suppliedNames,
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// A supplied document is someone else's file, so a field that should hold a SHA-256 may hold
// anything. "abc" is a malformed document to report, not a slice out of range.
func TestAMalformedDigestIsReportedNotFatal(t *testing.T) {
	for _, bad := range []string{"abc", "", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		broken := strings.ReplaceAll(string(suppliedDoc("", "")), strings.Repeat("a", 64), bad)
		_, err := FromPackage(syntheticPackage(), Options{
			MsisVersion: "test", Supplied: []Supplied{forF1([]byte(broken))}})
		if bad == "" {
			// No digest at all is not malformed - it means the document does not say, and
			// msis records that it could not check rather than refusing.
			if err != nil {
				t.Errorf("a document with no digest was refused: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q was accepted as a SHA-256", bad)
			continue
		}
		if !strings.Contains(err.Error(), "not a SHA-256") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// A component without a bom-ref is valid CycloneDX, and msis emits it - but the merged document
// has to be able to refer to it, if only to say its dependencies are unknown. It is given an
// ADDRESS, which is not identity: no purl, name or version is invented (#29 D4).
func TestAComponentWithNoRefIsAddressedNotRejected(t *testing.T) {
	supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "app", "name": "a.dll"}},
  "components": [{"type": "library", "name": "helper"}],
  "dependencies": [{"ref": "app", "dependsOn": []}]
}`)

	doc, data := mergedDoc(t, forF1(supplied))

	var helper *Component
	for i := range doc.Components {
		if doc.Components[i].Name == "helper" {
			helper = &doc.Components[i]
		}
	}
	if helper == nil {
		t.Fatal("the component was dropped")
	}
	if helper.BOMRef == "" {
		t.Fatal("the component has no ref, so nothing in the document can say anything about it")
	}
	if !strings.Contains(helper.BOMRef, "/supplied/app.cdx.json/") {
		t.Errorf("the assigned ref %q is not in the namespace of the document it came from",
			helper.BOMRef)
	}
	if helper.raw.str("purl") != "" || helper.raw.str("version") != "" {
		t.Error("identity was invented along with the address")
	}
	var says bool
	props, _ := helper.raw["properties"].([]any)
	for _, item := range props {
		if m, _ := item.(map[string]any); m["name"] == propSuppliedRef {
			says = true
		}
	}
	if !says {
		t.Error("the document does not say that msis assigned this ref rather than its author")
	}

	for _, p := range conformance.Check(data, conformance.Expected{
		SuppliedComponentNames: []string{"a.dll", "helper"},
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// --- round 3: references that would outlive their definitions ---------------------------------

// A supplied document may define a bom-ref in metadata.tools - the scanner that produced it -
// and reference it from a component's evidence. msis imports components, not someone else's
// tooling: the scanner is not in the product, and putting it in the installer's metadata.tools
// would say it produced THIS document. So the reference cannot be kept, and the build is
// refused rather than publishing one that resolves to nothing.
func TestAReferenceToSomethingNotImportedIsRefused(t *testing.T) {
	supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {
    "component": {"type": "application", "bom-ref": "app", "name": "a.dll"},
    "tools": {"components": [{"type": "application", "bom-ref": "scanner", "name": "some-scanner"}]}
  },
  "components": [
    {
      "type": "library", "bom-ref": "left", "name": "left",
      "evidence": {"identity": [{"field": "purl", "confidence": 1, "tools": ["scanner"]}]}
    }
  ],
  "dependencies": [{"ref": "app", "dependsOn": ["left"]}, {"ref": "left", "dependsOn": []}]
}`)

	_, err := FromPackage(syntheticPackage(), Options{
		MsisVersion: "test", Supplied: []Supplied{forF1(supplied)}})
	if err == nil {
		t.Fatal("a reference to a definition msis does not import was published")
	}
	for _, want := range []string{"scanner", "metadata.tools", "tools"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}

// The supplied document's own defect, told apart from the one above: a reference to something
// nothing defines. The message has to distinguish them, because one is a limitation of msis and
// the other is a mistake in the document the user was given.
func TestAReferenceToNothingAtAllIsRefused(t *testing.T) {
	supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "app", "name": "a.dll"}},
  "components": [
    {
      "type": "cryptographic-asset", "bom-ref": "cert", "name": "cert",
      "cryptoProperties": {
        "assetType": "certificate",
        "certificateProperties": {"signatureAlgorithmRef": "nowhere"}
      }
    }
  ],
  "dependencies": [{"ref": "app", "dependsOn": ["cert"]}, {"ref": "cert", "dependsOn": []}]
}`)

	_, err := FromPackage(syntheticPackage(), Options{
		MsisVersion: "test", Supplied: []Supplied{forF1(supplied)}})
	if err == nil {
		t.Fatal("a reference to nothing was published")
	}
	if !strings.Contains(err.Error(), "does not define at all") {
		t.Errorf("%q does not distinguish this from a definition msis chose not to import", err)
	}
}

// A BOM-Link addresses ANOTHER document by design, so it is not a local reference and must not
// be refused for failing to resolve inside this one.
func TestABOMLinkIsNotTreatedAsALocalReference(t *testing.T) {
	supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "app", "name": "a.dll"}},
  "components": [
    {
      "type": "library", "bom-ref": "left", "name": "left",
      "evidence": {"identity": [{"field": "purl", "confidence": 1,
        "tools": ["urn:cdx:3e671687-395b-41f5-a30f-a58921a69b79/1#scanner"]}]}
    }
  ],
  "dependencies": [{"ref": "app", "dependsOn": ["left"]}, {"ref": "left", "dependsOn": []}]
}`)

	doc, data := mergedDoc(t, forF1(supplied))
	if !strings.Contains(string(data), "urn:cdx:3e671687") {
		t.Error("the BOM-Link did not survive")
	}
	for _, p := range conformance.Check(data, conformance.Expected{
		IdentifiedComponents:   identifiedRefs(doc),
		SuppliedComponentNames: []string{"a.dll", "left"},
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// CycloneDX permits a document with no metadata at all. msis cannot use one - `for=` would have
// nothing to attach the file to - but it has to SAY so, not reach into a map that is not there.
func TestADocumentWithNoSubjectIsRefusedNotFatal(t *testing.T) {
	for _, supplied := range []string{
		`{"bomFormat":"CycloneDX","specVersion":"1.6","components":[{"type":"library","name":"helper"}]}`,
		`{"bomFormat":"CycloneDX","specVersion":"1.6","metadata":{},"components":[]}`,
		`{"bomFormat":"CycloneDX","specVersion":"1.6","metadata":{"component":{}}}`,
	} {
		_, err := FromPackage(syntheticPackage(), Options{
			MsisVersion: "test", Supplied: []Supplied{forF1([]byte(supplied))}})
		if err == nil {
			t.Errorf("a document with no subject was merged: %s", supplied)
			continue
		}
		if !strings.Contains(err.Error(), "no metadata.component") {
			t.Errorf("%q does not say what is missing", err)
		}
	}
}

// --- round 4: a URN is not a BOM-Link, and nothing is dropped in silence ----------------------

// urnDoc writes a document whose refs are URNs. The schema only says a local ref SHOULD NOT
// start with "urn:cdx:", so "urn:uuid:..." is an ordinary local ref - and exempting every URN
// from reference checking would let a dangling one through both the merge and conformance.
func urnDoc(reference string) []byte {
	return []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "urn:uuid:11111111-1111-4111-8111-111111111111", "name": "a.dll"}},
  "components": [
    {
      "type": "cryptographic-asset", "bom-ref": "urn:uuid:22222222-2222-4222-8222-222222222222",
      "name": "cert",
      "cryptoProperties": {
        "assetType": "certificate",
        "certificateProperties": {"signatureAlgorithmRef": "` + reference + `"}
      }
    },
    {"type": "cryptographic-asset", "bom-ref": "urn:uuid:33333333-3333-4333-8333-333333333333", "name": "alg"}
  ],
  "dependencies": [
    {"ref": "urn:uuid:11111111-1111-4111-8111-111111111111", "dependsOn": ["urn:uuid:22222222-2222-4222-8222-222222222222"]},
    {"ref": "urn:uuid:22222222-2222-4222-8222-222222222222", "dependsOn": ["urn:uuid:33333333-3333-4333-8333-333333333333"]},
    {"ref": "urn:uuid:33333333-3333-4333-8333-333333333333", "dependsOn": []}
  ]
}`)
}

func TestALocalURNRefIsNotMistakenForABOMLink(t *testing.T) {
	// Defined here: an ordinary local ref that happens to be a URN. It has to be namespaced
	// like any other, and it must not be left alone as though it addressed another document.
	doc, data := mergedDoc(t, forF1(urnDoc("urn:uuid:33333333-3333-4333-8333-333333333333")))
	if strings.Contains(string(data), `"urn:uuid:33333333`) {
		t.Error("a local ref that looks like a URN was left in the supplier's namespace")
	}
	for _, p := range conformance.Check(data, conformance.Expected{
		SuppliedComponentNames: []string{"a.dll", "cert", "alg"},
	}) {
		t.Errorf("conformance: %v", p)
	}
	_ = doc

	// Defined nowhere: exempting it because it starts with "urn:" would publish a reference
	// that resolves to nothing.
	_, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test",
		Supplied: []Supplied{forF1(urnDoc("urn:uuid:99999999-9999-4999-8999-999999999999"))}})
	if err == nil {
		t.Fatal("a dangling reference was published because it looked like a URN")
	}
	if !strings.Contains(err.Error(), "99999999") {
		t.Errorf("%q does not name it", err)
	}

	// And a real BOM-Link - urn:cdx: with the syntax the schema defines - still passes: it
	// addresses an element of ANOTHER document on purpose.
	link := "urn:cdx:3e671687-395b-41f5-a30f-a58921a69b79/1#alg"
	if _, _, err := func() (*Document, []byte, error) {
		d, e := FromPackage(syntheticPackage(), Options{MsisVersion: "test",
			Supplied: []Supplied{forF1(urnDoc(link))}})
		if e != nil {
			return nil, nil, e
		}
		raw, e := Marshal(d)
		return d, raw, e
	}(); err != nil {
		t.Errorf("a genuine BOM-Link was refused: %v", err)
	}
}

// metadata.tools has TWO valid shapes in 1.6 - the object with `components`, and the legacy
// array - and msis reads the field only to improve a diagnostic. Typing it as either one alone
// made a valid document fail to decode, which is a narrower thing than msis accepted before.
func TestTheLegacyToolsArrayIsAccepted(t *testing.T) {
	for _, tools := range []string{
		`[{"name": "some-scanner", "version": "1.0"}]`,
		`{"components": [{"type": "application", "name": "some-scanner"}]}`,
		`{"services": [{"name": "a-service"}]}`,
	} {
		supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {
    "component": {"type": "application", "bom-ref": "app", "name": "a.dll"},
    "tools": ` + tools + `
  },
  "components": [{"type": "library", "bom-ref": "left", "name": "left"}],
  "dependencies": [{"ref": "app", "dependsOn": ["left"]}, {"ref": "left", "dependsOn": []}]
}`)
		doc, err := FromPackage(syntheticPackage(), Options{
			MsisVersion: "test", Supplied: []Supplied{forF1(supplied)}})
		if err != nil {
			t.Errorf("tools=%s: %v", tools, err)
			continue
		}
		// Read, never imported: the supplier's tooling is not in the product.
		for _, c := range doc.Components {
			if c.Name == "some-scanner" || c.Name == "a-service" {
				t.Errorf("the supplier's tooling was imported as a component of the installer")
			}
		}
	}
}

// Components are carried raw, so nothing in one can be lost. Dependencies and compositions are
// read into types, and a type is a list of the fields msis knows - so a field it does not know
// vanishes without trace. `provides` vanished exactly that way. Anything that cannot be carried
// is refused instead.
func TestAFieldMsisCannotCarryIsRefusedNotDropped(t *testing.T) {
	for _, tc := range []struct{ name, body, mustSay string }{
		{
			name: "a composition carrying vulnerabilities",
			body: `,"compositions": [{"aggregate": "complete", "dependencies": ["app"],
                    "vulnerabilities": [{"bom-ref": "v1"}]}]`,
			mustSay: "compositions.vulnerabilities",
		},
		{
			name:    "a composition carrying a signature",
			body:    `,"compositions": [{"aggregate": "complete", "dependencies": ["app"], "signature": {"algorithm": "RS512"}}]`,
			mustSay: "compositions.signature",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "app", "name": "a.dll"}},
  "components": [{"type": "library", "bom-ref": "left", "name": "left"}],
  "dependencies": [{"ref": "app", "dependsOn": ["left"]}, {"ref": "left", "dependsOn": []}]` +
				tc.body + `
}`)
			_, err := FromPackage(syntheticPackage(), Options{
				MsisVersion: "test", Supplied: []Supplied{forF1(supplied)}})
			if err == nil {
				t.Fatal("a field msis cannot carry was dropped in silence")
			}
			if !strings.Contains(err.Error(), tc.mustSay) {
				t.Errorf("%q does not name %q", err, tc.mustSay)
			}
		})
	}
}

// A composition may be addressed like anything else, and its ref is a definition: dropping it
// would break a reference to it, and keeping it unchanged would collide with the host's.
func TestACompositionsOwnRefIsNamespaced(t *testing.T) {
	supplied := []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "app", "name": "a.dll"}},
  "components": [{"type": "library", "bom-ref": "left", "name": "left"}],
  "dependencies": [{"ref": "app", "dependsOn": ["left"]}, {"ref": "left", "dependsOn": []}],
  "compositions": [{"bom-ref": "cov", "aggregate": "complete", "dependencies": ["app", "left"]}]
}`)

	doc, data := mergedDoc(t, forF1(supplied))

	var found string
	for _, c := range doc.Compositions {
		if c.BOMRef != "" {
			found = c.BOMRef
		}
	}
	if found == "" {
		t.Fatal("the composition's own ref was dropped")
	}
	if !strings.Contains(found, "/supplied/app.cdx.json/") {
		t.Errorf("composition ref %q is not in the namespace of the document it came from", found)
	}
	for _, p := range conformance.Check(data, conformance.Expected{
		SuppliedComponentNames: []string{"a.dll", "left"},
	}) {
		t.Errorf("conformance: %v", p)
	}
}
