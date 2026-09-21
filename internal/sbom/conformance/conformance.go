// Package conformance checks that a CycloneDX document msis produced obeys the rules issue #29
// fixed for every emitter.
//
// It exists because those rules are cross-cutting and a rule that lives only in prose drifts.
// Tickets B, C and E each add an emitter; each runs its output through Check rather than
// restating what "correct" means. Ticket D is a consumer and reuses ValidateSchema alone.
//
// Some rules cannot be checked from the document. Whether a purl was derived from evidence, or
// whether an omitted payload ever existed, is invisible in the JSON - so Check takes an Expected
// describing what the emitter was given, and compares the document against that rather than
// against itself.
package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Expected is the independent input: what the emitter was handed, so the document can be
// checked for saying it. Leave a field nil to skip that comparison.
type Expected struct {
	// PayloadNames are the files the artifact is known to contain, as a MULTISET: list a
	// name twice if the artifact holds two files of that name, which happens whenever two
	// features contribute their own copy.
	//
	// Multiplicity is compared, not mere presence. Collapsing these into a set let a document
	// drop one of two same-named payloads and still pass - and let a Binary stream, which is
	// executed rather than installed, satisfy an expectation about installed payload. Only
	// components marked as payload are counted.
	PayloadNames []string

	// IdentifiedComponents are the bom-refs whose identity really was determined, and so may
	// legitimately carry a purl. Any OTHER component carrying one is guessing.
	IdentifiedComponents []string
}

