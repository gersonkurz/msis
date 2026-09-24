package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/scan"
)

// scanArgsValid settles what /SCAN is asked to scan (#69). With /SBOM it scans the documents
// that run writes; on its own it scans the CycloneDX documents named, and nothing else - there
// is no document to scan in a build that writes none, and an installer is not a document.
func scanArgsValid(args *cliArgs) error {
	if !args.scan || args.sbom {
		return nil
	}
	if args.build {
		return fmt.Errorf("/SCAN scans the SBOM a build writes; add /SBOM")
	}
	for _, f := range args.files {
		switch {
		case strings.HasSuffix(strings.ToLower(f), ".vex.cdx.json"):
			return fmt.Errorf("%s is a VEX document, not an inventory; /SCAN reads the SBOM beside it and applies it", f)
		case !strings.HasSuffix(strings.ToLower(f), ".cdx.json"):
			return fmt.Errorf("/SCAN on its own scans a CycloneDX document (.cdx.json); %s is not one"+
				"\n  hint: to describe and scan an installer, use /SBOM /SCAN", f)
		}
	}
	return nil
}

// scanDocuments runs the scan over docs and prints what it found and what it could not cover. A
// BOM-Link to another document scanned in the same run counts as covered; any other is named,
// because grype does not follow links and a bundle's document would otherwise read as clean.
//
// Every document is scanned before anything is printed, so a link counts as covered only once
// the document it names was scanned successfully - whichever order the documents come in, and
// never when that document's own scan failed. A failed scan does not stop the others; the first
// failure is returned once all are reported.
func scanDocuments(docs []string) error {
	results := make([]scanResult, 0, len(docs))
	for _, doc := range docs {
		results = append(results, scanOne(doc))
	}
	covered := coveredLinks(results)
	var first error
	for _, res := range results {
		if res.err != nil {
			fmt.Printf("  %s %s: %v\n", cli.Error("Not scanned:"), cli.Filename(res.doc), res.err)
			if first == nil {
				first = fmt.Errorf("%s: %w", res.doc, res.err)
			}
			continue
		}
		printScan(res, covered)
	}
	return first
}

// scanResult is one document's scan, or why it has none.
type scanResult struct {
	doc            string
	link           string // the BOM-Link naming this document
	report         scan.Report
	out, preserved string
	err            error
}

func scanOne(doc string) scanResult {
	res := scanResult{doc: doc}
	data, err := os.ReadFile(doc)
	if err != nil {
		res.err = err
		return res
	}
	raw, err := scan.Grype(doc)
	if err != nil {
		res.err = err
		return res
	}
	vexDoc, err := os.ReadFile(sbom.VEXSidecarPath(scan.Artifact(doc)))
	if err != nil {
		vexDoc = nil // none beside it; Analyze says so
	}
	if res.report, err = scan.Analyze(data, raw, vexDoc); err != nil {
		res.err = err
		return res
	}
	if res.out, res.preserved, err = scan.Write(doc, raw); err != nil {
		res.err = err
		return res
	}
	res.link, _ = documentLink(doc)
	return res
}

// coveredLinks is the BOM-Links of the documents that were scanned successfully.
func coveredLinks(results []scanResult) map[string]bool {
	covered := map[string]bool{}
	for _, res := range results {
		if res.err == nil && res.link != "" {
			covered[res.link] = true
		}
	}
	return covered
}

func printScan(res scanResult, scanned map[string]bool) {
	r, out, preserved := res.report, res.out, res.preserved
	open := 0
	for _, f := range r.Findings {
		if f.SuppressedBy == "" {
			open++
		}
	}
	fmt.Printf("  %s %s (%s, %s finding(s), %s answered by VEX)\n", cli.Success("Scan:"), cli.Filename(out),
		scan.Scanner, cli.Number(fmt.Sprintf("%d", open)), cli.Number(fmt.Sprintf("%d", len(r.Findings)-open)))
	if preserved != "" {
		fmt.Printf("  %s\n", cli.Info("Kept the previous report as "+preserved))
	}
	for _, f := range r.Findings {
		id := f.ID
		if len(f.Aliases) > 0 {
			id += " (" + strings.Join(f.Aliases, ", ") + ")"
		}
		line := fmt.Sprintf("%-8s %s  %s %s", f.Severity, id, f.Name, f.Version)
		if len(f.FixedIn) > 0 {
			line += "  fixed in " + strings.Join(f.FixedIn, ", ")
		}
		if f.SuppressedBy != "" {
			fmt.Printf("    %s\n      %s\n", line, cli.Info("answered by VEX: "+f.SuppressedBy))
		} else {
			fmt.Printf("    %s\n", cli.Warning(line))
		}
	}
	// What the result does not cover is said every time: "no findings" over components no
	// scanner can match is not a clean result.
	if r.Unidentified > 0 {
		fmt.Printf("  %s\n", cli.Warning(fmt.Sprintf(
			"Not scanned: %d of %d components carry neither a purl nor a CPE, so no scanner can match them",
			r.Unidentified, r.Components)))
	}
	for _, link := range unfollowed(r.Links, scanned) {
		fmt.Printf("  %s\n", cli.Warning("Not followed: "+link+
			" - grype does not follow BOM-Links; scan that document too"))
	}
	fmt.Printf("  %s\n", cli.Info("VEX: "+r.VEX))
}

// documentLink is the BOM-Link addressing doc itself: urn:cdx:<serial>/<version>.
func documentLink(doc string) (string, error) {
	data, err := os.ReadFile(doc)
	if err != nil {
		return "", err
	}
	var d struct {
		SerialNumber string `json:"serialNumber"`
		Version      int    `json:"version"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.SerialNumber == "" {
		return "", fmt.Errorf("%s names no serial number", doc)
	}
	return fmt.Sprintf("urn:cdx:%s/%d", strings.TrimPrefix(d.SerialNumber, "urn:uuid:"), d.Version), nil
}

// unfollowed is the links whose document was not scanned in the same run. A link's #fragment
// addresses a component within the document, so it is the document part that must match.
func unfollowed(links []string, scanned map[string]bool) []string {
	var out []string
	for _, link := range links {
		if doc, _, _ := strings.Cut(link, "#"); !scanned[doc] {
			out = append(out, link)
		}
	}
	return out
}
