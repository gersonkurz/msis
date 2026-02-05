// Package requirements provides launch condition generation for runtime prerequisites.
// These conditions are used in standalone MSI packages to verify required runtimes
// are installed before allowing installation to proceed.
package requirements

import (
	"fmt"
	"strings"

	"github.com/gersonkurz/msis/internal/ir"
)

// LaunchCondition represents a WiX launch condition for a prerequisite.
type LaunchCondition struct {
	// PropertyName is the MSI property name used in the condition
	PropertyName string

	// Condition is the WiX condition expression (e.g., "VC_REDIST_X64")
	Condition string

	// Message is the user-friendly error message if condition fails
	Message string

	// RegistrySearch is the XML for registry search to detect the prerequisite
	RegistrySearch string
}

// GenerateLaunchConditions creates launch conditions for the given requirements.
// The arch parameter should be "x64" or "x86".
func GenerateLaunchConditions(requirements []ir.Requirement, arch string) []LaunchCondition {
	var conditions []LaunchCondition

	for _, req := range requirements {
		if cond := generateConditionForRequirement(req, arch); cond != nil {
			conditions = append(conditions, *cond)
		}
	}

	return conditions
}

// generateConditionForRequirement creates a launch condition for a single requirement.
func generateConditionForRequirement(req ir.Requirement, arch string) *LaunchCondition {
	switch req.Type {
	case "vcredist":
		return generateVCRedistCondition(req.Version, arch)
	case "netfx":
		return generateNetFxCondition(req.Version)
	default:
		// Unknown type - if source is provided, we can't generate a condition
		// (user is responsible for detection). Otherwise, skip.
		return nil
	}
}

// generateVCRedistCondition creates a launch condition for VC++ Redistributable.
// VC++ 2015-2022 all use the same registry key family.
// Supported architectures: x64, x86, arm64
func generateVCRedistCondition(version, arch string) *LaunchCondition {
	// Property name based on version and arch
	propName := fmt.Sprintf("VCREDIST_%s_%s", strings.ToUpper(arch), normalizeVersion(version))

	// Registry key path - VC++ 2015+ use the 14.0 key
	var regKey string
	switch arch {
	case "x64":
		regKey = `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64`
	case "arm64":
		regKey = `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\arm64`
	default: // x86
		regKey = `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x86`
	}

	// Registry search XML
	regSearch := fmt.Sprintf(
		`<Property Id="%s">`+
			`<RegistrySearch Id="%s_Search" Root="HKLM" Key="%s" Name="Installed" Type="raw"/>`+
			`</Property>`,
		propName, propName, regKey)

	// Friendly display name
	displayName := fmt.Sprintf("Microsoft Visual C++ %s Redistributable (%s)", version, arch)

	return &LaunchCondition{
		PropertyName:   propName,
		Condition:      propName,
		Message:        fmt.Sprintf("%s is required but not installed. Please install it first.", displayName),
		RegistrySearch: regSearch,
	}
}

// generateNetFxCondition creates a launch condition for .NET Framework.
// Uses the Release DWORD value to detect version.
func generateNetFxCondition(version string) *LaunchCondition {
	// Get the minimum release value for this version
	minRelease, displayName := getNetFxReleaseValue(version)
	if minRelease == 0 {
		return nil // Unknown version
	}

	propName := fmt.Sprintf("NETFX%s_RELEASE", strings.ReplaceAll(version, ".", ""))

	// Registry search for .NET Framework release value
	regSearch := fmt.Sprintf(
		`<Property Id="%s">`+
			`<RegistrySearch Id="%s_Search" Root="HKLM" Key="SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full" Name="Release" Type="raw"/>`+
			`</Property>`,
		propName, propName)

	// Condition: property must exist and be >= minimum release value
	condition := fmt.Sprintf("%s >= %d", propName, minRelease)

	return &LaunchCondition{
		PropertyName:   propName,
		Condition:      condition,
		Message:        fmt.Sprintf("%s is required but not installed. Please install it first.", displayName),
		RegistrySearch: regSearch,
	}
}

// getNetFxReleaseValue returns the minimum Release DWORD value for a .NET Framework version.
// Reference: https://learn.microsoft.com/en-us/dotnet/framework/migration-guide/how-to-determine-which-versions-are-installed
func getNetFxReleaseValue(version string) (int, string) {
	switch version {
	case "4.8.1":
		return 533320, "Microsoft .NET Framework 4.8.1"
	case "4.8":
		return 528040, "Microsoft .NET Framework 4.8"
	case "4.7.2":
		return 461808, "Microsoft .NET Framework 4.7.2"
	case "4.7.1":
		return 461308, "Microsoft .NET Framework 4.7.1"
	case "4.7":
		return 460798, "Microsoft .NET Framework 4.7"
	case "4.6.2":
		return 394802, "Microsoft .NET Framework 4.6.2"
	default:
		return 0, ""
	}
}

// normalizeVersion converts version string to property-safe format.
func normalizeVersion(version string) string {
	return strings.ReplaceAll(version, ".", "_")
}

// GenerateXML generates the complete WiX XML for launch conditions.
// Returns two strings: registry searches XML and launch conditions XML.
func GenerateXML(conditions []LaunchCondition) (registrySearches string, launchConditions string) {
	var regBuilder, condBuilder strings.Builder

	for _, cond := range conditions {
		regBuilder.WriteString("        ")
		regBuilder.WriteString(cond.RegistrySearch)
		regBuilder.WriteString("\n")

		condBuilder.WriteString(fmt.Sprintf(
			`        <Launch Condition="%s" Message="%s"/>`+"\n",
			escapeXML(cond.Condition),
			escapeXML(cond.Message)))
	}

	return regBuilder.String(), condBuilder.String()
}

// escapeXML escapes special characters for XML attribute values.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
