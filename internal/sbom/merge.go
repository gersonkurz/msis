package sbom

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// Composition of a supplied component SBOM into the installer's document (#36).
//
// msis reads the bytes of a payload file and can say nothing about what is inside it. The build
// system that produced that file can, and a product's top-level dependencies - the ones the CRA
// asks about - live exactly there, inside the manufacturer's own binaries. `<sbom source= for=>`
// is the route to them.
//
// Three rules shape everything here, and they all say the same thing in different words:
//
//   - RECEIVING A DOCUMENT ESTABLISHES NOTHING ABOUT IT. Merging does not make a supplied
//     document complete, accurate, or about the file it claims to describe. Where it carries a
//     digest msis checks that one thing and fails the build on a mismatch; everything else is
//     carried as received and marked as supplied, so a reader can tell msis's own observations
//     from someone else's assertions.
//   - THE SUPPLIED DOCUMENT'S UNCERTAINTY SURVIVES. Its compositions are imported as they
//     stand. A component it says nothing about becomes explicitly unknown rather than silently
//     complete, and the payload file's own dependency graph is only declared complete when the
//     supplied document actually asserts completeness for what it describes.
//   - NOTHING IS INVENTED. Components are emitted VERBATIM - every field, including ones msis
//     does not model - with exactly one thing rewritten: the document-local bom-ref, which has
//     to be namespaced because two documents are becoming one and refs must stay unique. That
//     is addressing, not identity: purl, name, version, supplier, hashes and licences are
//     untouched.
type Supplied struct {
	Source string // the .cdx.json as authored in the .msis, published as provenance
	Target string // the install target the script named, for messages
	FileID string // the WiX File id of the payload it describes - an exact join, not a name
	Data   []byte // the document, as read from disk
}

// suppliedDocument is the permissive view of a supplied document: the parts msis reasons about
// as types, and the components as raw JSON so they can be re-emitted without loss.
type suppliedDocument struct {
	SerialNumber string           `json:"serialNumber"`
	Metadata     suppliedMetadata `json:"metadata"`
	Components   []rawComponent   `json:"components"`
	Dependencies []Dependency     `json:"dependencies"` // carries Provides; see the type
	Compositions []Composition    `json:"compositions"`
}

type suppliedMetadata struct {
	Component rawComponent `json:"component"`

	// Tools is read but never imported. A supplied document may define a bom-ref here - the
	// scanner that produced it - and reference it from a component's evidence. msis does not
	// import someone else's tooling as a component of the installer (it is not in the
	// product), so such a reference cannot be preserved; knowing the ref is defined HERE is
	// what lets the refusal say so instead of guessing.
	//
	// `any`, not a map: 1.6 defines this field as a oneOf - the object form with `components`
	// and `services`, and the legacy array of {name, version}. Typing it as either one makes
	// a valid document fail to decode, and this field exists to improve a diagnostic, not to
	// narrow what msis accepts.
	Tools any `json:"tools"`
}

// rawComponent is one component exactly as the supplier wrote it.
type rawComponent map[string]any

func (r rawComponent) str(key string) string {
	if v, ok := r[key].(string); ok {
		return v
	}
	return ""
}

// children gives the nested components, which CycloneDX allows and which carry bom-refs of
// their own: they have to be namespaced and accounted for like any other.
func (r rawComponent) children() []rawComponent {
	list, _ := r["components"].([]any)
	out := make([]rawComponent, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, rawComponent(m))
		}
	}
	return out
}

func (r rawComponent) sha256() string {
	list, _ := r["hashes"].([]any)
	for _, item := range list {
		h, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if alg, _ := h["alg"].(string); strings.EqualFold(alg, "SHA-256") {
			content, _ := h["content"].(string)
			return strings.ToLower(content)
		}
	}
	return ""
}

func (r rawComponent) addProperty(name, value string) {
	list, _ := r["properties"].([]any)
	r["properties"] = append(list, map[string]any{"name": name, "value": value})
}

// mergeSupplied composes each supplied document into doc. It runs after enrichment and before
// sorting, so what it adds is ordered with everything else.
// mergeState is what one merge run has to remember across the documents in it: which source
// has already been imported (so a document used for two files is imported once), and what each
// import declared (so the payload file's own marking can be decided from it).
type mergeState struct {
	roots    map[string]string // source -> the ref of the subject it describes
	declared map[string]string // ref -> the aggregate the supplier declared for its graph
}

