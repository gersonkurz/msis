package sbom

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/contact"
)

// Enrichment layers what the build knew onto a document derived from the artifact (#34).
//
// The direction is fixed and it matters: the ARTIFACT is the document, and enrichment adds
// facts to it. It never replaces one. A payload's digest always comes from the bytes in the
// package, so an enriched document and an artifact-only document of the same package agree on
// every hash - which is the property that makes the two interchangeable as evidence.
//
// Where the build's own reading CAN be checked against the artifact, it is, and a disagreement
// is fatal rather than recorded. If the file the build hashed is not the file the package
// contains, then the source path about to be published is a source path for different bytes,
// and publishing it would be worse than publishing nothing.

// enrich applies a build record to a document. It is called after the document is complete and
// before it is sorted, so everything it adds is ordered with the rest.
func enrich(doc *Document, rec *buildrecord.Record) error {
	if rec == nil {
		return nil
	}

	// The document now also holds what the build knew, not only what the artifact says.
	doc.Metadata.Lifecycles = append([]Lifecycle{{Phase: LifecycleBuild}}, doc.Metadata.Lifecycles...)
	// Who created the SBOM (BSI TR-03183-2 v2.1.0 §5.2.1): named by the build, never inferred
	// from the manufacturer, and validated when the variables were read (#64).
	// The licence the document itself is offered under, when the build grants one (#62). The
	// SBOM's creator decides it, never msis: an SBOM msis writes for a customer's product is the
	// customer's document.
	if l := rec.DataLicense; l != "" {
		doc.Metadata.Licenses = []LicenseExpression{{Expression: l}}
	}
	if c := rec.SBOMCreator; c != "" {
		if contact.IsEmail(c) {
			doc.Metadata.Manufacturer = creatorEntity("", "", c)
		} else {
			doc.Metadata.Manufacturer = creatorEntity("", c, "")
		}
	}
	doc.Metadata.Properties = append(doc.Metadata.Properties,
		Property{propBuildPath, string(rec.Path)},
		Property{propBuildScript, rec.Script},
	)
	for _, t := range rec.Toolchain {
		doc.Metadata.Properties = append(doc.Metadata.Properties,
			Property{propBuildTool + "." + strings.ToLower(t.Name), t.Version})
	}

	// What the build could not account for goes in the document, not only in the terminal.
	for _, u := range rec.Unresolved {
		doc.Metadata.Properties = append(doc.Metadata.Properties, Property{propBuildUnresolved, u})
	}

	if err := enrichFiles(doc, rec); err != nil {
		return err
	}
	enrichBinaries(doc, rec)
	enrichExtensionFiles(doc, rec)
	enrichPrerequisites(doc, rec)
	enrichChained(doc, rec)
	enrichRuntimes(doc, rec)

	// The coverage note has to say that enrichment happened AND what it still does not
	// reach, or a reader cannot tell a file with no source from one nobody looked for.
	doc.Metadata.Properties = append(doc.Metadata.Properties,
		Property{propBuildCoverage, buildCoverage(doc, rec)})
	return nil
}

// enrichFiles attaches a source path to each payload component the build can account for.
//
// The join is the WiX File id, which the artifact carries as msis:msi.fileKey and the generator
// assigned - an exact join, not a name match. A file the build knows nothing about keeps its
// artifact-only description; a merge module's contribution and the template's own Binary
// streams are exactly that, and inventing provenance for them is the thing #29 D4 forbids.
func enrichFiles(doc *Document, rec *buildrecord.Record) error {
	byKey := map[string]*Component{}
	for i := range doc.Components {
		if k := propertyValueOf(doc.Components[i].Properties, propFileKey); k != "" {
			byKey[k] = &doc.Components[i]
		}
	}

	var conflicts []string
	for _, f := range rec.Files {
		c, ok := byKey[f.FileID]
		if !ok {
			// The build produced a file the artifact does not contain. That is a real
			// difference and it is recorded rather than dropped.
			doc.Metadata.Properties = append(doc.Metadata.Properties,
				Property{propBuildUnresolved, fmt.Sprintf(
					"%s (%s) was built but is not in the artifact", f.FileID, f.Source)})
			continue
		}

		// The check that makes the source path trustworthy. The build read a file and the
		// package contains a file; if their bytes differ, the path is about to be published
		// against the wrong content.
		if artifact := sha256Of(c.Hashes); artifact != "" && !strings.EqualFold(artifact, f.SHA256) {
			conflicts = append(conflicts, fmt.Sprintf(
				"%s: the build read %s (%s...) but the package contains %s...",
				f.FileID, f.Source, f.SHA256[:16], artifact[:16]))
			continue
		}

		c.Properties = append(c.Properties, Property{propBuildSource, f.Source})
		// BSI TR-03183-2 v2.1.0 §5.2.2: a file with no version of its own takes its modification
		// date, from the file's metadata (#63). Only the build has that - the source file it read,
		// just verified to be the bytes that were packaged - so it is a build fact, stated as one.
		if c.Version == "" && !f.Modified.IsZero() {
			c.Version = f.Modified.UTC().Format(time.RFC3339)
			c.Properties = append(c.Properties, Property{propBuildVersionFrom,
				"the modification date of " + f.Source + ", which has no version of its own (BSI TR-03183-2 v2.1.0 §5.2.2)"})
		}
		if f.Root != "" {
			// WHICH bind path it resolved in, as for a Binary stream. In the ordinary case
			// that is the script's own directory; when it is not, the source path alone
			// would be read as relative to the .msis and point at a different file.
			c.Properties = append(c.Properties, Property{propBuildSourceRoot, f.Root})
		}
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf(
			"the build record disagrees with the artifact for %d file(s):\n  %s\n"+
				"  enrichment adds facts to what the package says and must never contradict "+
				"it, so no document is written",
			len(conflicts), strings.Join(conflicts, "\n  "))
	}
	return nil
}

