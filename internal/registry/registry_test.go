package registry

import (
	"fmt"
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

	// Should have a default Property (PS_RV_) with the search nested inside it
	if !strings.Contains(xml, "<Property Id='PS_RV_") {
		t.Error("Preservation XML should contain default Property elements")
	}
	if !strings.Contains(xml, "<RegistrySearch Id='PS_RV_") {
		t.Error("Preservation XML should contain RegistrySearch elements nested in the PS_RV_ property")
	}
	if !strings.Contains(xml, "Type='raw'") {
		t.Error("RegistrySearch should have Type='raw'")
	}
	if !strings.Contains(xml, "Root='HKLM'") {
		t.Error("RegistrySearch should have Root='HKLM'")
	}
	// Default values should be string literals on PS_RV_
	if !strings.Contains(xml, "Value='C:\\Logs\\app.log'") {
		t.Error("Default Property should have string default value")
	}
	// No custom actions: a SetProperty per preserved value cannot be sequenced
	// around AppSearch beyond ~100 values (WIX0179, issue #5).
	if strings.Contains(xml, "<SetProperty") {
		t.Errorf("Preservation XML must not contain SetProperty custom actions, got:\n%s", xml)
	}
	if strings.Contains(xml, "PS_RS_") {
		t.Errorf("Preservation XML must not contain separate PS_RS_ search properties, got:\n%s", xml)
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
	reg := ir.Registry{File: "binary.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	// Binary is a single #x prefix followed by the hex bytes — the MSI Registry
	// table format. The per-nibble "#x4#xF#x4#xB" form msis-2.x emitted makes
	// Windows Installer fail at WriteRegistryValues with Error 1406 (issue #6).
	if !strings.Contains(xml, "Value='#x4F4B'") {
		t.Errorf("Binary default should be '#x4F4B', got:\n%s", xml)
	}
	if strings.Contains(xml, "#x4#xF") {
		t.Errorf("Binary default must not use the per-nibble encoding, got:\n%s", xml)
	}
}

// TestExpandableIsNotPreserved guards half of issue #10. A preserved REG_EXPAND_SZ
// was doubly broken: the default carried no type marker, AND the Type='raw' search
// used to read the live value expands it and strips the type, so preserving
// "%TEMP%" wrote back a literal machine-specific path as REG_SZ. Excluded from
// preservation entirely, it is written normally as a proper REG_EXPAND_SZ.
func TestExpandableIsNotPreserved(t *testing.T) {
	// hex(2) is REG_EXPAND_SZ; this is "%PATH%" in UTF-16LE with its terminator.
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Expand"=hex(2):25,00,50,00,41,00,54,00,48,00,25,00,00,00
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "expand.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	components, err := proc.Process(ir.Registry{File: "expand.reg", Preserve: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	preserveXML := proc.GeneratePreservationXML(components, allIDs)

	// No preservation property at all for an expandable value.
	if strings.Contains(preserveXML, "PS_RV_") {
		t.Errorf("Expandable value must not be preserved, got:\n%s", preserveXML)
	}

	// It is written normally instead: correct type, value left unexpanded.
	xml := proc.GenerateXMLWithPreservedIDs(components, false, allIDs)
	if !strings.Contains(xml, "Value='%PATH%' Type='expandable'") {
		t.Errorf("Expandable value should be written as an unexpanded REG_EXPAND_SZ, got:\n%s", xml)
	}
	if strings.Contains(xml, "[PS_RV_") {
		t.Errorf("Expandable value must not reference a preservation property, got:\n%s", xml)
	}
}

// TestQwordTruncatesAndIsNotPreserved guards the other half of issue #10. MSI has no
// REG_QWORD form at all, so a QWORD is deliberately truncated to its low 32 bits
// rather than emitted at full width as an out-of-range "#N" — and it is excluded from
// preservation, because the raw search returns a live QWORD's raw bytes reinterpreted
// as UTF-16 text, which would be written back as mojibake REG_SZ.
func TestQwordTruncatesAndIsNotPreserved(t *testing.T) {
	// hex(b) is REG_QWORD, little-endian: 0x0123456789ABCDEF.
	// Low 32 bits are 0x89ABCDEF = 2309737967.
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Big"=hex(b):ef,cd,ab,89,67,45,23,01
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "qword.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	components, err := proc.Process(ir.Registry{File: "qword.reg", Preserve: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	preserveXML := proc.GeneratePreservationXML(components, allIDs)

	// A QWORD gets no preservation property at all.
	if strings.Contains(preserveXML, "PS_RV_") {
		t.Errorf("QWORD value must not be preserved, got:\n%s", preserveXML)
	}

	// Truncation happens in convertValue, so it applies wherever the value is written.
	// Assert the truncated value POSITIVELY: merely checking the 64-bit decimal is
	// absent would also pass if the value vanished or became zero.
	plainXML := proc.GenerateXML(components, false)
	if !strings.Contains(plainXML, "Value='2309737967' Type='integer'") {
		t.Errorf("Non-preserved path should write the truncated integer, got:\n%s", plainXML)
	}
	if strings.Contains(plainXML, "81985529216486895") {
		t.Errorf("Non-preserved path must not carry the full 64-bit value, got:\n%s", plainXML)
	}
}

// TestPreserveEscapesLeadingHash guards issue #11: a preserved REG_SZ beginning with
// '#' was emitted unescaped, and MSI reads a leading '#' in the Registry table as a
// type marker — the install failed with Error 1406 and rolled back. WiX stores
// "##FF0000" for the same value when it owns the write, so the two paths must agree.
func TestPreserveEscapesLeadingHash(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Colour"="#FF0000"
"Middle"="a#b"
"Plain"="plain-sz"
"JustHash"="#"
"AlreadyDoubled"="##"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "hash.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	components, err := proc.Process(ir.Registry{File: "hash.reg", Preserve: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	if !strings.Contains(xml, "Value='##FF0000'") {
		t.Errorf("Leading '#' should be doubled, got:\n%s", xml)
	}
	// Only the FIRST character is special — a '#' inside the string stays single,
	// and an ordinary value must not sprout a '#'.
	if !strings.Contains(xml, "Value='a#b'") {
		t.Errorf("A '#' that is not leading must be left alone, got:\n%s", xml)
	}
	if !strings.Contains(xml, "Value='plain-sz'") {
		t.Errorf("An ordinary string must be untouched, got:\n%s", xml)
	}
	// Boundaries. Exactly one '#' is prepended, never a blanket replacement, and an
	// authored "##" is NOT mistaken for something already encoded — it is a literal
	// two-character string and must survive the round trip as one.
	if !strings.Contains(xml, "Value='##'") {
		t.Errorf(`A bare "#" should become "##", got:`+"\n%s", xml)
	}
	if !strings.Contains(xml, "Value='###'") {
		t.Errorf(`An authored "##" should become "###", got:`+"\n%s", xml)
	}
}

// TestPreserveEscapesDefaultValue guards issue #9: the preserved default was the one
// attribute in this file interpolated without escaping, so a legal .reg default
// containing an apostrophe or an ampersand produced malformed WXS and failed the
// build with WIX0104.
func TestPreserveEscapesDefaultValue(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Owner"="O'Brien"
"Pair"="A&B"
"Markup"="<tag attr=\"v\">"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "escape.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	components, err := proc.Process(ir.Registry{File: "escape.reg", Preserve: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	for _, want := range []string{
		"Value='O&apos;Brien'",
		"Value='A&amp;B'",
		"Value='&lt;tag attr=&quot;v&quot;&gt;'",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("Preservation XML should contain %s, got:\n%s", want, xml)
		}
	}

	// The raw apostrophe would terminate the attribute early; the raw ampersand is
	// an undefined entity reference. Either one makes the WXS unparseable.
	if strings.Contains(xml, "Value='O'Brien'") {
		t.Errorf("Apostrophe must not be emitted raw, got:\n%s", xml)
	}
	if strings.Contains(xml, "Value='A&B'") {
		t.Errorf("Ampersand must not be emitted raw, got:\n%s", xml)
	}
}

