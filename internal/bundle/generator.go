package bundle

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/prereqcache"
	"github.com/gersonkurz/msis/internal/variables"
)

// Generator produces WiX Bundle XML from IR.
type Generator struct {
	Setup     *ir.Setup
	Variables variables.Dictionary
	WorkDir   string
	// AllowedPrereqArchs limits which prerequisite architectures are emitted.
	// If nil, all architectures are allowed.
	AllowedPrereqArchs map[string]bool

	// PrerequisitesFolder is the folder containing prerequisite installers.
	// Defaults to "prerequisites" relative to the .msis file.
	PrerequisitesFolder string

	// Cache is the prerequisite cache. If set, prerequisites are downloaded
	// to the global cache instead of expecting them in PrerequisitesFolder.
	Cache *prereqcache.Cache

	// CachedPaths maps "type/version/arch" to cached file paths.
	// Populated by EnsurePrerequisites().
	CachedPaths map[string]string
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
		CachedPaths:         make(map[string]string),
	}
}

func allowedPrereqArchsForPlatform(platform string) map[string]bool {
	switch strings.ToLower(platform) {
	case "x86":
		return map[string]bool{"x86": true}
	case "arm64":
		return map[string]bool{"arm64": true}
	case "x64":
		return map[string]bool{"x64": true}
	default:
		return nil
	}
}

// SetCache enables prerequisite caching.
func (g *Generator) SetCache(cache *prereqcache.Cache) {
	g.Cache = cache
}

// EnsurePrerequisites downloads/caches all prerequisites needed by the bundle.
// The progress callback receives status messages for UI display.
func (g *Generator) EnsurePrerequisites(progress func(msg string)) error {
	if g.Setup.Bundle == nil {
		return nil
	}

	// Determine platform for prerequisite resolution
	platform := g.Variables["PLATFORM"]
	if platform == "" {
		platform = "x64"
	}

	for _, prereq := range g.Setup.Bundle.Prerequisites {
		if err := g.ensurePrerequisite(prereq, platform, progress); err != nil {
			return err
		}
	}

	return nil
}