func mergeSupplied(doc *Document, supplied []Supplied) error {
	state := &mergeState{roots: map[string]string{}, declared: map[string]string{}}
	merged := map[string]string{} // FileID -> the source already merged onto it
	for _, s := range supplied {
		if first, ok := merged[s.FileID]; ok {
			return fmt.Errorf("both %s and %s are supplied for %s: msis does not combine two "+
				"documents into one claim about the same file, because their coverage "+
				"statements would have to be reconciled and only their authors can do that",
				first, s.Source, s.Target)
		}
		merged[s.FileID] = s.Source
		if err := mergeOne(doc, s, state); err != nil {
			return fmt.Errorf("merging %s for %s: %w", s.Source, s.Target, err)
		}
	}
	return nil
}

func mergeOne(doc *Document, s Supplied, state *mergeState) error {
	var sup suppliedDocument
	if err := json.Unmarshal(s.Data, &sup); err != nil {
		return fmt.Errorf("the supplied document is not readable CycloneDX JSON: %w", err)
	}

	// Components are carried raw, so nothing in one can be lost. Dependencies and
	// compositions are read into types, and a type is a list of the fields msis knows -
	// which means a field it does NOT know disappears without trace. `provides` disappeared
	// exactly that way once. So anything msis cannot carry is refused rather than dropped.
	if err := refuseWhatWouldBeDropped(s.Data); err != nil {
		return err
	}

	target := componentWithFileKey(doc, s.FileID)
	if target == nil {
		return fmt.Errorf("no component of the artifact carries file id %s", s.FileID)
	}

	// The one thing msis can check, and does. A digest that disagrees means the document
	// describes different bytes from the ones packaged, and publishing it would attach a
	// dependency graph to a file it was never about.
	if want := sup.Metadata.Component.sha256(); want != "" {
		// Validated before it is compared or printed. A supplied document is someone else's
		// file: "abc" in a SHA-256 field is a malformed document, which is a build error to
		// report, not a slice out of range.
		if !isSHA256(want) {
			return fmt.Errorf("the supplied document gives %q as the SHA-256 of what it "+
				"describes, which is not a SHA-256", want)
		}
		if got := sha256Of(target.Hashes); got != "" && got != want {
			return fmt.Errorf("the supplied document describes bytes with SHA-256 %s..., but "+
				"the package contains %s... - the document is about a different build of %s",
				abbreviate(want), abbreviate(got), target.Name)
		}
	}

	// Namespacing. Both documents number their own components from one, and a ref is
	// document-local addressing: two of them becoming one document means the imported side has
	// to move into a namespace of its own. Keyed on the SOURCE as authored, so the same build
	// produces the same refs every time.
	//
	// The same document may legitimately describe two files - identical bytes installed at two
	// targets - and then it is imported ONCE and both files point at it. Importing it twice
	// under one namespace would emit every component and every ref twice.
	prefix := namespaceFor(doc) + "/supplied/" + encodeRefPart(s.Source) + "/"
	rootRef, already := state.roots[s.Source]
	if !already {
		var err error
		if rootRef, err = importDocument(doc, s, sup, prefix, state); err != nil {
			return err
		}
		state.roots[s.Source] = rootRef
	}

	// The file contains what the supplied document describes. That is what `for=` asserts and
	// it is all msis asserts: the supplied subject's own graph is whatever its author said.
	doc.Dependencies = append(doc.Dependencies,
		Dependency{Ref: target.BOMRef, DependsOn: []string{rootRef}})

	// The payload file's own graph. Complete only when the supplier actually asserted
	// completeness for what the file contains.
	//
	// Otherwise it stays UNKNOWN - the marking the artifact-only document already gave it -
	// and not `incomplete`. The schema's own words settle this: incomplete says "additional
	// relationships exist", which is a claim that something is missing, and knowing one thing
	// that is in the file establishes nothing about whether anything else is. Unknown says
	// completeness is not known, which is the fact.
	aggregate := aggregateUnknown
	if state.declared[rootRef] == aggregateComplete {
		aggregate = aggregateComplete
		clearDependencyDeclaration(doc, target.BOMRef)
		doc.Compositions = append(doc.Compositions,
			Composition{Aggregate: aggregate, Dependencies: []string{target.BOMRef}})
	}

	doc.Metadata.Properties = append(doc.Metadata.Properties,
		Property{propSuppliedDocument, suppliedNote(s, sup, target, aggregate)})
	return nil
}