func TestPreserveEmptyBinaryValue(t *testing.T) {
	// A zero-byte REG_BINARY default is "#x" with nothing after it. Omitting the
	// Value attribute instead leaves the property undefined, which strips the type
	// marker from the write — verified by install probe: MSI then stores an empty
	// REG_SZ, while "#x" stores REG_BINARY with zero bytes.
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"EmptyBlob"=hex:
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "emptybin.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	components, err := proc.Process(ir.Registry{File: "emptybin.reg", Preserve: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	if !strings.Contains(xml, "<Property Id='PS_RV_00000' Value='#x' Secure='yes'>") {
		t.Errorf("Empty binary should emit Value='#x', got:\n%s", xml)
	}
	// The Value attribute must be present: an undefined property writes REG_SZ.
	if strings.Contains(xml, "<Property Id='PS_RV_00000' Secure='yes'>") {
		t.Errorf("Empty binary must not omit the Value attribute, got:\n%s", xml)
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

	proc := NewProcessor(tmpDir, "")
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

func TestPreserveDoesNotEmitNeverOverwrite(t *testing.T) {
	// NeverOverwrite must NOT be emitted for preserved components: it breaks major upgrades
	// (component marked do-not-install during costing while the old keypath still exists, then
	// RemoveExistingProducts wipes the values and they never get rewritten). Preservation is
	// handled by the RegistrySearch -> PS_RV mechanism instead.
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Setting"="Default"
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "neveroverwrite.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	reg := ir.Registry{File: "neveroverwrite.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GenerateXMLWithPreservedIDs(components, false, allIDs)

	if strings.Contains(xml, "NeverOverwrite") {
		t.Errorf("Preserved component must not have NeverOverwrite (breaks major upgrades), got:\n%s", xml)
	}
	// Preservation must still be in place via the PS_RV mechanism.
	if !strings.Contains(xml, "[PS_RV_") {
		t.Errorf("Preserved component should reference PS_RV properties, got:\n%s", xml)
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

	proc := NewProcessor(tmpDir, "")
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

	proc := NewProcessor(tmpDir, "")
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

func TestForceDeleteOnUninstallOnlyEmptyKeys(t *testing.T) {
	// Keys WITH values should NOT get ForceDeleteOnUninstall (runtime values may exist).
	// Keys WITHOUT values (empty structural keys) SHOULD get ForceDeleteOnUninstall.
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp]
"Version"="1.0.0"

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp\Hotkeys]

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp\Config]
"LogLevel"=dword:00000003

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp\Config\EmptyChild]
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "mixed.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	reg := ir.Registry{File: "mixed.reg"}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	// TestApp has values (via subkeys) → no ForceDeleteOnUninstall
	// Note: keys are nested hierarchically, so TestApp appears as Key='TestApp'
	if strings.Contains(xml, "Key='TestApp' ForceCreateOnInstall='yes' ForceDeleteOnUninstall='yes'") {
		t.Errorf("Key with values should NOT have ForceDeleteOnUninstall:\n%s", xml)
	}
	if !strings.Contains(xml, "Key='TestApp' ForceCreateOnInstall='yes'") {
		t.Errorf("Key with values should still have ForceCreateOnInstall:\n%s", xml)
	}

	// Hotkeys is empty → should have ForceDeleteOnUninstall
	if !strings.Contains(xml, "Key='Hotkeys' ForceCreateOnInstall='yes' ForceDeleteOnUninstall='yes'") {
		t.Errorf("Empty key should have ForceDeleteOnUninstall:\n%s", xml)
	}

	// Config has values → no ForceDeleteOnUninstall
	if strings.Contains(xml, "Key='Config' ForceCreateOnInstall='yes' ForceDeleteOnUninstall='yes'") {
		t.Errorf("Key with values should NOT have ForceDeleteOnUninstall:\n%s", xml)
	}

	// EmptyChild is empty → should have ForceDeleteOnUninstall
	if !strings.Contains(xml, "Key='EmptyChild' ForceCreateOnInstall='yes' ForceDeleteOnUninstall='yes'") {
		t.Errorf("Empty child key should have ForceDeleteOnUninstall:\n%s", xml)
	}
}