// ensurePrerequisite ensures a single prerequisite is available.
func (g *Generator) ensurePrerequisite(prereq ir.Prerequisite, platform string, progress func(msg string)) error {
	// Custom source - no caching needed
	if prereq.Source != "" {
		return nil
	}

	// No cache configured - expect files in PrerequisitesFolder
	if g.Cache == nil {
		return nil
	}

	// For vcredist, we need arch-specific versions
	// Bundles typically include x64, x86, and optionally arm64 to support all platforms
	if prereq.Type == "vcredist" {
		includeX64 := g.AllowedPrereqArchs == nil || g.AllowedPrereqArchs["x64"]
		includeX86 := g.AllowedPrereqArchs == nil || g.AllowedPrereqArchs["x86"]
		includeArm64 := g.AllowedPrereqArchs == nil || g.AllowedPrereqArchs["arm64"]
		var path string
		var err error

		// Ensure x64 version
		if includeX64 {
			path, err = g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "x64", "", progress)
			if err != nil {
				return fmt.Errorf("ensuring %s %s x64: %w", prereq.Type, prereq.Version, err)
			}
			g.CachedPaths[fmt.Sprintf("%s/%s/x64", prereq.Type, prereq.Version)] = path
		}

		// Ensure x86 version
		if includeX86 {
			path, err := g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "x86", "", progress)
			if err != nil {
				return fmt.Errorf("ensuring %s %s x86: %w", prereq.Type, prereq.Version, err)
			}
			g.CachedPaths[fmt.Sprintf("%s/%s/x86", prereq.Type, prereq.Version)] = path
		}

		// Ensure ARM64 version if available (VC++ 2022 has ARM64 support)
		// Try to cache ARM64 - if it fails (no URL available), that's OK for older versions
		if includeArm64 && prereqcache.LookupDownloadURL(prereq.Type, prereq.Version, "arm64") != nil {
			path, err = g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "arm64", "", progress)
			if err != nil {
				return fmt.Errorf("ensuring %s %s arm64: %w", prereq.Type, prereq.Version, err)
			}
			g.CachedPaths[fmt.Sprintf("%s/%s/arm64", prereq.Type, prereq.Version)] = path
		}
	} else {
		// netfx and others - architecture-neutral
		path, err := g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "", "", progress)
		if err != nil {
			return fmt.Errorf("ensuring %s %s: %w", prereq.Type, prereq.Version, err)
		}
		g.CachedPaths[fmt.Sprintf("%s/%s/", prereq.Type, prereq.Version)] = path
	}

	return nil
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

	// Determine source paths - use cached paths if available
	var sourcePath64, sourcePath32, sourcePathArm64 string
	displayName64 := ExpandArch(def.DisplayName, true)
	displayName32 := ExpandArch(def.DisplayName, false)

	includeX64 := g.AllowedPrereqArchs == nil || g.AllowedPrereqArchs["x64"]
	includeX86 := g.AllowedPrereqArchs == nil || g.AllowedPrereqArchs["x86"]
	includeArm64 := g.AllowedPrereqArchs == nil || g.AllowedPrereqArchs["arm64"]

	// Use cached paths if populated (either by Cache or directly)
	if len(g.CachedPaths) > 0 {
		key64 := fmt.Sprintf("%s/%s/x64", prereq.Type, prereq.Version)
		key86 := fmt.Sprintf("%s/%s/x86", prereq.Type, prereq.Version)
		keyNeutral := fmt.Sprintf("%s/%s/", prereq.Type, prereq.Version)
		keyArm64 := fmt.Sprintf("%s/%s/arm64", prereq.Type, prereq.Version)

		if includeX64 {
			if path, ok := g.CachedPaths[key64]; ok {
				sourcePath64 = path
			}
		}
		if includeX86 {
			if path, ok := g.CachedPaths[key86]; ok {
				sourcePath32 = path
			}
		}
		if includeArm64 {
			if path, ok := g.CachedPaths[keyArm64]; ok {
				sourcePathArm64 = path
			}
		}
		if path, ok := g.CachedPaths[keyNeutral]; ok {
			// Architecture-neutral (e.g., netfx)
			sourcePath64 = path
			sourcePath32 = path
		}
	}

	// Fall back to PrerequisitesFolder if no cached paths
	if sourcePath64 == "" && includeX64 {
		source64 := ExpandArch(def.Source, true)
		sourcePath64 = filepath.Join(g.PrerequisitesFolder, source64)
	}
	if sourcePath32 == "" && includeX86 {
		source32 := ExpandArch(def.Source, false)
		sourcePath32 = filepath.Join(g.PrerequisitesFolder, source32)
	}

	// For architecture-neutral prerequisites (like netfx), emit single package
	if prereq.Type != "vcredist" && sourcePath64 == sourcePath32 {
		sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s' DisplayName='%s' SourceFile='%s' "+
			"DetectCondition='%s' InstallArguments='%s' Permanent='yes' Vital='yes'/>\n",
			id, escapeXMLAttr(def.DisplayName), escapeXMLAttr(sourcePath64),
			escapeXMLAttr(def.DetectCondition), escapeXMLAttr(def.InstallArgs)))
		return sb.String(), nil
	}

	// Emit packages with appropriate install conditions
	// ARM64: NativeMachine = 43620 (0xAA64)
	// x64:   VersionNT64 AND NOT ARM64
	// x86:   NOT VersionNT64

	if includeArm64 && sourcePathArm64 != "" {
		displayNameArm64 := ExpandArch(def.DisplayName, true)
		displayNameArm64 = strings.Replace(displayNameArm64, "x64", "ARM64", 1)
		sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s_arm64' DisplayName='%s' SourceFile='%s' "+
			"DetectCondition='%s' InstallArguments='%s' Permanent='yes' Vital='yes' "+
			"InstallCondition='NativeMachine = 43620'/>\n",
			id, escapeXMLAttr(displayNameArm64), escapeXMLAttr(sourcePathArm64),
			escapeXMLAttr(def.DetectCondition), escapeXMLAttr(def.InstallArgs)))
	}

	if includeX64 {
		condition := "VersionNT64"
		if includeArm64 {
			condition = "VersionNT64 AND NOT NativeMachine = 43620"
		}
		sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s_x64' DisplayName='%s' SourceFile='%s' "+
			"DetectCondition='%s' InstallArguments='%s' Permanent='yes' Vital='yes' "+
			"InstallCondition='%s'/>\n",
			id, escapeXMLAttr(displayName64), escapeXMLAttr(sourcePath64),
			escapeXMLAttr(def.DetectCondition), escapeXMLAttr(def.InstallArgs), condition))
	}

	if includeX86 {
		sb.WriteString(fmt.Sprintf("      <ExePackage Id='%s_x86' DisplayName='%s' SourceFile='%s' "+
			"DetectCondition='%s' InstallArguments='%s' Permanent='yes' Vital='yes' "+
			"InstallCondition='NOT VersionNT64'/>\n",
			id, escapeXMLAttr(displayName32), escapeXMLAttr(sourcePath32),
			escapeXMLAttr(def.DetectCondition), escapeXMLAttr(def.InstallArgs)))
	}

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

