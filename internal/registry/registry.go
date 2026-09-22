// Package registry processes .reg files and generates WiX registry components.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gersonkurz/go-regis3"
	"github.com/gersonkurz/msis/internal/ir"
)

// DefaultSDDL is the default security descriptor for registry keys.
// Grants generic access to System, Built-in Users, Authenticated Users, Local Admin, and Local Service.
const DefaultSDDL = "O:BAG:SYD:(A;CIOI;GA;;;SY)(A;CIOI;GA;;;BU)(A;CIOI;GA;;;AU)(A;CIOI;GA;;;LA)(A;CIOI;GA;;;LS)"

// Component represents a WiX registry component.
type Component struct {
	ID        string
	GUID      string
	Permanent bool
	Preserve  bool
	Condition string
	SDDL      string
	Keys      []*RegistryKey
}

// RegistryKey represents a WiX RegistryKey element.
type RegistryKey struct {
	Root       string // HKLM, HKCU, etc.
	Key        string // Path without root
	Values     []*RegistryValue
	SubKeys    []*RegistryKey
	RemoveFlag bool
}

// RegistryValue represents a WiX RegistryValue element.
type RegistryValue struct {
	ID         string
	Name       string   // Empty for default value
	Type       string   // string, integer, binary, expandable, multiString
	Value      string   // For simple types
	MultiValue []string // For multiString type
	RemoveFlag bool

	// FromQword marks a value the .reg file declared as REG_QWORD. Type is
	// "integer" like a DWORD, because that is all MSI can write, but the two must
	// stay distinguishable: a QWORD cannot be preserved (see shouldPreserveValue).
	FromQword bool
}

// Processor handles registry file parsing and WiX generation.
type Processor struct {
	workDir          string
	upgradeCode      string
	nextKeyID        int
	nextValueID      int
	componentCounter int
	componentIDs     map[string]bool
	nextPreserveID   int

	// warnings collects build-time diagnostics about values msis had to alter or
	// that MSI will reinterpret. Drained by the generator and printed by main.go;
	// see Warnings().
	warnings []string
}

// Warnings returns build-time diagnostics gathered while processing registry files,
// in the order they were found. Deliberately warnings rather than errors: each names
// either a deliberate, documented narrowing or an ambiguity only the author can
// settle, so failing the build would break packages that work today. The point is
// that the author is told, rather than the tool silently deciding for them.
func (p *Processor) Warnings() []string {
	return p.warnings
}

func (p *Processor) warn(format string, args ...any) {
	p.warnings = append(p.warnings, fmt.Sprintf(format, args...))
}

// NewProcessor creates a new registry processor.
// The upgradeCode is used to make component GUIDs product-unique,
// preventing shared component conflicts between different products.
func NewProcessor(workDir string, upgradeCode string) *Processor {
	return &Processor{
		workDir:      workDir,
		upgradeCode:  upgradeCode,
		componentIDs: make(map[string]bool),
	}
}

// Process parses a registry item and returns WiX components.
func (p *Processor) Process(reg ir.Registry) ([]*Component, error) {
	// Resolve file path
	filePath := reg.File
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(p.workDir, filePath)
	}

	// Parse the .reg file using go-regis3
	root, err := regis3.ParseFile(filePath, &regis3.ParseOptions{
		AllowHashtagComments:   true,
		AllowSemicolonComments: true,
		IgnoreWhitespaces:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("parsing registry file %s: %w", reg.File, err)
	}

	// Apply SDDL default
	sddl := reg.SDDL
	if sddl == "" {
		sddl = DefaultSDDL
	}

	// Generate component
	compID := p.nextComponentIDStr()
	guid := generateGUID(p.upgradeCode + "/registry_" + reg.File)

	comp := &Component{
		ID:        compID,
		GUID:      guid,
		Permanent: reg.Permanent,
		Preserve:  reg.Preserve,
		Condition: reg.Condition,
		SDDL:      sddl,
	}

	// Convert regis3 tree to our RegistryKey structure
	comp.Keys = p.convertKeyEntry(root)

	for _, key := range comp.Keys {
		p.warnFormattedValues(key, reg.Preserve)
	}

	return []*Component{comp}, nil
}

