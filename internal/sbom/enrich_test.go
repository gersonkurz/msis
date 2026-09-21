package sbom

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/burnread"
	"github.com/gersonkurz/msis/internal/msiread"
)

// Enrichment adds facts to what the artifact says and must never contradict it. These drive
// the rule directly rather than through a build, because the interesting cases - the build and
// the package disagreeing about a file - are ones a working build does not produce.

// recordFor builds a record whose files match a synthetic package, so a test can then change
// exactly one thing.
func recordFor(t *testing.T, files ...buildrecord.File) *buildrecord.Record {
	t.Helper()
	rec := buildrecord.New(buildrecord.PathMSI, "testdata/setup.msis", nil)
	rec.Files = append(rec.Files, files...)
	return rec
}

func docWith(t *testing.T, rec *buildrecord.Record) (*Document, error) {
	t.Helper()
	pkg := syntheticPackage()
	return FromPackage(pkg, Options{MsisVersion: "test", Build: rec})
}

// syntheticPackage's first file, so a record can agree or disagree with it deliberately.
func firstFile(t *testing.T) msiread.File {
	t.Helper()
	pkg := syntheticPackage()
	if len(pkg.Files) == 0 {
		t.Fatal("the synthetic package has no files")
	}
	return pkg.Files[0]
}

