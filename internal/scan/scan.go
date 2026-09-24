// Package scan runs a vulnerability scanner over an SBOM msis wrote, and says what the result
// does and does not cover (#69).
//
// msis scans nothing itself. It runs grype - found on PATH, never downloaded - on the document,
// keeps grype's JSON verbatim beside it, and adds the three things a raw scan leaves out:
//
//   - What could not be scanned. A scanner matches on package identifiers (purl, CPE), and a
//     payload file msis could not identify has none (decisions D4). "No vulnerabilities found"
//     over components nobody could match is not a clean result, so the count is always stated.
//   - What the document only links to. A bundle's document names its installers' documents by
//     BOM-Link rather than repeating them, and grype does not follow links.
//   - Which findings the product's VEX statements answer. Only a statement msis evaluated
//     against THIS document (#37) and that still holds suppresses anything; one whose
//     conditions lapsed was already moved to in_triage by that evaluation, and suppresses
//     nothing. grype cannot read CycloneDX VEX itself (0.119: "unable to detect document
//     format"), and it could not know which statements still hold if it did.
package scan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/vex"
)

// Scanner is the one tool /SCAN drives; the product owner's decision for #69 was grype first.
const Scanner = "grype"

// ErrNoScanner is returned when grype is not on PATH.
var ErrNoScanner = errors.New("grype is not on PATH; /SCAN runs it but never installs it - " +
	"install it from https://github.com/anchore/grype, then run the scan again")

// Grype runs grype on one CycloneDX document and returns its JSON report, verbatim. It runs from
// the document's directory with a relative name, so the report does not record where the
// document sits on this machine (it still names grype's own database location, which grype
// always does).
func Grype(doc string) ([]byte, error) {
	exe, err := exec.LookPath(Scanner)
	if err != nil {
		return nil, ErrNoScanner
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe, "sbom:"+filepath.Base(doc), "-o", "json")
	cmd.Dir = filepath.Dir(doc)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	// grype exits 2 when a --fail-on threshold is met - set by the user's grype configuration
	// or GRYPE_FAIL_ON_SEVERITY, which grype reads whatever msis passes. That is grype reporting
	// findings, not failing, and D19 says a finding never fails a run: the report it wrote is
	// kept as for any other scan. Only a report that is really there counts.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 && isReport(stdout.Bytes()) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("grype on %s: %v\n%s", filepath.Base(doc), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// isReport reports whether data is a grype JSON report: an object with a matches list.
func isReport(data []byte) bool {
	var r struct {
		Matches *[]json.RawMessage `json:"matches"`
	}
	return json.Unmarshal(data, &r) == nil && r.Matches != nil
}

// Finding is one grype match, joined to the component it is about.
type Finding struct {
	ID        string   // grype's id, e.g. GO-2026-5970
	Aliases   []string // related ids, e.g. the CVE
	Severity  string
	Component string // the SBOM bom-ref: grype reports it as the artifact id
	Name      string
	Version   string
	FixedIn   []string
	// SuppressedBy is the VEX statement that answers this finding, e.g.
	// "CVE-2026-56852: not_affected (code_not_reachable)"; empty when none does.
	SuppressedBy string
}

// Report is what one scan found and covered.
type Report struct {
	Findings     []Finding
	Components   int      // components in the document, the subject and nested ones included
	Unidentified int      // of those, how many carry neither a purl nor a CPE
	Links        []string // BOM-Links the document makes, which the scan did not follow
	VEX          string   // the VEX document applied, or why none was
}

type grypeReport struct {
	Matches []struct {
		Vulnerability struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Fix      struct {
				Versions []string `json:"versions"`
			} `json:"fix"`
		} `json:"vulnerability"`
		RelatedVulnerabilities []struct {
			ID string `json:"id"`
		} `json:"relatedVulnerabilities"`
		Artifact struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"artifact"`
	} `json:"matches"`
	Descriptor struct {
		Timestamp string `json:"timestamp"`
	} `json:"descriptor"`
}

type docComponent struct {
	BOMRef             string `json:"bom-ref"`
	PURL               string `json:"purl"`
	CPE                string `json:"cpe"`
	ExternalReferences []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"externalReferences"`
	Components []docComponent `json:"components"`
}

type document struct {
	SerialNumber string `json:"serialNumber"`
	Version      int    `json:"version"`
	Metadata     struct {
		Component docComponent `json:"component"`
	} `json:"metadata"`
	Components      []docComponent   `json:"components"`
	Vulnerabilities []map[string]any `json:"vulnerabilities"`
}

// Analyze joins grype's report to the document it scanned and to the document's VEX sidecar, if
// there is one (vexDoc may be nil). It reads; it runs nothing.
func Analyze(doc, grype, vexDoc []byte) (Report, error) {
	var d document
	if err := json.Unmarshal(doc, &d); err != nil {
		return Report{}, fmt.Errorf("reading the scanned document: %w", err)
	}
	var g grypeReport
	if err := json.Unmarshal(grype, &g); err != nil {
		return Report{}, fmt.Errorf("reading grype's report: %w", err)
	}

	var r Report
	var walk func(c docComponent)
	walk = func(c docComponent) {
		r.Components++
		if c.PURL == "" && c.CPE == "" {
			r.Unidentified++
		}
		for _, e := range c.ExternalReferences {
			if e.Type == "bom" {
				r.Links = append(r.Links, e.URL)
			}
		}
		for _, n := range c.Components {
			walk(n)
		}
	}
	walk(d.Metadata.Component)
	for _, c := range d.Components {
		walk(c)
	}
	sort.Strings(r.Links)

	answers, why := vexAnswers(d, vexDoc)
	r.VEX = why

	for _, m := range g.Matches {
		f := Finding{
			ID: m.Vulnerability.ID, Severity: m.Vulnerability.Severity,
			Component: m.Artifact.ID, Name: m.Artifact.Name, Version: m.Artifact.Version,
			FixedIn: m.Vulnerability.Fix.Versions,
		}
		for _, rel := range m.RelatedVulnerabilities {
			if rel.ID != "" && rel.ID != f.ID {
				f.Aliases = append(f.Aliases, rel.ID)
			}
		}
		for _, id := range append([]string{f.ID}, f.Aliases...) {
			if a, ok := answers[id+"\x00"+f.Component]; ok {
				f.SuppressedBy = a
				break
			}
		}
		r.Findings = append(r.Findings, f)
	}
	sort.Slice(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Component < b.Component
	})
	return r, nil
}