// warnFormattedValues reports string values that Windows Installer will reinterpret.
//
// The Registry table's Value column is an MSI Formatted field, so a value written
// literally is not written literally: "[Foo]" is substituted (to nothing, if Foo is
// undefined) and "[~]" is the REG_MULTI_SZ separator, which changes the value's TYPE.
// Both were measured while fixing #11 — `a[Foo]b` installed as `ab`, and `a[~]b` as a
// REG_MULTI_SZ of ['a','b'].
//
// Only values written DIRECTLY into that column are affected. A preserved value
// reaches it as "[PS_RV_nnnnn]" and the property's content is substituted in without a
// second formatting pass, so its brackets survive.
//
// Preservation is decided per VALUE, not per component: `preserve="yes"` on the
// <registry> element is not enough, because shouldPreserveValue additionally excludes
// expandable strings, QWORDs and values already starting with "[". Those are written
// literally even in a preserved component, so they are still at risk — and for exactly
// the same reason, recommending preserve="yes" to their author would be useless advice.
//
// A value starting with "[" is otherwise left alone: that is the documented,
// intentional property reference, and warning on it would fire on a large share of real
// packages and teach people to ignore the whole class. But "[~]" is checked FIRST,
// because it is a type marker rather than a property reference and is significant at
// the start of a value too — "[~]a" would otherwise slip through the exemption while
// silently installing as a multi-string.
//
// An MSI escape "[\c]" — a backslash and ONE character between brackets, which
// Formatted resolves to that character — is the remedy this warning recommends, so
// it is neutralised before the check: a value that reads "a[\[]b" has done what was
// asked and installs as "a[b". Neutralised, not deleted: the escape is REPLACED by a
// placeholder character, because deleting it would join its neighbours into a token
// that is not there — "[INSTALLDIR][~[\x]]" minus its escape reads "[INSTALLDIR][~]"
// and would be warned about as a multi-string, although Formatted resolves the inner
// escape to "x" and no separator exists. The .reg spelling of that escape is "[\\[]", because a .reg
// string uses "\\" for one backslash, and the remedy says so: an author who copies the
// MSI form "[\[]" into a .reg file gets "a[[]b", because the parser reads "\[" as an
// escaped "[" — and is then warned again, with the same advice, about the result.
//
// The contract this warning enforces the visible half of — a .reg string is an MSI
// Formatted field and msis does not transform it — is docs/decisions.md D4.
func (p *Processor) warnFormattedValues(key *RegistryKey, componentPreserved bool) {
	for _, val := range key.Values {
		if val.RemoveFlag || val.Type == "multiString" {
			continue
		}
		preservable := shouldPreserveValue(val)
		if componentPreserved && preservable {
			continue // written through a property; brackets survive
		}
		unescaped := msiEscapedChar.ReplaceAllString(val.Value, escapePlaceholder)
		if !strings.ContainsAny(unescaped, "[]") {
			continue
		}

		multiSep := strings.Contains(unescaped, "[~]")
		if !multiSep && strings.HasPrefix(unescaped, "[") {
			continue
		}

		detail := "Windows Installer will substitute [...] references in it"
		if multiSep {
			detail = "Windows Installer reads [~] as the REG_MULTI_SZ separator, so this will install as a multi-string"
		}
		remedy := `Escape a literal bracket as [\[], which in a .reg file is spelled [\\[].`
		if !componentPreserved && preservable {
			remedy += ` Setting preserve="yes" on the <registry> element also protects it, ` +
				`because the value is then written through a property.`
		}
		p.warn("registry value %q in %s is %q: %s. %s",
			displayValueName(val.Name), key.Key, val.Value, detail, remedy)
	}
	for _, sub := range key.SubKeys {
		p.warnFormattedValues(sub, componentPreserved)
	}
}

// msiEscapedChar matches the MSI Formatted escape "[\c]": a backslash and exactly one
// character between brackets, which Windows Installer resolves to that character.
var msiEscapedChar = regexp.MustCompile(`\[\\.\]`)

// escapePlaceholder stands in for an escape during the warning check. It occupies the
// escape's position so the text on either side stays apart, and is none of "[", "]" or
// "~", so it can form no token of its own.
const escapePlaceholder = "_"

// displayValueName names a registry value for a diagnostic, including the unnamed
// default value, which would otherwise print as an empty string.
func displayValueName(name string) string {
	if name == "" {
		return "(default)"
	}
	return name
}

// convertKeyEntry converts a regis3 KeyEntry tree to RegistryKey structures.
func (p *Processor) convertKeyEntry(entry *regis3.KeyEntry) []*RegistryKey {
	var result []*RegistryKey

	// Check if the root is a hive (HKEY_LOCAL_MACHINE, etc.)
	wixRoot := mapHiveToWixRoot(entry.Name())
	if wixRoot != "" {
		// Root is a hive, process its subkeys directly
		keys := p.processHiveRoot(entry)
		result = append(result, keys...)
	} else {
		// Root is a container, look for hive children
		subKeys := entry.SubKeys()
		subKeyNames := make([]string, 0, len(subKeys))
		for name := range subKeys {
			subKeyNames = append(subKeyNames, name)
		}
		sort.Strings(subKeyNames)

		for _, name := range subKeyNames {
			subKey := subKeys[name]
			keys := p.processHiveRoot(subKey)
			result = append(result, keys...)
		}
	}

	return result
}