func TestForceDeleteOnUninstallNotOnPermanent(t *testing.T) {
	// Permanent components should never get ForceDeleteOnUninstall, even for empty keys
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\TestApp\EmptyKey]
`
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "perm.reg")
	if err := os.WriteFile(regFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	reg := ir.Registry{File: "perm.reg", Permanent: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	xml := proc.GenerateXML(components, false)

	if strings.Contains(xml, "ForceDeleteOnUninstall") {
		t.Errorf("Permanent component should never have ForceDeleteOnUninstall:\n%s", xml)
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

	proc := NewProcessor(tmpDir, "")
	reg := ir.Registry{File: "empty.reg", Preserve: true}

	components, err := proc.Process(reg)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	preserveXML := proc.GeneratePreservationXML(components, allIDs)

	// Empty string should produce a default Property without a Value attribute
	// (WiX rejects Value=''), still carrying Secure and the nested search.
	if !strings.Contains(preserveXML, "<Property Id='PS_RV_00000' Secure='yes'>") {
		t.Errorf("Empty string should produce Property without Value attribute, got:\n%s", preserveXML)
	}
	if !strings.Contains(preserveXML, "<RegistrySearch Id='PS_RV_00000_Registry'") {
		t.Errorf("Empty-default property should still nest its RegistrySearch, got:\n%s", preserveXML)
	}
}

// TestPreserveManyValuesEmitsNoCustomActions guards issue #5: preservation used to
// emit a SetProperty custom action per value, all sequenced After='AppSearch'.
// Only ~100 sequence numbers exist in that gap, so a .reg file with a few hundred
// preserved values failed to build at all (WIX0179).
func TestPreserveManyValuesEmitsNoCustomActions(t *testing.T) {
	const count = 1500

	var content strings.Builder
	content.WriteString("Windows Registry Editor Version 5.00\n\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\MyApp]\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&content, "\"Value%04d\"=\"default%04d\"\n", i, i)
	}

	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "many.reg")
	if err := os.WriteFile(regFile, []byte(content.String()), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	proc := NewProcessor(tmpDir, "")
	components, err := proc.Process(ir.Registry{File: "many.reg", Preserve: true})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	allIDs := proc.BuildAllPreservedIDs(components)
	xml := proc.GeneratePreservationXML(components, allIDs)

	if n := strings.Count(xml, "<SetProperty"); n != 0 {
		t.Errorf("Preservation of %d values emitted %d SetProperty custom actions, want 0", count, n)
	}
	if n := strings.Count(xml, "<RegistrySearch"); n != count {
		t.Errorf("Got %d RegistrySearch elements, want %d", n, count)
	}
	if n := strings.Count(xml, "<Property Id='PS_RV_"); n != count {
		t.Errorf("Got %d PS_RV_ properties, want %d", n, count)
	}
}

// TestWarnQwordTruncation guards issue #14. A REG_QWORD is narrowed to 32 bits because
// MSI cannot store one (#10); that is deliberate and documented, but it used to happen
// with nothing said at build time.
func TestWarnQwordTruncation(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Big"=hex(b):ef,cd,ab,89,67,45,23,01
`
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "q.reg"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor(tmpDir, "")
	if _, err := proc.Process(ir.Registry{File: "q.reg"}); err != nil {
		t.Fatal(err)
	}

	w := proc.Warnings()
	if len(w) != 1 {
		t.Fatalf("expected one warning, got %d: %v", len(w), w)
	}
	for _, want := range []string{"Big", "REG_QWORD", "2309737967"} {
		if !strings.Contains(w[0], want) {
			t.Errorf("warning should mention %q, got: %s", want, w[0])
		}
	}
}

