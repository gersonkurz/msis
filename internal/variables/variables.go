// Package variables handles variable dictionary management and Handlebars resolution.
package variables

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aymerick/raymond"
	"github.com/gersonkurz/msis/internal/contact"
	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/spdx"
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
//
// Values are substituted VERBATIM. This dictionary holds file paths, product names
// and registry values — not HTML — and callers resolve things like a <files source=>
// path with it. Passing the map to raymond directly applies its default HTML
// escaping, which turned a manufacturer of "R&D" into "R&amp;D" in every consumer.
// Escaping belongs to the .wxs render (internal/template), which does it
// deliberately and documents which placeholders are escaped.
func (d Dictionary) Resolve(s string) (string, error) {
	tpl, err := raymond.Parse(keepBackslashBeforeReference(s))
	if err != nil {
		return "", err
	}
	return tpl.Exec(d.verbatimContext())
}

// keepBackslashBeforeReference stops a backslash immediately before a {{ from being
// read as Handlebars' escape for a literal mustache.
//
// On Windows a backslash there is a path separator, and these strings are paths:
// Resolve is applied to <files source=>, <setenv> values and the bundle's MSI source
// paths. Treating it as an escape both suppressed the substitution and swallowed the
// separator, so "bin\{{PLATFORM}}\app.exe" silently became "bin{{PLATFORM}}\app.exe"
// (issue #13). Nothing errored; the broken path simply flowed on.
//
// Handlebars consumes one backslash from a run before {{ — one leaves a literal
// mustache, two leave one backslash and substitute, three leave two, and so on. Adding
// one to the run therefore makes every authored backslash survive as a separator and
// the reference always substitute.
//
// The deliberate cost: \{{...}} can no longer produce a literal mustache. A .msis has
// no use for one, and Windows paths are the common case by a wide margin.
func keepBackslashBeforeReference(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		run := i
		for run < len(s) && s[run] == '\\' {
			run++
		}
		b.WriteString(s[i:run])
		if strings.HasPrefix(s[run:], "{{") {
			b.WriteByte('\\')
		}
		i = run
	}
	return b.String()
}

// verbatimContext exposes every variable as a SafeString, so raymond substitutes
// values without HTML-escaping them.
func (d Dictionary) verbatimContext() map[string]any {
	ctx := make(map[string]any, len(d))
	for name, value := range d {
		ctx[name] = raymond.SafeString(value)
	}
	return ctx
}

// ResolveAll resolves all variable references within the dictionary itself, so a
// value may refer to a variable whose own value refers to a third:
//
//	PRODUCT_VERSION = "1.2.3"
//	PRODUCT_NAME    = "My Product - {{PRODUCT_VERSION}}"
//	BUILD_TARGET    = "{{PRODUCT_NAME}}.msi"
//
// Resolution is dependency-driven, not a single pass over the map. Go randomises
// map order, so a single pass gave BUILD_TARGET the still-unresolved text of
// PRODUCT_NAME whenever it happened to be visited first, and the build produced a
// file literally named "My Product - {{PRODUCT_VERSION}}.msi" — about one run in
// five (issue #7).
func (d Dictionary) ResolveAll() error {
	r := &resolver{
		dict:     d,
		resolved: make(map[string]bool, len(d)),
		onPath:   make(map[string]bool),
	}
	// Sorted so that a dictionary with more than one cycle always reports the same
	// one: this is a determinism fix, and that has to include the failures.
	keys := make([]string, 0, len(d))
	for key := range d {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if err := r.resolve(key, nil); err != nil {
			return err
		}
	}
	return nil
}

// resolver carries the state of one ResolveAll.
type resolver struct {
	dict     Dictionary
	resolved map[string]bool // finished, and must not be rendered again
	onPath   map[string]bool // currently being resolved: a revisit is a cycle
}