// processHiveRoot processes a registry hive root (HKEY_LOCAL_MACHINE, etc.)
func (p *Processor) processHiveRoot(entry *regis3.KeyEntry) []*RegistryKey {
	var result []*RegistryKey

	// Map the hive name to WiX root
	wixRoot := mapHiveToWixRoot(entry.Name())
	if wixRoot == "" {
		// Not a recognized hive, skip
		return nil
	}

	// Process subkeys of the hive (the actual registry paths)
	subKeys := entry.SubKeys()
	subKeyNames := make([]string, 0, len(subKeys))
	for name := range subKeys {
		subKeyNames = append(subKeyNames, name)
	}
	sort.Strings(subKeyNames)

	for _, name := range subKeyNames {
		subKey := subKeys[name]
		key := p.convertSubKey(subKey, wixRoot, subKey.Name())
		if key != nil {
			result = append(result, key)
		}
	}

	return result
}

// convertSubKey recursively converts a subkey to a RegistryKey.
func (p *Processor) convertSubKey(entry *regis3.KeyEntry, root, keyPath string) *RegistryKey {
	key := &RegistryKey{
		Root:       root,
		Key:        keyPath,
		RemoveFlag: entry.RemoveFlag(),
	}

	// Process values
	values := entry.Values()
	valueNames := make([]string, 0, len(values))
	for name := range values {
		valueNames = append(valueNames, name)
	}
	sort.Strings(valueNames)

	for _, name := range valueNames {
		val := values[name]
		regVal := p.convertValue(val)
		if regVal != nil {
			key.Values = append(key.Values, regVal)
		}
	}

	// Process default value
	if defVal := entry.DefaultValue(); defVal != nil {
		regVal := p.convertValue(defVal)
		if regVal != nil {
			key.Values = append(key.Values, regVal)
		}
	}

	// Process subkeys recursively
	subKeys := entry.SubKeys()
	subKeyNames := make([]string, 0, len(subKeys))
	for name := range subKeys {
		subKeyNames = append(subKeyNames, name)
	}
	sort.Strings(subKeyNames)

	for _, name := range subKeyNames {
		subKey := subKeys[name]
		childPath := keyPath + "\\" + subKey.Name()
		child := p.convertSubKey(subKey, root, childPath)
		if child != nil {
			key.SubKeys = append(key.SubKeys, child)
		}
	}

	return key
}

// convertValue converts a regis3 ValueEntry to a RegistryValue.
func (p *Processor) convertValue(entry *regis3.ValueEntry) *RegistryValue {
	val := &RegistryValue{
		ID:         p.nextValueIDStr(),
		Name:       entry.Name(),
		RemoveFlag: entry.RemoveFlag(),
	}

	// Map type and extract value
	switch entry.Kind() {
	case regis3.RegSz:
		val.Type = "string"
		val.Value = entry.GetString("")
	case regis3.RegExpandSz:
		val.Type = "expandable"
		val.Value = entry.GetString("")
	case regis3.RegDword:
		val.Type = "integer"
		val.Value = fmt.Sprintf("%d", entry.GetDword(0))
	case regis3.RegQword:
		// MSI cannot express a REG_QWORD: the Registry table's "#N" form and WiX's
		// Type='integer' are both REG_DWORD, and there is no 64-bit counterpart.
		// So a QWORD is deliberately TRUNCATED to its low 32 bits rather than
		// emitted at full width, which produced an out-of-range "#N" that MSI does
		// not define a meaning for. Truncating is lossy and documented as such
		// (docs/tutorial.md, registry value types); it is chosen over rejecting the
		// value so existing packages keep building.
		full := entry.GetQword(0)
		val.Type = "integer"
		val.Value = fmt.Sprintf("%d", uint32(full))
		val.FromQword = true
		if uint64(uint32(full)) != full {
			// Warn only when numeric bits are lost. A QWORD inside 32-bit range still
			// changes TYPE — it installs as a REG_DWORD, because MSI has no 64-bit
			// form at all — but its VALUE is unchanged, and the type narrowing is
			// unavoidable for every QWORD and documented in docs/tutorial.md. Warning
			// on every QWORD would therefore fire on packages where nothing is
			// actionable. Deliberate policy, not an oversight.
			p.warn("registry value %q is a REG_QWORD of %d, which Windows Installer cannot store: "+
				"it will install as a REG_DWORD of %d (the low 32 bits). "+
				"Write 64-bit values from the application or a custom action instead.",
				displayValueName(entry.Name()), full, uint32(full))
		}
	case regis3.RegMultiSz:
		val.Type = "multiString"
		val.MultiValue = entry.GetMultiString()
	case regis3.RegBinary:
		val.Type = "binary"
		val.Value = strings.ToUpper(hex.EncodeToString(entry.Data()))
	case regis3.RegEscapedDword, regis3.RegEscapedQword:
		// Variable substitution - keep as string, will be resolved at install time
		val.Type = "integer"
		val.Value = entry.GetString("")
	default:
		// Unknown type - encode as binary
		val.Type = "binary"
		val.Value = strings.ToUpper(hex.EncodeToString(entry.Data()))
	}

	return val
}