// TestWarnQwordThatFitsIsSilent: a QWORD inside 32-bit range keeps its numeric value,
// though its TYPE still narrows to REG_DWORD — that narrowing is unavoidable for every
// QWORD, since MSI has no 64-bit form, and is documented in docs/tutorial.md. Warning
// on it would therefore be unactionable, and a warning nobody can act on trains people
// to ignore the ones that matter. Deliberate policy: warn only when numeric bits are lost.
func TestWarnQwordThatFitsIsSilent(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Small"=hex(b):2a,00,00,00,00,00,00,00
`
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "q2.reg"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor(tmpDir, "")
	if _, err := proc.Process(ir.Registry{File: "q2.reg"}); err != nil {
		t.Fatal(err)
	}
	if w := proc.Warnings(); len(w) != 0 {
		t.Errorf("a QWORD that fits in 32 bits should not warn, got: %v", w)
	}
}

// TestWarnFormattedRegistryValues guards the other half of #14. The Registry table's
// Value column is an MSI Formatted field, so "a[Foo]b" installs as "ab" and "a[~]b"
// installs as a REG_MULTI_SZ — both measured while fixing #11.
func TestWarnFormattedRegistryValues(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Brackets"="a[Foo]b"
"MultiSep"="a[~]b"
"Intentional"="[INSTALLDIR]app.exe"
"Plain"="nothing special"
`
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "f.reg"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor(tmpDir, "")
	if _, err := proc.Process(ir.Registry{File: "f.reg"}); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(proc.Warnings(), "\n")
	if !strings.Contains(joined, "Brackets") {
		t.Errorf("a mid-string reference should warn, got: %s", joined)
	}
	if !strings.Contains(joined, "MultiSep") || !strings.Contains(joined, "REG_MULTI_SZ") {
		t.Errorf("[~] should warn and name the type change, got: %s", joined)
	}
	// A value that STARTS with "[" is the documented, intentional property reference.
	// Warning on it would fire on every package using [INSTALLDIR] and train people to
	// ignore the warning entirely.
	if strings.Contains(joined, "Intentional") {
		t.Errorf("a leading [PROPERTY] reference must not warn, got: %s", joined)
	}
	if strings.Contains(joined, "Plain") {
		t.Errorf("an ordinary value must not warn, got: %s", joined)
	}
}

// TestWarnFormattedSkipsPreserved: a preserved value reaches the Registry table as
// "[PS_RV_nnnnn]" and its content is substituted without a second formatting pass, so
// its brackets survive and there is nothing to warn about. Measured in #11.
func TestWarnFormattedSkipsPreserved(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Brackets"="a[Foo]b"
`
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "p.reg"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor(tmpDir, "")
	if _, err := proc.Process(ir.Registry{File: "p.reg", Preserve: true}); err != nil {
		t.Fatal(err)
	}
	if w := proc.Warnings(); len(w) != 0 {
		t.Errorf("a preserved value keeps its brackets, so must not warn, got: %v", w)
	}
}
