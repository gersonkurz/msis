package parser

import (
	"strings"
	"testing"
)

func TestParseMsisBool(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"yes", true},
		{"Yes", true},
		{"YES", true},
		{"no", false},
		{"No", false},
		{"NO", false},
		{"on", true},
		{"On", true},
		{"ON", true},
		{"off", false},
		{"Off", false},
		{"OFF", false},
		{"1", true},
		{"0", false},
		{"", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseMsisBool(tt.input)
			if result != tt.expected {
				t.Errorf("parseMsisBool(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseMsisBoolDefault(t *testing.T) {
	// Empty string should return default
	if parseMsisBoolDefault("", true) != true {
		t.Error("expected true for empty string with default true")
	}
	if parseMsisBoolDefault("", false) != false {
		t.Error("expected false for empty string with default false")
	}

	// IMPORTANT: Invalid value should return false, NOT the default
	// This matches msis-2.x behavior (Codex review feedback)
	if parseMsisBoolDefault("invalid", true) != false {
		t.Error("expected false for invalid value (not default true)")
	}
	if parseMsisBoolDefault("maybe", true) != false {
		t.Error("expected false for 'maybe' (not default true)")
	}
}

func TestInvalidBooleanYieldsFalse(t *testing.T) {
	// Codex review: invalid enabled="maybe" should yield false, not default true
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <feature name="Test" enabled="maybe">
        <files source="C:\src" target="[INSTALLDIR]dest"/>
    </feature>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(setup.Features))
	}

	// "maybe" is invalid, so enabled should be false (not default true)
	if setup.Features[0].Enabled {
		t.Error("expected enabled=false for invalid value 'maybe', got true")
	}
}

func TestParseMinimalSetup(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <set name="PRODUCT_NAME" value="Test Product"/>
    <set name="PRODUCT_VERSION" value="1.0.0"/>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Sets) != 2 {
		t.Errorf("expected 2 sets, got %d", len(setup.Sets))
	}

	if setup.Sets[0].Name != "PRODUCT_NAME" {
		t.Errorf("expected PRODUCT_NAME, got %s", setup.Sets[0].Name)
	}
	if setup.Sets[0].Value != "Test Product" {
		t.Errorf("expected 'Test Product', got %s", setup.Sets[0].Value)
	}
}

func TestParseFeatureWithItems(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <feature name="Main Feature" enabled="yes">
        <files source="C:\src" target="[INSTALLDIR]dest"/>
        <set-env name="MY_VAR" value="my_value"/>
    </feature>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(setup.Features))
	}

	feature := setup.Features[0]
	if feature.Name != "Main Feature" {
		t.Errorf("expected 'Main Feature', got %s", feature.Name)
	}
	if !feature.Enabled {
		t.Error("expected feature to be enabled")
	}
	if len(feature.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(feature.Items))
	}
}

func TestParseNestedFeatures(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <feature name="Parent" enabled="yes">
        <feature name="Child 1" enabled="yes">
            <files source="C:\src1" target="[INSTALLDIR]dest1"/>
        </feature>
        <feature name="Child 2" enabled="no">
            <files source="C:\src2" target="[INSTALLDIR]dest2"/>
        </feature>
    </feature>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Features) != 1 {
		t.Fatalf("expected 1 top-level feature, got %d", len(setup.Features))
	}

	parent := setup.Features[0]
	if parent.Name != "Parent" {
		t.Errorf("expected 'Parent', got %s", parent.Name)
	}

	if len(parent.SubFeatures) != 2 {
		t.Fatalf("expected 2 sub-features, got %d", len(parent.SubFeatures))
	}

	child1 := parent.SubFeatures[0]
	if child1.Name != "Child 1" {
		t.Errorf("expected 'Child 1', got %s", child1.Name)
	}
	if !child1.Enabled {
		t.Error("expected Child 1 to be enabled")
	}

	child2 := parent.SubFeatures[1]
	if child2.Name != "Child 2" {
		t.Errorf("expected 'Child 2', got %s", child2.Name)
	}
	if child2.Enabled {
		t.Error("expected Child 2 to be disabled")
	}
}

func TestParseService(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <feature name="Services" enabled="yes">
        <service
            file-name="myservice.exe"
            service-name="MyService"
            service-display-name="My Service"
            start="auto"
            description="My service description"
        />
    </feature>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(setup.Features))
	}

	feature := setup.Features[0]
	if len(feature.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(feature.Items))
	}

	item := feature.Items[0]
	if item.ItemType() != "service" {
		t.Errorf("expected 'service', got %s", item.ItemType())
	}
}

