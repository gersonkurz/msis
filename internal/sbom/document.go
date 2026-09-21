package sbom

// CycloneDX 1.6 types, carrying only the fields msis emits. The vendored schema in
// The schema vendored in conformance/schema is the authority on shape; these structs are the subset that gets used, and
// the conformance test validates the output against the real thing rather than against them.
//
// Every slice is emitted in a defined order (see sortDocument) because two documents describing
// the same input have to differ in exactly two fields - metadata.timestamp and serialNumber -
// and nothing else.

import "encoding/json"

type Document struct {
	BOMFormat    string        `json:"bomFormat"`
	SpecVersion  string        `json:"specVersion"`
	SerialNumber string        `json:"serialNumber,omitempty"`
	Version      int           `json:"version"`
	Metadata     Metadata      `json:"metadata"`
	Components   []Component   `json:"components"`
	Dependencies []Dependency  `json:"dependencies,omitempty"`
	Compositions []Composition `json:"compositions,omitempty"`
}

type Metadata struct {
	Timestamp  string     `json:"timestamp,omitempty"`
	Tools      Tools      `json:"tools"`
	Component  Component  `json:"component"`
	Supplier   *Supplier  `json:"supplier,omitempty"`
	Properties []Property `json:"properties,omitempty"`
}

// Tools uses the 1.6 object form rather than the deprecated array, because a tool expressed as
// a component can carry its own hash - which is what lets the document say which msis produced
// it, not merely which version claimed to.
type Tools struct {
	Components []Component `json:"components,omitempty"`
}

type Supplier struct {
	Name string `json:"name,omitempty"`
}

type Component struct {
	Type        string     `json:"type"`
	BOMRef      string     `json:"bom-ref,omitempty"`
	Name        string     `json:"name"`
	Version     string     `json:"version,omitempty"`
	Description string     `json:"description,omitempty"`
	PURL        string     `json:"purl,omitempty"`
	Supplier    *Supplier  `json:"supplier,omitempty"`
	Hashes      []Hash     `json:"hashes,omitempty"`
	Properties  []Property `json:"properties,omitempty"`

	// ExternalReferences carries BOM-Links: a bundle's document points at the document for
	// each installer it chains rather than repeating that installer's contents.
	ExternalReferences []ExternalReference `json:"externalReferences,omitempty"`

	// raw is set only for a component imported from a supplied document (#36). That
	// component is emitted EXACTLY as its author wrote it - including every field msis does
	// not model, licences above all - because re-serialising it through the struct above
	// would silently drop whatever this file does not happen to mention. The typed fields
	// beside it are populated too, so sorting and the digest rules still work.
	raw rawComponent
}

// MarshalJSON emits an imported component verbatim and everything else from the struct.
func (c Component) MarshalJSON() ([]byte, error) {
	if c.raw != nil {
		return json.Marshal(map[string]any(c.raw))
	}
	// A distinct type so the marshaller does not call this method again.
	type plain Component
	return json.Marshal(plain(c))
}

// ExternalReference points at something outside the document. msis emits exactly one kind, a
// BOM-Link of type "bom", and carries the subject digest with it: a link identifies a document,
// and the digest is what proves that document describes the bytes this one carries.
type ExternalReference struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Comment string `json:"comment,omitempty"`
	Hashes  []Hash `json:"hashes,omitempty"`
}

type Hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Dependency states a known relationship. An entry with an empty dependsOn asserts that the
// component depends on NOTHING, which is true only when that is actually known; for an opaque
// payload the dependencies are unknown, and that is said through Compositions instead. The
// three states - known-empty, unknown, incomplete - are distinct and must not be collapsed.
type Dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn"`

	// Provides is the other relationship 1.6 defines on a dependency: the components that
	// implement a specification this one declares. msis never emits one of its own - it has
	// no way to know - but a supplied document may, and dropping it on the way through would
	// lose an edge its author put there (#36).
	Provides []string `json:"provides,omitempty"`
}

// Composition records how complete a part of the document is. It names the components it
// applies to explicitly: a completeness declaration does not cascade through containment or
// transitive dependencies.
type Composition struct {
	// BOMRef: a composition may be addressed like anything else. msis emits none of its own,
	// but a supplied one may have one, and dropping it would break a reference to it (#36).
	BOMRef string `json:"bom-ref,omitempty"`

	Aggregate  string   `json:"aggregate"`
	Assemblies []string `json:"assemblies,omitempty"`

	// Dependencies is a separate field from Assemblies and means a different thing: how
	// completely the listed components' DEPENDENCY GRAPH is known, rather than how completely
	// the components themselves are described. An opaque payload needs both.
	Dependencies []string `json:"dependencies,omitempty"`
}