// RemovalEntry represents a registry key or value to be removed.
type RemovalEntry struct {
	IsKey bool   // true for key removal, false for value removal
	Root  string // HKLM, HKCU, etc.
	Key   string // Registry key path
	Name  string // Value name (empty for key removal or default value)
}

// BuildAllPreservedIDs pre-builds preservation ID maps for all components.
// Returns a slice parallel to the components slice, where each entry is either
// nil (non-preserved) or a map of "keyPath+valueName" -> preserve ID.
// This must be called before GenerateXML and GeneratePreservationXML to ensure
// both methods use the same IDs.
func (p *Processor) BuildAllPreservedIDs(components []*Component) []map[string]int {
	result := make([]map[string]int, len(components))
	for i, comp := range components {
		if !comp.Preserve {
			continue
		}
		ids := make(map[string]int)
		for _, key := range comp.Keys {
			if !key.RemoveFlag {
				p.collectPreservedIDs(key, ids)
			}
		}
		result[i] = ids
	}
	return result
}

// GenerateXML generates WiX XML for the components.
func (p *Processor) GenerateXML(components []*Component, setPermissions bool) string {
	return p.GenerateXMLWithPreservedIDs(components, setPermissions, nil)
}

// GenerateXMLWithPreservedIDs generates WiX XML for the components, using pre-built preserved IDs.
func (p *Processor) GenerateXMLWithPreservedIDs(components []*Component, setPermissions bool, allPreservedIDs []map[string]int) string {
	var sb strings.Builder

	for i, comp := range components {
		var preservedIDs map[string]int
		if allPreservedIDs != nil && i < len(allPreservedIDs) {
			preservedIDs = allPreservedIDs[i]
		}
		p.generateComponentXML(comp, &sb, setPermissions, preservedIDs)
	}

	return sb.String()
}

// GeneratePreservationXML generates Property+RegistrySearch elements for preserved registry values.
// These elements must appear at Package level (before components) so that existing registry
// values are read before the install writes new ones. If a value already exists, it's preserved;
// otherwise the default from the .reg file is used.
func (p *Processor) GeneratePreservationXML(components []*Component, allPreservedIDs []map[string]int) string {
	var sb strings.Builder

	for i, comp := range components {
		if !comp.Preserve {
			continue
		}
		var preservedIDs map[string]int
		if allPreservedIDs != nil && i < len(allPreservedIDs) {
			preservedIDs = allPreservedIDs[i]
		}
		if preservedIDs == nil {
			continue
		}
		for _, key := range comp.Keys {
			if !key.RemoveFlag {
				p.generatePreservationPropertiesRecursive(key, key.Root, &sb, preservedIDs)
			}
		}
	}

	return sb.String()
}

// collectPreservedIDs recursively collects preserved value IDs from the key tree.
func (p *Processor) collectPreservedIDs(key *RegistryKey, ids map[string]int) {
	for _, val := range key.Values {
		if val.RemoveFlag {
			continue
		}
		if shouldPreserveValue(val) {
			lookupKey := key.Key + val.Name
			ids[lookupKey] = p.nextPreserveID
			p.nextPreserveID++
		}
	}
	for _, subKey := range key.SubKeys {
		if !subKey.RemoveFlag {
			p.collectPreservedIDs(subKey, ids)
		}
	}
}