func TestParseSilentSetup(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup silent="true">
    <set name="PRODUCT_NAME" value="Silent Product"/>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !setup.Silent {
		t.Error("expected setup to be silent")
	}
}

func TestParseBundle(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <bundle source_64bit="x64.msi" source_32bit="x86.msi"/>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if setup.Bundle == nil {
		t.Fatal("expected bundle to be present")
	}

	if setup.Bundle.Source64bit != "x64.msi" {
		t.Errorf("expected 'x64.msi', got %s", setup.Bundle.Source64bit)
	}
	if setup.Bundle.Source32bit != "x86.msi" {
		t.Errorf("expected 'x86.msi', got %s", setup.Bundle.Source32bit)
	}

	if !setup.IsSetupBundle() {
		t.Error("expected IsSetupBundle() to return true")
	}
}

func TestParseBundleWithPrerequisites(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <bundle>
        <prerequisite type="vcredist" version="2022"/>
        <prerequisite type="netfx" version="4.8"/>
        <msi source_64bit="app-x64.msi" source_32bit="app-x86.msi"/>
    </bundle>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if setup.Bundle == nil {
		t.Fatal("expected bundle to be present")
	}

	// Check prerequisites
	if len(setup.Bundle.Prerequisites) != 2 {
		t.Fatalf("expected 2 prerequisites, got %d", len(setup.Bundle.Prerequisites))
	}

	if setup.Bundle.Prerequisites[0].Type != "vcredist" {
		t.Errorf("expected 'vcredist', got %s", setup.Bundle.Prerequisites[0].Type)
	}
	if setup.Bundle.Prerequisites[0].Version != "2022" {
		t.Errorf("expected '2022', got %s", setup.Bundle.Prerequisites[0].Version)
	}

	if setup.Bundle.Prerequisites[1].Type != "netfx" {
		t.Errorf("expected 'netfx', got %s", setup.Bundle.Prerequisites[1].Type)
	}
	if setup.Bundle.Prerequisites[1].Version != "4.8" {
		t.Errorf("expected '4.8', got %s", setup.Bundle.Prerequisites[1].Version)
	}

	// Check MSI
	if setup.Bundle.MSI == nil {
		t.Fatal("expected MSI to be present")
	}
	if setup.Bundle.MSI.Source64bit != "app-x64.msi" {
		t.Errorf("expected 'app-x64.msi', got %s", setup.Bundle.MSI.Source64bit)
	}
	if setup.Bundle.MSI.Source32bit != "app-x86.msi" {
		t.Errorf("expected 'app-x86.msi', got %s", setup.Bundle.MSI.Source32bit)
	}
}

func TestParseBundleWithExePackage(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <bundle>
        <exe id="CustomPrereq" source="prereq.exe" detect="HKLM\SOFTWARE\Prereq" args="/quiet"/>
        <msi source="app.msi"/>
    </bundle>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if setup.Bundle == nil {
		t.Fatal("expected bundle to be present")
	}

	// Check exe package
	if len(setup.Bundle.ExePackages) != 1 {
		t.Fatalf("expected 1 exe package, got %d", len(setup.Bundle.ExePackages))
	}

	exe := setup.Bundle.ExePackages[0]
	if exe.ID != "CustomPrereq" {
		t.Errorf("expected 'CustomPrereq', got %s", exe.ID)
	}
	if exe.Source != "prereq.exe" {
		t.Errorf("expected 'prereq.exe', got %s", exe.Source)
	}
	if exe.DetectCondition != "HKLM\\SOFTWARE\\Prereq" {
		t.Errorf("expected 'HKLM\\SOFTWARE\\Prereq', got %s", exe.DetectCondition)
	}
	if exe.InstallArgs != "/quiet" {
		t.Errorf("expected '/quiet', got %s", exe.InstallArgs)
	}

	// Check MSI (single source)
	if setup.Bundle.MSI == nil {
		t.Fatal("expected MSI to be present")
	}
	if setup.Bundle.MSI.Source != "app.msi" {
		t.Errorf("expected 'app.msi', got %s", setup.Bundle.MSI.Source)
	}
}

