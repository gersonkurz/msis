package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/vex"
)

// The VEX sidecar (#37): `<vex source="app.vex.json"/>`.
//
// The file is the team's own, kept in their repository beside the .msis and maintained across
// releases - which is the point. msis reads it at build time, checks each statement's recorded
// conditions against the release being built, and writes the evaluated result beside the SBOM.
//
// Resolved against the .msis, not through WiX's bind paths: like a supplied component SBOM, this
// file is read by msis and never packaged, so WiX never resolves it and its search order does
// not apply.

func resolveVEX(source, script string) (*vex.Source, error) {
	if source == "" {
		return nil, nil
	}
	path := source
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(script), source)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("<vex source=%q>: %w", source, err)
	}
	return &vex.Source{Path: filepath.ToSlash(source), Data: data}, nil
}

// emitVEX evaluates the statements against the document just written and puts the result beside
// it. The SBOM is not touched: a VEX document annotates an inventory, and changing the inventory
// because someone assessed it would make the two disagree about what was shipped.
func emitVEX(artifact string, bom *sbom.Document, src *vex.Source) error {
	if src == nil {
		return nil
	}
	self, _ := os.Executable()
	doc, err := vex.Apply(bom, *src, vex.Options{MsisVersion: Version, MsisPath: self})
	if err != nil {
		return fmt.Errorf("evaluating %s: %w", src.Path, err)
	}

	// Validated before it is written, against the same vendored schema every other document
	// answers to: a VEX document IS a CycloneDX BOM, and one that does not validate is one
	// nothing downstream will read.
	data, err := sbom.Marshal(doc)
	if err != nil {
		return err
	}
	if err := vex.Validate(data); err != nil {
		return fmt.Errorf("the VEX document msis produced from %s is not valid CycloneDX: %w",
			src.Path, err)
	}

	out, preserved, err := sbom.Write(sbom.VEXStem(artifact), doc)
	if err != nil {
		return err
	}

	applies, review := 0, 0
	for _, v := range doc.Vulnerabilities {
		if v.Property(sbom.PropApplicability) == sbom.ApplicabilityApplies {
			applies++
		} else {
			review++
		}
	}
	fmt.Printf("  %s %s (%s statement(s) apply, %s need review)\n",
		cli.Success("VEX:"), cli.Filename(out),
		cli.Number(fmt.Sprintf("%d", applies)), cli.Number(fmt.Sprintf("%d", review)))
	if review > 0 {
		fmt.Printf("  %s\n", cli.Warning(fmt.Sprintf(
			"Warning: %d assessment(s) no longer hold for this release and no longer "+
				"suppress a finding; see %s in the document", review, sbom.PropReviewReason)))
	}
	if preserved != "" {
		fmt.Printf("  %s\n", cli.Info("Kept the previous VEX document as "+preserved))
	}
	return nil
}