// enrichBinaries attributes a Binary-table stream to the file the build resolved for it.
//
// Matched by CONTENT. The template declares the installer-hook DLL as Binary id "binary.dll"
// whatever the file on disk is called, so a name join would miss it - and the streams WiX's own
// extensions contribute have no file behind them at all. A digest match is the only claim that
// can be shown to be true, and where nothing matches, nothing is claimed.
func enrichBinaries(doc *Document, rec *buildrecord.Record) {
	byDigest := map[string]buildrecord.Binary{}
	for _, b := range rec.Binaries {
		byDigest[strings.ToLower(b.SHA256)] = b
	}
	for i := range doc.Components {
		c := &doc.Components[i]
		if propertyValueOf(c.Properties, propRole) != roleBinary {
			continue
		}
		b, ok := byDigest[strings.ToLower(sha256Of(c.Hashes))]
		if !ok {
			continue
		}
		c.Properties = append(c.Properties,
			Property{propBuildSource, b.Source},
			Property{propBuildSourceRoot, b.Root},
		)
	}
}

// enrichExtensionFiles attributes a Binary-table stream to the WiX extension package that
// shipped it (#67, decisions D18): its bytes must be those of a file in the extension the build
// loaded. A stream that merely has WiX's name - a template can define WixUI_Bmp_Banner itself -
// matches nothing and stays as it was. The package's facts are what its .nuspec declares,
// pinned in internal/wix and checked against nuget.org before a release: the authors, reached
// through the repository they name; the licence by the id BSI §6.1 prescribes - ScanCode's, as
// the agreement has no SPDX id, and so one concluded expression (licencesOf); and the package
// version as the stream's version.
func enrichExtensionFiles(doc *Document, rec *buildrecord.Record) {
	byDigest := map[string]buildrecord.ExtensionFile{}
	for _, e := range rec.Extensions {
		if _, seen := byDigest[strings.ToLower(e.SHA256)]; !seen { // rec is sorted: the first is stable
			byDigest[strings.ToLower(e.SHA256)] = e
		}
	}
	for i := range doc.Components {
		c := &doc.Components[i]
		if propertyValueOf(c.Properties, propRole) != roleBinary {
			continue
		}
		e, ok := byDigest[strings.ToLower(sha256Of(c.Hashes))]
		if !ok {
			continue
		}
		from := fmt.Sprintf("%s %s, %s", e.Package, e.Version, e.Entry)
		c.Version = e.Version
		c.Manufacturer = creatorEntity(e.Authors, e.Repository, "")
		c.Licenses = licencesOf(e.License)
		// The repository the package declares is its source code (#68): BSI §5.2.3 accepts
		// the repository itself where a version in it cannot be named.
		c.ExternalReferences = append(c.ExternalReferences, sourceReference(e.Repository))
		c.Properties = withoutProperty(c.Properties, propIdentityUnknown)
		c.Properties = append(c.Properties,
			Property{propIdentityUnknown, "the file " + from + ", matched by SHA-256"},
			Property{propBuildExtension, from},
		)
	}
}

