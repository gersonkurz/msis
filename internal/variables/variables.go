// Package variables handles variable dictionary management and Handlebars resolution.
package variables

import (
	"strings"

	"github.com/aymerick/raymond"
	"github.com/gersonkurz/msis/internal/ir"
)

// Dictionary holds the variable name-value mappings for template resolution.
type Dictionary map[string]string

// New creates a new variable dictionary with default values seeded.
func New() Dictionary {
	return Dictionary{
		// Default platform (can be overridden by .msis)
		"PLATFORM": "x64",

		// AppData directory prefix (empty by default)
		"APPDATADIR_PREFIX": "",

		// Whether to add install directory to PATH
		"ADD_TO_PATH": "False",

		// Whether to remove registry tree on uninstall
		"REMOVE_REGISTRY_TREE": "False",

		// Logo prefix for UI customization (empty = use WiX defaults)
		// Set to e.g. "MyCompany" to use MyCompany_WixUiBanner.bmp, etc.
		"LOGO_PREFIX": "",
	}
}

// LoadFromSetup populates the dictionary from parsed <set> elements.
// Values from the .msis file override defaults.
func (d Dictionary) LoadFromSetup(setup *ir.Setup) {
	for _, s := range setup.Sets {
		d[s.Name] = s.Value
	}
}

// Get returns the value for a variable, or empty string if not found.
func (d Dictionary) Get(name string) string {
	return d[name]
}

// Set sets a variable value.
func (d Dictionary) Set(name, value string) {
	d[name] = value
}

// Has returns true if the variable exists in the dictionary.
func (d Dictionary) Has(name string) bool {
	_, ok := d[name]
	return ok
}

// Resolve applies Handlebars template resolution to a string.
// Variables are referenced using {{VAR_NAME}} syntax.
func (d Dictionary) Resolve(s string) (string, error) {
	tpl, err := raymond.Parse(s)
	if err != nil {
		return "", err
	}
	return tpl.Exec(d)
}

// ResolveAll resolves all variable references within the dictionary itself.
// This handles cases like: PRODUCT_FULL_NAME = "{{PRODUCT_NAME}} {{PRODUCT_VERSION}}"
func (d Dictionary) ResolveAll() error {
	// Build a list of keys that need resolution
	toResolve := make(map[string]string)

	for key, value := range d {
		// Check if value contains Handlebars syntax
		if containsTemplate(value) {
			toResolve[key] = value
		}
	}

	// Resolve each value
	for key, value := range toResolve {
		resolved, err := d.Resolve(value)
		if err != nil {
			return err
		}
		d[key] = resolved
	}

	return nil
}

// containsTemplate checks if a string contains Handlebars template syntax.
func containsTemplate(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '{' && s[i+1] == '{' {
			return true
		}
	}
	return false
}

// GetBool returns the boolean value for a variable.
// Recognized true values: "True", "Yes", "On", "1" (case-insensitive)
// All other values (including empty/missing) return false.
func (d Dictionary) GetBool(name string) bool {
	value := d[name]
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	switch lower {
	case "true", "yes", "on", "1":
		return true
	default:
		return false
	}
}

// Platform returns the target platform (x86 or x64).
func (d Dictionary) Platform() string {
	return d.Get("PLATFORM")
}

// ProductName returns the product name.
func (d Dictionary) ProductName() string {
	return d.Get("PRODUCT_NAME")
}

// ProductVersion returns the product version.
func (d Dictionary) ProductVersion() string {
	return d.Get("PRODUCT_VERSION")
}

// UpgradeCode returns the upgrade code GUID.
func (d Dictionary) UpgradeCode() string {
	return d.Get("UPGRADE_CODE")
}

// Manufacturer returns the manufacturer name.
func (d Dictionary) Manufacturer() string {
	return d.Get("MANUFACTURER")
}

// InstallDir returns the install directory name.
func (d Dictionary) InstallDir() string {
	return d.Get("INSTALLDIR")
}

// BuildTarget returns the output MSI filename.
func (d Dictionary) BuildTarget() string {
	return d.Get("BUILD_TARGET")
}

// DeprecatedVariable describes a deprecated variable with migration guidance.
type DeprecatedVariable struct {
	Name    string
	Message string
}

// DeprecatedVariables is the list of deprecated variables with migration hints.
var DeprecatedVariables = []DeprecatedVariable{
	{
		Name:    "INCLUDE_VCREDIST",
		Message: "INCLUDE_VCREDIST is deprecated. Use <requires type=\"vcredist\" version=\"2022\"/> instead.",
	},
	{
		Name:    "INCLUDE_VC100",
		Message: "INCLUDE_VC100 (VC++ 2010) is deprecated. Note: VC++ 2010 is obsolete; migrate to <requires type=\"vcredist\" version=\"2022\"/> which provides a newer runtime.",
	},
	{
		Name:    "INCLUDE_VC140",
		Message: "INCLUDE_VC140 (VC++ 2015) is deprecated. Use <requires type=\"vcredist\" version=\"2022\"/> instead (2022 is backward-compatible with 2015-2019).",
	},
	{
		Name:    "INCLUDE_MFC",
		Message: "INCLUDE_MFC is deprecated. Merge modules are no longer supported in msis 3.x.",
	},
}

// CheckDeprecated checks for deprecated variables and returns warnings.
// Returns a slice of warning messages for any deprecated variables that are set.
func (d Dictionary) CheckDeprecated() []string {
	var warnings []string
	for _, dep := range DeprecatedVariables {
		if d.GetBool(dep.Name) {
			warnings = append(warnings, dep.Message)
		}
	}
	return warnings
}
