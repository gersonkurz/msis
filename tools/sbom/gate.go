package main

// The release gate for SBOM coverage (#63): a release stops when a document it produced says
// less than the last release's did. Two views, deliberately different in kind:
//
//   - msis's own per-field counts, read straight off the CycloneDX: how many components carry no
//     licence, no creator, no version; how many hashed components lack SHA-512; how many payload
//     files lack their bsi:component:filename. Each is a MAXIMUM - a count above it fails, and a
//     count below it asks for the baseline to be lowered, so an improvement is locked in.
//   - sbomqs's BSI TR-03183-2 v2.1.0 score, as an external second opinion, against a MINIMUM.
//     sbomqs reads BSI differently from msis in places (decisions D10, D11), which is why it is
//     a floor and not the authority.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// sbomqsModule is the pinned sbomqs: `go run` fetches exactly this version, verified against
// the Go checksum database, so the score is reproducible and no binary is vendored.
const sbomqsModule = "github.com/interlynk-io/sbomqs/v2@v2.1.2"

// coverage is what the gate counts in one document.
type coverage struct {
	NoLicence  int `json:"noLicence"`
	NoCreator  int `json:"noCreator"`
	NoVersion  int `json:"noVersion"`
	NoSHA512   int `json:"noSHA512"`
	NoFilename int `json:"noFilename"`
}

type baseline struct {
	coverage         // maxima
	BSI      float64 // sbomqs --bsi-v21 total score, minimum
}

// gateBaselines is the coverage of the last release's documents, keyed by the artifact's name
// after "msis-<version>-". Measured on the 3.0.6 release inputs, 2026-09-24. Lower a maximum or
// raise a minimum when a change improves a document; the gate says when that is due.
var gateBaselines = map[string]baseline{
	"x64.msi":   {coverage{NoLicence: 12, NoCreator: 18, NoVersion: 7}, 6.89},
	"x86.msi":   {coverage{NoLicence: 12, NoCreator: 18, NoVersion: 7}, 6.89},
	"arm64.msi": {coverage{NoLicence: 12, NoCreator: 18, NoVersion: 7}, 6.89},
	"setup.exe": {coverage{NoLicence: 10, NoCreator: 9, NoVersion: 6}, 6.75},
}

type gateComponent struct {
	Version  string `json:"version"`
	Licenses []struct {
		License *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"license"`
		Expression string `json:"expression"`
	} `json:"licenses"`
	Manufacturer *gateEntity   `json:"manufacturer"`
	Supplier     *gateEntity   `json:"supplier"`
	Authors      []gateContact `json:"authors"`
	Hashes       []hash        `json:"hashes"`
	Properties   []struct {
		Name string `json:"name"`
	} `json:"properties"`
	Components []gateComponent `json:"components"`
}

// gateEntity and gateContact are CycloneDX's organizationalEntity and organizationalContact, the
// parts that name someone: an empty object, or one whose only fields are empty, names no one.
type gateEntity struct {
	Name    string        `json:"name"`
	URL     []string      `json:"url"`
	Contact []gateContact `json:"contact"`
}

type gateContact struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

func (c gateContact) names() bool { return c.Name != "" || c.Email != "" || c.Phone != "" }

func (e *gateEntity) names() bool {
	if e == nil {
		return false
	}
	if e.Name != "" {
		return true
	}
	for _, u := range e.URL {
		if u != "" {
			return true
		}
	}
	for _, c := range e.Contact {
		if c.names() {
			return true
		}
	}
	return false
}

// hasCreator and hasLicence count only a value that says something; `{}` or `null` does not.
func (c gateComponent) hasCreator() bool {
	if c.Manufacturer.names() || c.Supplier.names() {
		return true
	}
	for _, a := range c.Authors {
		if a.names() {
			return true
		}
	}
	return false
}

func (c gateComponent) hasLicence() bool {
	for _, l := range c.Licenses {
		if l.Expression != "" || (l.License != nil && (l.License.ID != "" || l.License.Name != "")) {
			return true
		}
	}
	return false
}

func (c gateComponent) has(prop string) bool {
	for _, p := range c.Properties {
		if p.Name == prop {
			return true
		}
	}
	return false
}

// measure counts a document's gaps: the artifact and every component, nested ones included.
func measure(data []byte) (coverage, error) {
	var doc struct {
		Metadata struct {
			Component *gateComponent `json:"component"`
		} `json:"metadata"`
		Components []gateComponent `json:"components"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return coverage{}, err
	}
	var cov coverage
	var walk func([]gateComponent)
	walk = func(cs []gateComponent) {
		for _, c := range cs {
			if !c.hasLicence() {
				cov.NoLicence++
			}
			if !c.hasCreator() {
				cov.NoCreator++
			}
			if c.Version == "" {
				cov.NoVersion++
			}
			if len(c.Hashes) > 0 && !hasAlg(c.Hashes, "SHA-512") {
				cov.NoSHA512++
			}
			if c.has("msis:msi.fileKey") && !c.has("bsi:component:filename") {
				cov.NoFilename++
			}
			walk(c.Components)
		}
	}
	// The artifact itself (metadata.component) is measured too: its SHA-512 is D10's, and
	// nothing else in the document would show it missing.
	if doc.Metadata.Component != nil {
		walk([]gateComponent{*doc.Metadata.Component})
	}
	walk(doc.Components)
	return cov, nil
}

