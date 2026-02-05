package requirements

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
)

func TestGenerateLaunchConditions_VCRedist(t *testing.T) {
	reqs := []ir.Requirement{
		{Type: "vcredist", Version: "2022"},
	}

	conditions := GenerateLaunchConditions(reqs, "x64")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	cond := conditions[0]
	if cond.PropertyName != "VCREDIST_X64_2022" {
		t.Errorf("expected property name 'VCREDIST_X64_2022', got %s", cond.PropertyName)
	}
	if !strings.Contains(cond.RegistrySearch, "VC\\Runtimes\\x64") {
		t.Error("registry search should reference x64 runtime key")
	}
	if !strings.Contains(cond.Message, "Visual C++ 2022") {
		t.Error("message should mention Visual C++ 2022")
	}
}

func TestGenerateLaunchConditions_VCRedist_x86(t *testing.T) {
	reqs := []ir.Requirement{
		{Type: "vcredist", Version: "2022"},
	}

	conditions := GenerateLaunchConditions(reqs, "x86")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	cond := conditions[0]
	if cond.PropertyName != "VCREDIST_X86_2022" {
		t.Errorf("expected property name 'VCREDIST_X86_2022', got %s", cond.PropertyName)
	}
	if !strings.Contains(cond.RegistrySearch, "VC\\Runtimes\\x86") {
		t.Error("registry search should reference x86 runtime key")
	}
}

func TestGenerateLaunchConditions_VCRedist_arm64(t *testing.T) {
	reqs := []ir.Requirement{
		{Type: "vcredist", Version: "2022"},
	}

	conditions := GenerateLaunchConditions(reqs, "arm64")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	cond := conditions[0]
	if cond.PropertyName != "VCREDIST_ARM64_2022" {
		t.Errorf("expected property name 'VCREDIST_ARM64_2022', got %s", cond.PropertyName)
	}
	if !strings.Contains(cond.RegistrySearch, "VC\\Runtimes\\arm64") {
		t.Error("registry search should reference arm64 runtime key")
	}
	if !strings.Contains(cond.Message, "arm64") {
		t.Error("message should mention arm64 architecture")
	}
}

func TestGenerateLaunchConditions_NetFx(t *testing.T) {
	reqs := []ir.Requirement{
		{Type: "netfx", Version: "4.8"},
	}

	conditions := GenerateLaunchConditions(reqs, "x64")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	cond := conditions[0]
	if cond.PropertyName != "NETFX48_RELEASE" {
		t.Errorf("expected property name 'NETFX48_RELEASE', got %s", cond.PropertyName)
	}
	if !strings.Contains(cond.Condition, ">= 528040") {
		t.Errorf("condition should check for release >= 528040, got %s", cond.Condition)
	}
	if !strings.Contains(cond.RegistrySearch, "NET Framework Setup\\NDP\\v4\\Full") {
		t.Error("registry search should reference .NET Framework registry key")
	}
	if !strings.Contains(cond.Message, ".NET Framework 4.8") {
		t.Error("message should mention .NET Framework 4.8")
	}
}

func TestGenerateLaunchConditions_Multiple(t *testing.T) {
	reqs := []ir.Requirement{
		{Type: "vcredist", Version: "2022"},
		{Type: "netfx", Version: "4.8"},
		{Type: "vcredist", Version: "2019"},
	}

	conditions := GenerateLaunchConditions(reqs, "x64")

	if len(conditions) != 3 {
		t.Fatalf("expected 3 conditions, got %d", len(conditions))
	}

	// Check all types are present
	types := make(map[string]bool)
	for _, c := range conditions {
		types[c.PropertyName] = true
	}

	if !types["VCREDIST_X64_2022"] {
		t.Error("missing VCREDIST_X64_2022")
	}
	if !types["NETFX48_RELEASE"] {
		t.Error("missing NETFX48_RELEASE")
	}
	if !types["VCREDIST_X64_2019"] {
		t.Error("missing VCREDIST_X64_2019")
	}
}

func TestGenerateLaunchConditions_UnknownType(t *testing.T) {
	reqs := []ir.Requirement{
		{Type: "unknown", Version: "1.0"},
	}

	conditions := GenerateLaunchConditions(reqs, "x64")

	if len(conditions) != 0 {
		t.Errorf("expected 0 conditions for unknown type, got %d", len(conditions))
	}
}

func TestGenerateLaunchConditions_CustomSource(t *testing.T) {
	// When source is provided with unknown type, we skip condition generation
	// (user is responsible for ensuring prerequisite is met)
	reqs := []ir.Requirement{
		{Type: "custom", Source: "custom.exe"},
	}

	conditions := GenerateLaunchConditions(reqs, "x64")

	if len(conditions) != 0 {
		t.Errorf("expected 0 conditions for custom source, got %d", len(conditions))
	}
}

func TestGenerateXML(t *testing.T) {
	conditions := []LaunchCondition{
		{
			PropertyName:   "VCREDIST_X64",
			Condition:      "VCREDIST_X64",
			Message:        "VC++ required",
			RegistrySearch: `<Property Id="VCREDIST_X64"><RegistrySearch .../></Property>`,
		},
	}

	regXML, condXML := GenerateXML(conditions)

	if !strings.Contains(regXML, "VCREDIST_X64") {
		t.Error("registry XML should contain property")
	}
	if !strings.Contains(condXML, "<Launch Condition") {
		t.Error("condition XML should contain Launch element")
	}
	if !strings.Contains(condXML, "VC++ required") {
		t.Error("condition XML should contain message")
	}
}

func TestGetNetFxReleaseValue(t *testing.T) {
	tests := []struct {
		version     string
		wantRelease int
		wantName    string
	}{
		{"4.8.1", 533320, "Microsoft .NET Framework 4.8.1"},
		{"4.8", 528040, "Microsoft .NET Framework 4.8"},
		{"4.7.2", 461808, "Microsoft .NET Framework 4.7.2"},
		{"4.7.1", 461308, "Microsoft .NET Framework 4.7.1"},
		{"4.7", 460798, "Microsoft .NET Framework 4.7"},
		{"4.6.2", 394802, "Microsoft .NET Framework 4.6.2"},
		{"3.5", 0, ""}, // Unsupported
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			release, name := getNetFxReleaseValue(tt.version)
			if release != tt.wantRelease {
				t.Errorf("release = %d, want %d", release, tt.wantRelease)
			}
			if name != tt.wantName {
				t.Errorf("name = %s, want %s", name, tt.wantName)
			}
		})
	}
}

func TestEscapeXML(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"a & b", "a &amp; b"},
		{"a < b", "a &lt; b"},
		{"a > b", "a &gt; b"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{`x >= 100`, "x &gt;= 100"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := escapeXML(tt.input)
			if got != tt.want {
				t.Errorf("escapeXML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
