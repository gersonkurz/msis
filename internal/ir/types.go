// Package ir defines the Intermediate Representation for .msis files.
// These types mirror the msis.xsd schema structure.
package ir

import "strings"

// Setup is the root element of an .msis file.
type Setup struct {
	Silent   bool
	Sets     []Set
	Requires []Requirement // Top-level runtime requirements
	Features []Feature
	Items    []Item // Top-level items outside features
	Bundle   *Bundle
	SBOMs    []SuppliedSBOM // Component SBOMs to compose into the installer's document (#36)
	// Components are facts the script declares about payload files (#64)
	Components []DeclaredComponent
	VEX        string // Path to the VEX document annotating this product's SBOM (#37)
}

// SuppliedSBOM is a CycloneDX document the build supplies for one payload file.
// Example: <sbom source="app.cdx.json" for="[INSTALLDIR]app.exe"/>
//
// msis cannot see inside a payload binary; the build system that produced it can. This is the
// only route to describing what is INSIDE a shipped file, which is where a product's top-level
// dependencies actually live.
type SuppliedSBOM struct {
	Source string // the .cdx.json, relative to the .msis
	For    string // the install target of the file it describes, e.g. [INSTALLDIR]app.exe
}

// DeclaredComponent is what the script's author states about one payload file, for the facts
// msis cannot read from the bytes (#64).
// Example: <component for="[INSTALLDIR]libfoo.dll" name="libfoo" version="2.3.1" license="MIT"/>
//
// It is the short form of a supplied SBOM (<sbom>): msis turns it into one and merges it by the
// same rules. Every value is the author's declaration, and the document says so.
type DeclaredComponent struct {
	For     string // the install target of the file it describes, e.g. [INSTALLDIR]libfoo.dll
	Name    string // defaults to the file's name
	Version string
	Creator string // email address, or a URL when there is none (BSI TR-03183-2 v2.1.0 §5.2.2)
	License string // an SPDX licence expression
	PURL    string
	CPE     string
	// SourceCode is the URL of the file's source code (#68): BSI TR-03183-2 v2.1.0 §5.2.3's
	// source code URI - the version in its repository where the author can name it, else the
	// repository itself.
	SourceCode string

	// Recursive applies a folder declaration to files in subfolders too (#65). Only meaningful
	// for a folder target; true unless the script says recursive="no".
	Recursive bool
}