func TestParseBundleValidation(t *testing.T) {
	tests := []struct {
		name    string
		xml     string
		wantErr string
	}{
		{
			name: "prerequisite missing type",
			xml: `<?xml version="1.0" encoding="utf-8"?>
<setup><bundle><prerequisite version="4.8"/></bundle></setup>`,
			wantErr: "requires 'type' attribute",
		},
		{
			name: "prerequisite missing version and source",
			xml: `<?xml version="1.0" encoding="utf-8"?>
<setup><bundle><prerequisite type="netfx"/></bundle></setup>`,
			wantErr: "requires 'version' or 'source' attribute",
		},
		{
			name: "msi missing source",
			xml: `<?xml version="1.0" encoding="utf-8"?>
<setup><bundle><msi/></bundle></setup>`,
			wantErr: "requires 'source'",
		},
		{
			name: "exe missing source",
			xml: `<?xml version="1.0" encoding="utf-8"?>
<setup><bundle><exe id="test"/></bundle></setup>`,
			wantErr: "requires 'source' attribute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBytes([]byte(tt.xml))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestParseTopLevelItems(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <files source="C:\src" target="[INSTALLDIR]dest"/>
    <exclude folder="temp"/>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Items) != 2 {
		t.Errorf("expected 2 top-level items, got %d", len(setup.Items))
	}
}

func TestParseDoNotOverwrite(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <files source="C:\src" target="[INSTALLDIR]dest" do-not-overwrite="true"/>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(setup.Items))
	}

	// Type assertion to get the Files item
	files, ok := setup.Items[0].(interface{ ItemType() string })
	if !ok || files.ItemType() != "files" {
		t.Fatal("expected files item")
	}
}

func TestParseExecute(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <feature name="Scripts" enabled="yes">
        <execute cmd="setup.bat" when="after-install" directory="[INSTALLDIR]"/>
    </feature>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(setup.Features))
	}

	feature := setup.Features[0]
	if len(feature.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(feature.Items))
	}

	if feature.Items[0].ItemType() != "execute" {
		t.Errorf("expected 'execute', got %s", feature.Items[0].ItemType())
	}
}

func TestUnknownElementRejected(t *testing.T) {
	// Codex review: parser must reject unknown elements
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <foo bar="baz"/>
</setup>`

	_, err := ParseBytes([]byte(xml))
	if err == nil {
		t.Error("expected error for unknown element <foo>, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown element") {
		t.Errorf("expected 'unknown element' error, got: %v", err)
	}
}

func TestUnknownAttributeRejected(t *testing.T) {
	// Codex review: parser must reject unknown attributes
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup bogus="value">
    <set name="TEST" value="test"/>
</setup>`

	_, err := ParseBytes([]byte(xml))
	if err == nil {
		t.Error("expected error for unknown attribute 'bogus', got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown attribute") {
		t.Errorf("expected 'unknown attribute' error, got: %v", err)
	}
}

func TestUnknownAttributeOnItemsRejected(t *testing.T) {
	// Codex review rev2: unknown attributes must be rejected on ALL elements
	tests := []struct {
		name string
		xml  string
	}{
		{"files", `<setup><files source="C:\src" target="[INSTALLDIR]" bogus="x"/></setup>`},
		{"set", `<setup><set name="X" value="Y" bogus="x"/></setup>`},
		{"set-env", `<setup><feature name="F"><set-env name="X" value="Y" bogus="x"/></feature></setup>`},
		{"service", `<setup><feature name="F"><service file-name="x.exe" service-name="Svc" bogus="x"/></feature></setup>`},
		{"execute", `<setup><feature name="F"><execute cmd="x" when="after-install" bogus="x"/></feature></setup>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBytes([]byte(`<?xml version="1.0"?>` + tt.xml))
			if err == nil {
				t.Errorf("expected error for unknown attribute on <%s>, got nil", tt.name)
			}
			if err != nil && !strings.Contains(err.Error(), "unknown attribute") {
				t.Errorf("expected 'unknown attribute' error, got: %v", err)
			}
		})
	}
}

func TestEmptyButPresentAttributesAllowed(t *testing.T) {
	// Codex review rev2/rev3: empty value is allowed if attribute is present
	// This is different from missing attribute
	tests := []struct {
		name string
		xml  string
	}{
		{"set with empty value", `<setup><set name="X" value=""/></setup>`},
		{"files with empty source", `<setup><files source="" target="[INSTALLDIR]"/></setup>`},
		{"feature with empty name", `<setup><feature name=""/></setup>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBytes([]byte(`<?xml version="1.0"?>` + tt.xml))
			if err != nil {
				t.Errorf("expected no error for %s, got: %v", tt.name, err)
			}
		})
	}
}