// Aggregate values used here, from the schema's enumeration.
const (
	// aggregateIncomplete: some of this is known to be missing.
	aggregateIncomplete = "incomplete"
	// aggregateUnknown: whether anything is missing is not known.
	aggregateUnknown = "unknown"
	// aggregateComplete: nothing is missing. Used for the product's dependency graph, which
	// is exactly what the package contains.
	aggregateComplete = "complete"
)

// Property names msis emits. Namespaced so a consumer can tell them from anyone else's, and
// listed together so the vocabulary is visible in one place.
const (
	propRole            = "msis:role"
	propInstallTarget   = "msis:installTarget"
	propComponentID     = "msis:msi.component"
	propComponentGUID   = "msis:msi.componentGuid"
	propFileKey         = "msis:msi.fileKey"
	propSequence        = "msis:msi.sequence"
	propProductCode     = "msis:msi.productCode"
	propUpgradeCode     = "msis:msi.upgradeCode"
	propSubjectArtifact = "msis:subject.artifact"
	propCoverage        = "msis:coverage"
	propIdentityUnknown = "msis:identity"

	// Bundle-side vocabulary. A bundle inventories installers rather than files, so it needs
	// terms for where a payload lives and whether its bytes are in the artifact at all.
	propBundleCode         = "msis:burn.bundleCode"
	propEngineVersion      = "msis:burn.engineVersion"
	propPackageID          = "msis:burn.packageId"
	propPackageKind        = "msis:burn.packageKind"
	propInstallCondition   = "msis:burn.installCondition"
	propCarried            = "msis:payload.carried"
	propPayloadUnavailable = "msis:payload.unavailable"
	propDownloadURL        = "msis:payload.downloadUrl"
	propBOMLink            = "msis:bomLink"

	// Build-time enrichment (#34). Namespaced under msis:build so a reader can tell at a
	// glance which facts came from the artifact and which from the build that produced it -
	// the two have different evidentiary weight and a document that mixed them silently
	// would be claiming more than it can show.
	propBuildPath       = "msis:build.path"
	propBuildScript     = "msis:build.script"
	propBuildTool       = "msis:build.tool"
	propBuildSource     = "msis:build.source"
	propBuildSourceRoot = "msis:build.sourceRoot"
	propBuildUnresolved = "msis:build.unresolved"
	propBuildCoverage   = "msis:build.coverage"
	propPrereqType      = "msis:prerequisite.type"
	propPrereqArch      = "msis:prerequisite.arch"
	propPrereqCache     = "msis:prerequisite.cache"
	propLaunchCondition = "msis:launchCondition"

	// Composition of supplied documents (#36). propSuppliedFrom marks a component msis did
	// not observe but received, which is both its provenance and the reason it may carry no
	// digest - msis never had those bytes. propSuppliedDocument records, per merged document,
	// what the merge did and did not establish.
	propSuppliedFrom     = "msis:supplied.from"
	propSuppliedDocument = "msis:supplied.document"

	// propSuppliedRef says msis gave a component its bom-ref because the supplied document
	// gave it none. A bom-ref is addressing, not identity, so assigning one invents nothing -
	// but a reader must not mistake it for something its author wrote.
	propSuppliedRef = "msis:supplied.ref"
)

// Role values for propRole.
const (
	rolePayload = "payload"       // installed onto the machine
	roleBinary  = "binary-stream" // executed during installation, never installed

	// From build-time enrichment (#34). There is exactly one, because enrichment attaches
	// provenance to components the artifact already has rather than inventing its own - a
	// prerequisite the bundle carries is already a chain package, and saying where it came
	// from is a property of that component, not a second component beside it.
	//
	// roleRequiredRuntime is the exception, and has to be: a runtime the installer merely
	// DETECTS appears nowhere in the artifact, because nothing was distributed. That is a
	// different category from a bundled prerequisite, and conflating the two would claim a
	// distribution that never happened.
	roleRequiredRuntime = "required-runtime"

	// roleChained is what packageComponent writes for a chain package. It is
	// string(burnread.RoleChained); TestChainRoleMatchesBurnread pins the two together.
	roleChained = "chained-installer"
)

// A bundle's roles are burnread.Role values, emitted as they are rather than restated here, so
// the document's vocabulary and the reader's cannot drift apart.
