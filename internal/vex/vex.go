// Package vex evaluates a VEX document against the SBOM of the build it accompanies (#37).
//
// A CVE matched against a component is not an exploitable vulnerability in the product. "Not
// relevant, because the vulnerable code path is never reached" is a real and common answer, and
// unless it is recorded in machine-readable form it is re-litigated at every audit. That is what
// a VEX statement is for.
//
// msis assesses nothing. It cannot: whether a vulnerable path is reachable is a question about
// the product's own source, which msis never sees. What msis can do is the part that is
// mechanical and therefore gets skipped - CHECK THAT AN ASSESSMENT STILL APPLIES.
//
// That check is the whole point of this package, and it exists because of one specific way a
// VEX sidecar goes wrong: a library is byte-identical between two releases, the consuming
// application starts calling the vulnerable path, and a `not_affected` statement carries
// forward on the strength of the unchanged hash. The vulnerability is now real and the document
// says it is not. So:
//
//   - An unchanged component digest does NOT carry a statement forward. Exploitability is a
//     property of the product using the component, not of the component.
//   - A statement records the release it was assessed against, and msis checks the release
//     being built against it. Reuse across releases is possible, but only where the assessor
//     said so explicitly - never by inference.
//   - A statement whose conditions no longer hold is neither dropped nor silently retained. It
//     is kept, flagged, and - where it was SUPPRESSING a finding - no longer says so.
package vex

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// Source is the VEX document the script supplied, as read.
type Source struct {
	Path string // as authored in the .msis, published as provenance
	Data []byte
}

// Options carries what the sidecar needs but the inputs cannot supply.
type Options struct {
	MsisVersion string
	MsisPath    string
	Now         func() time.Time
	NewSerial   func() (string, error)
}

// Apply evaluates each statement in src against the document describing this build, and returns
// the VEX sidecar to write beside the artifact.
//
// The SBOM is not touched. A VEX document annotates an inventory; changing the inventory
// because someone assessed it would make the two disagree about what was shipped.
func Apply(bom *sbom.Document, src Source, opts Options) (*sbom.Document, error) {
	var supplied sbom.Document
	if err := json.Unmarshal(src.Data, &supplied); err != nil {
		return nil, fmt.Errorf("%s is not readable CycloneDX JSON: %w", src.Path, err)
	}
	if len(supplied.Vulnerabilities) == 0 {
		return nil, fmt.Errorf("%s carries no vulnerabilities, so there is nothing to "+
			"assess; a VEX document is a set of statements about an inventory", src.Path)
	}

	release := bom.Metadata.Component.Version
	if release == "" {
		return nil, fmt.Errorf("the document for this build states no product version, so " +
			"there is nothing to check an assessment's scope against")
	}

	known := componentIndex(bom)

	out := make([]sbom.Vulnerability, 0, len(supplied.Vulnerabilities))
	var applies, review int
	for i, statement := range supplied.Vulnerabilities {
		evaluated, ok, err := evaluate(statement, known, release, i)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Path, err)
		}
		if ok {
			applies++
		} else {
			review++
		}
		out = append(out, evaluated)
	}

	doc, err := sidecar(bom, src, opts, out)
	if err != nil {
		return nil, err
	}
	doc.Metadata.Properties = append(doc.Metadata.Properties, sbom.Property{
		Name: sbom.PropVEXCoverage,
		Value: fmt.Sprintf(
			"%d statement(s) from %s evaluated against %s %s: %d still apply, %d need review. "+
				"msis checks only the conditions an assessment recorded - whether the "+
				"assessment itself is right is its author's claim, not msis's.",
			len(out), src.Path, bom.Metadata.Component.Name, release, applies, review),
	})
	sortStatements(doc.Vulnerabilities)
	return doc, nil
}

// component is what the SBOM says about one ref: enough to check a recorded condition against.
type component struct {
	digest string
	name   string
}

func componentIndex(bom *sbom.Document) map[string]component {
	out := map[string]component{}
	var add func(c sbom.Component)
	add = func(c sbom.Component) {
		if c.BOMRef != "" {
			out[c.BOMRef] = component{digest: sha256Of(c.Hashes), name: c.Name}
		}
		// A component may contain components - a supplied document's dependency graph is
		// exactly that shape (#36) - and those are shipped too. A statement naming one would
		// otherwise be flagged as assessing something the build does not contain.
		for _, child := range c.Nested() {
			add(child)
		}
	}
	for _, c := range bom.Components {
		add(c)
	}
	// The subject is not one of the components, and a statement may well be about the product
	// as a whole rather than about one file in it.
	add(bom.Metadata.Component)
	return out
}