func TestMissingRequiredAttribute(t *testing.T) {
	// Codex review: parser must enforce required attributes
	tests := []struct {
		name string
		xml  string
		err  string
	}{
		{
			name: "files missing source",
			xml:  `<setup><files target="[INSTALLDIR]dest"/></setup>`,
			err:  "'source'",
		},
		{
			name: "files missing target",
			xml:  `<setup><files source="C:\src"/></setup>`,
			err:  "'target'",
		},
		{
			name: "set missing name",
			xml:  `<setup><set value="test"/></setup>`,
			err:  "'name'",
		},
		{
			name: "set missing value",
			xml:  `<setup><set name="TEST"/></setup>`,
			err:  "'value'",
		},
		{
			name: "feature missing name",
			xml:  `<setup><feature enabled="yes"/></setup>`,
			err:  "'name'",
		},
		{
			name: "execute missing cmd",
			xml:  `<setup><execute when="after-install"/></setup>`,
			err:  "'cmd'",
		},
		{
			name: "execute missing when",
			xml:  `<setup><execute cmd="test.bat"/></setup>`,
			err:  "'when'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBytes([]byte(`<?xml version="1.0"?>` + tt.xml))
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
			if err != nil && !strings.Contains(err.Error(), tt.err) {
				t.Errorf("expected error containing %s, got: %v", tt.err, err)
			}
		})
	}
}

func TestItemOrderPreservation(t *testing.T) {
	// Codex review: items must preserve document order, not be grouped by type
	xml := `<?xml version="1.0" encoding="utf-8"?>
<setup>
    <feature name="Test" enabled="yes">
        <files source="C:\src1" target="[INSTALLDIR]dest1"/>
        <set-env name="VAR1" value="value1"/>
        <files source="C:\src2" target="[INSTALLDIR]dest2"/>
        <execute cmd="setup.bat" when="after-install"/>
        <set-env name="VAR2" value="value2"/>
    </feature>
</setup>`

	setup, err := ParseBytes([]byte(xml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(setup.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(setup.Features))
	}

	feature := setup.Features[0]
	if len(feature.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(feature.Items))
	}

	// Verify exact order: files, set-env, files, execute, set-env
	expectedOrder := []string{"files", "set-env", "files", "execute", "set-env"}
	for i, expected := range expectedOrder {
		if feature.Items[i].ItemType() != expected {
			t.Errorf("item[%d]: expected %s, got %s", i, expected, feature.Items[i].ItemType())
		}
	}
}

func TestParseNg1BmoExample(t *testing.T) {
	// Test parsing the actual production example
	setup, err := Parse("../../../production-examples/ng1-bmo/installer.msis")
	if err != nil {
		t.Skipf("Skipping production example test: %v", err)
	}

	// Verify expected structure from ng1-bmo
	if len(setup.Sets) < 10 {
		t.Errorf("expected at least 10 sets, got %d", len(setup.Sets))
	}

	// Check for expected set values
	foundProductName := false
	for _, s := range setup.Sets {
		if s.Name == "PRODUCT_NAME" && s.Value == "NG1 (64-bit)" {
			foundProductName = true
			break
		}
	}
	if !foundProductName {
		t.Error("expected to find PRODUCT_NAME set")
	}

	// Check for NG1 feature
	if len(setup.Features) != 1 {
		t.Errorf("expected 1 top-level feature (NG1), got %d", len(setup.Features))
	}

	ng1Feature := setup.Features[0]
	if ng1Feature.Name != "NG1" {
		t.Errorf("expected 'NG1', got %s", ng1Feature.Name)
	}

	// NG1 feature should have 4 sub-features
	if len(ng1Feature.SubFeatures) != 4 {
		t.Errorf("expected 4 sub-features, got %d", len(ng1Feature.SubFeatures))
	}

	// Find "NG1 Server" sub-feature and check it has files and set-env items
	var serverFeature *struct {
		Name      string
		Items     int
		HasSetEnv bool
	}
	for _, sf := range ng1Feature.SubFeatures {
		if sf.Name == "NG1 Server" {
			hasSetEnv := false
			for _, item := range sf.Items {
				if item.ItemType() == "set-env" {
					hasSetEnv = true
					break
				}
			}
			serverFeature = &struct {
				Name      string
				Items     int
				HasSetEnv bool
			}{sf.Name, len(sf.Items), hasSetEnv}
			break
		}
	}

	if serverFeature == nil {
		t.Error("expected to find 'NG1 Server' feature")
	} else {
		if serverFeature.Items < 7 {
			t.Errorf("expected at least 7 items in NG1 Server, got %d", serverFeature.Items)
		}
		if !serverFeature.HasSetEnv {
			t.Error("expected NG1 Server to have set-env items")
		}
	}
}