func hasAlg(hs []hash, alg string) bool {
	for _, h := range hs {
		if h.Alg == alg {
			return true
		}
	}
	return false
}

// compare is the gate's verdict on one document: failures stop the release, notes ask for the
// baseline to follow an improvement.
func compare(got coverage, score float64, want baseline) (failures, notes []string) {
	check := func(field string, got, max int) {
		switch {
		case got > max:
			failures = append(failures, fmt.Sprintf("%s: %d, the baseline allows %d", field, got, max))
		case got < max:
			notes = append(notes, fmt.Sprintf("%s improved to %d (baseline %d): lower the baseline", field, got, max))
		}
	}
	check("components without a licence", got.NoLicence, want.NoLicence)
	check("components without a creator", got.NoCreator, want.NoCreator)
	check("components without a version", got.NoVersion, want.NoVersion)
	check("hashed components without SHA-512", got.NoSHA512, want.NoSHA512)
	check("payload files without bsi:component:filename", got.NoFilename, want.NoFilename)
	if score < want.BSI {
		failures = append(failures, fmt.Sprintf("sbomqs BSI v2.1 score %.2f, the baseline requires %.2f", score, want.BSI))
	}
	return failures, notes
}

// bsiScore runs the pinned sbomqs on one document.
func bsiScore(path string) (float64, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "run", sbomqsModule, "compliance", "--bsi-v21", "-j", path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("sbomqs on %s: %v\n%s", path, err, stderr.String())
	}
	var r struct {
		Summary *struct {
			Total float64 `json:"total_score"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil || r.Summary == nil {
		return 0, fmt.Errorf("sbomqs on %s: no score in its output (%v)", path, err)
	}
	return r.Summary.Total, nil
}

// gate checks every release document present in dist; at least one must be.
func gate(version, dist string) error {
	keys := make([]string, 0, len(gateBaselines))
	for k := range gateBaselines {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var failures []string
	checked := 0
	for _, k := range keys {
		artifact := filepath.Join(dist, "msis-"+version+"-"+k)
		if _, err := os.Stat(artifact); err != nil {
			continue
		}
		checked++
		doc := artifact + ".cdx.json"
		data, err := os.ReadFile(doc)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: the artifact has no SBOM: %v", k, err))
			continue
		}
		got, err := measure(data)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", doc, err))
			continue
		}
		score, err := bsiScore(doc)
		if err != nil {
			return err
		}
		f, notes := compare(got, score, gateBaselines[k])
		fmt.Printf("%s: BSI v2.1 %.2f; %+v\n", k, score, got)
		for _, n := range notes {
			fmt.Printf("  note: %s\n", n)
		}
		for _, x := range f {
			failures = append(failures, k+": "+x)
		}
	}
	if checked == 0 {
		return fmt.Errorf("no release artifact msis-%s-* in %s", version, dist)
	}
	if len(failures) > 0 {
		return fmt.Errorf("SBOM coverage fell below the last release's:\n  %s", strings.Join(failures, "\n  "))
	}
	return nil
}