// resolve brings one variable to its final value, resolving whatever it depends on
// first.
//
// Dependencies are discovered from what raymond ACTUALLY EVALUATES rather than by
// reading the template text. Scanning is unreliable in both directions: a regex
// misses hyphenated names and whitespace-control forms like {{~X~}}, while a
// parse-tree walk reports references inside untaken {{#if}} branches and so invents
// cycles that never arise at run time. Rendering with a sentinel in place of each
// unresolved variable reports exactly the ones that were reached.
func (r *resolver) resolve(key string, chain []string) error {
	if r.resolved[key] {
		return nil
	}
	value, ok := r.dict[key]
	if !ok {
		return nil
	}
	if !containsTemplate(value) {
		r.resolved[key] = true
		return nil
	}
	if r.onPath[key] {
		return fmt.Errorf("variable reference cycle: %s", strings.Join(append(chain, key), " -> "))
	}
	r.onPath[key] = true
	defer delete(r.onPath, key)

	// Every round either finishes the value or resolves at least one dependency,
	// so the number of rounds cannot exceed the number of variables.
	for round := 0; round <= len(r.dict); round++ {
		tpl, err := raymond.Parse(keepBackslashBeforeReference(r.dict[key]))
		if err != nil {
			return fmt.Errorf("variable %s: %w", key, err)
		}

		var pending []string
		ctx := make(map[string]any, len(r.dict))
		for name, v := range r.dict {
			if !r.resolved[name] && containsTemplate(v) {
				name := name
				ctx[name] = func() any {
					pending = append(pending, name)
					return raymond.SafeString("")
				}
				continue
			}
			ctx[name] = raymond.SafeString(v)
		}

		out, err := tpl.Exec(ctx)

		if len(pending) > 0 {
			// Discard BOTH the output and the error. A sentinel stands in for a
			// value we do not have yet, and raymond treats a function in the
			// context as a block helper, so any {{#X}}...{{/X}} section rendered
			// against one is wrong. The only thing wanted from this render is which
			// variables it reached.
			//
			// Take only the FIRST one, then render again. Everything raymond
			// evaluated after it is speculative: a sentinel renders as empty, so an
			// unresolved {{#if FLAG}} takes the ELSE branch regardless of what FLAG
			// will turn out to be, and the references harvested from that branch may
			// be ones the real value never reaches. Resolving them anyway invents
			// dependencies — and, when such a branch refers back, an outright false
			// cycle. Resolving one at a time costs an extra render per dependency
			// and keeps discovery honest.
			if err := r.resolve(pending[0], append(chain, key)); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("variable %s: %w", key, err)
		}
		// Mark resolved rather than re-testing the text for "{{": completion is a fact
		// about this walk, not something to re-derive from the output.
		r.dict[key] = out
		r.resolved[key] = true
		return nil
	}
	return fmt.Errorf("variable %s: gave up resolving after %d rounds", key, len(r.dict)+1)
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

// HookDllDir returns the template subfolder that holds the native hook DLL for the target platform.
// Unlike template selection (which maps arm64 to the x64 template), the hook DLL is arch-native:
// x86 -> x86, arm64 -> arm64, everything else (incl. x64/default) -> x64.
func (d Dictionary) HookDllDir() string {
	switch strings.ToLower(d.Platform()) {
	case "x86":
		return "x86"
	case "arm64":
		return "arm64"
	default:
		return "x64"
	}
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

// RegistryTreeActive reports whether REMOVE_REGISTRY_TREE requests registry cleanup. The variable
// holds a comma-separated list of registry roots, NOT a boolean, so a non-empty value is "active"
// unless it is an explicit false-like token (False/No/Off/0, case-insensitive) or empty/missing.
func (d Dictionary) RegistryTreeActive() bool {
	v := strings.TrimSpace(d["REMOVE_REGISTRY_TREE"])
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "false", "no", "off", "0":
		return false
	}
	return true
}

// CheckInstallerHookUsage returns build-time warnings about the installer-hook variables
// (REMOVE_FOLDERS_ON_UNINSTALL / RETAIN_FILES_ON_UNINSTALL / REMOVE_REGISTRY_TREE). These behaviors
// are implemented by the native installer-hook DLL, so they only take effect when
// USE_INSTALLER_HOOKS=True.
func (d Dictionary) CheckInstallerHookUsage() []string {
	removeFolders := d.GetBool("REMOVE_FOLDERS_ON_UNINSTALL")
	useHooks := d.GetBool("USE_INSTALLER_HOOKS")
	retain := strings.TrimSpace(d["RETAIN_FILES_ON_UNINSTALL"])
	registryActive := d.RegistryTreeActive()
	cleanupActive := removeFolders && useHooks

	var warnings []string
	if removeFolders {
		warnings = append(warnings, "REMOVE_FOLDERS_ON_UNINSTALL=True recursively deletes the INSTALLDIR "+
			"and APPDATADIR trees on full uninstall, including files your application created at runtime "+
			"(databases, logs, config). RETAIN_FILES_ON_UNINSTALL can exempt specific files, but only when "+
			"built against an updated hook DLL that honors it.")
	}
	if removeFolders && !useHooks {
		warnings = append(warnings, "REMOVE_FOLDERS_ON_UNINSTALL has no effect unless USE_INSTALLER_HOOKS=True "+
			"(folder removal is performed by the installer-hook DLL).")
	}
	if registryActive {
		warnings = append(warnings, "REMOVE_REGISTRY_TREE recursively deletes the listed registry trees on "+
			"full uninstall. This can remove state not owned by the MSI.")
	}
	if registryActive && !useHooks {
		warnings = append(warnings, "REMOVE_REGISTRY_TREE has no effect unless USE_INSTALLER_HOOKS=True.")
	}
	if retain != "" && !cleanupActive {
		warnings = append(warnings, "RETAIN_FILES_ON_UNINSTALL has no effect unless folder cleanup is active "+
			"(set both REMOVE_FOLDERS_ON_UNINSTALL=True and USE_INSTALLER_HOOKS=True).")
	}
	return warnings
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

// CheckSBOMVariables validates the variables that end up in the SBOM or the installer: the
// contact variables (#64) and SBOM_DATA_LICENSE, the licence the documents themselves are
// offered under (an SPDX expression, #62).
//
// The contact variables: MANUFACTURER_URL and MANUFACTURER_EMAIL are
// written into the installer (ARPURLINFOABOUT, ARPCONTACT; a bundle's AboutUrl) and read back as
// the product creator's contact, and SBOM_CREATOR names who created the SBOM; BSI TR-03183-2
// v2.1.0 asks for an email address or a URL. A malformed value is a build error, not a warning:
// once it is in the installer, every document read from it inherits it.
//
// The values are trimmed IN the dictionary, not only for the check: the templates and the build
// record read the dictionary, and a value validated as "support@acme.example" but written as
// " support@acme.example " would be rejected when read back out of the installer, and silently
// vanish from the SBOM. A value that is only whitespace becomes unset (#64's review).
func (d Dictionary) CheckSBOMVariables() error {
	for _, name := range []string{"MANUFACTURER_URL", "MANUFACTURER_EMAIL", "SBOM_CREATOR", "SBOM_DATA_LICENSE"} {
		if v, ok := d[name]; ok {
			d[name] = strings.TrimSpace(v)
		}
	}
	if v := d["MANUFACTURER_URL"]; v != "" && !contact.IsURL(v) {
		return fmt.Errorf("MANUFACTURER_URL %q is not an absolute http(s) URL", v)
	}
	if v := d["MANUFACTURER_EMAIL"]; v != "" && !contact.IsEmail(v) {
		return fmt.Errorf("MANUFACTURER_EMAIL %q is not a plain email address", v)
	}
	if v := d["SBOM_CREATOR"]; v != "" && !contact.IsEmail(v) && !contact.IsURL(v) {
		return fmt.Errorf("SBOM_CREATOR %q is neither an email address nor an absolute http(s) URL", v)
	}
	if v := d["SBOM_DATA_LICENSE"]; v != "" {
		if err := spdx.Valid(v); err != nil {
			return fmt.Errorf("SBOM_DATA_LICENSE %q is not an SPDX licence expression: %v", v, err)
		}
	}
	return nil
}