// shouldPreserveValue determines if a registry value should be preserved.
// String values starting with "[" are skipped (they're already WiX property references).
// MultiString and expandable values are not preserved — see below.
func shouldPreserveValue(val *RegistryValue) bool {
	if val.RemoveFlag {
		return false
	}
	// Skip multiString - no simple default encoding for preservation
	if val.Type == "multiString" {
		return false
	}
	// Skip REG_EXPAND_SZ. Preservation reads the live value with a Type='raw'
	// RegistrySearch, and that search EXPANDS a REG_EXPAND_SZ and drops its type
	// marker: an install probe showed a live "%TEMP%" arriving in the property as
	// "C:\Users\<name>\AppData\Local\Temp", which was then written back as a plain
	// REG_SZ. Preserving such a value therefore both loses the type and bakes a
	// machine-specific path into the customer's registry. No default encoding can
	// fix that, because the damage happens in the search. Left unpreserved, the
	// value is written normally as a proper, unexpanded REG_EXPAND_SZ; a live edit
	// is overwritten by the .reg default, which is the lesser harm.
	if val.Type == "expandable" {
		return false
	}
	// Skip REG_QWORD, for the same reason and with worse consequences. The raw
	// search returns the QWORD's raw bytes reinterpreted as UTF-16 text: an install
	// probe seeded 0xFEDCBA9876543210 and AppSearch handed back "㈐癔몘ﻜ" — which is
	// exactly those eight bytes read as UTF-16LE — and it was then written back as a
	// REG_SZ. Preserving a live QWORD therefore destroys it. Unpreserved, the .reg
	// value is written as the documented 32-bit truncation instead: lossy, but
	// deterministic and a number rather than mojibake.
	if val.FromQword {
		return false
	}
	// Skip string values starting with "[" (already property references)
	if val.Type == "string" && strings.HasPrefix(val.Value, "[") {
		return false
	}
	return true
}

// generatePreservationPropertiesRecursive walks the key tree and generates
// preservation XML for each preservable value: one PS_RV_XXXXX Property holding
// the .reg file default, with the RegistrySearch nested inside it. AppSearch
// overwrites the default only when the search finds an existing value; a failed
// search leaves the Property table default in place. This is the same two-element
// idiom msis-2.x emits (msi-simplified/WxsItem/RegistryKey.cs).
//
// Deliberately NOT a separate search property plus a SetProperty that copies it:
// every SetProperty is a custom action needing its own sequence number in the
// narrow gap around AppSearch, so a .reg file with a few hundred preserved values
// could not be built at all (WIX0179).
func (p *Processor) generatePreservationPropertiesRecursive(key *RegistryKey, root string, sb *strings.Builder, preservedIDs map[string]int) {
	for _, val := range key.Values {
		if val.RemoveFlag || !shouldPreserveValue(val) {
			continue
		}
		lookupKey := key.Key + val.Name
		id, ok := preservedIDs[lookupKey]
		if !ok {
			continue
		}

		// Encode default value based on type. An empty default omits the Value
		// attribute entirely — WiX rejects Value=''.
		// Secure='yes' lets the searched value survive the client→server handoff
		// during an elevated install.
		defaultValue := encodePreservationDefault(val)
		valueAttr := ""
		if defaultValue != "" {
			valueAttr = fmt.Sprintf(" Value='%s'", escapeXML(defaultValue))
		}
		nameAttr := ""
		if val.Name != "" {
			nameAttr = fmt.Sprintf(" Name='%s'", escapeXML(val.Name))
		}

		sb.WriteString(fmt.Sprintf("    <Property Id='PS_RV_%05d'%s Secure='yes'>\n", id, valueAttr))
		sb.WriteString(fmt.Sprintf("        <RegistrySearch Id='PS_RV_%05d_Registry' Type='raw' Root='%s' Key='%s'%s/>\n",
			id, root, escapeXML(key.Key), nameAttr))
		sb.WriteString("    </Property>\n")
	}

	for _, subKey := range key.SubKeys {
		if !subKey.RemoveFlag {
			p.generatePreservationPropertiesRecursive(subKey, root, sb, preservedIDs)
		}
	}
}

// encodePreservationDefault encodes a registry value's default for a WiX Property Value attribute.
func encodePreservationDefault(val *RegistryValue) string {
	switch val.Type {
	case "integer":
		// DWord/QWord: prefix with # (e.g., "#3")
		return "#" + val.Value
	case "binary":
		// Binary: a single #x prefix followed by the hex bytes ("#x010203").
		// val.Value is an uppercase hex string like "010203". This is the MSI
		// Registry table format, and the same shape a Type='raw' RegistrySearch
		// returns for an existing REG_BINARY, so default and preserved value agree.
		//
		// Zero bytes is "#x" with nothing after it, NOT an omitted Value attribute.
		// Verified by install probe: omitting it leaves the property undefined, the
		// write has no type marker left, and MSI stores an empty REG_SZ instead of
		// an empty REG_BINARY. A bare "#x" stores REG_BINARY with zero bytes.
		return "#x" + val.Value
	case "string":
		// SZ: literal value (empty string → omit the Value attribute), except that
		// a leading '#' is MSI's Registry-table type marker and a literal one must
		// be doubled. Unescaped, "#FF0000" fails the install with Error 1406 and
		// rolls back (issue #11).
		//
		// "##" is MSI's own convention, not a guess, confirmed from both ends:
		// WiX stores "##FF0000" for the same value on the non-preserved path, and a
		// Type='raw' search over a live REG_SZ of "#00FF00" hands back "##00FF00".
		// Doubling here makes the preserved and non-preserved paths agree.
		//
		// Only the first character is special; '#' elsewhere is literal.
		// REG_EXPAND_SZ never reaches here — shouldPreserveValue excludes it.
		if strings.HasPrefix(val.Value, "#") {
			return "#" + val.Value
		}
		return val.Value
	default:
		return val.Value
	}
}

