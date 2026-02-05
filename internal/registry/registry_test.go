package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
)

func TestProcessBasicRegistry(t *testing.T) {
	// Create a temp .reg file
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp]
"Version"="1.0.0"
"Count"=dword:00000005
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "test.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{
		File: "test.reg",
	}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if len(components) != 1 {
		t.Fatalf("Expected 1 component, got %d", len(components))
	}

	comp := components[0]
	if comp.ID == "" {
		t.Error("Component ID should not be empty")
	}
	if comp.GUID == "" {
		t.Error("Component GUID should not be empty")
	}
	if comp.SDDL != DefaultSDDL {
		t.Errorf("Expected default SDDL, got %s", comp.SDDL)
	}

	if len(comp.Keys) == 0 {
		t.Fatal("Expected at least one registry key")
	}

	// Verify the key structure - keys are nested hierarchically
	key := comp.Keys[0]
	if key.Root != "HKLM" {
		t.Errorf("Expected root HKLM, got %s", key.Root)
	}

	// Find the TestApp key by traversing the hierarchy
	found := findKeyWithValues(key)
	if found == nil {
		t.Fatal("Could not find key with values")
	}
	if !strings.Contains(found.Key, "TestApp") {
		t.Errorf("Expected key path to contain TestApp, got %s", found.Key)
	}
	if len(found.Values) < 2 {
		t.Errorf("Expected at least 2 values, got %d", len(found.Values))
	}
}

// findKeyWithValues recursively finds a key that has values
func findKeyWithValues(key *RegistryKey) *RegistryKey {
	if len(key.Values) > 0 {
		return key
	}
	for _, sub := range key.SubKeys {
		if found := findKeyWithValues(sub); found != nil {
			return found
		}
	}
	return nil
}

func TestProcessRegistryWithAttributes(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_CURRENT_USER\Software\MyApp]
"Setting"="Value"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "settings.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{
		File:      "settings.reg",
		SDDL:      "D:(A;;GA;;;WD)",
		Permanent: true,
		Condition: "INSTALL_FEATURE",
	}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if len(components) != 1 {
		t.Fatalf("Expected 1 component, got %d", len(components))
	}

	comp := components[0]
	if comp.SDDL != "D:(A;;GA;;;WD)" {
		t.Errorf("Expected custom SDDL, got %s", comp.SDDL)
	}
	if !comp.Permanent {
		t.Error("Expected Permanent to be true")
	}
	if comp.Condition != "INSTALL_FEATURE" {
		t.Errorf("Expected condition INSTALL_FEATURE, got %s", comp.Condition)
	}
}

func TestProcessRegistryValueTypes(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\Types]
"StringVal"="Hello"
"DwordVal"=dword:0000000a
"BinaryVal"=hex:01,02,03
"ExpandVal"=hex(2):25,00,50,00,41,00,54,00,48,00,25,00,00,00
"MultiVal"=hex(7):41,00,00,00,42,00,00,00,00,00
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "types.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "types.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if len(components) != 1 {
		t.Fatalf("Expected 1 component, got %d", len(components))
	}

	// Find the key with values (traverse hierarchy)
	foundKey := findKeyWithValues(components[0].Keys[0])
	if foundKey == nil {
		t.Fatal("Could not find key with values")
	}

	// Verify we have multiple values
	if len(foundKey.Values) < 4 {
		t.Errorf("Expected at least 4 values, got %d", len(foundKey.Values))
	}

	// Check types
	typeMap := make(map[string]string)
	for _, val := range foundKey.Values {
		typeMap[val.Name] = val.Type
	}

	if typeMap["StringVal"] != "string" {
		t.Errorf("StringVal type mismatch: %s", typeMap["StringVal"])
	}
	if typeMap["DwordVal"] != "integer" {
		t.Errorf("DwordVal type mismatch: %s", typeMap["DwordVal"])
	}
	if typeMap["BinaryVal"] != "binary" {
		t.Errorf("BinaryVal type mismatch: %s", typeMap["BinaryVal"])
	}
	if typeMap["ExpandVal"] != "expandable" {
		t.Errorf("ExpandVal type mismatch: %s", typeMap["ExpandVal"])
	}
	if typeMap["MultiVal"] != "multiString" {
		t.Errorf("MultiVal type mismatch: %s", typeMap["MultiVal"])
	}
}

func TestGenerateXML(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp]
"Version"="1.0"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "test.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "test.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	// Verify XML contains expected elements
	if !strings.Contains(xml, "<Component") {
		t.Error("XML should contain <Component>")
	}
	if !strings.Contains(xml, "<RegistryKey") {
		t.Error("XML should contain <RegistryKey>")
	}
	if !strings.Contains(xml, "<RegistryValue") {
		t.Error("XML should contain <RegistryValue>")
	}
	if !strings.Contains(xml, "Root='HKLM'") {
		t.Error("XML should contain Root='HKLM'")
	}
	if !strings.Contains(xml, "Type='string'") {
		t.Error("XML should contain Type='string'")
	}
}

func TestGenerateXMLWithPermissions(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\SecureApp]
"Data"="Secret"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "secure.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{
		File: "secure.reg",
		SDDL: "D:(A;;GA;;;WD)",
	}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Without permissions
	xmlNoPerm := proc.GenerateXML(components, false)
	if strings.Contains(xmlNoPerm, "PermissionEx") {
		t.Error("XML without permissions should not contain PermissionEx")
	}

	// With permissions (uses core PermissionEx for SDDL support in WiX 6)
	xmlWithPerm := proc.GenerateXML(components, true)
	if !strings.Contains(xmlWithPerm, "<PermissionEx") {
		t.Error("XML with permissions should contain <PermissionEx>")
	}
	if !strings.Contains(xmlWithPerm, "D:(A;;GA;;;WD)") {
		t.Error("XML should contain the SDDL string")
	}
}

