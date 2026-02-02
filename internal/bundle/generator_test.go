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