// enrichPrerequisites and enrichChained attach provenance to components the ARTIFACT already
// has, matched by digest.
//
// They used to create components of their own, which was wrong three ways at once: the
// document then claimed contents the artifact was never checked against, a prerequisite with
// no digest became a component with no digest and the emitter refused to write anything, and
// an auto-bundle's MSI inherited the wrapper's prerequisites. Attaching to what is already
// there fixes all three - there is nothing to invent, the match IS the verification, and a
// record entry that matches nothing is reported rather than published.
//
// The digest is the join because it is the only thing that can be shown to be true. A name
// would not do: the bundle's chain names packages after what the author called them, and the
// build knows them by the file it read.
func enrichPrerequisites(doc *Document, rec *buildrecord.Record) {
	for _, p := range rec.Prereqs {
		c, ambiguous := chainComponentWithDigest(doc, p.SHA256)
		if c == nil {
			doc.Metadata.Properties = append(doc.Metadata.Properties,
				Property{propBuildUnresolved, describeUnmatched(
					"prerequisite "+p.DisplayName(), p.SHA256, p.Source, ambiguous)})
			continue
		}
		if p.DownloadURL != "" {
			c.ExternalReferences = append(c.ExternalReferences,
				ExternalReference{Type: "distribution", URL: p.DownloadURL,
					Comment: "where the build fetched this prerequisite from"})
		}
		if p.CachePath != "" {
			c.Properties = append(c.Properties, Property{propPrereqCache, p.CachePath})
		}
		if p.Source != "" {
			c.Properties = append(c.Properties, Property{propBuildSource, p.Source})
			if p.Root != "" {
				c.Properties = append(c.Properties,
					Property{propBuildSourceRoot, p.Root})
			}
		}
		c.Properties = append(c.Properties, Property{propPrereqType, p.Type})
		if p.Arch != "" {
			c.Properties = append(c.Properties, Property{propPrereqArch, p.Arch})
		}
		// What the bytes were checked against before being chained (#50): a pin msis
		// carries, the script's own sha256=, or nothing. The artifact cannot say this; only
		// the build can, and a reader deciding how much to trust the chained installer needs it.
		if p.Verification != "" {
			c.Properties = append(c.Properties, Property{propPrereqVerification, p.Verification})
		}
	}
}

// enrichChained says which file on disk each chained installer was built from - which the
// bundle artifact cannot say, because it holds only the packaged bytes.
func enrichChained(doc *Document, rec *buildrecord.Record) {
	for _, ch := range rec.Chained {
		c, ambiguous := chainComponentWithDigest(doc, ch.SHA256)
		if c == nil {
			doc.Metadata.Properties = append(doc.Metadata.Properties,
				Property{propBuildUnresolved, describeUnmatched(
					"chained "+ch.Kind, ch.SHA256, ch.Source, ambiguous)})
			continue
		}
		c.Properties = append(c.Properties, Property{propBuildSource, ch.Source})
		if ch.Root != "" {
			// Which bind path it came from, as for payload. Without it the path reads as
			// relative to the .msis, and when the WXS directory shadowed the script's copy
			// that names a different file from the one that was packaged.
			c.Properties = append(c.Properties, Property{propBuildSourceRoot, ch.Root})
		}
	}
}

// chainComponentWithDigest finds the ONE chain package carrying exactly these bytes.
//
// Two restrictions, both of them the difference between a verified claim and a plausible one:
//
//   - Only chain packages are candidates. A prerequisite and a chained installer ARE chain
//     packages; the bootstrapper's own payloads and the bundle's loose payloads are not, and
//     taking the first digest match over the whole document could attach a chain package's
//     provenance to one of those instead.
//   - Exactly one match, or none. Equal bytes prove the two are the same CONTENT, not that this
//     record produced that package: with a bundle chaining the same installer twice, first-match
//     would give both records the same component and leave the other unattributed. Where the
//     digest cannot single one out, nothing is attached and the ambiguity is reported.
//
// The second return value is non-empty only in the ambiguous case, and then names what was
// found, so the caller can say why nothing was attached.
func chainComponentWithDigest(doc *Document, digest string) (*Component, string) {
	if digest == "" {
		return nil, ""
	}
	want := strings.ToLower(digest)
	var found []int
	for i := range doc.Components {
		if propertyValueOf(doc.Components[i].Properties, propRole) != roleChained {
			continue
		}
		if sha256Of(doc.Components[i].Hashes) == want {
			found = append(found, i)
		}
	}
	switch len(found) {
	case 0:
		return nil, ""
	case 1:
		return &doc.Components[found[0]], ""
	}
	refs := make([]string, 0, len(found))
	for _, i := range found {
		refs = append(refs, doc.Components[i].BOMRef)
	}
	sort.Strings(refs)
	return nil, "several chain packages carry those bytes (" + strings.Join(refs, ", ") + "), " +
		"so which of them this was built from cannot be told from the digest alone"
}

