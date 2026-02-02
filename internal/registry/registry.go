// Package registry processes .reg files and generates WiX registry components.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
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
	Name       string // Empty for default value
	Type       string // string, integer, binary, expandable, multiString
	Value      string // For simple types
	MultiValue []string // For multiString type
	RemoveFlag bool
}

// Processor handles registry file parsing and WiX generation.
type Processor struct {
	workDir          string
	nextKeyID        int
	nextValueID      int
	componentCounter int
	componentIDs     map[string]bool
}

// NewProcessor creates a new registry processor.
func NewProcessor(workDir string) *Processor {
	return &Processor{
		workDir:      workDir,
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
	guid := generateGUID("registry_" + reg.File)

	comp := &Component{
		ID:        compID,
		GUID:      guid,
		Permanent: reg.Permanent,
		Condition: reg.Condition,
		SDDL:      sddl,
	}

	// Convert regis3 tree to our RegistryKey structure
	comp.Keys = p.convertKeyEntry(root)

	return []*Component{comp}, nil
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
		val.Type = "integer"
		val.Value = fmt.Sprintf("%d", entry.GetQword(0))
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

// GenerateXML generates WiX XML for the components.
func (p *Processor) GenerateXML(components []*Component, setPermissions bool) string {
	var sb strings.Builder

	for _, comp := range components {
		p.generateComponentXML(comp, &sb, setPermissions)
	}

	return sb.String()
}

func (p *Processor) generateComponentXML(comp *Component, sb *strings.Builder, setPermissions bool) {
	// Collect all removal entries first (they go at component level in WiX 6)
	var removals []RemovalEntry
	for _, key := range comp.Keys {
		p.collectRemovals(key, &removals)
	}

	// Component attributes - KeyPath must be on a RegistryValue, not Component
	attrs := fmt.Sprintf("Id='%s' Guid='%s' NeverOverwrite='yes'", comp.ID, comp.GUID)
	if comp.Permanent {
		attrs += " Permanent='yes'"
	}
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
			p.generateRegistryKeyXML(key, sb, comp.SDDL, setPermissions, 3, &isFirstValue)
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

func (p *Processor) generateRegistryKeyXML(key *RegistryKey, sb *strings.Builder, sddl string, setPermissions bool, depth int, isFirstValue *bool) {
	indent := strings.Repeat("    ", depth)

	if key.RemoveFlag {
		// Removals are handled at component level
		return
	}

	// Open RegistryKey
	sb.WriteString(fmt.Sprintf("%s<RegistryKey Root='%s' Key='%s' ForceCreateOnInstall='yes'>\n",
		indent, key.Root, escapeXML(key.Key)))

	// Add permissions if enabled (use util: namespace for WiX 6)
	if setPermissions && sddl != "" {
		sb.WriteString(fmt.Sprintf("%s    <util:PermissionEx Sddl='%s'/>\n", indent, escapeXML(sddl)))
	}

	// Generate values (skip removals, they're at component level)
	for _, val := range key.Values {
		if !val.RemoveFlag {
			p.generateRegistryValueXML(val, sb, depth+1, isFirstValue)
		}
	}

	// Generate subkeys
	for _, subKey := range key.SubKeys {
		// Subkeys don't repeat Root
		p.generateSubKeyXML(subKey, sb, sddl, setPermissions, depth+1, isFirstValue)
	}

	sb.WriteString(fmt.Sprintf("%s</RegistryKey>\n", indent))
}

func (p *Processor) generateSubKeyXML(key *RegistryKey, sb *strings.Builder, sddl string, setPermissions bool, depth int, isFirstValue *bool) {
	indent := strings.Repeat("    ", depth)

	if key.RemoveFlag {
		// Removals are handled at component level
		return
	}

	// Extract just the last part of the key path for nested keys
	parts := strings.Split(key.Key, "\\")
	keyName := parts[len(parts)-1]

	sb.WriteString(fmt.Sprintf("%s<RegistryKey Key='%s' ForceCreateOnInstall='yes'>\n",
		indent, escapeXML(keyName)))

	// Add permissions if enabled (use util: namespace for WiX 6)
	if setPermissions && sddl != "" {
		sb.WriteString(fmt.Sprintf("%s    <util:PermissionEx Sddl='%s'/>\n", indent, escapeXML(sddl)))
	}

	// Generate values (skip removals, they're at component level)
	for _, val := range key.Values {
		if !val.RemoveFlag {
			p.generateRegistryValueXML(val, sb, depth+1, isFirstValue)
		}
	}

	// Generate subkeys
	for _, subKey := range key.SubKeys {
		p.generateSubKeyXML(subKey, sb, sddl, setPermissions, depth+1, isFirstValue)
	}

	sb.WriteString(fmt.Sprintf("%s</RegistryKey>\n", indent))
}

func (p *Processor) generateRegistryValueXML(val *RegistryValue, sb *strings.Builder, depth int, isFirstValue *bool) {
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

	if val.Type == "multiString" {
		// MultiString needs child elements
		sb.WriteString(fmt.Sprintf("%s<RegistryValue%s Type='%s'%s>\n", indent, nameAttr, val.Type, keyPathAttr))
		for _, s := range val.MultiValue {
			sb.WriteString(fmt.Sprintf("%s    <MultiStringValue>%s</MultiStringValue>\n", indent, escapeXML(s)))
		}
		sb.WriteString(fmt.Sprintf("%s</RegistryValue>\n", indent))
	} else {
		// Simple value
		sb.WriteString(fmt.Sprintf("%s<RegistryValue%s Value='%s' Type='%s'%s/>\n",
			indent, nameAttr, escapeXML(val.Value), val.Type, keyPathAttr))
	}
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
