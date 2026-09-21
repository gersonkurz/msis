package main

import (
	"fmt"
	"os"
	"strings"

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
	if strings.EqualFold(pathExt(path), ".msi") {
		return nil
	}
	return fmt.Errorf("/SBOM reads a built .msi; %s is not one"+
		"\n  hint: build it first (msis /BUILD ...), then point /SBOM at the .msi it produced"+
		"\n  note: bundles (.exe) are not supported yet - see issue #33", path)
}
