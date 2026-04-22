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
	// Check that no Component element has KeyPath attribute
	for _, line := range strings.Split(xml, "\n") {
		if strings.Contains(line, "<Component ") && strings.Contains(line, "KeyPath='yes'") {
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

func TestPreserveBasicStringValues(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"LogFile"="C:\\Logs\\app.log"
"Description"="My Application"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "preserve.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "preserve.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if !components[0].Preserve {
		t.Error("Component should have Preserve=true")
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	// Should have default Property (PS_RV_) and search Property (PS_RS_)
	if !strings.Contains(xml, "<Property Id='PS_RV_") {
		t.Error("Preservation XML should contain default Property elements")
	}
	if !strings.Contains(xml, "<Property Id='PS_RS_") {
		t.Error("Preservation XML should contain search Property elements")
	}
	if !strings.Contains(xml, "<RegistrySearch") {
		t.Error("Preservation XML should contain RegistrySearch elements")
	}
	if !strings.Contains(xml, "Type='raw'") {
		t.Error("RegistrySearch should have Type='raw'")
	}
	if !strings.Contains(xml, "Root='HKLM'") {
		t.Error("RegistrySearch should have Root='HKLM'")
	}
	// Default values should be string literals on PS_RV_ (not PS_RS_)
	if !strings.Contains(xml, "Value='C:\\Logs\\app.log'") {
		t.Error("Default Property should have string default value")
	}
	// Should have SetProperty to conditionally override
	if !strings.Contains(xml, "<SetProperty") {
		t.Error("Preservation XML should contain SetProperty elements")
	}
	if !strings.Contains(xml, "After='AppSearch'") {
		t.Error("SetProperty should run After='AppSearch'")
	}
	// Properties must be Secure to survive client→server handoff in elevated installs
	if !strings.Contains(xml, "Secure='yes'") {
		t.Error("Preservation properties should have Secure='yes'")
	}
}

func TestPreserveDwordValues(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"LogLevel"=dword:00000003
"MaxRetries"=dword:0000000a
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "dword.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "dword.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	// DWord defaults should be prefixed with #
	if !strings.Contains(xml, "Value='#3'") {
		t.Errorf("DWord value should have #-prefixed default, got:\n%s", xml)
	}
	if !strings.Contains(xml, "Value='#10'") {
		t.Errorf("DWord value 0x0a should be '#10', got:\n%s", xml)
	}
}

func TestPreserveBinaryValues(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"WindowPos"=hex:4f,4b
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "binary.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "binary.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	// Binary should be encoded as #xH#xH per nibble (matching MSIS2 format)
	if !strings.Contains(xml, "#x4#xF#x4#xB") {
		t.Errorf("Binary value should have #xH#xH encoding, got:\n%s", xml)
	}
}

func TestPreserveRegistryValueReferences(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"LogLevel"=dword:00000003
"AppName"="TestApp"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "refs.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "refs.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GenerateXMLWithPreservedIDs(components, false, allIDs)

	// RegistryValue elements should reference [PS_RV_XXXXX] instead of literal values
	if !strings.Contains(xml, "[PS_RV_") {
		t.Errorf("Registry XML should contain PS_RV_ property references, got:\n%s", xml)
	}
	// Type should be 'string' for preserved values (WiX interprets the prefixed content)
	if count := strings.Count(xml, "Type='string'"); count < 2 {
		t.Errorf("Expected at least 2 Type='string' attributes for preserved values, got %d", count)
	}
	// Should NOT contain the literal default values in RegistryValue elements
	if strings.Contains(xml, "Value='3'") || strings.Contains(xml, "Value='TestApp'") {
		t.Errorf("Registry XML should not contain literal values for preserved items, got:\n%s", xml)
	}
}

func TestPreserveNeverOverwrite(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Setting"="Default"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "neveroverwrite.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "neveroverwrite.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GenerateXMLWithPreservedIDs(components, false, allIDs)

	if !strings.Contains(xml, "NeverOverwrite='yes'") {
		t.Errorf("Preserved component should have NeverOverwrite='yes', got:\n%s", xml)
	}
}

func TestNonPreservedRegistryUnchanged(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Version"="1.0"
"Count"=dword:00000005
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "nopreserve.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "nopreserve.reg", Preserve: false}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if components[0].Preserve {
		t.Error("Component should have Preserve=false")
	}

	// No preservation XML should be generated
	allIDs := proc.BuildAllPreservedIDs(components)
	preserveXML := proc.GeneratePreservationXML(components, allIDs)
	if preserveXML != "" {
		t.Errorf("Non-preserved components should produce no preservation XML, got:\n%s", preserveXML)
	}

	// Registry XML should use literal values, not PS_RV_ references
	xml := proc.GenerateXMLWithPreservedIDs(components, false, allIDs)
	if strings.Contains(xml, "PS_RV_") {
		t.Errorf("Non-preserved registry XML should not contain PS_RV_ references, got:\n%s", xml)
	}
	if strings.Contains(xml, "NeverOverwrite") {
		t.Errorf("Non-preserved component should not have NeverOverwrite, got:\n%s", xml)
	}
	// Should contain literal values
	if !strings.Contains(xml, "Value='1.0'") {
		t.Errorf("Non-preserved XML should contain literal string value, got:\n%s", xml)
	}
}

func TestPreserveSkipsPropertyReferences(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"InstallDir"="[INSTALLDIR]"
"NormalValue"="Hello"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "propref.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "propref.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	preserveXML := proc.GeneratePreservationXML(components, allIDs)

	// Should NOT have a preservation property for "[INSTALLDIR]" (starts with "[")
	if strings.Contains(preserveXML, "INSTALLDIR") {
		t.Errorf("Should not preserve values starting with '[', got:\n%s", preserveXML)
	}
	// Should have a preservation property for "Hello"
	if !strings.Contains(preserveXML, "Value='Hello'") {
		t.Errorf("Should preserve normal string values, got:\n%s", preserveXML)
	}
}

func TestPreserveEmptyStringValue(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"EmptyVal"=""
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "empty.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir)
	reg := ir.Registry{File: "empty.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	preserveXML := proc.GeneratePreservationXML(components, allIDs)

	// Empty string should produce default Property without Value attribute (but with Secure)
	if !strings.Contains(preserveXML, "<Property Id='PS_RV_00000' Secure='yes'/>") {
		t.Errorf("Empty string should produce self-closing Property without Value attribute, got:\n%s", preserveXML)
	}
}
