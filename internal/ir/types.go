// Package ir defines the Intermediate Representation for .msis files.
// These types mirror the msis.xsd schema structure.
package ir

// Setup is the root element of an .msis file.
type Setup struct {
	Silent   bool
	Sets     []Set
	Features []Feature
	Items    []Item // Top-level items outside features
	Bundle   *Bundle
}

// Set represents a variable definition: <set name="..." value="..."/>
type Set struct {
	Name  string
	Value string
}

// Feature represents a feature grouping with nested items.
type Feature struct {
	Name      string
	Enabled   bool // default true
	Condition string
	Allowed   bool // default true
	Items     []Item
	SubFeatures []Feature
}

// Item is an interface for all setup items that can appear in a feature.
type Item interface {
	ItemType() string
}

// Files represents: <files source="..." target="..." do-not-overwrite="..."/>
type Files struct {
	Source        string
	Target        string
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

// SetEnv represents: <set-env name="..." value="..."/>
type SetEnv struct {
	Name  string
	Value string
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
}

func (s Service) ItemType() string { return "service" }

// Exclude represents: <exclude folder="..."/>
type Exclude struct {
	Folder string
}

func (e Exclude) ItemType() string { return "exclude" }

// Execute represents: <execute cmd="..." when="..." directory="..."/>
type Execute struct {
	Cmd       string
	When      string // before-install, after-install, before-uninstall, after-uninstall
	Directory string
}

func (e Execute) ItemType() string { return "execute" }

// Bundle represents: <bundle source_64bit="..." source_32bit="..."/>
type Bundle struct {
	Source64bit string
	Source32bit string
}

func (b Bundle) ItemType() string { return "bundle" }

// IsSetupBundle returns true if this setup is a bundle (multi-MSI installer).
func (s *Setup) IsSetupBundle() bool {
	return s.Bundle != nil
}