// IsFolder reports whether the declaration names a folder rather than one file (#65): a target
// that ends in a separator, or a bare root such as [INSTALLDIR] - the WHOLE target, so a file
// named "notes[old]" stays a file.
func (c DeclaredComponent) IsFolder() bool {
	if strings.HasSuffix(c.For, `\`) || strings.HasSuffix(c.For, "/") {
		return true
	}
	return strings.HasPrefix(c.For, "[") && strings.Index(c.For, "]") == len(c.For)-1
}

// Requirement represents a runtime dependency declaration.
// Example: <requires type="vcredist" version="2022"/>
type Requirement struct {
	Type    string // vcredist, netfx
	Version string // 2022, 4.8, etc.
	Source  string // optional override path for offline/custom scenarios
	// SHA256 is the digest the supplied Source must have, lowercase hex, or "" when the
	// script gave none. A download msis performs is pinned by msis itself (D5); this is the
	// author's pin for a file they supplied (#50).
	SHA256 string
}

// Set represents a variable definition: <set name="..." value="..."/>
type Set struct {
	Name  string
	Value string
}

// Feature represents a feature grouping with nested items.
type Feature struct {
	Name        string
	Enabled     bool // default true
	Condition   string
	Allowed     bool // default true
	Items       []Item
	SubFeatures []Feature
}

// Item is an interface for all setup items that can appear in a feature.
type Item interface {
	ItemType() string
}

// Files represents: <files source="..." target="..." do-not-overwrite="..."/>
type Files struct {
	Source         string
	Target         string
	DoNotOverwrite bool
}

func (f Files) ItemType() string { return "files" }

// Registry represents: <registry file="..." sddl="..." preserve="..." permanent="..." condition="..."/>
type Registry struct {
	File      string
	SDDL      string
	Preserve  bool
	Permanent bool
	Condition string
}

func (r Registry) ItemType() string { return "registry" }

// SetEnv represents: <set-env name="..." value="..." permanent="..."/>
type SetEnv struct {
	Name      string
	Value     string
	Permanent bool // if true, env var survives uninstall (default: false)
}

func (s SetEnv) ItemType() string { return "set-env" }

// Shortcut represents: <shortcut name="..." target="..." file="..." description="..." icon="..."/>
type Shortcut struct {
	Name        string
	Target      string // DESKTOP, STARTMENU
	File        string
	Description string
	Icon        string
}

func (s Shortcut) ItemType() string { return "shortcut" }

// Service represents: <service file-name="..." service-name="..." .../>
type Service struct {
	FileName           string
	ServiceName        string
	ServiceDisplayName string
	Start              string // auto, demand, disabled
	Description        string
	ServiceType        string // ownProcess, shareProcess
	ErrorControl       string // ignore, normal, critical
	Restart            string
	StartAfterInstall  string // yes (default), no
}

func (s Service) ItemType() string { return "service" }

// Exclude represents: <exclude folder="..."/>
type Exclude struct {
	Folder string
}

func (e Exclude) ItemType() string { return "exclude" }

// Execute represents: <execute cmd="..." when="..." directory="..." fail-on-error="..." quiet="..."/>
type Execute struct {
	Cmd         string
	When        string // before-install, after-install, before-uninstall, after-uninstall
	Directory   string
	FailOnError bool   // true → Return='check' (non-zero exit fails the install)
	Quiet       string // "no" (default, visible console), "yes" (always hidden), "auto" (hidden only when UILevel<=3, i.e. /qn or /qb)
}

func (e Execute) ItemType() string { return "execute" }

// Bundle represents a bootstrapper bundle configuration.
// Supports both legacy shorthand and new nested syntax:
//
//	Legacy: <bundle source_64bit="..." source_32bit="..." source_arm64="..."/>
//	New:    <bundle><prerequisite .../><msi .../></bundle>
type Bundle struct {
	// Legacy shorthand attributes (still supported)
	Source64bit string
	Source32bit string
	SourceArm64 string

	// New nested elements
	Prerequisites []Prerequisite
	MSI           *BundleMSI
	ExePackages   []ExePackage
}

func (b Bundle) ItemType() string { return "bundle" }

// Prerequisite represents a well-known prerequisite like VC++ or .NET Framework.
// Example: <prerequisite type="vcredist" version="2022"/>
type Prerequisite struct {
	Type    string // vcredist, netfx
	Version string // 2022, 4.8, etc.
	Source  string // optional override path
	SHA256  string // digest the supplied Source must have, lowercase hex; "" when none given (#50)
}

// BundleMSI represents the main MSI package(s) in a bundle.
// Example: <msi source_64bit="app-x64.msi" source_32bit="app-x86.msi" source_arm64="app-arm64.msi"/>
type BundleMSI struct {
	Source      string // single MSI (platform-neutral)
	Source64bit string // x64 MSI
	Source32bit string // x86 MSI
	SourceArm64 string // ARM64 MSI
}

// ExePackage represents a custom executable package in the bundle chain.
// Example: <exe id="..." source="..." detect="..." args="..."/>
type ExePackage struct {
	ID              string
	Source          string
	DetectCondition string
	InstallArgs     string
}

// CreateFolder represents: <create-folder target="[APPDATADIR]MyApp\Logs"/>
// Creates an empty directory at install time.
type CreateFolder struct {
	Target string
}

func (c CreateFolder) ItemType() string { return "create-folder" }

// RemoveOnUninstall represents items to remove during uninstall.
// Both fields may be set on one item; the generator emits a component for each and gives them
// distinct ids (issue #23 - they used to collide, and the build failed with WIX0091).
// Example: <remove-on-uninstall registry="HKLM\Software\MyCompany\MyApp"/>
// Example: <remove-on-uninstall folder="[APPDATADIR]MyCompany\MyApp"/>
type RemoveOnUninstall struct {
	Registry string // Registry path like "HKLM\Software\MyCompany\MyApp"
	Folder   string // Folder path like "[APPDATADIR]MyCompany\MyApp"
}

func (r RemoveOnUninstall) ItemType() string { return "remove-on-uninstall" }

// IsSetupBundle returns true if this setup is a bundle (multi-MSI installer).
func (s *Setup) IsSetupBundle() bool {
	return s.Bundle != nil
}