func (p *Processor) generateComponentXML(comp *Component, sb *strings.Builder, setPermissions bool, preservedIDs map[string]int) {
	// Collect all removal entries first (they go at component level in WiX 6)
	var removals []RemovalEntry
	for _, key := range comp.Keys {
		p.collectRemovals(key, &removals)
	}

	// Component attributes - KeyPath must be on a RegistryValue, not Component
	attrs := fmt.Sprintf("Id='%s' Guid='%s'", comp.ID, comp.GUID)
	if comp.Permanent {
		attrs += " Permanent='yes'"
	}
	// NOTE: We intentionally do NOT emit NeverOverwrite for preserved components.
	// Preservation is handled entirely by the RegistrySearch -> PS_RV -> Value='[PS_RV_xxxxx]'
	// mechanism (read the live value, write it back; fall back to the .reg default on fresh install).
	// NeverOverwrite is both redundant with that mechanism and actively harmful during major
	// upgrades: per MSI semantics it "only affects the action state of the component during
	// costing", so when the keypath already exists (previous version installed), the component is
	// marked do-not-install BEFORE RemoveExistingProducts runs. RemoveExistingProducts then wipes
	// the old values and the new component never rewrites them, so preserved values vanish on
	// upgrade. The PS_RV mechanism alone is the correct, upgrade-safe preservation.
	if comp.Condition != "" {
		attrs += fmt.Sprintf(" Condition='%s'", escapeXML(comp.Condition))
	}

	sb.WriteString(fmt.Sprintf("        <Component %s>\n", attrs))

	// Emit removal entries at component level (WiX 6 requirement)
	for _, removal := range removals {
		indent := "            "
		if removal.IsKey {
			sb.WriteString(fmt.Sprintf("%s<RemoveRegistryKey Action='removeOnInstall' Root='%s' Key='%s'/>\n",
				indent, removal.Root, escapeXML(removal.Key)))
		} else {
			nameAttr := ""
			if removal.Name != "" {
				nameAttr = fmt.Sprintf(" Name='%s'", escapeXML(removal.Name))
			}
			sb.WriteString(fmt.Sprintf("%s<RemoveRegistryValue Root='%s' Key='%s'%s/>\n",
				indent, removal.Root, escapeXML(removal.Key), nameAttr))
		}
	}

	// Generate registry keys (additions only, with KeyPath on first value)
	isFirstValue := true
	for _, key := range comp.Keys {
		if !key.RemoveFlag {
			p.generateRegistryKeyXML(key, sb, comp.SDDL, setPermissions, !comp.Permanent, 3, &isFirstValue, preservedIDs)
		}
	}

	// If no RegistryValue was emitted (delete-only .reg file), add a dummy keypath value
	// WiX/MSI requires every component to have a KeyPath resource
	if isFirstValue {
		// Find the first non-removal key to use as the keypath location
		root, keyPath := p.findFirstKeyPath(comp.Keys)
		if root != "" && keyPath != "" {
			sb.WriteString(fmt.Sprintf("            <RegistryValue Root='%s' Key='%s' Name='_msis_keypath' Value='' Type='string' KeyPath='yes'/>\n",
				root, escapeXML(keyPath)))
		}
	}

	sb.WriteString("        </Component>\n")
}

// collectRemovals recursively collects all removal entries from the key tree.
func (p *Processor) collectRemovals(key *RegistryKey, removals *[]RemovalEntry) {
	if key.RemoveFlag {
		*removals = append(*removals, RemovalEntry{
			IsKey: true,
			Root:  key.Root,
			Key:   key.Key,
		})
		return // Don't process children of removed keys
	}

	// Collect value removals
	for _, val := range key.Values {
		if val.RemoveFlag {
			*removals = append(*removals, RemovalEntry{
				IsKey: false,
				Root:  key.Root,
				Key:   key.Key,
				Name:  val.Name,
			})
		}
	}

	// Recurse into subkeys
	for _, subKey := range key.SubKeys {
		p.collectRemovals(subKey, removals)
	}
}

