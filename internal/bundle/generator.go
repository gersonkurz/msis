package bundle

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// Generator produces WiX Bundle XML from IR.
type Generator struct {
	Setup     *ir.Setup
	Variables variables.Dictionary
	WorkDir   string

	// PrerequisitesFolder is the folder containing prerequisite installers.
	// Defaults to "prerequisites" relative to the .msis file.
	PrerequisitesFolder string
}

// NewGenerator creates a new bundle generator.
func NewGenerator(setup *ir.Setup, vars variables.Dictionary, workDir string) *Generator {
	prereqFolder := vars["PREREQUISITES_FOLDER"]
	if prereqFolder == "" {
		prereqFolder = filepath.Join(workDir, "prerequisites")
	}

	return &Generator{
		Setup:               setup,
		Variables:           vars,
		WorkDir:             workDir,
		PrerequisitesFolder: prereqFolder,
	}
}

// GeneratedBundle holds the generated bundle XML fragments.
type GeneratedBundle struct {
	ChainXML string // <Chain> content with ExePackage and MsiPackage elements
}

// Generate produces the bundle chain XML.
func (g *Generator) Generate() (*GeneratedBundle, error) {
	if g.Setup.Bundle == nil {
		return nil, fmt.Errorf("setup does not contain a bundle")
	}

	bundle := g.Setup.Bundle
	var chain strings.Builder

	// Generate prerequisite packages
	for i, prereq := range bundle.Prerequisites {
		xml, err := g.generatePrerequisitePackage(prereq, i)
		if err != nil {
			return nil, err
		}
		chain.WriteString(xml)
	}

	// Generate custom exe packages
	for _, exe := range bundle.ExePackages {
		xml := g.generateExePackage(exe)
		chain.WriteString(xml)
	}

	// Generate MSI package(s)
	msiXML, err := g.generateMSIPackages(bundle)
	if err != nil {
		return nil, err
	}
	chain.WriteString(msiXML)

	return &GeneratedBundle{
		ChainXML: chain.String(),
	}, nil
}

// generatePrerequisitePackage generates an ExePackage for a well-known prerequisite.
func (g *Generator) generatePrerequisitePackage(prereq ir.Prerequisite, index int) (string, error) {
	def := LookupPrerequisite(prereq.Type, prereq.Version)
	if def == nil && prereq.Source == "" {
		return "", fmt.Errorf("unknown prerequisite: type=%q version=%q", prereq.Type, prereq.Version)
	}

	var sb strings.Builder
	id := sanitizeID(fmt.Sprintf("Prereq_%s_%s", prereq.Type, prereq.Version))

	// If custom source is provided, emit single package (user handles arch selection)
	if prereq.Source != "" {
		displayName := prereq.Type
		if prereq.Version != "" {
			displayName = fmt.Sprintf("%s %s", prereq.Type, prereq.Version)
		}
		detectCondition := ""
		installArgs := ""
		if def != nil {
			displayName = def.DisplayName
			detectCondition = def.DetectCondition
			installArgs = def.InstallArgs
		}

		sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s' DisplayName='%s' SourceFile='%s'",
			id, escapeXMLAttr(displayName), escapeXMLAttr(prereq.Source)))
		if detectCondition != "" {
			sb.WriteString(fmt.Sprintf(" DetectCondition='%s'", escapeXMLAttr(detectCondition)))
		}
		if installArgs != "" {
			sb.WriteString(fmt.Sprintf(" InstallArguments='%s'", escapeXMLAttr(installArgs)))
		}
		sb.WriteString(" Permanent='yes' Vital='yes'/>\n")
		return sb.String(), nil
	}

	// Use well-known definition with arch-specific packages
	source64 := ExpandArch(def.Source, true)
	source32 := ExpandArch(def.Source, false)
	displayName64 := ExpandArch(def.DisplayName, true)
	displayName32 := ExpandArch(def.DisplayName, false)

	sourcePath64 := filepath.Join(g.PrerequisitesFolder, source64)
	sourcePath32 := filepath.Join(g.PrerequisitesFolder, source32)

	// x64 package
	sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s_x64' DisplayName='%s' SourceFile='%s' "+
		"DetectCondition='%s' InstallArguments='%s' Permanent='yes' Vital='yes' "+
		"InstallCondition='VersionNT64'/>\n",
		id, escapeXMLAttr(displayName64), escapeXMLAttr(sourcePath64),
		escapeXMLAttr(def.DetectCondition), escapeXMLAttr(def.InstallArgs)))

	// x86 package
	sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s_x86' DisplayName='%s' SourceFile='%s' "+
		"DetectCondition='%s' InstallArguments='%s' Permanent='yes' Vital='yes' "+
		"InstallCondition='NOT VersionNT64'/>\n",
		id, escapeXMLAttr(displayName32), escapeXMLAttr(sourcePath32),
		escapeXMLAttr(def.DetectCondition), escapeXMLAttr(def.InstallArgs)))

	return sb.String(), nil
}

// generateExePackage generates an ExePackage for a custom exe.
func (g *Generator) generateExePackage(exe ir.ExePackage) string {
	var sb strings.Builder

	id := exe.ID
	if id == "" {
		id = sanitizeID(fmt.Sprintf("ExePackage_%s", filepath.Base(exe.Source)))
	}

	sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s' SourceFile='%s'",
		id, escapeXMLAttr(exe.Source)))

	if exe.DetectCondition != "" {
		sb.WriteString(fmt.Sprintf(" DetectCondition='%s'", escapeXMLAttr(exe.DetectCondition)))
	}
	if exe.InstallArgs != "" {
		sb.WriteString(fmt.Sprintf(" InstallArguments='%s'", escapeXMLAttr(exe.InstallArgs)))
	}

	sb.WriteString(" Permanent='yes' Vital='yes'/>\n")
	return sb.String()
}

