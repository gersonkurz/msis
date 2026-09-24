package sbom

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/contact"
	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// Declaration is what the script's <component> states about ONE installed file (#64, #65): its
// creator, licence, version, name and identifiers. Unlike a supplied document (<sbom>), which
// describes what is INSIDE a file, a declaration is about the file itself, so its facts go onto
// the file's own component (decisions D16), where BSI TR-03183-2 looks for them.
type Declaration struct {
	Source string // the element, e.g. `setup.msis <component for="[INSTALLDIR]lib\">`, for provenance
	Target string // the file's install target, for messages
	FileID string // the WiX File id: an exact join, not a name

	Name, Version, Creator, License, PURL, CPE string
	SourceCode                                 string // a URL: BSI §5.2.3's source code URI (#68)
}

// OneDescriptionPerFile refuses two descriptions of one file - two <sbom>s, two <component>s, a
// folder <component> and a file's own, or a <component> and an <sbom>. msis does not combine two
// claims about the same file: reconciling them is only their authors' to do. It is checked on
// every build, not only when a document is written.
func OneDescriptionPerFile(supplied []Supplied, declared []Declaration) error {
	first := map[string]string{} // FileID -> what describes it
	check := func(fileID, source, target string) error {
		if prev, ok := first[fileID]; ok {
			return fmt.Errorf("both %s and %s are supplied for %s: msis does not combine two "+
				"descriptions into one claim about the same file; only their authors can "+
				"reconcile them", prev, source, target)
		}
		first[fileID] = source
		return nil
	}
	for _, s := range supplied {
		if err := check(s.FileID, s.Source, s.Target); err != nil {
			return err
		}
	}
	for _, d := range declared {
		if err := check(d.FileID, d.Source, d.Target); err != nil {
			return err
		}
	}
	return nil
}

// CheckDeclarations holds each declared version to the version the built package records for its
// file (D13). applyDeclarations applies the same rule when a document is written; this runs after
// every build, so /BUILD without /SBOM does not ship a script that contradicts its installer.
func CheckDeclarations(pkg *msiread.Package, declared []Declaration) error {
	for _, d := range declared {
		for _, f := range pkg.Files {
			if f.ID == d.FileID {
				if err := versionConflict(d, f.Version, f.Name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// versionConflict is the one statement of D13: only trailing ".0" groups may differ.
func versionConflict(d Declaration, recorded, file string) error {
	if d.Version == "" || recorded == "" || sameVersion(d.Version, recorded) {
		return nil
	}
	return fmt.Errorf("%s declares version %s, but the package records version %s for %s; "+
		"correct the declaration or leave the version out", d.Source, d.Version, recorded, file)
}

// applyDeclarations writes each declaration's facts onto the file's own component, and says which
// fields came from where. Runs after enrichment, so a version the build filled in from the source
// file's modification date (D15) is known to be msis's, and a declared one replaces it.
func applyDeclarations(doc *Document, declared []Declaration) error {
	byKey := map[string]*Component{}
	for i := range doc.Components {
		if k := propertyValueOf(doc.Components[i].Properties, propFileKey); k != "" {
			byKey[k] = &doc.Components[i]
		}
	}
	for _, d := range declared {
		c := byKey[d.FileID]
		if c == nil {
			return fmt.Errorf("%s: no component of the artifact carries file id %s", d.Source, d.FileID)
		}
		var fields []string
		if d.Version != "" {
			fallback := propertyValueOf(c.Properties, propBuildVersionFrom) != ""
			if !fallback {
				if err := versionConflict(d, c.Version, c.Name); err != nil {
					return err
				}
			}
			if c.Version == "" || fallback {
				c.Version = d.Version
				c.Properties = withoutProperty(c.Properties, propBuildVersionFrom)
				fields = append(fields, "version")
			}
		}
		if d.Name != "" {
			// The name its creator gives it; the file's own name stays in bsi:component:filename.
			c.Name = d.Name
			fields = append(fields, "name")
		}
		if d.License != "" {
			c.Licenses = licencesOf(d.License)
			fields = append(fields, "license")
		}
		if d.Creator != "" {
			if contact.IsEmail(d.Creator) {
				c.Manufacturer = creatorEntity("", "", d.Creator)
			} else {
				c.Manufacturer = creatorEntity("", d.Creator, "")
			}
			fields = append(fields, "creator")
		}
		if d.PURL != "" {
			c.PURL = d.PURL
			fields = append(fields, "purl")
		}
		if d.CPE != "" {
			c.CPE = d.CPE
			fields = append(fields, "cpe")
		}
		if d.SourceCode != "" {
			c.ExternalReferences = append(c.ExternalReferences, sourceReference(d.SourceCode))
			fields = append(fields, "source")
		}
		if d.PURL != "" || d.CPE != "" {
			// The identity is no longer undetermined: the script states it.
			c.Properties = withoutProperty(c.Properties, propIdentityUnknown)
			c.Properties = append(c.Properties, Property{propIdentityUnknown, "declared by " + d.Source})
		}
		sort.Strings(fields)
		c.Properties = append(c.Properties,
			Property{propDeclaredBy, d.Source},
			Property{propDeclaredFields, strings.Join(fields, ",")})
	}
	return nil
}

// licencesOf gives a declared licence as BSI's pair where CycloneDX 1.6 allows it (decisions D12):
// a single licence id as the original licence (declared) and the distribution licence (concluded).
// Anything else - a compound "Apache-2.0 OR MIT", a LicenseRef-, an id outside CycloneDX's SPDX
// list - can only be ONE expression, so it is given as the distribution licence BSI requires.
func licencesOf(expr string) []LicenseChoice {
	if conformance.IsLicenseID(expr) {
		return []LicenseChoice{
			{License: &LicenseID{ID: expr, Acknowledgement: "declared"}},
			{License: &LicenseID{ID: expr, Acknowledgement: "concluded"}},
		}
	}
	return []LicenseChoice{{Expression: expr, Acknowledgement: "concluded"}}
}

func withoutProperty(props []Property, name string) []Property {
	out := props[:0]
	for _, p := range props {
		if p.Name != name {
			out = append(out, p)
		}
	}
	return out
}