// Check runs every rule and returns each failure. It returns them all rather than stopping at
// the first, because an emitter under development usually breaks several at once and a single
// error hides the shape of the problem.
func Check(schemaDir string, data []byte, want Expected) []error {
	var problems []error
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}

	if err := ValidateSchema(schemaDir, data); err != nil {
		fail("schema: %w", err)
	}

	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return append(problems, fmt.Errorf("the document is not JSON: %w", err))
	}

	// --- identity -------------------------------------------------------------------------
	// #29 D4: never guess. A wrong purl produces false CVE matches and hides real ones, which
	// is worse than no purl at all.
	identified := map[string]bool{}
	for _, ref := range want.IdentifiedComponents {
		identified[ref] = true
	}
	for _, c := range doc.Components {
		if c.PURL != "" && !identified[c.BOMRef] {
			fail("component %q carries purl %q but its identity was not determined", c.BOMRef, c.PURL)
		}
	}

	// --- digests --------------------------------------------------------------------------
	// #29 D5: SHA-256 on every payload file, no exceptions. The digest is what makes the
	// document verifiable against a customer's installation.
	for _, c := range doc.Components {
		if !hasSHA256(c.Hashes) {
			fail("component %q has no SHA-256", c.BOMRef)
		}
	}

	// --- completeness ---------------------------------------------------------------------
	if len(want.PayloadNames) > 0 {
		have := map[string]int{}
		for _, c := range doc.Components {
			if c.role() == rolePayload {
				have[c.Name]++
			}
		}
		expect := map[string]int{}
		for _, name := range want.PayloadNames {
			expect[name]++
		}
		for name, n := range expect {
			switch got := have[name]; {
			case got == 0:
				fail("payload %q is in the artifact but missing from the document", name)
			case got < n:
				fail("the artifact holds %d payload file(s) named %q; the document has %d",
					n, name, got)
			}
		}
	}

	// --- references -------------------------------------------------------------------------
	refs := map[string]bool{doc.Metadata.Component.BOMRef: true}
	for _, c := range doc.Components {
		if c.BOMRef == "" {
			fail("component %q has no bom-ref", c.Name)
			continue
		}
		if refs[c.BOMRef] {
			fail("bom-ref %q is used more than once", c.BOMRef)
		}
		refs[c.BOMRef] = true
	}
	for _, d := range doc.Dependencies {
		if !refs[d.Ref] {
			fail("dependency names ref %q, which is not a component", d.Ref)
		}
		for _, on := range d.DependsOn {
			if !refs[on] {
				fail("dependency %q dependsOn %q, which is not a component", d.Ref, on)
			}
		}
	}
	for _, comp := range doc.Compositions {
		// Both fields name components and both have to resolve. Checking only assemblies let
		// a composition declare the dependency completeness of something that is not there.
		for _, a := range comp.Assemblies {
			if !refs[a] {
				fail("composition %q names %q in assemblies, which is not a component",
					comp.Aggregate, a)
			}
		}
		for _, d := range comp.Dependencies {
			if !refs[d] {
				fail("composition %q names %q in dependencies, which is not a component",
					comp.Aggregate, d)
			}
		}
	}

	// --- knowledge states -------------------------------------------------------------------
	//
	// Three states, and the document has to say which applies to each component:
	//
	//   known-empty  a dependency entry with an empty dependsOn - "depends on nothing"
	//   unknown      no dependency entry, and a composition listing it under DEPENDENCIES
	//   incomplete   a dependency entry, and a composition saying the graph is partial
	//
	// The composition's `dependencies` field is what carries this, not `assemblies`: the two
	// are separate in the schema and mean different things - how completely a component is
	// described, versus how completely its dependency graph is known.
	if len(doc.Compositions) == 0 {
		fail("no compositions: the document does not say how complete it is")
	}
	// Every declaration is kept, not just the last. Overwriting let a later "complete" bury
	// an earlier "unknown" for the same component, so a contradiction passed or failed
	// depending on the order the compositions happened to appear in.
	depsDeclared := map[string][]string{} // ref -> every aggregate declared for its graph
	for _, comp := range doc.Compositions {
		switch comp.Aggregate {
		case "complete", "incomplete", "incomplete_first_party_only", "incomplete_first_party_proprietary_only",
			"incomplete_first_party_opensource_only", "incomplete_third_party_only",
			"incomplete_third_party_proprietary_only", "incomplete_third_party_opensource_only",
			"unknown", "not_specified":
		default:
			fail("composition aggregate %q is not one the schema defines", comp.Aggregate)
		}
		if len(comp.Assemblies) == 0 && len(comp.Dependencies) == 0 {
			fail("composition %q names no components; a completeness declaration does not cascade",
				comp.Aggregate)
		}
		for _, ref := range comp.Dependencies {
			depsDeclared[ref] = append(depsDeclared[ref], comp.Aggregate)
		}
	}

	// A component's dependency graph cannot be two different degrees of known at once,
	// whichever order the declarations appear in.
	for ref, aggregates := range depsDeclared {
		distinct := map[string]bool{}
		for _, a := range aggregates {
			distinct[a] = true
		}
		if len(distinct) > 1 {
			sorted := make([]string, 0, len(distinct))
			for a := range distinct {
				sorted = append(sorted, a)
			}
			sort.Strings(sorted)
			fail("%q has conflicting dependency-completeness declarations: %s",
				ref, strings.Join(sorted, " and "))
		}
	}

	stated := map[string]bool{}
	for _, d := range doc.Dependencies {
		stated[d.Ref] = true
		if len(d.DependsOn) != 0 {
			continue
		}
		// An empty dependsOn asserts "depends on nothing". That is only compatible with a
		// declaration that the graph is fully known.
		switch {
		case len(depsDeclared[d.Ref]) == 0:
			fail("%q has an empty dependsOn, asserting it depends on nothing, and no "+
				"composition says otherwise; if that is unknown, say so in a composition's "+
				"dependencies", d.Ref)
		case !allComplete(depsDeclared[d.Ref]):
			fail("%q has an empty dependsOn, asserting it depends on nothing, while a "+
				"composition declares its dependencies %s - the document contradicts itself",
				d.Ref, strings.Join(depsDeclared[d.Ref], " and "))
		}
	}
	// A component with neither a dependency entry nor a declaration is simply unstated, which
	// is the gap this whole section exists to close.
	for _, c := range doc.Components {
		if !stated[c.BOMRef] && len(depsDeclared[c.BOMRef]) == 0 {
			fail("nothing in the document says what %q depends on, or that it is unknown",
				c.BOMRef)
		}
	}

	// --- NTIA minimum elements ----------------------------------------------------------------
	if doc.Metadata.Timestamp == "" {
		fail("no metadata.timestamp (NTIA requires one)")
	}
	if len(doc.Metadata.Tools.Components) == 0 {
		fail("no metadata.tools (NTIA requires the SBOM's author)")
	}
	if doc.Metadata.Component.Name == "" {
		fail("the subject component has no name")
	}
	if doc.Metadata.Component.Version == "" {
		fail("the subject component has no version")
	}
	if doc.Metadata.Supplier == nil || doc.Metadata.Supplier.Name == "" {
		fail("no supplier (NTIA requires one)")
	}
	if !hasSHA256(doc.Metadata.Component.Hashes) {
		fail("the subject component has no SHA-256: a BOM-Link could not be checked against " +
			"the artifact it claims to describe")
	}

	// --- ordering -----------------------------------------------------------------------------
	// #29 D6: sorted, not merely stable, so a diff between two documents is a release review.
	if !sort.SliceIsSorted(doc.Components, func(i, j int) bool {
		return doc.Components[i].BOMRef < doc.Components[j].BOMRef
	}) {
		fail("components are not sorted by bom-ref")
	}

	return problems
}

