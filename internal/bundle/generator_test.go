package bundle

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

func TestGenerateLegacyBundle(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Source64bit: "app-x64.msi",
			Source32bit: "app-x86.msi",
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have both MSI packages with install conditions
	if !strings.Contains(result.ChainXML, "MainPackage_x64") {
		t.Error("expected MainPackage_x64")
	}
	if !strings.Contains(result.ChainXML, "MainPackage_x86") {
		t.Error("expected MainPackage_x86")
	}
	if !strings.Contains(result.ChainXML, "app-x64.msi") {
		t.Error("expected app-x64.msi")
	}
	if !strings.Contains(result.ChainXML, "app-x86.msi") {
		t.Error("expected app-x86.msi")
	}
	if !strings.Contains(result.ChainXML, "VersionNT64") {
		t.Error("expected VersionNT64 condition")
	}
}

func TestGenerateBundleWithPrerequisites(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Prerequisites: []ir.Prerequisite{
				{Type: "vcredist", Version: "2022"},
				{Type: "netfx", Version: "4.8"},
			},
			MSI: &ir.BundleMSI{
				Source64bit: "app-x64.msi",
				Source32bit: "app-x86.msi",
			},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have VC++ prerequisite
	if !strings.Contains(result.ChainXML, "Prereq_vcredist_2022") {
		t.Error("expected Prereq_vcredist_2022")
	}
	if !strings.Contains(result.ChainXML, "vc_redist.x64.exe") {
		t.Error("expected vc_redist.x64.exe")
	}
	if !strings.Contains(result.ChainXML, "vc_redist.x86.exe") {
		t.Error("expected vc_redist.x86.exe")
	}

	// Should have .NET Framework prerequisite
	if !strings.Contains(result.ChainXML, "Prereq_netfx_4_8") {
		t.Error("expected Prereq_netfx_4_8")
	}
	if !strings.Contains(result.ChainXML, "ndp48") {
		t.Error("expected ndp48 installer")
	}

	// Should have MSI packages
	if !strings.Contains(result.ChainXML, "MainPackage_x64") {
		t.Error("expected MainPackage_x64")
	}
}

func TestGenerateBundleWithExePackage(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			ExePackages: []ir.ExePackage{
				{
					ID:              "CustomPrereq",
					Source:          "prereq.exe",
					DetectCondition: "HKLM\\SOFTWARE\\Test",
					InstallArgs:     "/quiet",
				},
			},
			MSI: &ir.BundleMSI{
				Source: "app.msi",
			},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have custom exe package
	if !strings.Contains(result.ChainXML, "Id='CustomPrereq'") {
		t.Error("expected CustomPrereq ID")
	}
	if !strings.Contains(result.ChainXML, "prereq.exe") {
		t.Error("expected prereq.exe")
	}
	if !strings.Contains(result.ChainXML, "DetectCondition") {
		t.Error("expected DetectCondition")
	}
	if !strings.Contains(result.ChainXML, "/quiet") {
		t.Error("expected /quiet args")
	}

	// Should have single MSI package (no conditions)
	if !strings.Contains(result.ChainXML, "Id='MainPackage'") {
		t.Error("expected MainPackage ID")
	}
	if !strings.Contains(result.ChainXML, "app.msi") {
		t.Error("expected app.msi")
	}
}

func TestGenerateBundleNoMSI(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			// No MSI specified
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	_, err := gen.Generate()
	if err == nil {
		t.Fatal("expected error for bundle with no MSI")
	}
	if !strings.Contains(err.Error(), "no MSI source") {
		t.Errorf("expected 'no MSI source' error, got: %v", err)
	}
}

func TestGenerateBundleUnknownPrerequisite(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Prerequisites: []ir.Prerequisite{
				{Type: "unknown", Version: "1.0"},
			},
			MSI: &ir.BundleMSI{Source: "app.msi"},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	_, err := gen.Generate()
	if err == nil {
		t.Fatal("expected error for unknown prerequisite")
	}
	if !strings.Contains(err.Error(), "unknown prerequisite") {
		t.Errorf("expected 'unknown prerequisite' error, got: %v", err)
	}
}

func TestPrerequisitesFolderDefault(t *testing.T) {
	setup := &ir.Setup{Bundle: &ir.Bundle{}}
	vars := variables.New()
	gen := NewGenerator(setup, vars, "/path/to/project")

	if !strings.Contains(gen.PrerequisitesFolder, "prerequisites") {
		t.Errorf("expected default prerequisites folder, got: %s", gen.PrerequisitesFolder)
	}
}

