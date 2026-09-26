package sbom

import (
	"fmt"
	"sort"
	"strings"
)

// Composition of an analyzer's findings into the installer's document (#82, D25).
//
// msis reads each payload file's bytes and cannot say what package a file belongs to. An
// analyzer run over the extracted payload can, where the package declares itself: a Python
// distribution's dist-info METADATA, a jar's Maven pom.properties, a .NET application's
// .deps.json. Those are the package's own statements, read by another tool, and msis carries
// them as such - never an identification the analyzer inferred from a PE version resource or a
// file name, which is the guessing D4 forbids.
//
// Each package becomes a component the file it was found in depends on, marked with the
// analyzer and the declaration it rests on. A file a supplied SBOM already describes (#36) keeps
// that description: the supplier's document is authoritative, and the analyzer's packages for
// that file are dropped and counted.
type Analyzed struct {
	Tool     Component         // the analyzer, for metadata.tools: name, version, digest
	Packages []AnalyzedPackage // what it identified, each joined to a payload file
	Skipped  map[string]int    // identifications not imported, by reason
}

// AnalyzedPackage is one package, identified from what it declares about itself.
type AnalyzedPackage struct {
	FileID    string // the WiX File id of the payload file the evidence is in
	Name      string
	Version   string
	PURL      string
	Cataloger string // the analyzer's name for what found it, e.g. python-installed-package-cataloger
	Basis     string // the declaration the identity rests on: one of AnalyzerBases
	Within    string // for a package nested in an archive, the path inside it
}

// AnalyzerBases are the declarations an analyzed identity may rest on (D25): a package's own
// statement about itself, never an inference.
var AnalyzerBases = []string{"dist-info METADATA", "pom.properties", ".deps.json"}

// mergeAnalyzed attaches a's packages to the files they were found in. It runs after the
// supplied documents are merged, so a file one of them describes is known and left alone.
// AnalyzedCounts is what an analyzer contributed to one document.
type AnalyzedCounts struct {
	Imported int // packages that became components
	Files    int // payload files they were found in
	Dropped  int // packages in files a supplied SBOM describes, left out
}

func mergeAnalyzed(doc *Document, a *Analyzed, supplied []Supplied) (AnalyzedCounts, error) {
	var counts AnalyzedCounts
	if a == nil {
		return counts, nil
	}
	described := map[string]bool{}
	for _, s := range supplied {
		described[s.FileID] = true
	}
	tool := a.Tool.Name + " " + a.Tool.Version

	byFile := map[string][]AnalyzedPackage{}
	for _, p := range a.Packages {
		if !validBasis(p.Basis) {
			return counts, fmt.Errorf("the analyzer identified %s %s from %q, which is not a package's own "+
				"declaration (D25)", p.Name, p.Version, p.Basis)
		}
		if described[p.FileID] {
			counts.Dropped++
			continue
		}
		byFile[p.FileID] = append(byFile[p.FileID], p)
	}

	var refs []string
	for _, fileID := range sortedKeysOf(byFile) {
		target := componentWithFileKey(doc, fileID)
		if target == nil {
			return counts, fmt.Errorf("the analyzer reports packages in file id %s, which no component of the "+
				"artifact carries", fileID)
		}
		var on []string
		seen := map[string]bool{}
		for _, p := range byFile[fileID] {
			ref := target.BOMRef + "/analyzed/" + encodeRefPart(p.PURL)
			if p.Within != "" {
				ref += "/" + encodeRefPart(p.Within)
			}
			if seen[ref] {
				continue // one declaration seen twice in one file names one package
			}
			seen[ref] = true
			props := []Property{
				{propAnalyzedBy, tool},
				{propAnalyzerFinder, p.Cataloger},
				{propAnalyzerBasis, p.Basis},
			}
			if p.Within != "" {
				props = append(props, Property{propAnalyzerWithin, p.Within})
			}
			doc.Components = append(doc.Components, Component{
				Type: "library", BOMRef: ref, Name: p.Name, Version: p.Version, PURL: p.PURL,
				Properties: props,
			})
			on = append(on, ref)
		}
		sort.Strings(on)
		// The file contains these packages. Whether it contains others is not known - the
		// analyzer claims no completeness - so the file's own graph stays unknown.
		doc.Dependencies = append(doc.Dependencies, Dependency{Ref: target.BOMRef, DependsOn: on})
		refs = append(refs, on...)
		counts.Imported += len(on)
		counts.Files++
	}
	if len(refs) > 0 {
		sort.Strings(refs)
		doc.Compositions = append(doc.Compositions,
			Composition{Aggregate: aggregateUnknown, Assemblies: refs, Dependencies: refs})
	}

	doc.Metadata.Tools.Components = append(doc.Metadata.Tools.Components, a.Tool)
	doc.Metadata.Properties = append(doc.Metadata.Properties,
		Property{propAnalyzerDocument, analyzedNote(tool, counts, a.Skipped)})
	return counts, nil
}

func validBasis(basis string) bool {
	for _, b := range AnalyzerBases {
		if basis == b {
			return true
		}
	}
	return false
}

// analyzedNote states what the analyzer contributed and what msis left out, in the document.
func analyzedNote(tool string, c AnalyzedCounts, skipped map[string]int) string {
	note := fmt.Sprintf("%s identified %d package(s) in %d payload file(s) from their own declarations "+
		"(dist-info METADATA, pom.properties, .deps.json)", tool, c.Imported, c.Files)
	if c.Dropped > 0 {
		note += fmt.Sprintf("; %d more were in files a supplied SBOM describes, which it keeps", c.Dropped)
	}
	var left []string
	for _, reason := range sortedKeysOf(skipped) {
		left = append(left, fmt.Sprintf("%d %s", skipped[reason], reason))
	}
	if len(left) > 0 {
		note += "; not imported, because no package declares that identity (D25): " +
			strings.Join(left, ", ")
	}
	return note + ". An analyzer claims no completeness: each file's dependency graph stays unknown."
}

// analyzedBy is the analyzer a component came from, or "" for one msis observed or received.
func analyzedBy(c Component) string { return propertyValueOf(c.Properties, propAnalyzedBy) }

func sortedKeysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