func (p *Processor) generateRegistryKeyXML(key *RegistryKey, sb *strings.Builder, sddl string, setPermissions bool, canForceDelete bool, depth int, isFirstValue *bool, preservedIDs map[string]int) {
	indent := strings.Repeat("    ", depth)

	if key.RemoveFlag {
		// Removals are handled at component level
		return
	}

	// Only add ForceDeleteOnUninstall to empty keys (no values in this key or any subkeys).
	// Keys with values are left alone — MSI removes individual values it created, and
	// runtime-created values (not in the .reg file) are preserved.
	deleteAttr := ""
	if canForceDelete && !keyTreeHasValues(key) {
		deleteAttr = " ForceDeleteOnUninstall='yes'"
	}
	sb.WriteString(fmt.Sprintf("%s<RegistryKey Root='%s' Key='%s' ForceCreateOnInstall='yes'%s>\n",
		indent, key.Root, escapeXML(key.Key), deleteAttr))

	// Add permissions if enabled
	// Note: Use core WiX PermissionEx (not util:PermissionEx) for Sddl attribute on registry keys
	if setPermissions && sddl != "" {
		sb.WriteString(fmt.Sprintf("%s    <PermissionEx Sddl='%s'/>\n", indent, escapeXML(sddl)))
	}

	// Generate values (skip removals, they're at component level)
	for _, val := range key.Values {
		if !val.RemoveFlag {
			p.generateRegistryValueXML(val, key, sb, depth+1, isFirstValue, preservedIDs)
		}
	}

	// Generate subkeys
	for _, subKey := range key.SubKeys {
		// Subkeys don't repeat Root
		p.generateSubKeyXML(subKey, sb, sddl, setPermissions, canForceDelete, depth+1, isFirstValue, preservedIDs)
	}

	sb.WriteString(fmt.Sprintf("%s</RegistryKey>\n", indent))
}

func (p *Processor) generateSubKeyXML(key *RegistryKey, sb *strings.Builder, sddl string, setPermissions bool, canForceDelete bool, depth int, isFirstValue *bool, preservedIDs map[string]int) {
	indent := strings.Repeat("    ", depth)

	if key.RemoveFlag {
		// Removals are handled at component level
		return
	}

	// Extract just the last part of the key path for nested keys
	parts := strings.Split(key.Key, "\\")
	keyName := parts[len(parts)-1]

	// Only add ForceDeleteOnUninstall to empty keys (no values in this key or any subkeys)
	deleteAttr := ""
	if canForceDelete && !keyTreeHasValues(key) {
		deleteAttr = " ForceDeleteOnUninstall='yes'"
	}
	sb.WriteString(fmt.Sprintf("%s<RegistryKey Key='%s' ForceCreateOnInstall='yes'%s>\n",
		indent, escapeXML(keyName), deleteAttr))

	// Add permissions if enabled (core WiX PermissionEx, not util:PermissionEx)
	if setPermissions && sddl != "" {
		sb.WriteString(fmt.Sprintf("%s    <PermissionEx Sddl='%s'/>\n", indent, escapeXML(sddl)))
	}

	// Generate values (skip removals, they're at component level)
	for _, val := range key.Values {
		if !val.RemoveFlag {
			p.generateRegistryValueXML(val, key, sb, depth+1, isFirstValue, preservedIDs)
		}
	}

	// Generate subkeys
	for _, subKey := range key.SubKeys {
		p.generateSubKeyXML(subKey, sb, sddl, setPermissions, canForceDelete, depth+1, isFirstValue, preservedIDs)
	}

	sb.WriteString(fmt.Sprintf("%s</RegistryKey>\n", indent))
}