// describeUnmatched states, in the document, that the build arranged something the artifact
// does not carry. Silence would let a reader assume the two agreed.
func describeUnmatched(what, digest, source, ambiguous string) string {
	note := fmt.Sprintf("the build arranged %s", what)
	if source != "" {
		note += " from " + source
	}
	switch {
	case ambiguous != "":
		note += ", but " + ambiguous
	case digest == "":
		note += ", but could not read its bytes, so it could not be matched to anything the " +
			"artifact carries"
	default:
		note += fmt.Sprintf(", but no component of this artifact carries those bytes (%s...)",
			digest[:min(16, len(digest))])
	}
	return note
}

// enrichRuntimes records what /STANDALONE turned into launch conditions.
//
// These are EXTERNALLY REQUIRED, not shipped. They get no hash and no payload role, because
// nothing was distributed: the installer checks for them and refuses to run without them. An
// SBOM that listed them like bundled payload would claim a distribution that never happened,
// which is the opposite of what the document is for.
func enrichRuntimes(doc *Document, rec *buildrecord.Record) {
	ns := namespaceFor(doc)
	for _, rt := range rec.Runtimes {
		c := Component{
			Type:    "application",
			BOMRef:  ns + "/required-runtime/" + encodeRefPart(rt.Type+"/"+rt.Version),
			Name:    rt.Type,
			Version: rt.Version,
			Description: "externally required runtime: this installer detects it and refuses " +
				"to run without it; it does not ship it",
			Properties: []Property{
				{propRole, roleRequiredRuntime},
				{propCarried, "false"},
				{propPayloadUnavailable,
					"nothing was distributed for this runtime, so there are no bytes to hash"},
				{propIdentityUnknown, "undetermined: a runtime required on the machine"},
			},
		}
		if rt.Condition != "" {
			c.Properties = append(c.Properties, Property{propLaunchCondition, rt.Condition})
		}
		doc.Components = append(doc.Components, c)
		linkToRoot(doc, c.BOMRef)
	}
}

// linkToRoot adds a component to what the subject contains, and to the "contents unknown"
// composition - a prerequisite's own contents are as opaque as any other payload's.
func linkToRoot(doc *Document, ref string) {
	root := doc.Metadata.Component.BOMRef
	for i := range doc.Dependencies {
		if doc.Dependencies[i].Ref == root {
			doc.Dependencies[i].DependsOn = append(doc.Dependencies[i].DependsOn, ref)
		}
	}
	for i := range doc.Compositions {
		if doc.Compositions[i].Aggregate == aggregateUnknown &&
			len(doc.Compositions[i].Assemblies) > 0 {
			doc.Compositions[i].Assemblies = append(doc.Compositions[i].Assemblies, ref)
			doc.Compositions[i].Dependencies = append(doc.Compositions[i].Dependencies, ref)
			return
		}
	}
	doc.Compositions = append(doc.Compositions, Composition{
		Aggregate:    aggregateUnknown,
		Assemblies:   []string{ref},
		Dependencies: []string{ref},
	})
}

// namespaceFor recovers the document's ref namespace from its subject, so an enriched
// component's ref sits in the same namespace as everything else.
func namespaceFor(doc *Document) string {
	ref := doc.Metadata.Component.BOMRef
	if i := strings.LastIndex(ref, "/"); i > 0 {
		return ref[:i]
	}
	return "msis/unidentified"
}

// buildCoverage says what enrichment reached and what it did not, because "no source path" and
// "nobody looked" are different answers.
func buildCoverage(doc *Document, rec *buildrecord.Record) string {
	withSource, payload := 0, 0
	for _, c := range doc.Components {
		if propertyValueOf(c.Properties, propRole) != rolePayload {
			continue
		}
		payload++
		if propertyValueOf(c.Properties, propBuildSource) != "" {
			withSource++
		}
	}
	note := fmt.Sprintf(
		"enriched from the %s build of %s: %d of %d payload components carry the source they "+
			"were built from", rec.Path, rec.Script, withSource, payload)
	if withSource < payload {
		note += "; the rest were contributed by a merge module, a template or the WiX " +
			"toolchain and have no source in this script"
	}
	if len(rec.Unresolved) > 0 {
		note += fmt.Sprintf("; %d input(s) the build could not account for are listed in "+
			"%s properties", len(rec.Unresolved), propBuildUnresolved)
	}
	return note
}

func propertyValueOf(props []Property, name string) string {
	for _, p := range props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func sha256Of(hashes []Hash) string {
	for _, h := range hashes {
		if strings.EqualFold(h.Alg, "SHA-256") {
			return strings.ToLower(h.Content)
		}
	}
	return ""
}
