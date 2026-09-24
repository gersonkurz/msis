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
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
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

	// UnhashableComponents are the bom-refs whose bytes are not in the artifact at all - a
	// bundle payload the engine downloads at install time is the case that exists. They are
	// the only components allowed to lack a SHA-256, and even then they must carry some
	// digest the artifact records and say why the SHA-256 is missing.
	//
	// The caller must derive this from the ARTIFACT, not from the document: an exemption
	// read out of the document under test would excuse exactly the components whose digest
	// the emitter dropped. The check is two-sided for the same reason - a ref listed here
	// that DOES carry a SHA-256 fails too, so a stale list cannot hide a regression.
	UnhashableComponents []string

	// SuppliedComponentNames are the component NAMES a supplied SBOM contributed (#36),
	// as a MULTISET, derived from the supplied document rather than from the output.
	//
	// Names rather than bom-refs because the emitter namespaces an imported ref - a caller
	// listing refs would have to re-derive that namespacing, and a re-derivation that drifts
	// tests nothing. Names come straight out of the supplied document.
	//
	// The check is two-sided. A merged component that is NOT expected means the merge invented
	// one; an expected component that is missing means the merge dropped one. And a supplied
	// component may carry no digest at all - msis never had those bytes, so there was never a
	// hash to drop - which is why the marking must be earned rather than assumed: a component
	// marked as supplied while also being installed payload would launder the digest rule, and
	// that combination fails.
	SuppliedComponentNames []string

	// DetectedComponents are the bom-refs for things the installer merely DETECTS and does
	// not distribute - a /STANDALONE build's prerequisites become launch conditions (#34).
	// They carry no digest of any kind, and could not: nothing was shipped, so there are no
	// bytes anywhere to hash. That is a narrower case than UnhashableComponents, where the
	// artifact at least records a digest for a payload it will fetch later.
	//
	// As with the other expectations, the caller derives this from the SCRIPT or the build,
	// never from the document under test, and the check is two-sided: a ref listed here that
	// DOES carry a digest fails, because the list and the build would then disagree.
	DetectedComponents []string
}