// importDocument brings one supplied document into doc: its components verbatim, its
// relationships and coverage statements rewritten into the namespace, and an explicit marking
// for everything its author left unstated. It returns the ref of the subject it describes.
func importDocument(doc *Document, s Supplied, sup suppliedDocument, prefix string,
	state *mergeState) (string, error) {

	// The subject first, and before anything is written into it: a document with no
	// metadata.component describes nothing, and `for=` would have nothing to attach the file
	// to. CycloneDX permits omitting it; msis cannot use such a document, and says so rather
	// than reaching into a map that is not there.
	if len(sup.Metadata.Component) == 0 {
		return "", fmt.Errorf("the supplied document has no metadata.component, so it does " +
			"not say what it describes; msis merges a document ABOUT a file, and there is " +
			"nothing here to attach to one")
	}

	// Every ref the document DEFINES, wherever it defines one. Components nest, but so do
	// pedigree ancestors and variants, and a ref left in the supplier's namespace either
	// dangles or collides with the host's.
	rename := map[string]string{}
	collectRefs(sup.Metadata.Component, prefix, rename)
	for _, c := range sup.Components {
		collectRefs(c, prefix, rename)
	}
	// A composition may be addressed too, and its ref is imported with it.
	for _, comp := range sup.Compositions {
		if comp.BOMRef != "" {
			rename[comp.BOMRef] = prefix + encodeRefPart(comp.BOMRef)
		}
	}

	// A component with no bom-ref is valid CycloneDX and msis emits it - but the merged
	// document has to be able to refer to it, if only to say that its dependencies are
	// unknown. So it is GIVEN an address, deterministically by position, and the document says
	// that msis assigned it. An address is not identity: no purl, name or version is invented,
	// which is what #29 D4 forbids.
	components := componentTree(sup)
	for i, c := range components {
		if c.str("bom-ref") == "" {
			c["bom-ref"] = fmt.Sprintf("%sunaddressed/%d", prefix, i)
			c.addProperty(propSuppliedRef, "assigned by msis: the supplied document gave this "+
				"component no bom-ref, and a merged document has to be able to refer to it")
		}
	}

	// A reference that would survive the import pointing at something that will not. The
	// supplier's own tooling is the case that exists: `metadata.tools` may define a bom-ref
	// and a component's `evidence.identity.tools` may reference it, and msis imports
	// components rather than someone else's tooling - so the reference cannot be preserved
	// and the document is refused rather than published with a ref that resolves to nothing.
	if err := checkRetainedReferences(sup, rename); err != nil {
		return "", err
	}

	// Imported components, emitted verbatim apart from the rewritten refs.
	imported := map[string]bool{}
	for _, c := range append([]rawComponent{sup.Metadata.Component}, sup.Components...) {
		applyRename(c, rename)
		markSupplied(c, s.Source)
		doc.Components = append(doc.Components, importedComponent(c))
	}
	// Read AFTER the rename, so it is the final address in both cases - the supplier's ref
	// moved into the namespace, or the one msis assigned.
	rootRef := sup.Metadata.Component.str("bom-ref")
	for _, c := range components {
		imported[c.str("bom-ref")] = true
	}

	for _, d := range sup.Dependencies {
		ref, ok := rename[d.Ref]
		if !ok {
			return "", fmt.Errorf("the supplied document states a dependency for %q, which is "+
				"not one of its components", d.Ref)
		}
		// A legitimately empty dependsOn says "depends on nothing" and survives as it is:
		// emitting it as nil would turn a known-empty graph into an unstated one.
		on, err := renameAll(d.DependsOn, rename, d.Ref, "depends on")
		if err != nil {
			return "", err
		}
		provides, err := renameAll(d.Provides, rename, d.Ref, "provides")
		if err != nil {
			return "", err
		}
		doc.Dependencies = append(doc.Dependencies,
			Dependency{Ref: ref, DependsOn: on, Provides: provides})
	}

	// The supplier's own coverage statements, as they stand.
	declared, err := dependencyAggregates(sup, rename)
	if err != nil {
		return "", err
	}
	for ref, aggregate := range declared {
		state.declared[ref] = aggregate
	}
	for _, comp := range sup.Compositions {
		out := Composition{Aggregate: comp.Aggregate, BOMRef: rename[comp.BOMRef]}
		for _, a := range comp.Assemblies {
			if ref, ok := rename[a]; ok {
				out.Assemblies = append(out.Assemblies, ref)
			}
		}
		for _, d := range comp.Dependencies {
			if ref, ok := rename[d]; ok {
				out.Dependencies = append(out.Dependencies, ref)
			}
		}
		if len(out.Assemblies) == 0 && len(out.Dependencies) == 0 {
			continue // it named nothing this document now contains
		}
		doc.Compositions = append(doc.Compositions, out)
	}

	// What the supplier did not declare. Two different silences, and they are not the same
	// fact:
	//
	//   - An explicit empty dependsOn IS an assertion - "this depends on nothing" - and #29
	//     calls that a knowledge state of its own. Recording it as complete states what the
	//     supplier said; anything else would either contradict their assertion or leave the
	//     document self-contradictory, since an empty dependsOn beside "unknown" says both
	//     that the graph is empty and that nobody knows it.
	//   - Everything else is unknown: nothing was claimed, so nothing is claimed here.
	//
	// Both are readings of what the supplier wrote, not additions to it.
	knownEmpty := map[string]bool{}
	for _, d := range sup.Dependencies {
		ref, ok := rename[d.Ref]
		if !ok || len(d.DependsOn) != 0 {
			continue
		}
		knownEmpty[ref] = true
		if got := declared[ref]; got != "" && got != aggregateComplete {
			return "", fmt.Errorf("the supplied document says %q depends on nothing while "+
				"declaring its dependencies %s, which cannot both be true", d.Ref, got)
		}
	}
	var complete, unknown []string
	for ref := range imported {
		switch {
		case declared[ref] != "":
			// The supplier declared it; their statement was imported above.
		case knownEmpty[ref]:
			complete = append(complete, ref)
		default:
			unknown = append(unknown, ref)
		}
	}
	for _, group := range []struct {
		aggregate string
		refs      []string
	}{{aggregateComplete, complete}, {aggregateUnknown, unknown}} {
		if len(group.refs) == 0 {
			continue
		}
		sort.Strings(group.refs)
		doc.Compositions = append(doc.Compositions,
			Composition{Aggregate: group.aggregate, Dependencies: group.refs})
	}
	return rootRef, nil
}