// evaluate decides whether one statement still applies, and records both the verdict and what
// it was based on. It returns an error only for a statement that cannot be evaluated at all.
func evaluate(statement sbom.Vulnerability, known map[string]component, release string,
	index int) (sbom.Vulnerability, bool, error) {

	id := statement.ID()
	if id == "" {
		return nil, false, fmt.Errorf("statement %d has no id, so there is nothing to say it "+
			"is about", index)
	}

	// The condition that cannot be inferred. Without it msis would have to guess whether an
	// assessment made for some earlier release was meant to cover this one, and guessing
	// "yes" is exactly how a `not_affected` outlives the reason it was true.
	assessed := statement.Property(sbom.PropAssessedVersion)
	if assessed == "" {
		return nil, false, fmt.Errorf("the statement for %s does not record which release it "+
			"was assessed against; add a %q property. Without it msis cannot tell an "+
			"assessment that still applies from one that has been overtaken, and carrying it "+
			"forward anyway is how a vulnerability gets hidden", id, sbom.PropAssessedVersion)
	}

	var failed []string

	// Scope. The assessed release always counts; anything beyond it is what the assessor
	// explicitly widened to, and never what msis inferred.
	if !inScope(release, assessed, statement.Property(sbom.PropAppliesToVersions)) {
		failed = append(failed, fmt.Sprintf(
			"it was assessed against %s and this is %s; an assessment is about the product "+
				"using the component, so it does not carry to another release unless %s says so",
			assessed, release, sbom.PropAppliesToVersions))
	}

	// The refs it is about, and the digests of those refs now.
	refs := statement.AffectedRefs()
	if len(refs) == 0 {
		failed = append(failed, "it names no affected component, so what it is about cannot "+
			"be checked against this build")
	}
	var observed []string
	for _, ref := range refs {
		c, ok := known[ref]
		if !ok {
			failed = append(failed, fmt.Sprintf(
				"it assesses %q, which this build does not contain", ref))
			continue
		}
		if c.digest != "" {
			observed = append(observed, ref+"="+c.digest)
		}
		switch want := statement.Property(sbom.PropAssessedDigest); {
		case want == "":
			// No digest recorded, so there is nothing to check; the version scope carries
			// the weight.
		case c.digest == "":
			// A condition that CANNOT be checked has not been met. A component msis never
			// hashed - one contributed by a supplied document, which the conformance rules
			// admit without a digest - would otherwise let a recorded digest condition pass
			// by being unverifiable, which is the reassuring answer and the wrong one.
			failed = append(failed, fmt.Sprintf(
				"it was assessed against %s... of %s, and this build records no digest for "+
					"that component, so the condition cannot be checked",
				abbreviate(want), c.name))
		case !strings.EqualFold(want, c.digest):
			failed = append(failed, fmt.Sprintf(
				"%s now carries %s..., not the %s... it was assessed against",
				c.name, abbreviate(c.digest), abbreviate(want)))
		}
	}

	statement.SetProperty(sbom.PropObservedDigest, strings.Join(observed, " "))
	if len(failed) == 0 {
		statement.SetProperty(sbom.PropApplicability, sbom.ApplicabilityApplies)
		return statement, true, nil
	}

	sort.Strings(failed)
	statement.SetProperty(sbom.PropApplicability, sbom.ApplicabilityNeedsReview)
	statement.SetProperty(sbom.PropReviewReason, strings.Join(failed, "; "))

	// And the part that matters most. A statement that SUPPRESSES a finding - not_affected,
	// false_positive, resolved - must stop suppressing it, or a consumer reads a conclusion
	// whose premises msis has just reported as no longer true. It is moved to in_triage,
	// which is CycloneDX's own word for "being investigated", and what it used to say is
	// preserved beside it so nothing is lost.
	//
	// A statement that WARNS - exploitable, in_triage - is left exactly as it is. Neutralising
	// it would be weakening a warning because its scope lapsed, which is the same mistake in
	// the other direction and a worse one.
	if state := statement.AnalysisState(); suppresses(state) {
		statement.SetProperty(sbom.PropPreviousState, state)
		statement.SetAnalysisState("in_triage")
	}
	return statement, false, nil
}