func (p *Processor) generateRegistryValueXML(val *RegistryValue, key *RegistryKey, sb *strings.Builder, depth int, isFirstValue *bool, preservedIDs map[string]int) {
	indent := strings.Repeat("    ", depth)

	if val.RemoveFlag {
		// Removals are handled at component level
		return
	}

	// Name attribute (empty for default value)
	nameAttr := ""
	if val.Name != "" {
		nameAttr = fmt.Sprintf(" Name='%s'", escapeXML(val.Name))
	}

	// KeyPath goes on the first RegistryValue (WiX 6 requirement)
	keyPathAttr := ""
	if *isFirstValue {
		keyPathAttr = " KeyPath='yes'"
		*isFirstValue = false
	}

	// Check if this value has a preservation property reference
	if preservedIDs != nil {
		lookupKey := key.Key + val.Name
		if id, ok := preservedIDs[lookupKey]; ok {
			// Emit reference to preservation property instead of literal value
			// Type is always 'string' — WiX interprets the prefixed content from the property
			sb.WriteString(fmt.Sprintf("%s<RegistryValue%s Value='[PS_RV_%05d]' Type='string'%s/>\n",
				indent, nameAttr, id, keyPathAttr))
			return
		}
	}

	if val.Type == "multiString" {
		// MultiString needs child elements
		sb.WriteString(fmt.Sprintf("%s<RegistryValue%s Type='%s'%s>\n", indent, nameAttr, val.Type, keyPathAttr))
		for _, s := range val.MultiValue {
			sb.WriteString(fmt.Sprintf("%s    <MultiStringValue>%s</MultiStringValue>\n", indent, escapeXML(s)))
		}
		sb.WriteString(fmt.Sprintf("%s</RegistryValue>\n", indent))
	} else {
		// Simple value, written verbatim: XML-escaped and nothing else. The Registry
		// table's Value column is an MSI Formatted field, so Windows Installer will
		// substitute [Foo], split on [~] and resolve [\[] in whatever lands here. msis
		// deliberately does not escape or rewrite on the author's behalf — "[" means
		// two different things and only the author knows which (docs/decisions.md D4);
		// warnFormattedValues reports the cases that will be reinterpreted.
		sb.WriteString(fmt.Sprintf("%s<RegistryValue%s Value='%s' Type='%s'%s/>\n",
			indent, nameAttr, escapeXML(val.Value), val.Type, keyPathAttr))
	}
}

// keyTreeHasValues returns true if the key or any of its subkeys contain non-removal values.
// Keys without values are "structural" (empty) and safe to ForceDeleteOnUninstall.
// Keys with values may have runtime-created siblings that must not be deleted.
func keyTreeHasValues(key *RegistryKey) bool {
	for _, val := range key.Values {
		if !val.RemoveFlag {
			return true
		}
	}
	for _, subKey := range key.SubKeys {
		if !subKey.RemoveFlag && keyTreeHasValues(subKey) {
			return true
		}
	}
	return false
}

// findFirstKeyPath finds the first non-removal key to use for a dummy keypath.
// Returns root and key path, or empty strings if no suitable key found.
func (p *Processor) findFirstKeyPath(keys []*RegistryKey) (string, string) {
	for _, key := range keys {
		if !key.RemoveFlag {
			return key.Root, key.Key
		}
		// Check subkeys recursively
		if root, path := p.findFirstKeyPathInSubKeys(key.SubKeys); root != "" {
			return root, path
		}
	}
	return "", ""
}

// findFirstKeyPathInSubKeys recursively searches subkeys for a non-removal key.
func (p *Processor) findFirstKeyPathInSubKeys(keys []*RegistryKey) (string, string) {
	for _, key := range keys {
		if !key.RemoveFlag {
			return key.Root, key.Key
		}
		if root, path := p.findFirstKeyPathInSubKeys(key.SubKeys); root != "" {
			return root, path
		}
	}
	return "", ""
}

// nextComponentIDStr generates a unique component ID.
func (p *Processor) nextComponentIDStr() string {
	id := fmt.Sprintf("REG_CID_%05d", p.componentCounter)
	p.componentCounter++
	return id
}

// nextValueIDStr generates a unique value ID string.
func (p *Processor) nextValueIDStr() string {
	id := fmt.Sprintf("RV_%05d", p.nextValueID)
	p.nextValueID++
	return id
}

// mapHiveToWixRoot maps a registry hive name to WiX root identifier.
func mapHiveToWixRoot(name string) string {
	switch strings.ToUpper(name) {
	case "HKEY_LOCAL_MACHINE":
		return "HKLM"
	case "HKEY_CURRENT_USER":
		return "HKCU"
	case "HKEY_CLASSES_ROOT":
		return "HKCR"
	case "HKEY_USERS":
		return "HKU"
	case "HKEY_CURRENT_CONFIG":
		return "HKCC"
	default:
		return ""
	}
}

// generateGUID creates a deterministic GUID from a path.
func generateGUID(path string) string {
	hash := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(hash[0:4]),
		hex.EncodeToString(hash[4:6]),
		hex.EncodeToString(hash[6:8]),
		hex.EncodeToString(hash[8:10]),
		hex.EncodeToString(hash[10:16]))
}

// escapeXML escapes special characters for XML attributes.
func escapeXML(s string) string {
	var buf bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			buf.WriteString("&amp;")
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		case '"':
			buf.WriteString("&quot;")
		case '\'':
			buf.WriteString("&apos;")
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