// The check that makes a published source path trustworthy. If the file the build hashed is
// not the file the package contains, the path is about to be published against the wrong
// bytes - so no document is written at all.
func TestBuildRecordConflictIsFatal(t *testing.T) {
	f := firstFile(t)
	rec := recordFor(t, buildrecord.File{
		FileID: f.ID,
		Source: "src/wrong.dll",
		SHA256: strings.Repeat("9", 64), // not what the package contains
	})

	_, err := docWith(t, rec)
	if err == nil {
		t.Fatal("a build record that disagrees with the artifact must not produce a document")
	}
	for _, want := range []string{f.ID, "src/wrong.dll", "never contradict"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// ...and when they agree, the source is attached. The refusal above would otherwise be
// satisfied by refusing everything.
func TestAnAgreeingRecordAttachesTheSource(t *testing.T) {
	f := firstFile(t)
	rec := recordFor(t, buildrecord.File{
		FileID: f.ID,
		Source: "src/a.dll",
		SHA256: f.SHA256,
	})

	doc, err := docWith(t, rec)
	if err != nil {
		t.Fatalf("an agreeing record must enrich, not fail: %v", err)
	}

	var found bool
	for _, c := range doc.Components {
		if propertyValueOf(c.Properties, propFileKey) != f.ID {
			continue
		}
		found = true
		if got := propertyValueOf(c.Properties, propBuildSource); got != "src/a.dll" {
			t.Errorf("build source = %q, want src/a.dll", got)
		}
		// The digest still comes from the ARTIFACT - enrichment adds, it does not replace.
		if sha256Of(c.Hashes) != strings.ToLower(f.SHA256) {
			t.Errorf("the payload digest changed: %s", sha256Of(c.Hashes))
		}
	}
	if !found {
		t.Fatalf("no component carries file key %s", f.ID)
	}

	// The document says which build produced it, and how completely enrichment reached.
	if propertyValueOf(doc.Metadata.Properties, propBuildPath) != string(buildrecord.PathMSI) {
		t.Error("the document does not record which build path produced it")
	}
	if propertyValueOf(doc.Metadata.Properties, propBuildCoverage) == "" {
		t.Error("the document does not say how far enrichment reached")
	}
}

// A file the build produced that the artifact does not contain is a real difference, and it is
// recorded rather than dropped - the artifact is authoritative, but the disagreement is a fact.
func TestAFileBuiltButNotPackagedIsRecorded(t *testing.T) {
	rec := recordFor(t, buildrecord.File{
		FileID: "FILE_ID99999",
		Source: "src/ghost.dll",
		SHA256: strings.Repeat("a", 64),
	})

	doc, err := docWith(t, rec)
	if err != nil {
		t.Fatalf("a file missing from the artifact is not fatal, only notable: %v", err)
	}

	var noted bool
	for _, p := range doc.Metadata.Properties {
		if p.Name == propBuildUnresolved && strings.Contains(p.Value, "src/ghost.dll") {
			noted = true
		}
	}
	if !noted {
		t.Error("a file the build produced but the artifact lacks was dropped silently")
	}
}

// Enrichment must not disturb the document's determinism: two runs over one record differ only
// where they already did (#29 D6).
func TestEnrichmentStaysDeterministic(t *testing.T) {
	f := firstFile(t)
	build := func() []byte {
		rec := recordFor(t,
			buildrecord.File{FileID: f.ID, Source: "src/a.dll", SHA256: f.SHA256})
		rec.AddTool("wix", "7.0.0")
		rec.AddTool("msis", "test")
		rec.Sort()
		doc, err := docWith(t, rec)
		if err != nil {
			t.Fatal(err)
		}
		data, err := Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		canon, err := CanonicalForDiff(data)
		if err != nil {
			t.Fatal(err)
		}
		return canon
	}
	if a, b := build(), build(); string(a) != string(b) {
		t.Errorf("two enriched documents for one record differ:\n%s\n---\n%s", a, b)
	}
}

// A document with no build record is unchanged: `/SBOM` against an artifact alone still works,
// which is the case #34 must not break.
func TestNoRecordMeansNoEnrichment(t *testing.T) {
	doc, err := docWith(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Metadata.Properties {
		if strings.HasPrefix(p.Name, "msis:build.") {
			t.Errorf("an artifact-only document carries a build property: %s", p.Name)
		}
	}
	for _, c := range doc.Components {
		if propertyValueOf(c.Properties, propBuildSource) != "" {
			t.Errorf("%s carries a build source with no record", c.BOMRef)
		}
	}
}

// A Binary stream is attributed to the file the build resolved BY CONTENT. The template
// declares the installer-hook DLL as Binary id "binary.dll" whatever the file is called, so a
// name join would miss it - and picking any candidate would attribute the wrong provenance the
// moment a build resolves more than one.
func TestBinariesAreAttributedByContentNotByPosition(t *testing.T) {
	pkg := syntheticPackage()
	if len(pkg.Binaries) < 2 {
		pkg.Binaries = []msiread.Binary{
			{Name: "first.dll", Size: 3, SHA256: strings.Repeat("1", 64)},
			{Name: "second.dll", Size: 3, SHA256: strings.Repeat("2", 64)},
		}
	}

	rec := buildrecord.New(buildrecord.PathMSI, "testdata/setup.msis", nil)
	// Deliberately in the OPPOSITE order to the streams, and with names that would mislead
	// a name-based join: the source called "second" holds the first stream's bytes.
	rec.Binaries = []buildrecord.Binary{
		{Name: "second.dll", Source: "x64/second.dll", Root: "templates",
			SHA256: strings.Repeat("2", 64)},
		{Name: "first.dll", Source: "x64/first.dll", Root: "custom-templates",
			SHA256: strings.Repeat("1", 64)},
	}

	doc, err := FromPackage(pkg, Options{MsisVersion: "test", Build: rec})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		strings.Repeat("1", 64): "x64/first.dll",
		strings.Repeat("2", 64): "x64/second.dll",
	}
	seen := 0
	for _, c := range doc.Components {
		if propertyValueOf(c.Properties, propRole) != roleBinary {
			continue
		}
		digest := sha256Of(c.Hashes)
		source := propertyValueOf(c.Properties, propBuildSource)
		if expect, ok := want[digest]; ok {
			seen++
			if source != expect {
				t.Errorf("the stream with digest %s… was attributed to %q, want %q",
					digest[:8], source, expect)
			}
		} else if source != "" {
			t.Errorf("a stream the build did not resolve was attributed to %q", source)
		}
	}
	if seen != 2 {
		t.Fatalf("%d of the two streams were attributed; the test is not measuring the join", seen)
	}
}

// #29 D5 is absolute for payload: a component for bytes the installer distributes must carry a
// digest, and no document goes out without one. The runtime exemption must not widen that.
func TestAPayloadWithoutADigestStillRefusesToEmit(t *testing.T) {
	pkg := syntheticPackage()
	if len(pkg.Files) == 0 {
		t.Fatal("the synthetic package has no files")
	}
	pkg.Files[0].SHA256 = "" // payload, and unhashed

	if _, err := FromPackage(pkg, Options{MsisVersion: "test"}); err == nil {
		t.Fatal("a payload component with no SHA-256 must not produce a document")
	}

	// ...and the exemption really is narrow: it turns on the ROLE, which no payload
	// component can hold, so nothing that should carry a digest can slip through by
	// merely lacking one.
	payload := Component{
		Properties: []Property{
			{propRole, rolePayload},
			{propPayloadUnavailable, "claiming an excuse a payload may not claim"},
		},
	}
	if admissibleWithoutDigest(payload) {
		t.Error("a payload component was admitted without a digest")
	}
	runtime := Component{
		Properties: []Property{
			{propRole, roleRequiredRuntime},
			{propPayloadUnavailable, "nothing was distributed"},
		},
	}
	if !admissibleWithoutDigest(runtime) {
		t.Error("a detected-only runtime was refused, so /STANDALONE cannot be described")
	}
	// Even a runtime has to SAY why, or the document leaves a reader inferring.
	silent := Component{Properties: []Property{{propRole, roleRequiredRuntime}}}
	if admissibleWithoutDigest(silent) {
		t.Error("a component with no digest and no reason was admitted")
	}
}

// --- attaching provenance to a chain package (#34) ------------------------------------------

// chainedDoc builds a bundle document with a record applied. A BUNDLE, not an MSI: a
// prerequisite and a chained installer are chain packages, and that is the only shape where
// the question "which component carries these bytes" is a real one.
func chainedDoc(t *testing.T, b *burnread.Bundle, rec *buildrecord.Record) *Document {
	t.Helper()
	b.Path = writeArtifact(t, t.TempDir(), filepath.Base(b.Path), "the bundle itself\n")
	doc, err := FromBundle(b, Options{MsisVersion: "test", Build: rec})
	if err != nil {
		t.Fatalf("FromBundle: %v", err)
	}
	return doc
}

func componentWithPackageID(doc *Document, id string) *Component {
	for i := range doc.Components {
		if propertyValueOf(doc.Components[i].Properties, propPackageID) == id {
			return &doc.Components[i]
		}
	}
	return nil
}

func bundleRecord(chained []buildrecord.Chained, prereqs []buildrecord.Prerequisite) *buildrecord.Record {
	rec := buildrecord.New(buildrecord.PathBundle, "testdata/setup.msis", nil)
	rec.Chained, rec.Prereqs = chained, prereqs
	return rec
}

// The document's spelling of the chain role is what enrichment filters on, and it is written
// by two different files. If they ever drift, provenance silently attaches to nothing.
func TestTheChainRoleIsSpeltTheSameOnBothSides(t *testing.T) {
	if roleChained != string(burnread.RoleChained) {
		t.Fatalf("enrichment looks for %q, but packageComponent writes %q",
			roleChained, burnread.RoleChained)
	}
}

// Provenance is attached to the CHAIN PACKAGE carrying those bytes - not to whatever component
// happens to match first. The bootstrapper's own payloads and the bundle's loose payloads are
// not chain packages, and attributing a chained installer's source to one of them would publish
// a provenance that was never checked against anything.
func TestProvenanceAttachesToTheChainPackageWithThoseBytes(t *testing.T) {
	b := syntheticBundle("suite.exe")
	chainDigest := b.Packages[0].Payloads[0].SHA256

	// The bootstrapper payload is given the SAME bytes, and it comes first in the document.
	// A first-match over all components lands here.
	b.UX[0].SHA256 = chainDigest

	doc := chainedDoc(t, b, bundleRecord(
		[]buildrecord.Chained{{Kind: "MsiPackage", Source: "sources/Example.msi",
			Root: "wxs", SHA256: chainDigest}}, nil))

	pkg := componentWithPackageID(doc, "Main")
	if pkg == nil {
		t.Fatal("no component for the chain package")
	}
	if got := propertyValueOf(pkg.Properties, propBuildSource); got != "sources/Example.msi" {
		t.Errorf("the chain package was not attributed: %q", got)
	}
	// And WHERE it resolved, for the same reason payload carries it: without the root the
	// path reads as relative to the .msis, which is a different file when the WXS directory
	// shadowed the script's copy.
	if got := propertyValueOf(pkg.Properties, propBuildSourceRoot); got != "wxs" {
		t.Errorf("source root = %q, want wxs", got)
	}

	for _, c := range doc.Components {
		if propertyValueOf(c.Properties, propRole) == roleChained {
			continue
		}
		if got := propertyValueOf(c.Properties, propBuildSource); got != "" {
			t.Errorf("%s is not a chain package but was attributed %q", c.BOMRef, got)
		}
	}
}

// Equal bytes prove the two are the same CONTENT, not that this record produced that package.
// Where more than one chain package carries them, nothing is attached and the ambiguity is
// stated: first-match would give this record one of them and leave the other unattributed,
// which reads exactly like a verified association and is not one.
func TestAmbiguousBytesAreReportedNotGuessed(t *testing.T) {
	b := syntheticBundle("suite.exe")
	twin := b.Packages[0]
	twin.ID, twin.DisplayName = "MainTwin", "Example (again)"
	twin.Payloads = append([]burnread.Payload(nil), b.Packages[0].Payloads...)
	twin.Payloads[0].ID = "MainTwinPayload"
	b.Packages = append(b.Packages, twin)
	digest := b.Packages[0].Payloads[0].SHA256

	doc := chainedDoc(t, b, bundleRecord(
		[]buildrecord.Chained{{Kind: "MsiPackage", Source: "sources/Example.msi", SHA256: digest}},
		nil))

	for _, c := range doc.Components {
		if got := propertyValueOf(c.Properties, propBuildSource); got != "" {
			t.Errorf("%s was attributed %q although the digest names two packages",
				c.BOMRef, got)
		}
	}

	var said string
	for _, p := range doc.Metadata.Properties {
		if p.Name == propBuildUnresolved && strings.Contains(p.Value, "sources/Example.msi") {
			said = p.Value
		}
	}
	if said == "" {
		t.Fatal("the ambiguity was dropped silently")
	}
	for _, want := range []string{"several chain packages", "Main", "MainTwin"} {
		if !strings.Contains(said, want) {
			t.Errorf("%q does not mention %q", said, want)
		}
	}
}

// A record entry matching nothing in the artifact is REPORTED, not published. The build
// arranged something the artifact does not carry, and that disagreement is a fact a reader
// needs - publishing the source path anyway would describe bytes that were never shipped.
func TestProvenanceForBytesTheArtifactLacksIsReportedNotPublished(t *testing.T) {
	doc := chainedDoc(t, syntheticBundle("suite.exe"), bundleRecord(
		[]buildrecord.Chained{{Kind: "MsiPackage", Source: "sources/ghost.msi",
			SHA256: strings.Repeat("e", 64)}},
		[]buildrecord.Prerequisite{{Type: "vcredist", Version: "2022", Carried: true,
			SHA256: strings.Repeat("d", 64)}}))

	for _, c := range doc.Components {
		if got := propertyValueOf(c.Properties, propBuildSource); got == "sources/ghost.msi" {
			t.Errorf("%s was given provenance for bytes it does not carry", c.BOMRef)
		}
		if propertyValueOf(c.Properties, propPrereqType) != "" {
			t.Errorf("%s was given a prerequisite's provenance it does not carry", c.BOMRef)
		}
	}

	var notedChain, notedPrereq bool
	for _, p := range doc.Metadata.Properties {
		if p.Name != propBuildUnresolved {
			continue
		}
		if strings.Contains(p.Value, "sources/ghost.msi") {
			notedChain = true
		}
		if strings.Contains(p.Value, "vcredist") {
			notedPrereq = true
		}
	}
	if !notedChain {
		t.Error("a chained installer the artifact does not carry was dropped silently")
	}
	if !notedPrereq {
		t.Error("a prerequisite the artifact does not carry was dropped silently")
	}
}
