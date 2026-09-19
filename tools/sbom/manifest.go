package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// manifest is the packaging-time evidence that ties this SBOM to one release run.
//
// Without it the tool read binaries from one directory and installers from another with nothing
// connecting them, and asked timestamps to stand in for a binding. Timestamps do not carry that
// weight: copying an older release's assets into dist, or refreshing an mtime, restores the
// appearance of a matched pair. So the hashes are taken at the two moments that matter and
// written down.
//
//	sbom -capture   after the binaries are built, BEFORE packaging: hashes each binary and
//	                records the WiX version observed at that moment
//	sbom -seal      after packaging succeeds: re-checks those binary hashes and adds the
//	                artifacts'
//	sbom            generates the document, refusing unless every recorded hash still matches
//
// The claim this supports is exact and no larger: these binaries were present, unchanged, when
// packaging began in the run that produced these artifacts. It is not proof that a binary's bytes
// are inside a given MSI - that would mean extracting the packaged executable from the MSI's CAB -
// and nothing in the document says otherwise.
type manifest struct {
	Version string `json:"version"`

	// WixVersion is observed at capture time, not at generation time. Querying the installed
	// WiX when the document is written lets a later toolchain upgrade silently rewrite a
	// finished release's build provenance.
	WixVersion string `json:"wixVersion,omitempty"`

	Binaries  map[string]string `json:"binaries"`
	Artifacts map[string]string `json:"artifacts,omitempty"`
}

// manifestName sits beside the artifacts, inside the directory clean-bootstrap wipes, so a
// manifest can never outlive the release run that wrote it.
const manifestName = "build-manifest.json"

func manifestPath(dist string) string { return filepath.Join(dist, manifestName) }

func writeManifest(dist string, m *manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(dist), append(data, '\n'), 0644)
}

func readManifest(dist string) (*manifest, error) {
	data, err := os.ReadFile(manifestPath(dist))
	if err != nil {
		return nil, fmt.Errorf("no packaging manifest: %w\n"+
			"an SBOM may only be generated from a release run that recorded one; run `just release-all`", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("packaging manifest is unreadable: %w", err)
	}
	return &m, nil
}

// capture records the binaries and the toolchain as they are immediately before packaging.
func capture(version, dist, binDir, wixVersion string) error {
	binaries, err := statFiles(binDir, binaryNames())
	if err != nil {
		return err
	}
	m := &manifest{
		Version:    version,
		WixVersion: strings.TrimSpace(wixVersion),
		Binaries:   map[string]string{},
	}
	if m.WixVersion == "(unavailable)" {
		m.WixVersion = ""
	}
	for _, b := range binaries {
		m.Binaries[b.name] = b.sha256
	}
	return writeManifest(dist, m)
}

// seal adds the artifacts once packaging has succeeded, after re-checking that the binaries it
// was handed are still the ones captured before packaging began.
func seal(version, dist, binDir string) error {
	m, err := readManifest(dist)
	if err != nil {
		return err
	}
	if m.Version != version {
		return fmt.Errorf("packaging manifest is for %s, not %s; the release run was interrupted", m.Version, version)
	}
	if err := verifyRecorded(binDir, m.Binaries, "binary"); err != nil {
		return err
	}

	artifacts, err := statFiles(dist, artifactNames(version))
	if err != nil {
		return err
	}
	m.Artifacts = map[string]string{}
	for _, a := range artifacts {
		m.Artifacts[a.name] = a.sha256
	}
	return writeManifest(dist, m)
}

// verifyRecorded re-hashes each recorded file and fails on the first disagreement. Names are
// walked in sorted order so the message does not depend on map iteration.
func verifyRecorded(dir string, recorded map[string]string, kind string) error {
	names := make([]string, 0, len(recorded))
	for name := range recorded {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, name)
		sum, err := sha256File(path)
		if err != nil {
			return fmt.Errorf("%s recorded at packaging time is missing: %w", kind, err)
		}
		if sum != recorded[name] {
			return fmt.Errorf(
				"%s %s does not match what packaging recorded\n"+
					"  recorded %s\n  on disk  %s\n"+
					"the tree has changed since the release was built; run `just release-all`",
				kind, name, recorded[name], sum)
		}
	}
	return nil
}
