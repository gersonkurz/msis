package vex

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom"
)

// Evaluating VEX statements against the build they accompany (#37).
//
// Every test here is about the same thing from a different side: an assessment is a claim about
// a product at a point in time, and the moment a tool starts inferring that it still holds, it
// starts hiding vulnerabilities.

const libRef = "msis/aaaa/file/%5binstalldir%5dlib.dll"
const libDigest = "1111111111111111111111111111111111111111111111111111111111111111"

// inventory is the SBOM of one release: an application and the library it ships.
func inventory(release, digest string) *sbom.Document {
	return &sbom.Document{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.6",
		SerialNumber: "urn:uuid:11111111-1111-4111-8111-111111111111",
		Version:      1,
		Metadata: sbom.Metadata{
			Timestamp: "2026-01-01T00:00:00Z",
			Component: sbom.Component{
				Type: "application", BOMRef: "msis/aaaa/product",
				Name: "Example", Version: release,
				Hashes: []sbom.Hash{{Alg: "SHA-256", Content: strings.Repeat("e", 64)}},
			},
			Supplier: &sbom.Supplier{Name: "Acme"},
		},
		Components: []sbom.Component{{
			Type: "file", BOMRef: libRef, Name: "lib.dll",
			Hashes: []sbom.Hash{{Alg: "SHA-256", Content: digest}},
		}},
	}
}