// generateMSIPackages generates MsiPackage elements for the main application.
// Architecture detection uses NativeMachine property (available in WiX 6):
//   - ARM64: NativeMachine = 43620 (0xAA64)
//   - x64:   VersionNT64 AND NOT NativeMachine = 43620
//   - x86:   NOT VersionNT64
func (g *Generator) generateMSIPackages(bundle *ir.Bundle) (string, error) {
	var sb strings.Builder

	// Determine if ARM64 is specified (affects x64 condition)
	hasArm64 := (bundle.MSI != nil && bundle.MSI.SourceArm64 != "") || bundle.SourceArm64 != ""

	// Check for nested MSI element first
	if bundle.MSI != nil {
		if bundle.MSI.Source != "" {
			// Single platform-neutral MSI
			source, err := g.Variables.Resolve(bundle.MSI.Source)
			if err != nil {
				return "", fmt.Errorf("resolving MSI source: %w", err)
			}
			sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage' SourceFile='%s'>\n"+
				"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
				"      </MsiPackage>\n",
				escapeXMLAttr(source)))
		} else {
			// Platform-specific MSIs
			if bundle.MSI.SourceArm64 != "" {
				source, err := g.Variables.Resolve(bundle.MSI.SourceArm64)
				if err != nil {
					return "", fmt.Errorf("resolving MSI source_arm64: %w", err)
				}
				sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage_arm64' SourceFile='%s' "+
					"InstallCondition='NativeMachine = 43620'>\n"+
					"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
					"      </MsiPackage>\n",
					escapeXMLAttr(source)))
			}
			if bundle.MSI.Source64bit != "" {
				source, err := g.Variables.Resolve(bundle.MSI.Source64bit)
				if err != nil {
					return "", fmt.Errorf("resolving MSI source_64bit: %w", err)
				}
				// If ARM64 is also specified, exclude ARM64 from x64 condition
				condition := "VersionNT64"
				if hasArm64 {
					condition = "VersionNT64 AND NOT NativeMachine = 43620"
				}
				sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage_x64' SourceFile='%s' "+
					"InstallCondition='%s'>\n"+
					"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
					"      </MsiPackage>\n",
					escapeXMLAttr(source), condition))
			}
			if bundle.MSI.Source32bit != "" {
				source, err := g.Variables.Resolve(bundle.MSI.Source32bit)
				if err != nil {
					return "", fmt.Errorf("resolving MSI source_32bit: %w", err)
				}
				sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage_x86' SourceFile='%s' "+
					"InstallCondition='NOT VersionNT64'>\n"+
					"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
					"      </MsiPackage>\n",
					escapeXMLAttr(source)))
			}
		}
	} else if bundle.Source64bit != "" || bundle.Source32bit != "" || bundle.SourceArm64 != "" {
		// Legacy shorthand syntax
		if bundle.SourceArm64 != "" {
			source, err := g.Variables.Resolve(bundle.SourceArm64)
			if err != nil {
				return "", fmt.Errorf("resolving source_arm64: %w", err)
			}
			sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage_arm64' SourceFile='%s' "+
				"InstallCondition='NativeMachine = 43620'>\n"+
				"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
				"      </MsiPackage>\n",
				escapeXMLAttr(source)))
		}
		if bundle.Source64bit != "" {
			source, err := g.Variables.Resolve(bundle.Source64bit)
			if err != nil {
				return "", fmt.Errorf("resolving source_64bit: %w", err)
			}
			// If ARM64 is also specified, exclude ARM64 from x64 condition
			condition := "VersionNT64"
			if hasArm64 {
				condition = "VersionNT64 AND NOT NativeMachine = 43620"
			}
			sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage_x64' SourceFile='%s' "+
				"InstallCondition='%s'>\n"+
				"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
				"      </MsiPackage>\n",
				escapeXMLAttr(source), condition))
		}
		if bundle.Source32bit != "" {
			source, err := g.Variables.Resolve(bundle.Source32bit)
			if err != nil {
				return "", fmt.Errorf("resolving source_32bit: %w", err)
			}
			sb.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage_x86' SourceFile='%s' "+
				"InstallCondition='NOT VersionNT64'>\n"+
				"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
				"      </MsiPackage>\n",
				escapeXMLAttr(source)))
		}
	} else {
		return "", fmt.Errorf("bundle has no MSI source specified")
	}

	return sb.String(), nil
}

// escapeXMLAttr escapes special characters for XML attribute values.
func escapeXMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// sanitizeID converts a string to a valid WiX identifier.
// WiX identifiers must start with a letter or underscore and contain only
// letters, digits, underscores, and periods. We replace invalid chars with underscores.
func sanitizeID(s string) string {
	var result strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
			result.WriteRune(r)
		} else if r >= '0' && r <= '9' {
			result.WriteRune(r)
		} else if r == '.' || r == '-' || r == ' ' {
			// Replace common separators with underscore
			result.WriteRune('_')
		}
		// Skip other invalid characters
	}

	// Ensure non-empty
	if result.Len() == 0 {
		return "ID"
	}

	// Ensure starts with letter or underscore
	str := result.String()
	if str[0] >= '0' && str[0] <= '9' {
		return "_" + str
	}
	return str
}