// ValidateSchema checks a document against the vendored CycloneDX 1.6 schema. Separated because
// ticket D consumes documents rather than emitting them and needs this alone.
func ValidateSchema(schemaDir string, data []byte) error {
	sch, err := compile(schemaDir)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	return sch.Validate(v)
}

// Compiling the schema chain takes about a second, so the result is cached - but keyed by
// directory, not once for the process. A single sync.Once made the FIRST caller's directory
// win: a later call naming a different or missing directory silently validated against the
// cached schema and reported success, which is the one outcome a validator must never produce.
type compiledSchema struct {
	schema *jsonschema.Schema
	err    error
}

var (
	schemaMu    sync.Mutex
	schemaCache = map[string]compiledSchema{}
)

// compile loads the vendored schema and the two it references. Registered under their canonical
// $id values, not their filenames: the refs inside are absolute URLs and would not otherwise
// resolve - and an unresolvable ref means a constraint silently not checked.
func compile(dir string) (*jsonschema.Schema, error) {
	schemaMu.Lock()
	defer schemaMu.Unlock()
	if got, ok := schemaCache[dir]; ok {
		return got.schema, got.err
	}

	result := func() compiledSchema {
		c := jsonschema.NewCompiler()
		for _, name := range []string{"spdx.schema.json", "jsf-0.82.schema.json", "bom-1.6.schema.json"} {
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				return compiledSchema{err: fmt.Errorf(
					"the vendored CycloneDX schema is incomplete: %w", err)}
			}
			doc, err := jsonschema.UnmarshalJSON(f)
			f.Close()
			if err != nil {
				return compiledSchema{err: fmt.Errorf("parsing %s: %w", name, err)}
			}
			c.AddResource("http://cyclonedx.org/schema/"+name, doc)
		}
		sch, err := c.Compile("http://cyclonedx.org/schema/bom-1.6.schema.json")
		return compiledSchema{schema: sch, err: err}
	}()

	schemaCache[dir] = result
	return result.schema, result.err
}

func hasSHA256(hashes []hash) bool {
	for _, h := range hashes {
		if strings.EqualFold(h.Alg, "SHA-256") && len(h.Content) == 64 {
			return true
		}
	}
	return false
}

// The shapes this package reads. Deliberately its own minimal view rather than the emitter's
// types: a check that shares structs with the thing it checks cannot catch a field that was
// never emitted.
type document struct {
	Metadata struct {
		Timestamp string `json:"timestamp"`
		Tools     struct {
			Components []component `json:"components"`
		} `json:"tools"`
		Component component `json:"component"`
		Supplier  *struct {
			Name string `json:"name"`
		} `json:"supplier"`
	} `json:"metadata"`
	Components   []component `json:"components"`
	Dependencies []struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn"`
	} `json:"dependencies"`
	Compositions []struct {
		Aggregate    string   `json:"aggregate"`
		Assemblies   []string `json:"assemblies"`
		Dependencies []string `json:"dependencies"`
	} `json:"compositions"`
}

type component struct {
	BOMRef     string     `json:"bom-ref"`
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	PURL       string     `json:"purl"`
	Hashes     []hash     `json:"hashes"`
	Properties []property `json:"properties"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// rolePayload marks a component that is installed, as opposed to a Binary-table stream that is
// executed during installation and never lands on the machine. The distinction matters to the
// completeness check: a stream must not satisfy an expectation about installed payload.
const rolePayload = "payload"

func (c component) role() string {
	for _, p := range c.Properties {
		if p.Name == "msis:role" {
			return p.Value
		}
	}
	return ""
}

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

// allComplete reports whether every declaration says the dependency graph is fully known. Only
// then is an empty dependsOn - "depends on nothing" - consistent with what the document says.
func allComplete(aggregates []string) bool {
	for _, a := range aggregates {
		if a != "complete" {
			return false
		}
	}
	return len(aggregates) > 0
}