// statement writes one VEX document containing a single assessment.
func statement(state string, props map[string]string) Source {
	list := make([]any, 0, len(props))
	for _, name := range []string{
		sbom.PropAssessedVersion, sbom.PropAppliesToVersions, sbom.PropAssessedDigest,
	} {
		if v, ok := props[name]; ok {
			list = append(list, map[string]any{"name": name, "value": v})
		}
	}
	doc := map[string]any{
		"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
		"vulnerabilities": []any{map[string]any{
			"id": "CVE-2024-1234",
			"analysis": map[string]any{
				"state":         state,
				"justification": "code_not_reachable",
				"detail":        "the vulnerable entry point is never called",
			},
			"affects":    []any{map[string]any{"ref": libRef}},
			"properties": list,
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return Source{Path: "app.vex.json", Data: data}
}

func apply(t *testing.T, bom *sbom.Document, src Source) *sbom.Document {
	t.Helper()
	doc, err := Apply(bom, src, Options{MsisVersion: "test",
		NewSerial: func() (string, error) {
			return "urn:uuid:22222222-2222-4222-8222-222222222222", nil
		}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return doc
}

func only(t *testing.T, doc *sbom.Document) sbom.Vulnerability {
	t.Helper()
	if len(doc.Vulnerabilities) != 1 {
		t.Fatalf("%d statements, want 1", len(doc.Vulnerabilities))
	}
	return doc.Vulnerabilities[0]
}

// THE decisive case, and the reason this package exists.
//
// The library is byte-identical between 4.1 and 4.2. The application that ships it has started
// calling the vulnerable path - which msis cannot see and has no way of knowing. If the
// unchanged digest were allowed to carry the assessment forward, the document would state
// `not_affected` about a vulnerability that is now real, and the alert that should have been
// raised would be subtracted by the very record meant to make triage honest.
func TestAnUnchangedDigestDoesNotCarryAnAssessmentForward(t *testing.T) {
	assessed := statement("not_affected", map[string]string{
		sbom.PropAssessedVersion: "4.1",
		sbom.PropAssessedDigest:  libDigest,
	})

	// Same release: it applies, unchanged.
	v := only(t, apply(t, inventory("4.1", libDigest), assessed))
	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityApplies {
		t.Fatalf("at 4.1 the assessment is %q, want %q", got, sbom.ApplicabilityApplies)
	}
	if got := v.AnalysisState(); got != "not_affected" {
		t.Errorf("at 4.1 the state is %q; an assessment that still holds must be left alone", got)
	}

	// The next release, with the SAME library bytes.
	v = only(t, apply(t, inventory("4.2", libDigest), assessed))
	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityNeedsReview {
		t.Fatalf("at 4.2 the assessment is %q, want %q - an unchanged digest carried it forward",
			got, sbom.ApplicabilityNeedsReview)
	}
	if got := v.AnalysisState(); got == "not_affected" {
		t.Error("at 4.2 the statement still says not_affected, so a consumer subtracts a " +
			"vulnerability that nobody has assessed for this release")
	}
	if got := v.AnalysisState(); got != "in_triage" {
		t.Errorf("state = %q, want in_triage - CycloneDX's own word for being investigated", got)
	}
	// Nothing is lost: what it used to say, and why it no longer applies, are both recorded.
	if got := v.Property(sbom.PropPreviousState); got != "not_affected" {
		t.Errorf("the previous state was not preserved: %q", got)
	}
	if reason := v.Property(sbom.PropReviewReason); !strings.Contains(reason, "4.1") ||
		!strings.Contains(reason, "4.2") {
		t.Errorf("the reason does not say which releases are involved: %q", reason)
	}
}

// Reuse across releases is possible - but only because the assessor said so, never because msis
// worked it out. The difference between those two is the whole safety property.
func TestReuseAcrossReleasesIsTheAssessorsDecision(t *testing.T) {
	for _, tc := range []struct {
		name      string
		appliesTo string
		release   string
		want      string
	}{
		{"an explicitly listed release", "4.2", "4.2", sbom.ApplicabilityApplies},
		{"one of several listed", "4.2, 4.3", "4.3", sbom.ApplicabilityApplies},
		{"every release", "*", "9.9", sbom.ApplicabilityApplies},
		{"a release nobody listed", "4.2", "4.3", sbom.ApplicabilityNeedsReview},
		{"nothing widened at all", "", "4.2", sbom.ApplicabilityNeedsReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]string{sbom.PropAssessedVersion: "4.1"}
			if tc.appliesTo != "" {
				props[sbom.PropAppliesToVersions] = tc.appliesTo
			}
			v := only(t, apply(t, inventory(tc.release, libDigest), statement("not_affected", props)))
			if got := v.Property(sbom.PropApplicability); got != tc.want {
				t.Errorf("at %s with appliesTo=%q: %q, want %q",
					tc.release, tc.appliesTo, got, tc.want)
			}
		})
	}
}

// A statement that records no release cannot be evaluated at all: msis would have to guess
// whether it was meant for this one, and the safe-looking guess is the dangerous one. The build
// fails and says what to add, which is a one-line fix in the document the team maintains.
func TestAStatementWithNoAssessedReleaseIsRefused(t *testing.T) {
	_, err := Apply(inventory("4.1", libDigest), statement("not_affected", nil),
		Options{MsisVersion: "test"})
	if err == nil {
		t.Fatal("a statement with no recorded scope was evaluated anyway")
	}
	for _, want := range []string{"CVE-2024-1234", sbom.PropAssessedVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}

// The digest condition is the assessor's own: where they recorded one, a component that no
// longer matches it means the thing they looked at is not the thing being shipped.
func TestAChangedComponentInvalidatesTheAssessment(t *testing.T) {
	changed := strings.Repeat("2", 64)
	v := only(t, apply(t, inventory("4.1", changed), statement("not_affected", map[string]string{
		sbom.PropAssessedVersion: "4.1",
		sbom.PropAssessedDigest:  libDigest,
	})))

	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityNeedsReview {
		t.Fatalf("applicability = %q; the component was replaced under the assessment", got)
	}
	if !strings.Contains(v.Property(sbom.PropReviewReason), "lib.dll") {
		t.Errorf("the reason does not name the component: %q", v.Property(sbom.PropReviewReason))
	}
	// And the digest that IS shipped is recorded, so the reassessment starts from a fact.
	if !strings.Contains(v.Property(sbom.PropObservedDigest), changed) {
		t.Errorf("the observed digest was not recorded: %q", v.Property(sbom.PropObservedDigest))
	}
}

// A statement about something this build does not contain is stale rather than wrong - a
// component legitimately disappears between releases. It is flagged, not dropped: dropping it
// would lose the assessment and the audit trail with it.
func TestAStatementAboutSomethingNotShippedIsFlagged(t *testing.T) {
	src := statement("not_affected", map[string]string{sbom.PropAssessedVersion: "4.1"})
	src.Data = []byte(strings.ReplaceAll(string(src.Data), libRef, "msis/aaaa/file/gone.dll"))

	v := only(t, apply(t, inventory("4.1", libDigest), src))
	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityNeedsReview {
		t.Fatalf("applicability = %q", got)
	}
	if !strings.Contains(v.Property(sbom.PropReviewReason), "gone.dll") {
		t.Errorf("the reason does not name what is missing: %q", v.Property(sbom.PropReviewReason))
	}
}

// Neutralising works in one direction only. A statement that SUPPRESSES a finding must stop
// suppressing it when its premises lapse; a statement that WARNS must keep warning. Silencing a
// warning because its scope ran out would be the same mistake pointing the other way, and a
// worse one.
func TestALapsedWarningIsNotSilenced(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"not_affected", "in_triage"},
		{"false_positive", "in_triage"},
		{"resolved", "in_triage"},
		{"exploitable", "exploitable"},
		{"in_triage", "in_triage"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			v := only(t, apply(t, inventory("4.2", libDigest),
				statement(tc.state, map[string]string{sbom.PropAssessedVersion: "4.1"})))

			if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityNeedsReview {
				t.Fatalf("applicability = %q", got)
			}
			if got := v.AnalysisState(); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

// The inventory is not touched. A VEX document annotates what was shipped; changing what was
// shipped because someone assessed it would make the two disagree about the same build.
func TestTheSBOMIsUnchangedByApplyingVEX(t *testing.T) {
	bom := inventory("4.1", libDigest)
	before, err := sbom.Marshal(bom)
	if err != nil {
		t.Fatal(err)
	}

	apply(t, bom, statement("not_affected", map[string]string{sbom.PropAssessedVersion: "4.1"}))

	after, err := sbom.Marshal(bom)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("applying a VEX document changed the SBOM it was applied to")
	}
}

// The sidecar says which inventory it is about, and does so the way every other cross-document
// reference in this repo does: a BOM-Link to a serial and a version (#33).
func TestTheSidecarAddressesTheInventoryItAnnotates(t *testing.T) {
	bom := inventory("4.1", libDigest)
	doc := apply(t, bom, statement("not_affected", map[string]string{sbom.PropAssessedVersion: "4.1"}))

	want := "urn:cdx:11111111-1111-4111-8111-111111111111/1"
	var linked bool
	for _, r := range doc.Metadata.Component.ExternalReferences {
		if r.Type == "bom" && r.URL == want {
			linked = true
		}
	}
	if !linked {
		t.Errorf("the sidecar does not link to %s", want)
	}
	if doc.SerialNumber == bom.SerialNumber {
		t.Error("the sidecar reuses the inventory's serial; a BOM-Link addresses ONE document")
	}
	// Same product, so the index joins the two without being told.
	if doc.Metadata.Component.BOMRef != bom.Metadata.Component.BOMRef {
		t.Error("the sidecar is about a different subject from the inventory")
	}

	// And it is a CycloneDX document like any other.
	data, err := sbom.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(data); err != nil {
		t.Errorf("the sidecar is not valid CycloneDX: %v", err)
	}
}

// #29 D6: two runs over one input produce one document, or a diff is not a release review.
func TestEvaluationIsDeterministic(t *testing.T) {
	src := statement("not_affected", map[string]string{sbom.PropAssessedVersion: "4.1"})

	first, err := sbom.Marshal(apply(t, inventory("4.1", libDigest), src))
	if err != nil {
		t.Fatal(err)
	}
	second, err := sbom.Marshal(apply(t, inventory("4.1", libDigest), src))
	if err != nil {
		t.Fatal(err)
	}
	a, err := sbom.CanonicalForDiff(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sbom.CanonicalForDiff(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two evaluations of one input produced different documents")
	}
}

// Statements are carried EXACTLY as their author wrote them, minus the verdict msis adds. A
// vulnerability has eighteen fields in the schema and msis reads four; decoding through a struct
// would drop ratings, advisories and credits, and an assessment that lost its evidence on the
// way through looks just like one that never had any.
func TestAStatementIsCarriedVerbatim(t *testing.T) {
	src := Source{Path: "app.vex.json", Data: []byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "vulnerabilities": [{
    "id": "CVE-2024-1234",
    "source": {"name": "NVD", "url": "https://nvd.nist.gov/vuln/detail/CVE-2024-1234"},
    "ratings": [{"severity": "high", "method": "CVSSv31", "score": 8.1}],
    "cwes": [787],
    "advisories": [{"url": "https://example.invalid/advisory"}],
    "credits": {"individuals": [{"name": "A Reporter"}]},
    "analysis": {"state": "not_affected", "justification": "code_not_reachable"},
    "affects": [{"ref": "` + libRef + `"}],
    "properties": [{"name": "` + sbom.PropAssessedVersion + `", "value": "4.1"}]
  }]
}`)}

	doc := apply(t, inventory("4.1", libDigest), src)
	data, err := sbom.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CVSSv31", "787", "example.invalid/advisory", "A Reporter", "NVD"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%q did not survive; msis models four of a vulnerability's fields and "+
				"must not lose the rest", want)
		}
	}
	if err := Validate(data); err != nil {
		t.Errorf("the sidecar is not valid CycloneDX: %v", err)
	}
}

// A document with nothing in it is a mistake worth reporting: a VEX file that assesses nothing
// is almost always one that failed to be written, and merging it silently would leave the team
// believing their assessments are being checked.
func TestAnEmptyVEXDocumentIsRefused(t *testing.T) {
	_, err := Apply(inventory("4.1", libDigest),
		Source{Path: "app.vex.json", Data: []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`)},
		Options{MsisVersion: "test"})
	if err == nil {
		t.Fatal("a VEX document with no statements was accepted")
	}
	if !strings.Contains(err.Error(), "app.vex.json") {
		t.Errorf("%q does not name the file", err)
	}
}

// --- round 2: what the first review found --------------------------------------------------

// A condition that CANNOT be checked has not been met.
//
// A component contributed by a supplied SBOM legitimately has no digest - msis never held its
// bytes, and the conformance rules admit exactly that (#36). If a recorded digest condition were
// treated as satisfied whenever there is nothing to compare it with, the unverifiable case would
// produce the reassuring answer, and a `not_affected` would ride on a condition nobody checked.
func TestADigestConditionThatCannotBeCheckedIsNotSatisfied(t *testing.T) {
	bom := inventory("4.1", libDigest)
	// As a supplied component would be: present, described, and not hashed by msis.
	bom.Components = append(bom.Components, sbom.Component{
		Type: "library", BOMRef: "msis/aaaa/supplied/x/zlib", Name: "zlib",
	})

	src := statement("not_affected", map[string]string{
		sbom.PropAssessedVersion: "4.1",
		sbom.PropAssessedDigest:  libDigest,
	})
	src.Data = []byte(strings.ReplaceAll(string(src.Data), libRef, "msis/aaaa/supplied/x/zlib"))

	v := only(t, apply(t, bom, src))
	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityNeedsReview {
		t.Fatalf("applicability = %q; a digest condition passed against a component with no "+
			"digest to compare", got)
	}
	if reason := v.Property(sbom.PropReviewReason); !strings.Contains(reason, "cannot be checked") {
		t.Errorf("the reason does not say the condition was unverifiable: %q", reason)
	}
	if v.AnalysisState() == "not_affected" {
		t.Error("the statement still suppresses the finding")
	}
}

// A component nested inside another is shipped exactly as its parent is - a supplied document's
// dependency graph is that shape, and #36 keeps it, refs and all. A statement about one of those
// children is about something the build really contains, and reporting it as absent would
// neutralise a valid assessment.
func TestANestedComponentCanBeAssessed(t *testing.T) {
	// Parsed from JSON, which is how a document with nested components reaches anything that
	// did not build it in memory.
	var bom sbom.Document
	if err := json.Unmarshal([]byte(`{
  "bomFormat": "CycloneDX", "specVersion": "1.6",
  "serialNumber": "urn:uuid:11111111-1111-4111-8111-111111111111", "version": 1,
  "metadata": {
    "timestamp": "2026-01-01T00:00:00Z",
    "component": {"type": "application", "bom-ref": "msis/aaaa/product", "name": "Example",
                  "version": "4.1",
                  "hashes": [{"alg": "SHA-256", "content": "`+strings.Repeat("e", 64)+`"}]},
    "supplier": {"name": "Acme"}
  },
  "components": [
    {
      "type": "file", "bom-ref": "`+libRef+`", "name": "lib.dll",
      "hashes": [{"alg": "SHA-256", "content": "`+libDigest+`"}],
      "components": [
        {"type": "library", "bom-ref": "msis/aaaa/supplied/x/vendored", "name": "vendored",
         "hashes": [{"alg": "SHA-256", "content": "`+strings.Repeat("7", 64)+`"}]}
      ]
    }
  ]
}`), &bom); err != nil {
		t.Fatal(err)
	}

	src := statement("not_affected", map[string]string{
		sbom.PropAssessedVersion: "4.1",
		sbom.PropAssessedDigest:  strings.Repeat("7", 64),
	})
	src.Data = []byte(strings.ReplaceAll(string(src.Data), libRef, "msis/aaaa/supplied/x/vendored"))

	v := only(t, apply(t, &bom, src))
	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityApplies {
		t.Fatalf("applicability = %q, reason %q - a nested component was reported as absent",
			got, v.Property(sbom.PropReviewReason))
	}
	if got := v.AnalysisState(); got != "not_affected" {
		t.Errorf("state = %q; a valid assessment was neutralised", got)
	}
}

// The sidecar is about the same PRODUCT as the inventory, and says so the way a consumer
// identifies one: by UpgradeCode. Windows Installer defines that as the identifier constant
// across releases, and it is what the corpus index keys a product on - so a sidecar carrying
// only the product's NAME is a different product there, and its assessments join to nothing.
func TestTheSidecarCarriesTheProductsIdentity(t *testing.T) {
	bom := inventory("4.1", libDigest)
	bom.Metadata.Properties = []sbom.Property{
		{Name: "msis:msi.upgradeCode", Value: "{AAAA1111-2222-4333-8444-555566667777}"},
		{Name: "msis:msi.productCode", Value: "{BBBB1111-2222-4333-8444-555566667777}"},
		{Name: "msis:subject.artifact", Value: "app.msi"},
		{Name: "msis:coverage", Value: "what the INVENTORY covers, which this document did not do"},
	}

	doc := apply(t, bom, statement("not_affected", map[string]string{sbom.PropAssessedVersion: "4.1"}))

	got := map[string]string{}
	for _, p := range doc.Metadata.Properties {
		got[p.Name] = p.Value
	}
	for _, want := range []string{"msis:msi.upgradeCode", "msis:msi.productCode", "msis:subject.artifact"} {
		if got[want] == "" {
			t.Errorf("%s was not carried over; a consumer cannot tell this is the same product", want)
		}
	}
	// But not the inventory's own findings: this document derived nothing from an artifact,
	// and repeating a coverage note it did not produce would be a claim it cannot make.
	if _, ok := got["msis:coverage"]; ok {
		t.Error("the inventory's coverage note was copied onto a document that inventoried nothing")
	}
}

// #62: the sidecar is evaluated against its inventory, so it carries that inventory's
// generation context rather than inventing one of its own.
func TestTheSidecarCarriesTheInventorysGenerationContext(t *testing.T) {
	bom := inventory("4.1", libDigest)
	bom.Metadata.Lifecycles = []sbom.Lifecycle{{Phase: sbom.LifecycleBuild}, {Phase: sbom.LifecyclePostBuild}}
	doc := apply(t, bom, statement("not_affected", map[string]string{
		sbom.PropAssessedVersion: "4.1", sbom.PropAssessedDigest: libDigest}))
	if !reflect.DeepEqual(doc.Metadata.Lifecycles, bom.Metadata.Lifecycles) {
		t.Errorf("sidecar lifecycles %v, want the inventory's %v", doc.Metadata.Lifecycles, bom.Metadata.Lifecycles)
	}
}

// #64: the sidecar was created by the same entity as the inventory it evaluates.
func TestTheSidecarNamesTheInventorysCreator(t *testing.T) {
	bom := inventory("4.1", libDigest)
	bom.Metadata.Manufacturer = &sbom.OrganizationalEntity{Contact: []sbom.OrganizationalContact{{Email: "sbom@acme.example"}}}
	bom.Metadata.Licenses = []sbom.LicenseExpression{{Expression: "CC0-1.0"}}
	doc := apply(t, bom, statement("not_affected", map[string]string{
		sbom.PropAssessedVersion: "4.1", sbom.PropAssessedDigest: libDigest}))
	if !reflect.DeepEqual(doc.Metadata.Manufacturer, bom.Metadata.Manufacturer) {
		t.Errorf("sidecar creator %+v, want the inventory's %+v", doc.Metadata.Manufacturer, bom.Metadata.Manufacturer)
	}
	if !reflect.DeepEqual(doc.Metadata.Licenses, bom.Metadata.Licenses) {
		t.Errorf("sidecar data licence %+v, want the inventory's %+v", doc.Metadata.Licenses, bom.Metadata.Licenses)
	}
}