// AutoBundleGenerator generates a bundle wrapper for an MSI with prerequisites.
type AutoBundleGenerator struct {
	Variables           variables.Dictionary
	WorkDir             string
	MSIPath             string            // Path to the generated MSI file
	Requirements        []ir.Prerequisite // Prerequisites to install before MSI
	PrerequisitesFolder string

	// Cache is the prerequisite cache. If set, prerequisites are downloaded
	// to the global cache instead of expecting them in PrerequisitesFolder.
	Cache *prereqcache.Cache

	// CachedPaths maps "type/version/arch" to cached file paths.
	// Populated by EnsurePrerequisites().
	CachedPaths map[string]string
}

// NewAutoBundleGenerator creates a generator for auto-bundling.
func NewAutoBundleGenerator(vars variables.Dictionary, workDir, msiPath string, requirements []ir.Prerequisite) *AutoBundleGenerator {
	prereqFolder := vars["PREREQUISITES_FOLDER"]
	if prereqFolder == "" {
		prereqFolder = filepath.Join(workDir, "prerequisites")
	}

	return &AutoBundleGenerator{
		Variables:           vars,
		WorkDir:             workDir,
		MSIPath:             msiPath,
		Requirements:        requirements,
		PrerequisitesFolder: prereqFolder,
		CachedPaths:         make(map[string]string),
	}
}

// SetCache enables prerequisite caching for auto-bundling.
func (g *AutoBundleGenerator) SetCache(cache *prereqcache.Cache) {
	g.Cache = cache
}

// EnsurePrerequisites downloads/caches all prerequisites needed for auto-bundling.
// The progress callback receives status messages for UI display.
func (g *AutoBundleGenerator) EnsurePrerequisites(progress func(msg string)) error {
	// Determine platform for prerequisite resolution
	platform := g.Variables["PLATFORM"]
	if platform == "" {
		platform = "x64"
	}

	for _, prereq := range g.Requirements {
		if err := g.ensurePrerequisite(prereq, platform, progress); err != nil {
			return err
		}
	}

	return nil
}