func TestPrerequisitesFolderOverride(t *testing.T) {
	setup := &ir.Setup{Bundle: &ir.Bundle{}}
	vars := variables.New()
	vars["PREREQUISITES_FOLDER"] = "/custom/prereqs"
	gen := NewGenerator(setup, vars, "/path/to/project")

	if gen.PrerequisitesFolder != "/custom/prereqs" {
		t.Errorf("expected /custom/prereqs, got: %s", gen.PrerequisitesFolder)
	}
}

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with.dot", "with_dot"},
		{"with-dash", "with_dash"},
		{"with space", "with_space"},
		{"file.exe", "file_exe"},
		{"123start", "_123start"},
		{"valid_123", "valid_123"},
		{"special@#$chars", "specialchars"},
		{"", "ID"},
		{"...", "___"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeID(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExePackageAutoIDSanitized(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			ExePackages: []ir.ExePackage{
				{Source: "my-app.setup.exe"}, // Has dots and dashes
			},
			MSI: &ir.BundleMSI{Source: "app.msi"},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// ID should be sanitized (dots and dashes replaced)
	if !strings.Contains(result.ChainXML, "Id='ExePackage_my_app_setup_exe'") {
		t.Errorf("expected sanitized ID, got: %s", result.ChainXML)
	}
}

func TestPrerequisiteCustomSourceSinglePackage(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Prerequisites: []ir.Prerequisite{
				{Type: "vcredist", Version: "2022", Source: "/custom/vc_redist_combined.exe"},
			},
			MSI: &ir.BundleMSI{Source: "app.msi"},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have single package (no _x64/_x86 suffix)
	if !strings.Contains(result.ChainXML, "Id='Prereq_vcredist_2022'") {
		t.Errorf("expected single package ID without arch suffix")
	}
	// Should NOT have arch-specific packages
	if strings.Contains(result.ChainXML, "Prereq_vcredist_2022_x64") {
		t.Error("should not have x64 variant when custom source is provided")
	}
	if strings.Contains(result.ChainXML, "Prereq_vcredist_2022_x86") {
		t.Error("should not have x86 variant when custom source is provided")
	}
	// Should use custom source
	if !strings.Contains(result.ChainXML, "/custom/vc_redist_combined.exe") {
		t.Error("should use custom source path")
	}
}

func TestAutoBundleGenerator(t *testing.T) {
	prereqs := []ir.Prerequisite{
		{Type: "vcredist", Version: "2022"},
		{Type: "netfx", Version: "4.8"},
	}
	vars := variables.New()
	gen := NewAutoBundleGenerator(vars, ".", "MyApp.msi", prereqs)

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have prerequisite packages
	if !strings.Contains(result.ChainXML, "Prereq_vcredist_2022") {
		t.Error("expected vcredist prerequisite package")
	}
	if !strings.Contains(result.ChainXML, "Prereq_netfx_4_8") {
		t.Error("expected netfx prerequisite package")
	}

	// Should have main MSI package
	if !strings.Contains(result.ChainXML, "MainPackage") {
		t.Error("expected MainPackage for MSI")
	}
	if !strings.Contains(result.ChainXML, "MyApp.msi") {
		t.Error("expected MSI path in output")
	}
}

func TestAutoBundleGenerator_NoPrereqs(t *testing.T) {
	prereqs := []ir.Prerequisite{}
	vars := variables.New()
	gen := NewAutoBundleGenerator(vars, ".", "MyApp.msi", prereqs)

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should only have main MSI package
	if !strings.Contains(result.ChainXML, "MainPackage") {
		t.Error("expected MainPackage for MSI")
	}
	// Should not have prerequisite packages
	if strings.Contains(result.ChainXML, "Prereq_") {
		t.Error("should not have prerequisite packages when none specified")
	}
}

func TestRequirementsToPrerequisites(t *testing.T) {
	requirements := []ir.Requirement{
		{Type: "vcredist", Version: "2022", Source: ""},
		{Type: "netfx", Version: "4.8", Source: "custom.exe"},
	}

	prereqs, err := RequirementsToPrerequisites(requirements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(prereqs) != 2 {
		t.Fatalf("expected 2 prerequisites, got %d", len(prereqs))
	}

	if prereqs[0].Type != "vcredist" || prereqs[0].Version != "2022" {
		t.Error("first prerequisite should be vcredist 2022")
	}
	if prereqs[1].Type != "netfx" || prereqs[1].Version != "4.8" || prereqs[1].Source != "custom.exe" {
		t.Error("second prerequisite should be netfx 4.8 with custom source")
	}
}

func TestRequirementsToPrerequisitesVersionNormalization(t *testing.T) {
	// Versions 2023-2026 should normalize to 2022
	requirements := []ir.Requirement{
		{Type: "vcredist", Version: "2026"},
	}

	prereqs, err := RequirementsToPrerequisites(requirements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if prereqs[0].Version != "2022" {
		t.Errorf("expected vcredist 2026 to normalize to 2022, got %s", prereqs[0].Version)
	}
}

func TestRequirementsToPrerequisitesValidation(t *testing.T) {
	// Unknown version without custom source should fail
	requirements := []ir.Requirement{
		{Type: "vcredist", Version: "9999"},
	}

	_, err := RequirementsToPrerequisites(requirements)
	if err == nil {
		t.Error("expected error for unknown version")
	}
	if !strings.Contains(err.Error(), "available versions") {
		t.Errorf("error should mention available versions: %v", err)
	}

	// Custom source should bypass validation
	requirements = []ir.Requirement{
		{Type: "vcredist", Version: "9999", Source: "custom.exe"},
	}
	prereqs, err := RequirementsToPrerequisites(requirements)
	if err != nil {
		t.Fatalf("custom source should bypass validation: %v", err)
	}
	if prereqs[0].Version != "9999" {
		t.Error("custom source should preserve original version")
	}
}

func TestGeneratorWithCachedPaths(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Prerequisites: []ir.Prerequisite{
				{Type: "vcredist", Version: "2022"},
			},
			MSI: &ir.BundleMSI{Source: "app.msi"},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	// Simulate cached paths (without actual cache)
	gen.CachedPaths["vcredist/2022/x64"] = "/cache/vcredist/2022/vc_redist.x64.exe"
	gen.CachedPaths["vcredist/2022/x86"] = "/cache/vcredist/2022/vc_redist.x86.exe"

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should use cached paths
	if !strings.Contains(result.ChainXML, "/cache/vcredist/2022/vc_redist.x64.exe") {
		t.Error("expected cached x64 path")
	}
	if !strings.Contains(result.ChainXML, "/cache/vcredist/2022/vc_redist.x86.exe") {
		t.Error("expected cached x86 path")
	}
}

func TestGeneratorWithCachedPathsNetfx(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Prerequisites: []ir.Prerequisite{
				{Type: "netfx", Version: "4.8"},
			},
			MSI: &ir.BundleMSI{Source: "app.msi"},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	// Simulate cached path for arch-neutral netfx
	gen.CachedPaths["netfx/4.8/"] = "/cache/netfx/4.8/ndp48-x86-x64-allos-enu.exe"

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should use cached path (single package, no arch variants)
	if !strings.Contains(result.ChainXML, "/cache/netfx/4.8/ndp48-x86-x64-allos-enu.exe") {
		t.Error("expected cached netfx path")
	}
	// Should not have arch-specific suffixes for netfx
	if strings.Contains(result.ChainXML, "Prereq_netfx_4_8_x64") {
		t.Error("netfx should not have arch-specific packages when path is same")
	}
}

func TestAutoBundleGeneratorWithCachedPaths(t *testing.T) {
	prereqs := []ir.Prerequisite{
		{Type: "vcredist", Version: "2022"},
	}
	vars := variables.New()
	gen := NewAutoBundleGenerator(vars, ".", "MyApp.msi", prereqs)

	// Simulate cached paths
	gen.CachedPaths["vcredist/2022/x64"] = "/cache/vc_redist.x64.exe"

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should use cached paths (default platform is x64, so only x64 included)
	if !strings.Contains(result.ChainXML, "/cache/vc_redist.x64.exe") {
		t.Error("expected cached x64 path")
	}
}

func TestAutoBundleGeneratorWithCachedPathsX86(t *testing.T) {
	prereqs := []ir.Prerequisite{
		{Type: "vcredist", Version: "2022"},
	}
	vars := variables.New()
	vars["PLATFORM"] = "x86"
	gen := NewAutoBundleGenerator(vars, ".", "MyApp.msi", prereqs)

	// Simulate cached paths
	gen.CachedPaths["vcredist/2022/x86"] = "/cache/vc_redist.x86.exe"

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should use cached paths (x86 platform)
	if !strings.Contains(result.ChainXML, "/cache/vc_redist.x86.exe") {
		t.Error("expected cached x86 path")
	}
}

func TestGeneratorWithCachedPathsArm64(t *testing.T) {
	setup := &ir.Setup{
		Bundle: &ir.Bundle{
			Prerequisites: []ir.Prerequisite{
				{Type: "vcredist", Version: "2022"},
			},
			MSI: &ir.BundleMSI{Source: "app.msi"},
		},
	}
	vars := variables.New()
	gen := NewGenerator(setup, vars, ".")

	// Simulate cached paths including ARM64
	gen.CachedPaths["vcredist/2022/x64"] = "/cache/vcredist/2022/vc_redist.x64.exe"
	gen.CachedPaths["vcredist/2022/x86"] = "/cache/vcredist/2022/vc_redist.x86.exe"
	gen.CachedPaths["vcredist/2022/arm64"] = "/cache/vcredist/2022/vc_redist.arm64.exe"

	result, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have ARM64 package
	if !strings.Contains(result.ChainXML, "Prereq_vcredist_2022_arm64") {
		t.Error("expected ARM64 package ID")
	}
	if !strings.Contains(result.ChainXML, "/cache/vcredist/2022/vc_redist.arm64.exe") {
		t.Error("expected cached ARM64 path")
	}
	// ARM64 condition: NativeMachine = 43620
	if !strings.Contains(result.ChainXML, "NativeMachine = 43620") {
		t.Error("expected ARM64 install condition")
	}
	// x64 should exclude ARM64 when ARM64 is present
	if !strings.Contains(result.ChainXML, "VersionNT64 AND NOT NativeMachine = 43620") {
		t.Error("expected x64 condition to exclude ARM64")
	}
}
