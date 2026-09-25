package main

// The last thing a release prints (#71): what it produced, what to upload, and what the scan
// covered. It runs after every gate - `just` stops at the first failing one - so reaching it
// means the release passed; the scan is a report, never a gate (D19), so whether it ran is
// stated here rather than left to scroll by.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/scan"
)

// uploadFile is one release asset: the product owner's decision for 3.0.6 is the installers,
// each installer's SBOM and the release SBOM. components\ and build-manifest.json are inputs to
// the build and its seal, and the scan reports name local paths.
type uploadFile struct {
	name string
	size int64
	what string
}

// releaseSummary is the lines the release ends with, and an error when an installer has no SBOM.
func releaseSummary(version, dist, scanDir string) ([]string, error) {
	var installers []string
	for k := range gateBaselines {
		name := "msis-" + version + "-" + k
		if _, err := os.Stat(filepath.Join(dist, name)); err == nil {
			installers = append(installers, name)
		}
	}
	sort.Strings(installers)
	if len(installers) == 0 {
		return nil, fmt.Errorf("no release installer msis-%s-* in %s", version, dist)
	}

	var files []uploadFile
	var docs []string
	add := func(name, what string) error {
		info, err := os.Stat(filepath.Join(dist, name))
		if err != nil {
			return err
		}
		files = append(files, uploadFile{name, info.Size(), what})
		return nil
	}
	for _, name := range installers {
		if err := add(name, "installer"); err != nil {
			return nil, err
		}
		doc := filepath.Base(sbom.SidecarPath(name))
		if err := add(doc, "its SBOM"); err != nil {
			return nil, fmt.Errorf("%s has no SBOM beside it; the release is incomplete", name)
		}
		docs = append(docs, doc)
	}
	if release := "msis-" + version + ".cdx.json"; add(release, "the release SBOM") == nil {
		docs = append(docs, release)
	}

	lines := []string{"", fmt.Sprintf("=== Release %s: done ===", version),
		fmt.Sprintf("Upload these %d files from %s:", len(files), dist)}
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("  %-36s %10s  %s", f.name, sizeOf(f.size), f.what))
	}
	lines = append(lines,
		"Not for upload: components\\ and build-manifest.json (the build's inputs and seal), and the scan reports.",
		"SBOM coverage: at or above the baseline (the gate passed; this summary runs only after it).")
	return append(lines, scanSummary(dist, scanDir, docs)...), nil
}

// scanSummary says, per SBOM, whether this release scanned it: a report counts only if it is
// at least as new as the document, so a report left over from an earlier run is not taken for
// this one's.
func scanSummary(dist, scanDir string, docs []string) []string {
	var scanned, open, answered, components, unidentified int
	var missing, links []string
	covered := map[string]bool{} // the BOM-Links of the documents this release scanned
	for _, doc := range docs {
		path := filepath.Join(dist, doc)
		report := scan.ReportPath(path, scanDir)
		di, derr := os.Stat(path)
		ri, rerr := os.Stat(report)
		if derr != nil || rerr != nil || ri.ModTime().Before(di.ModTime()) {
			missing = append(missing, doc)
			continue
		}
		data, _ := os.ReadFile(path)
		raw, _ := os.ReadFile(report)
		vexDoc, _ := os.ReadFile(sbom.VEXSidecarPath(scan.Artifact(path)))
		r, err := scan.Analyze(data, raw, vexDoc)
		if err != nil {
			missing = append(missing, doc+" (its report is unreadable)")
			continue
		}
		scanned++
		components += r.Components
		unidentified += r.Unidentified
		links = append(links, r.Links...)
		if link := documentLink(data); link != "" {
			covered[link] = true
		}
		for _, f := range r.Findings {
			if f.SuppressedBy == "" {
				open++
			} else {
				answered++
			}
		}
	}
	if scanned == 0 {
		return []string{"Scan: NOT SCANNED - no report from this release in " + scanDir +
			" (grype missing, or the scan did not complete; see above). A report, never a gate."}
	}
	out := []string{fmt.Sprintf("Scan: %d of %d SBOMs scanned: %d open finding(s), %d answered by VEX; reports in %s",
		scanned, len(docs), open, answered, scanDir)}
	// What that result does not cover (D19), here as well as in the scan's own output: a count of
	// findings over components no scanner could match is not a clean bill.
	if unidentified > 0 {
		out = append(out, fmt.Sprintf("  not covered: %d of %d components carry neither a purl nor a CPE, so no scanner can match them",
			unidentified, components))
	}
	sort.Strings(links)
	for _, link := range links {
		if doc, _, _ := strings.Cut(link, "#"); !covered[doc] {
			out = append(out, "  not covered: "+link+" - a BOM-Link to a document this release did not scan")
		}
	}
	if len(missing) > 0 {
		out = append(out, "  not scanned: "+strings.Join(missing, ", "))
	}
	return out
}

// documentLink is the BOM-Link that addresses a document: urn:cdx:<serial>/<version>.
func documentLink(data []byte) string {
	var d struct {
		SerialNumber string `json:"serialNumber"`
		Version      int    `json:"version"`
	}
	if json.Unmarshal(data, &d) != nil || d.SerialNumber == "" {
		return ""
	}
	return fmt.Sprintf("urn:cdx:%s/%d", strings.TrimPrefix(d.SerialNumber, "urn:uuid:"), d.Version)
}

func sizeOf(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