// vexAnswers is every (vulnerability id, component) a VEX sidecar's statements still answer,
// keyed "id\x00bom-ref". The sidecar counts only if it was evaluated against this very
// document - its subject's BOM-Link names this serial and version - and a statement counts only
// in a state that says there is nothing to act on. msis's evaluation already moved every
// statement whose conditions lapsed out of those states.
func vexAnswers(d document, vexDoc []byte) (map[string]string, string) {
	out := map[string]string{}
	if vexDoc == nil {
		return out, "no VEX document beside it"
	}
	var v document
	if err := json.Unmarshal(vexDoc, &v); err != nil {
		return out, fmt.Sprintf("the VEX document beside it is not readable (%v), so it answers nothing", err)
	}
	want := fmt.Sprintf("urn:cdx:%s/%d", strings.TrimPrefix(d.SerialNumber, "urn:uuid:"), d.Version)
	evaluated := false
	for _, e := range v.Metadata.Component.ExternalReferences {
		if e.Type == "bom" && e.URL == want {
			evaluated = true
		}
	}
	if !evaluated {
		return out, "the VEX document beside it was evaluated against another document, so it answers nothing here"
	}
	for _, s := range v.Vulnerabilities {
		id, _ := s["id"].(string)
		analysis, _ := s["analysis"].(map[string]any)
		state, _ := analysis["state"].(string)
		if id == "" || !vex.Suppresses(state) {
			continue
		}
		answer := id + ": " + state
		if j, _ := analysis["justification"].(string); j != "" {
			answer += " (" + j + ")"
		}
		affects, _ := s["affects"].([]any)
		for _, a := range affects {
			m, _ := a.(map[string]any)
			if ref, _ := m["ref"].(string); ref != "" {
				out[id+"\x00"+ref] = answer
			}
		}
	}
	return out, "applied: statements evaluated against this document"
}

// ReportPath is where the scan of doc is kept: beside it, as <artifact>.grype.json for
// <artifact>.cdx.json.
func ReportPath(doc string) string {
	return Artifact(doc) + ".grype.json"
}

// Artifact is the artifact a document describes: doc without its .cdx.json suffix, matched in
// any case - Windows accepts app.msi.CDX.JSON for app.msi.cdx.json, and so does /SCAN.
func Artifact(doc string) string {
	const suffix = ".cdx.json"
	if len(doc) >= len(suffix) && strings.EqualFold(doc[len(doc)-len(suffix):], suffix) {
		return doc[:len(doc)-len(suffix)]
	}
	return doc
}

var unsafeStamp = regexp.MustCompile(`[^0-9A-Za-z-]`)

// Write keeps raw at ReportPath(doc). A report already there is not overwritten: it is kept as
// <artifact>.<its own timestamp>.grype.json, as an SBOM is kept by its serial. A scan is a
// finding at a point in time - the vulnerability database changes daily - so an earlier one is
// evidence, not clutter. Every uncertainty refuses, as for documents: an unreadable report, one
// without a timestamp, or an archive name already taken, and nothing is replaced.
func Write(doc string, raw []byte) (out, preserved string, err error) {
	out = ReportPath(doc)
	if old, err := os.ReadFile(out); err == nil {
		var g grypeReport
		if err := json.Unmarshal(old, &g); err != nil || g.Descriptor.Timestamp == "" {
			return "", "", fmt.Errorf("%s exists and is not a grype report msis can date, so it is not replaced", out)
		}
		stamp := unsafeStamp.ReplaceAllString(g.Descriptor.Timestamp, "-")
		preserved = strings.TrimSuffix(out, ".grype.json") + "." + stamp + ".grype.json"
		if _, err := os.Stat(preserved); err == nil {
			return "", "", fmt.Errorf("%s exists, and so does %s, where it would be kept; nothing is replaced", out, preserved)
		}
		if err := os.Rename(out, preserved); err != nil {
			return "", "", fmt.Errorf("keeping the previous report: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("reading %s: %w", out, err)
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return "", "", err
	}
	return out, preserved, nil
}