// componentTree lists the components the merged document will carry as components - the
// supplied subject, the top-level ones, and everything nested under `components`. Refs defined
// elsewhere in the JSON (a pedigree ancestor, say) are renamed but are not components of the
// merged document, so they get no coverage declaration: naming one would make a composition
// point at something the document does not contain.
// checkRetainedReferences finds a reference that the import would keep while its definition
// stays behind. Walking the whole supplied document tells the difference between the two reasons
// that can happen, and the reader needs it: a ref defined in a part msis does not import is a
// limitation to explain, while a ref defined nowhere is the supplied document's own defect.
func checkRetainedReferences(sup suppliedDocument, rename map[string]string) error {
	elsewhere := map[string]string{}
	for name, node := range map[string]any{
		"metadata.tools": sup.Metadata.Tools, // either shape; collectRefs walks both
	} {
		refs := map[string]string{}
		collectRefs(node, "", refs)
		for ref := range refs {
			elsewhere[ref] = name
		}
	}

	fields := conformance.ReferenceFieldNames()
	var problems []string
	var walk func(node any, of string)
	walk = func(node any, of string) {
		switch n := node.(type) {
		case map[string]any:
			at := of
			if ref, ok := n["bom-ref"].(string); ok && ref != "" {
				at = ref
			}
			for key, v := range n {
				if key != "bom-ref" && fields[key] {
					for _, value := range stringsOf(v) {
						// What this document defines comes first: a local ref that is
						// itself a URN is still a local ref, and only the schema's own
						// BOM-Link syntax names an element of ANOTHER document.
						if _, ok := rename[value]; ok {
							continue
						}
						if conformance.IsBOMLink(value) {
							continue
						}
						where, known := elsewhere[value]
						switch {
						case known:
							problems = append(problems, fmt.Sprintf(
								"%q references %q in %s, which the supplied document defines "+
									"in %s; msis imports components, not the supplier's %s, so "+
									"that reference cannot be kept", at, value, key, where, where))
						default:
							problems = append(problems, fmt.Sprintf(
								"%q references %q in %s, which the supplied document does not "+
									"define at all", at, value, key))
						}
					}
				}
				walk(v, at)
			}
		case []any:
			for _, item := range n {
				walk(item, of)
			}
		}
	}
	walk(map[string]any(sup.Metadata.Component), "")
	for _, c := range sup.Components {
		walk(map[string]any(c), "")
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("the merged document would carry %d reference(s) that resolve to "+
		"nothing:\n  %s", len(problems), strings.Join(problems, "\n  "))
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

// refuseWhatWouldBeDropped reports any field of a supplied dependency or composition that the
// merge cannot carry across. The alternative is silence, and a document that silently lost half
// of what its author wrote looks exactly like one that never had it.
func refuseWhatWouldBeDropped(data []byte) error {
	var raw struct {
		Dependencies []map[string]any `json:"dependencies"`
		Compositions []map[string]any `json:"compositions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // the caller's own decode reports this properly
	}

	var lost []string
	check := func(what string, items []map[string]any, carried ...string) {
		known := map[string]bool{}
		for _, k := range carried {
			known[k] = true
		}
		for _, item := range items {
			for key := range item {
				if !known[key] {
					lost = append(lost, fmt.Sprintf("%s.%s", what, key))
				}
			}
		}
	}
	check("dependencies", raw.Dependencies, "ref", "dependsOn", "provides")
	check("compositions", raw.Compositions, "bom-ref", "aggregate", "assemblies", "dependencies")
	if len(lost) == 0 {
		return nil
	}
	sort.Strings(lost)
	return fmt.Errorf("the supplied document uses %s, which msis cannot carry into the merged "+
		"document; it is refused rather than dropped, because a document that silently lost "+
		"part of what its author wrote looks just like one that never had it",
		strings.Join(dedupe(lost), ", "))
}

func dedupe(in []string) []string {
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

func componentTree(sup suppliedDocument) []rawComponent {
	var out []rawComponent
	var walk func(rawComponent)
	walk = func(c rawComponent) {
		out = append(out, c)
		for _, child := range c.children() {
			walk(child)
		}
	}
	walk(sup.Metadata.Component)
	for _, c := range sup.Components {
		walk(c)
	}
	return out
}

func renameAll(refs []string, rename map[string]string, of, relation string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		to, ok := rename[r]
		if !ok {
			return nil, fmt.Errorf("the supplied document says %q %s %q, which it does not "+
				"define; msis cannot publish a reference that resolves to nothing", of, relation, r)
		}
		out = append(out, to)
	}
	return out, nil
}

// suppliedNote states what the merge did and, more importantly, what it did not establish.
func suppliedNote(s Supplied, sup suppliedDocument, target *Component, aggregate string) string {
	note := fmt.Sprintf("%s describes %s (%s) and was merged as received", s.Source,
		target.Name, s.Target)
	if sup.SerialNumber != "" {
		note += ", from document " + sup.SerialNumber
	}
	if sup.Metadata.Component.sha256() != "" {
		note += "; its subject digest matches the packaged bytes"
	} else {
		note += "; it carries no digest for its subject, so msis could not check that it " +
			"describes these bytes"
	}
	note += fmt.Sprintf("; the file's dependency graph is declared %s. Merging a document "+
		"establishes nothing about its completeness or accuracy - those are its author's "+
		"claims, not msis's.", aggregate)
	return note
}

// collectRefs finds every bom-ref the supplied JSON DEFINES, anywhere in it, and gives each its
// namespaced form.
//
// Anywhere, because `components` is not the only place a component lives: pedigree ancestors,
// descendants and variants are components too, with refs of their own, and one left in the
// supplier's namespace either dangles or collides with the host's.
func collectRefs(node any, prefix string, into map[string]string) {
	// rawComponent is a named type over map[string]any, and a type switch does not see
	// through that - so it is unwrapped rather than listed as a second case in every switch.
	if rc, ok := node.(rawComponent); ok {
		node = map[string]any(rc)
	}
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["bom-ref"].(string); ok && ref != "" {
			into[ref] = prefix + encodeRefPart(ref)
		}
		for _, v := range n {
			collectRefs(v, prefix, into)
		}
	case []any:
		for _, item := range n {
			collectRefs(item, prefix, into)
		}
	}
}

// applyRename rewrites every reference in the supplied JSON, wherever the schema puts one.
//
// `bom-ref` and `dependsOn` are not the whole story: 1.6 also references a component from
// `signatureAlgorithmRef`, `subjectPublicKeyRef`, `parent`, `provides`, `claims` and a dozen
// more. The set comes FROM the vendored schema (conformance.ReferenceFieldNames) rather than
// from a list here, because a list is a copy of the schema that drifts from it, and a field
// missed from it turns a valid supplied document into one with dangling references.
//
// Only values that are refs THIS document defines are rewritten, so a field that happens to
// share a name with a reference field but holds something else is left alone.
func applyRename(node any, rename map[string]string) {
	if rc, ok := node.(rawComponent); ok {
		node = map[string]any(rc)
	}
	switch n := node.(type) {
	case map[string]any:
		for key, v := range n {
			if key == "bom-ref" || conformance.ReferenceFieldNames()[key] {
				switch val := v.(type) {
				case string:
					if to, ok := rename[val]; ok {
						n[key] = to
						continue
					}
				case []any:
					for i, item := range val {
						if str, ok := item.(string); ok {
							if to, ok := rename[str]; ok {
								val[i] = to
							}
						}
					}
				}
			}
			applyRename(v, rename)
		}
	case []any:
		for _, item := range n {
			applyRename(item, rename)
		}
	}
}

// markSupplied records, on every imported component, which document it came from. It is
// provenance and it is also the marker that admits a component with no digest: msis never had
// these bytes, so there was never a hash for it to drop.
func markSupplied(c rawComponent, source string) {
	c.addProperty(propSuppliedFrom, source)
	for _, child := range c.children() {
		markSupplied(child, source)
	}
}

// importedComponent wraps a raw component so the document emits it verbatim while msis's own
// code can still see the two fields it needs: the ref it sorts by and the digest it reports.
func importedComponent(c rawComponent) Component {
	out := Component{
		Type:   c.str("type"),
		BOMRef: c.str("bom-ref"),
		Name:   c.str("name"),
		raw:    c,
	}
	if sum := c.sha256(); sum != "" {
		out.Hashes = []Hash{{Alg: "SHA-256", Content: sum}}
	}
	return out
}

// dependencyAggregates reports what the supplied document declares about each component's
// dependency graph, in the namespaced refs. A document that declares two different things for
// one component contradicts itself, and propagating that would make the merged document
// contradict itself too.
func dependencyAggregates(sup suppliedDocument, rename map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, comp := range sup.Compositions {
		for _, d := range comp.Dependencies {
			ref, ok := rename[d]
			if !ok {
				continue
			}
			if was, seen := out[ref]; seen && was != comp.Aggregate {
				return nil, fmt.Errorf("the supplied document declares the dependencies of %q "+
					"both %s and %s", d, was, comp.Aggregate)
			}
			out[ref] = comp.Aggregate
		}
	}
	return out, nil
}

// clearDependencyDeclaration takes one ref out of every existing dependency declaration, so the
// merge can state what is now known about it without contradicting the blanket `unknown` the
// artifact-only document gave it. The assemblies side is left alone: how completely the FILE
// itself is described has not changed - msis still has only its bytes.
func clearDependencyDeclaration(doc *Document, ref string) {
	// A NEW slice each time, never a filter in place: the artifact-only document gives one
	// composition the same backing array for assemblies and dependencies, so reusing it would
	// quietly drop unrelated components from the other half.
	var kept []Composition
	for _, c := range doc.Compositions {
		var deps []string
		for _, d := range c.Dependencies {
			if d != ref {
				deps = append(deps, d)
			}
		}
		c.Dependencies = deps
		// A composition left naming nothing declares nothing.
		if len(c.Assemblies) > 0 || len(c.Dependencies) > 0 {
			kept = append(kept, c)
		}
	}
	doc.Compositions = kept
}

func componentWithFileKey(doc *Document, fileID string) *Component {
	for i := range doc.Components {
		if propertyValueOf(doc.Components[i].Properties, propFileKey) == fileID {
			return &doc.Components[i]
		}
	}
	return nil
}