func TestMapHiveToWixRoot(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HKEY_LOCAL_MACHINE", "HKLM"},
		{"HKEY_CURRENT_USER", "HKCU"},
		{"HKEY_CLASSES_ROOT", "HKCR"},
		{"HKEY_USERS", "HKU"},
		{"HKEY_CURRENT_CONFIG", "HKCC"},
		{"hkey_local_machine", "HKLM"}, // case insensitive
		{"UNKNOWN", ""},
	}

	for _, tt := range tests {
		result := mapHiveToWixRoot(tt.input)
		if result != tt.expected {
			t.Errorf("mapHiveToWixRoot(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestEscapeXML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"<tag>", "&lt;tag&gt;"},
		{"a & b", "a &amp; b"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"it's", "it&apos;s"},
		{`<a href="test">link</a>`, "&lt;a href=&quot;test&quot;&gt;link&lt;/a&gt;"},
	}

	for _, tt := range tests {
		result := escapeXML(tt.input)
		if result != tt.expected {
			t.Errorf("escapeXML(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestDeleteMarkers(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[-HKEY_LOCAL_MACHINE\SOFTWARE\DeleteMe]

[HKEY_LOCAL_MACHINE\SOFTWARE\KeepMe]
"OldValue"=-
"NewValue"="Keep"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "delete.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "delete.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	// Should have RemoveRegistryKey for deleted key
	if !strings.Contains(xml, "<RemoveRegistryKey") {
		t.Error("XML should contain <RemoveRegistryKey> for deleted key")
	}

	// Should have RemoveRegistryValue for deleted value
	if !strings.Contains(xml, "<RemoveRegistryValue") {
		t.Error("XML should contain <RemoveRegistryValue> for deleted value")
	}

	// RemoveRegistryValue should have Root and Key attributes (WiX 6 requirement)
	if !strings.Contains(xml, "RemoveRegistryValue Root='HKLM' Key='SOFTWARE") {
		t.Error("RemoveRegistryValue should have Root and Key attributes")
	}
}

func TestKeyPathOnRegistryValue(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp]
"Version"="1.0"
"Name"="Test"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "keypath.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "keypath.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	// KeyPath should NOT be on Component (WiX 6 invalid)
	if strings.Contains(xml, "<Component") && strings.Contains(xml, "KeyPath='yes'") {
		// Need more precise check
		if strings.Contains(xml, "<Component Id='") && strings.Contains(xml, "KeyPath='yes' NeverOverwrite") {
			t.Error("KeyPath should not be on Component element")
		}
	}

	// KeyPath should be on a RegistryValue
	if !strings.Contains(xml, "<RegistryValue") || !strings.Contains(xml, "KeyPath='yes'") {
		t.Error("KeyPath should be on a RegistryValue element")
	}

	// Only one KeyPath='yes' should exist
	count := strings.Count(xml, "KeyPath='yes'")
	if count != 1 {
		t.Errorf("Expected exactly 1 KeyPath='yes', found %d", count)
	}
}

func TestDeleteOnlyRegFile(t *testing.T) {
	// A .reg file with only deletions should still generate valid WiX XML
	// with a dummy RegistryValue for KeyPath
	content := `Windows Registry Editor Version 5.00

[-HKEY_LOCAL_MACHINE\SOFTWARE\OldApp]

[HKEY_LOCAL_MACHINE\SOFTWARE\AnotherApp]
"OldSetting"=-
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "deleteonly.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "deleteonly.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	// Should have RemoveRegistryKey for the deleted key
	if !strings.Contains(xml, "<RemoveRegistryKey") {
		t.Error("XML should contain <RemoveRegistryKey>")
	}

	// Should have RemoveRegistryValue for the deleted value
	if !strings.Contains(xml, "<RemoveRegistryValue") {
		t.Error("XML should contain <RemoveRegistryValue>")
	}

	// Should have a KeyPath somewhere (dummy value for delete-only case)
	if !strings.Contains(xml, "KeyPath='yes'") {
		t.Error("XML should contain KeyPath='yes' for WiX component requirement")
	}

	// The dummy keypath value should have a recognizable marker
	if !strings.Contains(xml, "_msis_keypath") {
		t.Error("XML should contain dummy _msis_keypath value for delete-only components")
	}
}

func TestRemoveRegistryValueAtComponentLevel(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\Test\SubKey]
"DeleteThis"=-
"KeepThis"="Value"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "removevalue.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "removevalue.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	// RemoveRegistryValue should NOT be inside RegistryKey (it should be at component level)
	// Check that RemoveRegistryValue appears before the first RegistryKey
	removeIdx := strings.Index(xml, "<RemoveRegistryValue")
	regKeyIdx := strings.Index(xml, "<RegistryKey")

	if removeIdx == -1 {
		t.Fatal("XML should contain RemoveRegistryValue")
	}
	if regKeyIdx == -1 {
		t.Fatal("XML should contain RegistryKey")
	}

	if removeIdx > regKeyIdx {
		t.Error("RemoveRegistryValue should appear before RegistryKey (at component level)")
	}

	// RemoveRegistryValue should have complete path info
	if !strings.Contains(xml, "Root='HKLM'") {
		t.Error("RemoveRegistryValue should have Root attribute")
	}
	if !strings.Contains(xml, "Key='SOFTWARE\\Test\\SubKey'") {
		t.Error("RemoveRegistryValue should have Key attribute with full path")
	}
}
