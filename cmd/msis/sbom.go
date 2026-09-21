package main

import (
	"fmt"
	"os"
	"strings"

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
func runSBOM(path string) error {
	if strings.EqualFold(pathExt(path), ".exe") {
		return runBundleSBOM(path)
	}
	pkg, err := msiread.Read(path)
	if err != nil {
		return err
	}

	self, _ := os.Executable()
	doc, err := sbom.FromPackage(pkg, sbom.Options{
		MsisVersion: Version,
		MsisPath:    self,
	})
	if err != nil {
		return err
	}

	out, preserved, err := sbom.Write(path, doc)
	if err != nil {
		return err
	}

	fmt.Printf("  %s %s\n", cli.Success("Wrote:"), cli.Filename(out))
	fmt.Printf("  %s components, serial %s\n",
		cli.Number(fmt.Sprintf("%d", len(doc.Components))), doc.SerialNumber)
	if preserved != "" {
		// A document already referenced by a bundle's SBOM cannot simply be replaced: the
		// link addresses its serial number, and reissuing the parent does not repair one a
		// customer already holds.
		fmt.Printf("  %s\n", cli.Info("Kept the previous document as "+preserved))
	}

	// Say where the inventory stops, in the terminal as well as in the document.
	for _, m := range pkg.Media {
		if m.Unavailable != "" {
			fmt.Printf("  %s\n", cli.Warning("Warning: "+m.Unavailable+
				"; files it carries appear without a digest"))
		}
	}
	return nil
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

	self, _ := os.Executable()
	doc, err := sbom.FromBundle(b, sbom.Options{MsisVersion: Version, MsisPath: self})
	if err != nil {
		return err
	}

	out, preserved, err := sbom.Write(path, doc)
	if err != nil {
		return err
	}

	fmt.Printf("  %s %s\n", cli.Success("Wrote:"), cli.Filename(out))
	fmt.Printf("  %s components, serial %s\n",
		cli.Number(fmt.Sprintf("%d", len(doc.Components))), doc.SerialNumber)
	if preserved != "" {
		fmt.Printf("  %s\n", cli.Info("Kept the previous document as "+preserved))
	}

	// One line per chained package saying whether its own document was linked. A link that
	// was NOT made is the thing a reader needs to act on, so it is not left to the JSON.
	for _, c := range doc.Components {
		linked, why := sbom.BOMLink(c)
		switch {
		case linked != "":
			fmt.Printf("  %s %s -> %s\n", cli.Success("Linked:"), c.Name, linked)
		case why != "":
			fmt.Printf("  %s %s: %s\n", cli.Info("No link:"), c.Name, why)
		}
	}

	// Every payload, not just a chain package's: a bundle-level payload the engine expects
	// beside the installer has no digest either, and the terminal is where someone notices.
	for _, pay := range b.AllPayloads() {
		if pay.Unavailable != "" {
			fmt.Printf("  %s\n", cli.Warning("Warning: "+pay.Unavailable))
		}
	}
	return nil
}
