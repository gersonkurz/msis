package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/analyze"
	"github.com/gersonkurz/msis/internal/burnread"
	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom"
)

// runSBOM writes a CycloneDX bill of materials for a built installer.
//
// It reads the artifact, not the .msis that produced it. The script says what was asked for; the
// package says what shipped, and for a release that is months old the package is the only thing
// that still exists.
func runSBOM(path string, analyzePayload bool) error {
	if strings.EqualFold(pathExt(path), ".exe") {
		if analyzePayload {
			printAnalyzeBundle()
		}
		return runBundleSBOM(path)
	}
	opts := sbom.Options{MsisVersion: Version}
	var pkg *msiread.Package
	var err error
	if analyzePayload {
		// One read serves both: the package, and the payload syft is run over.
		pkg, opts.Analyzed, err = analyze.Syft(path)
		opts.AnalyzedCounts = &sbom.AnalyzedCounts{}
	} else {
		pkg, err = msiread.Read(path)
	}
	if err != nil {
		return err
	}
	doc, err := sbom.FromPackage(pkg, opts)
	if err != nil {
		return err
	}

	out, preserved, err := sbom.Write(path, doc)
	if err != nil {
		return err
	}

	fmt.Printf("  %s %s\n", cli.Success("Wrote:"), cli.Filename(out))
	fmt.Printf("  %s components, serial %s\n",
		cli.Number(fmt.Sprintf("%d", doc.ComponentCount())), doc.SerialNumber)
	if preserved != "" {
		// A document already referenced by a bundle's SBOM cannot simply be replaced: the
		// link addresses its serial number, and reissuing the parent does not repair one a
		// customer already holds.
		fmt.Printf("  %s\n", cli.Info("Kept the previous document as "+preserved))
	}
	warnNTIAUnknown(doc)
	printAnalyzed(opts.Analyzed, opts.AnalyzedCounts)

	// Say where the inventory stops, in the terminal as well as in the document.
	for _, m := range pkg.Media {
		if m.Unavailable != "" {
			fmt.Printf("  %s\n", cli.Warning("Warning: "+m.Unavailable+
				"; files it carries appear without a digest"))
		}
	}
	return nil
}

// printBOMLinks prints one line per chained package saying whether its own document was linked.
// A link that was NOT made is the thing a reader needs to act on, so it is not left to the JSON.
// Both `/SBOM` on a bundle and `/BUILD /SBOM` print it; a document without chain packages - an
// MSI's - prints nothing.
func printBOMLinks(doc *sbom.Document) {
	for _, c := range doc.Components {
		linked, why := sbom.BOMLink(c)
		switch {
		case linked != "":
			fmt.Printf("  %s %s -> %s\n", cli.Success("Linked:"), sbom.PackageLabel(c), linked)
		case why != "":
			fmt.Printf("  %s %s: %s\n", cli.Info("No link:"), sbom.PackageLabel(c), why)
		}
	}
}

// warnNTIAUnknown says in the terminal which NTIA minimum element the artifact does not record,
// as the document states it (#59) - a gap a reader should hear about, not only find in the JSON.
func warnNTIAUnknown(doc *sbom.Document) {
	for _, u := range doc.NTIAUnknowns() {
		fmt.Printf("  %s\n", cli.Warning("Warning: NTIA "+u+"; the SBOM marks it unknown"))
	}
}

// sbomablePath rejects what /SBOM cannot read, so pointing it at the script names the mistake
// instead of surfacing an installer error code.
func sbomablePath(path string) error {
	ext := pathExt(path)
	if strings.EqualFold(ext, ".msi") || strings.EqualFold(ext, ".exe") {
		return nil
	}
	return fmt.Errorf("/SBOM reads a built .msi or a Burn bundle .exe; %s is neither"+
		"\n  hint: build it first (msis /BUILD ...), then point /SBOM at what it produced", path)
}

// runBundleSBOM writes the document for a Burn bundle.
//
// A bundle inventories installers rather than files, and each chained installer is described by
// its OWN document rather than expanded here - so the order matters: run /SBOM over the chained
// .msi files first and the bundle's document will link to them. Every link is checked against
// the bytes the bundle actually carries, so a stale child document produces no link rather than
// a wrong one, and the terminal says which packages were linked and which were not.
func runBundleSBOM(path string) error {
	b, err := burnread.Read(path)
	if err != nil {
		return err
	}

	doc, err := sbom.FromBundle(b, sbom.Options{MsisVersion: Version})
	if err != nil {
		return err
	}

	out, preserved, err := sbom.Write(path, doc)
	if err != nil {
		return err
	}

	fmt.Printf("  %s %s\n", cli.Success("Wrote:"), cli.Filename(out))
	fmt.Printf("  %s components, serial %s\n",
		cli.Number(fmt.Sprintf("%d", doc.ComponentCount())), doc.SerialNumber)
	if preserved != "" {
		fmt.Printf("  %s\n", cli.Info("Kept the previous document as "+preserved))
	}
	warnNTIAUnknown(doc)
	printBOMLinks(doc)

	// Every payload, not just a chain package's: a bundle-level payload the engine expects
	// beside the installer has no digest either, and the terminal is where someone notices.
	for _, pay := range b.AllPayloads() {
		if pay.Unavailable != "" {
			fmt.Printf("  %s\n", cli.Warning("Warning: "+pay.Unavailable))
		}
	}
	return nil
}

// printAnalyzed says in the terminal what /ANALYZE added and what it left out (#82, D25); the
// document says the same in its metadata.
func printAnalyzed(a *sbom.Analyzed, c *sbom.AnalyzedCounts) {
	if a == nil || c == nil {
		return
	}
	fmt.Printf("  %s %s %s identified %s package(s) in %s payload file(s) from their own declarations\n",
		cli.Success("Analyzed:"), a.Tool.Name, a.Tool.Version, cli.Number(fmt.Sprintf("%d", c.Imported)),
		cli.Number(fmt.Sprintf("%d", c.Files)))
	if c.Dropped > 0 {
		fmt.Printf("  %s\n", cli.Info(fmt.Sprintf("%d more are in files a supplied SBOM describes; it is kept, "+
			"the analyzer's packages for those files are not", c.Dropped)))
	}
	var left []string
	for _, reason := range sortedKeys(a.Skipped) {
		left = append(left, fmt.Sprintf("%d %s", a.Skipped[reason], reason))
	}
	if len(left) > 0 {
		fmt.Printf("  %s\n", cli.Info("Not imported, as no package declares that identity (D25): "+strings.Join(left, ", ")))
	}
}

// printAnalyzeBundle explains why /ANALYZE does nothing for a bundle.
func printAnalyzeBundle() {
	fmt.Printf("  %s\n", cli.Info("/ANALYZE reads installed files; a bundle carries installers - run /SBOM /ANALYZE "+
		"on its chained .msi files, and the bundle's document links to theirs"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