// ensurePrerequisite ensures a single prerequisite is available for auto-bundling.
func (g *AutoBundleGenerator) ensurePrerequisite(prereq ir.Prerequisite, platform string, progress func(msg string)) error {
	// Custom source - no caching needed
	if prereq.Source != "" {
		return nil
	}

	// No cache configured - expect files in PrerequisitesFolder
	if g.Cache == nil {
		return nil
	}

	// For vcredist, we need arch-specific versions
	// Bundles typically include x64, x86, and optionally arm64 to support all platforms
	if prereq.Type == "vcredist" {
		allowed := allowedPrereqArchsForPlatform(platform)
		includeX64 := allowed == nil || allowed["x64"]
		includeX86 := allowed == nil || allowed["x86"]
		includeArm64 := allowed == nil || allowed["arm64"]
		var path string
		var err error

		// Ensure x64 version
		if includeX64 {
			path, err = g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "x64", "", progress)
			if err != nil {
				return fmt.Errorf("ensuring %s %s x64: %w", prereq.Type, prereq.Version, err)
			}
			g.CachedPaths[fmt.Sprintf("%s/%s/x64", prereq.Type, prereq.Version)] = path
		}

		// Ensure x86 version
		if includeX86 {
			path, err = g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "x86", "", progress)
			if err != nil {
				return fmt.Errorf("ensuring %s %s x86: %w", prereq.Type, prereq.Version, err)
			}
			g.CachedPaths[fmt.Sprintf("%s/%s/x86", prereq.Type, prereq.Version)] = path
		}

		// Ensure ARM64 version if available (VC++ 2022 has ARM64 support)
		// Try to cache ARM64 - if it fails (no URL available), that's OK for older versions
		if includeArm64 && prereqcache.LookupDownloadURL(prereq.Type, prereq.Version, "arm64") != nil {
			path, err = g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "arm64", "", progress)
			if err != nil {
				return fmt.Errorf("ensuring %s %s arm64: %w", prereq.Type, prereq.Version, err)
			}
			g.CachedPaths[fmt.Sprintf("%s/%s/arm64", prereq.Type, prereq.Version)] = path
		}
	} else {
		// netfx and others - architecture-neutral
		path, err := g.Cache.EnsurePrerequisite(prereq.Type, prereq.Version, "", "", progress)
		if err != nil {
			return fmt.Errorf("ensuring %s %s: %w", prereq.Type, prereq.Version, err)
		}
		g.CachedPaths[fmt.Sprintf("%s/%s/", prereq.Type, prereq.Version)] = path
	}

	return nil
}

// Generate produces the bundle chain XML for auto-bundling.
func (g *AutoBundleGenerator) Generate() (*GeneratedBundle, error) {
	var chain strings.Builder

	// Generate prerequisite packages (reuse existing logic)
	platform := g.Variables["PLATFORM"]
	if platform == "" {
		platform = "x64"
	}
	tempGen := &Generator{
		Variables:           g.Variables,
		WorkDir:             g.WorkDir,
		PrerequisitesFolder: g.PrerequisitesFolder,
		Cache:               g.Cache,
		CachedPaths:         g.CachedPaths,
		AllowedPrereqArchs:  allowedPrereqArchsForPlatform(platform),
	}

	for i, prereq := range g.Requirements {
		xml, err := tempGen.generatePrerequisitePackage(prereq, i)
		if err != nil {
			return nil, fmt.Errorf("generating prerequisite %s %s: %w", prereq.Type, prereq.Version, err)
		}
		chain.WriteString(xml)
	}

	// Generate MsiPackage for the main MSI
	chain.WriteString(fmt.Sprintf("      <MsiPackage Id='MainPackage' SourceFile='%s'>\n"+
		"        <MsiProperty Name='INSTALLDIR' Value='[InstallFolder]'/>\n"+
		"      </MsiPackage>\n",
		escapeXMLAttr(g.MSIPath)))

	return &GeneratedBundle{
		ChainXML: chain.String(),
	}, nil
}

// RequirementsToPrerequisites converts ir.Requirement slice to ir.Prerequisite slice.
func RequirementsToPrerequisites(requirements []ir.Requirement) []ir.Prerequisite {
	prereqs := make([]ir.Prerequisite, len(requirements))
	for i, req := range requirements {
		prereqs[i] = ir.Prerequisite{
			Type:    req.Type,
			Version: req.Version,
			Source:  req.Source,
		}
	}
	return prereqs
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