// suppresses reports whether a state tells a reader there is nothing to act on.
func suppresses(state string) bool {
	switch state {
	case "not_affected", "false_positive", "resolved", "resolved_with_pedigree":
		return true
	}
	return false
}

// inScope reports whether the release being built is one the assessment covers.
//
// Exact matches and `*` only. A range syntax would need version comparison, and msis has no
// business deciding whether "4.10" is after "4.9" for someone else's versioning scheme: getting
// that wrong silently widens an assessment, which is the failure this package exists to stop.
func inScope(release, assessed, appliesTo string) bool {
	if release == assessed {
		return true
	}
	for _, v := range strings.Split(appliesTo, ",") {
		switch v = strings.TrimSpace(v); v {
		case "":
		case "*":
			return true
		case release:
			return true
		}
	}
	return false
}

// sidecar builds the document the statements go into: a CycloneDX BOM about the same product,
// carrying a BOM-Link to the inventory it annotates.
func sidecar(bom *sbom.Document, src Source, opts Options,
	statements []sbom.Vulnerability) (*sbom.Document, error) {

	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewSerial == nil {
		opts.NewSerial = sbom.NewSerialNumber
	}
	serial, err := opts.NewSerial()
	if err != nil {
		return nil, fmt.Errorf("generating a serial number: %w", err)
	}

	// The subject is the same product, described the same way - including its digest, so a
	// consumer can tell which build this assessment is about without trusting the link alone.
	subject := bom.Metadata.Component

	link := fmt.Sprintf("urn:cdx:%s/%d", strings.TrimPrefix(bom.SerialNumber, "urn:uuid:"),
		bom.Version)
	if bom.SerialNumber == "" {
		return nil, fmt.Errorf("the document for this build has no serial number, so a VEX " +
			"document cannot address it")
	}
	subject.ExternalReferences = append(subject.ExternalReferences, sbom.ExternalReference{
		Type:    "bom",
		URL:     link,
		Comment: "the inventory these statements are about",
		Hashes:  subject.Hashes,
	})

	doc := &sbom.Document{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.6",
		SerialNumber: serial,
		Version:      1,
		Metadata: sbom.Metadata{
			Timestamp: opts.Now().UTC().Format(time.RFC3339),
			Tools:     sbom.Tools{Components: []sbom.Component{sbom.MsisTool(opts.MsisVersion, opts.MsisPath)}},
			Component: subject,
			Supplier:  bom.Metadata.Supplier,
			// The identity metadata is COPIED from the inventory rather than restated. A
			// consumer - the index above all - identifies a product by its UpgradeCode,
			// which Windows Installer defines as the identifier constant across releases;
			// a document naming only the product's NAME is a different product as far as
			// that consumer is concerned, and an assessment filed under it would join to
			// nothing and therefore subtract nothing.
			Properties: append(identityOf(bom), sbom.Property{
				Name: sbom.PropVEXSubject, Value: link,
			}),
		},
		// Explicitly empty, not nil: `components` is declared as an array, and a VEX document
		// inventories nothing - that is what the document it links to is for.
		Components:      []sbom.Component{},
		Vulnerabilities: statements,
	}
	return doc, nil
}

// identityOf copies the metadata that says WHICH product and which build this is about.
//
// Not everything: a coverage note or a build-record property describes the inventory's own
// derivation and would be a false claim on a document that derived nothing.
func identityOf(bom *sbom.Document) []sbom.Property {
	var out []sbom.Property
	for _, want := range sbom.IdentityProperties {
		for _, p := range bom.Metadata.Properties {
			if p.Name == want {
				out = append(out, p)
			}
		}
	}
	return out
}

func sortStatements(v []sbom.Vulnerability) {
	sort.SliceStable(v, func(i, j int) bool {
		if a, b := v[i].ID(), v[j].ID(); a != b {
			return a < b
		}
		return strings.Join(v[i].AffectedRefs(), ",") < strings.Join(v[j].AffectedRefs(), ",")
	})
}

// Validate checks the rendered sidecar against the vendored CycloneDX schema. A VEX document is
// a BOM, so it answers to the same schema as every other document msis writes.
func Validate(data []byte) error {
	return conformance.ValidateSchema(data)
}

func sha256Of(hashes []sbom.Hash) string {
	for _, h := range hashes {
		if strings.EqualFold(h.Alg, "SHA-256") {
			return strings.ToLower(h.Content)
		}
	}
	return ""
}

func abbreviate(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}