// Check runs every rule and returns each failure. It returns them all rather than stopping at
// the first, because an emitter under development usually breaks several at once and a single
// error hides the shape of the problem.
func Check(data []byte, want Expected) []error {
	var problems []error
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}

	if err := ValidateSchema(data); err != nil {
		fail("schema: %w", err)
	}

	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return append(problems, fmt.Errorf("the document is not JSON: %w", err))
	}
	components := doc.allComponents()

	// --- identity -------------------------------------------------------------------------
	// #29 D4: never guess. A wrong purl produces false CVE matches and hides real ones, which
	// is worse than no purl at all.
	identified := map[string]bool{}
	for _, ref := range want.IdentifiedComponents {
		identified[ref] = true
	}
	for _, c := range components {
		if c.PURL != "" && !identified[c.BOMRef] {
			fail("component %q carries purl %q but its identity was not determined", c.BOMRef, c.PURL)
		}
	}

	// --- digests --------------------------------------------------------------------------
	// #29 D5: SHA-256 on every payload file. The digest is what makes the document verifiable
	// against a customer's installation, so the single admitted exception - bytes that are
	// not in the artifact for msis to hash - has to be declared by the caller and still
	// carry a digest and a stated reason.
	unhashable := map[string]bool{}
	for _, ref := range want.UnhashableComponents {
		unhashable[ref] = true
	}
	detected := map[string]bool{}
	for _, ref := range want.DetectedComponents {
		detected[ref] = true
	}
	for _, c := range components {
		switch {
		case hasSHA256(c.Hashes):
			if unhashable[c.BOMRef] || detected[c.BOMRef] {
				fail("component %q was declared as distributing nothing but carries a "+
					"SHA-256; the expectation and the build disagree", c.BOMRef)
			}
		case detected[c.BOMRef]:
			// Nothing was distributed, so there are no bytes to hash anywhere - not even
			// a digest the artifact recorded for a later download. All that can be asked
			// is that the document say so.
			if !c.hasProperty(propPayloadUnavailable) {
				fail("component %q distributes nothing and the document does not say why "+
					"it has no digest", c.BOMRef)
			}
		case c.suppliedFrom() != "":
			// Received, not observed. msis never held these bytes, so it had no hash to
			// publish and none to drop; whatever digest the supplier gave is carried as
			// given. The exemption cannot launder a payload component - that is checked
			// below, where the two markings meeting is an error.
		case !unhashable[c.BOMRef]:
			fail("component %q has no SHA-256", c.BOMRef)
		case len(c.Hashes) == 0:
			fail("component %q is not in the artifact, which is admitted, but carries no "+
				"digest at all, so nothing about it can be verified", c.BOMRef)
		case !c.hasProperty(propPayloadUnavailable):
			fail("component %q has no SHA-256 and the document does not say why", c.BOMRef)
		}
	}

	// --- completeness ---------------------------------------------------------------------
	if len(want.PayloadNames) > 0 {
		have := map[string]int{}
		for _, c := range components {
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

	// --- supplied components (#36) --------------------------------------------------------
	// Merging a document establishes nothing about it, so the check is not that it is right -
	// it is that the merge neither invented a component nor dropped one, and that the marking
	// which excuses a missing digest cannot be attached to something msis itself packaged.
	{
		have, expect := map[string]int{}, map[string]int{}
		for _, c := range components {
			if c.suppliedFrom() == "" {
				continue
			}
			have[c.Name]++
			if c.role() == rolePayload {
				fail("component %q is marked as supplied by %q and as installed payload; "+
					"the supplied marking would then excuse a digest msis should have",
					c.BOMRef, c.suppliedFrom())
			}
		}
		for _, name := range want.SuppliedComponentNames {
			expect[name]++
		}
		for name, n := range expect {
			if got := have[name]; got != n {
				fail("the supplied document contributes %d component(s) named %q; the "+
					"merged document has %d", n, name, got)
			}
		}
		for name, n := range have {
			if _, ok := expect[name]; !ok {
				fail("the merged document has %d component(s) named %q marked as supplied, "+
					"but nothing supplied them", n, name)
			}
		}
	}

	// --- references -------------------------------------------------------------------------
	refs := map[string]bool{doc.Metadata.Component.BOMRef: true}
	for _, c := range components {
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
	// A reference is not only `dependsOn` and a composition's lists. The schema puts one in
	// `signatureAlgorithmRef`, in `evidence.identity.tools`, in `pedigree`, in `provides` -
	// twenty fields in all - and a document that keeps one pointing at something no longer
	// there is a document whose graph cannot be followed. Checking only the two outer lists
	// let exactly that through twice in review.
	//
	// Values that are BOM-Links address ANOTHER document by design and are not local refs, so
	// they are not expected to resolve here.
	for _, problem := range danglingInsideComponents(data, refs) {
		fail("%s", problem)
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
	for _, c := range components {
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
	} else if !msisIsHashed(doc.Metadata.Tools.Components) {
		fail("metadata.tools does not name msis with its SHA-256: the document must say which " +
			"build of the generator produced it (#29 D7)")
	}
	if doc.Metadata.Component.Name == "" {
		fail("the subject component has no name")
	}
	// NTIA's "present or explicitly unknown": an element the artifact does not record may be
	// absent only when the subject says so (msis:ntia.unknown, #59); a silent gap is a defect.
	if doc.Metadata.Component.Version == "" && !statedUnknown(doc.Metadata.Component, "version") {
		fail("the subject component has no version, and does not state it as unknown")
	}
	if (doc.Metadata.Supplier == nil || doc.Metadata.Supplier.Name == "") &&
		!statedUnknown(doc.Metadata.Component, "supplier") {
		fail("no supplier (NTIA requires one, or an explicit statement that it is unknown)")
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

// danglingInsideComponents reports every reference held inside a component that resolves to
// nothing. It walks the raw JSON rather than the types above, because the whole point is the
// fields this package does not model.
func danglingInsideComponents(data []byte, refs map[string]bool) []string {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	defined := map[string]bool{}
	for ref := range refs {
		defined[ref] = true
	}
	// Anything the document defines anywhere counts as defined - metadata.tools included -
	// so this reports references that resolve to NOTHING rather than merely to something the
	// types above do not model.
	collectDefined(raw, defined)

	fields := ReferenceFieldNames()
	seen := map[string]bool{}
	var problems []string
	var walk func(node any, where string)
	walk = func(node any, where string) {
		switch n := node.(type) {
		case map[string]any:
			at := where
			if ref, ok := n["bom-ref"].(string); ok && ref != "" {
				at = ref
			}
			for key, v := range n {
				if key != "bom-ref" && fields[key] {
					for _, value := range stringsOf(v) {
						// Defined here FIRST: a ref this document defines resolves whatever
						// it looks like, including one that is itself a URN. Only a value
						// that resolves to nothing needs the external-link exemption.
						if defined[value] || IsBOMLink(value) {
							continue
						}
						problem := fmt.Sprintf("component %q references %q in %s, which is "+
							"not in this document", at, value, key)
						if !seen[problem] {
							seen[problem] = true
							problems = append(problems, problem)
						}
					}
				}
				walk(v, at)
			}
		case []any:
			for _, item := range n {
				walk(item, where)
			}
		}
	}
	walk(raw["components"], "")
	if meta, ok := raw["metadata"].(map[string]any); ok {
		walk(meta["component"], "")
	}
	sort.Strings(problems)
	return problems
}

func collectDefined(node any, into map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["bom-ref"].(string); ok && ref != "" {
			into[ref] = true
		}
		for _, v := range n {
			collectDefined(v, into)
		}
	case []any:
		for _, item := range n {
			collectDefined(item, into)
		}
	}
}

func stringsOf(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ValidateSchema checks a document against the vendored CycloneDX 1.6 schema. Separated because
// ticket D consumes documents rather than emitting them and needs this alone.
func ValidateSchema(data []byte) error {
	sch, err := compile()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	return sch.Validate(v)
}

// schemaFS holds the official CycloneDX 1.6 schema and the two it references, vendored so that
// no caller needs the network, and EMBEDDED rather than read from a directory.
//
// It used to be a directory the caller named, cached per directory - a caller-chosen path meant
// a single sync.Once let the first caller's directory win, and a later call naming a different
// or missing one validated against the cached schema and reported success. Embedding removes
// the parameter and the bug class with it, and is what lets a tool outside this module (the
// SBOM index, #35) validate from any working directory.
//
//go:embed schema/*.json
var schemaFS embed.FS

var (
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
)

// compile loads the vendored schema and the two it references. Registered under their canonical
// $id values, not their filenames: the refs inside are absolute URLs and would not otherwise
// resolve - and an unresolvable ref means a constraint silently not checked.
func compile() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		for _, name := range []string{"spdx.schema.json", "jsf-0.82.schema.json", "bom-1.6.schema.json"} {
			f, err := schemaFS.Open("schema/" + name)
			if err != nil {
				schemaErr = fmt.Errorf("the vendored CycloneDX schema is incomplete: %w", err)
				return
			}
			doc, err := jsonschema.UnmarshalJSON(f)
			f.Close()
			if err != nil {
				schemaErr = fmt.Errorf("parsing %s: %w", name, err)
				return
			}
			c.AddResource("http://cyclonedx.org/schema/"+name, doc)
		}
		schema, schemaErr = c.Compile("http://cyclonedx.org/schema/bom-1.6.schema.json")
	})
	return schema, schemaErr
}

func hasSHA256(hashes []hash) bool {
	for _, h := range hashes {
		if strings.EqualFold(h.Alg, "SHA-256") && len(h.Content) == 64 {
			return true
		}
	}
	return false
}

// statedUnknown reports whether the component declares the NTIA element as unknown.
func statedUnknown(c component, element string) bool {
	for _, p := range c.Properties {
		if p.Name == "msis:ntia.unknown" && strings.HasPrefix(p.Value, element+": ") {
			return true
		}
	}
	return false
}

// msisIsHashed reports whether the tools name msis and carry the digest of the binary that ran.
func msisIsHashed(tools []component) bool {
	for _, t := range tools {
		if t.Name == "msis" && hasSHA256(t.Hashes) {
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

	// CycloneDX lets a component nest components, and a supplied document may well do so.
	// A nested one carries its own bom-ref and can be the target of a dependency, so every
	// rule here applies to it exactly as it does to a top-level one.
	Components []component `json:"components"`
}

// allComponents flattens the component tree, so no rule can be evaded by nesting.
func (d document) allComponents() []component {
	var out []component
	var walk func([]component)
	walk = func(list []component) {
		for _, c := range list {
			out = append(out, c)
			walk(c.Components)
		}
	}
	walk(d.Components)
	return out
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

// propPayloadUnavailable is the property a component must carry when it has no SHA-256: the
// document has to state the gap, not merely leave one.
const propPayloadUnavailable = "msis:payload.unavailable"

// propSuppliedFrom marks a component that came from a supplied document rather than from the
// artifact (#36). See Expected.SuppliedComponentNames.
const propSuppliedFrom = "msis:supplied.from"

func (c component) suppliedFrom() string {
	for _, p := range c.Properties {
		if p.Name == propSuppliedFrom {
			return p.Value
		}
	}
	return ""
}

func (c component) hasProperty(name string) bool {
	for _, p := range c.Properties {
		if p.Name == name {
			return true
		}
	}
	return false
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

// IsBOMLink reports whether a value is a BOM-Link: a reference to an element of ANOTHER
// document, which is therefore not expected to resolve inside this one.
//
// The test is the schema's own pattern for bomLinkElementType, read from the vendored copy
// rather than restated here. "Any urn:" is NOT the test and treating it as one is a hole: the
// schema says only that a local ref SHOULD NOT start with `urn:cdx:`, so `urn:uuid:...` is a
// perfectly ordinary local ref, and exempting it would let a dangling one through.
func IsBOMLink(value string) bool {
	bomLinkOnce.Do(func() {
		var schema map[string]any
		data, err := schemaFS.ReadFile("schema/bom-1.6.schema.json")
		if err != nil || json.Unmarshal(data, &schema) != nil {
			return
		}
		defs, _ := schema["definitions"].(map[string]any)
		el, _ := defs["bomLinkElementType"].(map[string]any)
		pattern, _ := el["pattern"].(string)
		if pattern == "" {
			return
		}
		bomLinkRe, _ = regexp.Compile(pattern)
	})
	return bomLinkRe != nil && bomLinkRe.MatchString(value)
}

var (
	bomLinkOnce sync.Once
	bomLinkRe   *regexp.Regexp
)

// ReferenceFieldNames gives the property names the CycloneDX schema declares as references to a
// component, DERIVED FROM THE VENDORED SCHEMA rather than listed here.
//
// It exists because a merge has to namespace imported refs (#36), and a ref is not only
// `bom-ref` and `dependsOn`: 1.6 puts one in `signatureAlgorithmRef`, `subjectPublicKeyRef`,
// `parent`, `provides`, `claims`, `requirements` and a dozen more. A hand-written list of those
// would be a copy of the schema that drifts from it, and a missed field silently turns a valid
// supplied document into one with dangling references.
//
// `bom-ref` is excluded: it DEFINES a reference rather than using one, and the caller treats the
// two differently.
func ReferenceFieldNames() map[string]bool {
	refFieldsOnce.Do(func() {
		refFields = map[string]bool{}
		var schema any
		data, err := schemaFS.ReadFile("schema/bom-1.6.schema.json")
		if err != nil || json.Unmarshal(data, &schema) != nil {
			return
		}
		var walk func(node any, property string)
		walk = func(node any, property string) {
			switch n := node.(type) {
			case map[string]any:
				if property != "" && property != "bom-ref" {
					if isRefSchema(n) || isRefSchema(n["items"]) {
						refFields[property] = true
					}
				}
				for k, v := range n {
					if k == "properties" {
						if props, ok := v.(map[string]any); ok {
							for name, sub := range props {
								walk(sub, name)
							}
							continue
						}
					}
					walk(v, property)
				}
			case []any:
				for _, item := range n {
					walk(item, property)
				}
			}
		}
		walk(schema, "")
	})
	return refFields
}

// isRefSchema reports whether a schema node points at one of the reference types. bomLinkElement
// is included: a field that may hold a link to another document may also hold a local ref, and
// the caller rewrites only values it recognises as local anyway.
func isRefSchema(node any) bool {
	m, ok := node.(map[string]any)
	if !ok {
		return false
	}
	ref, _ := m["$ref"].(string)
	switch ref[strings.LastIndex(ref, "/")+1:] {
	case "refType", "refLinkType", "bomLinkElementType":
		return ref != ""
	}
	return false
}

var (
	refFieldsOnce sync.Once
	refFields     map[string]bool
)
