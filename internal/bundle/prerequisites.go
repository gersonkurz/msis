// Package bundle provides WiX Bundle (bootstrapper) generation.
package bundle

import (
	"fmt"
	"sort"
	"strings"
)

// PrerequisiteDef defines a well-known prerequisite package.
type PrerequisiteDef struct {
	// DisplayName shown in bootstrapper UI
	DisplayName string

	// Source file name (use {arch} placeholder for x86/x64 variants)
	Source string

	// DetectCondition is a WiX burn condition to check if already installed.
	// It is OS-driven: right for a bundle that chains several architectures and
	// lets the runtime follow the machine.
	DetectCondition string

	// DetectConditionX86/X64 are the per-architecture forms, used when a bundle
	// chains exactly ONE architecture of this prerequisite. That is the auto-bundle
	// case, where PLATFORM fixes the wrapped MSI's architecture and the runtime must
	// follow the PACKAGE rather than the OS — a 32-bit application needs the x86
	// runtime on 64-bit Windows just as much as on 32-bit Windows.
	// Empty means the prerequisite is architecture-neutral (netfx) and
	// DetectCondition applies whatever the architecture.
	DetectConditionX86 string
	DetectConditionX64 string

	// DetectConditionArm64 is the ARM64 form. It says how to DETECT the runtime and
	// nothing about whether msis can download an installer for it — that is a
	// separate question, answered by prereqcache.LookupDownloadURL, which the cache
	// step already consults. Keeping the two apart matters for a prerequisite
	// supplied with <requires ... source="..."/>: msis has no download for it, but
	// the runtime it installs still has to be detected correctly.
	DetectConditionArm64 string

	// InstallArgs are default command-line arguments for silent install
	InstallArgs string

	// PerMachine indicates if this is a per-machine install
	PerMachine bool
}

// Prerequisites maps type -> version -> definition.
// Usage: Prerequisites["vcredist"]["2022"]
var Prerequisites = map[string]map[string]PrerequisiteDef{
	"vcredist": {
		"2022": {
			DisplayName:          "Microsoft Visual C++ 2015-2022 Redistributable ({arch})",
			Source:               "vc_redist.{arch}.exe",
			DetectCondition:      vcRedistDetect2022,
			DetectConditionX86:   vcRedistDetectX86,
			DetectConditionX64:   vcRedistDetectX64,
			DetectConditionArm64: vcRedistDetectArm64,
			InstallArgs:          "/install /quiet /norestart",
			PerMachine:           true,
		},
		"2019": {
			DisplayName:          "Microsoft Visual C++ 2015-2019 Redistributable ({arch})",
			Source:               "vc_redist.{arch}.exe",
			DetectCondition:      vcRedistDetect2019,
			DetectConditionX86:   vcRedistDetectX86,
			DetectConditionX64:   vcRedistDetectX64,
			DetectConditionArm64: vcRedistDetectArm64,
			InstallArgs:          "/install /quiet /norestart",
			PerMachine:           true,
		},
		"2017": {
			DisplayName:          "Microsoft Visual C++ 2017 Redistributable ({arch})",
			Source:               "vc_redist.{arch}.exe",
			DetectCondition:      vcRedistDetect2017,
			DetectConditionX86:   vcRedistDetectX86,
			DetectConditionX64:   vcRedistDetectX64,
			DetectConditionArm64: vcRedistDetectArm64,
			InstallArgs:          "/install /quiet /norestart",
			PerMachine:           true,
		},
		"2015": {
			DisplayName:          "Microsoft Visual C++ 2015 Redistributable ({arch})",
			Source:               "vc_redist.{arch}.exe",
			DetectCondition:      vcRedistDetect2015,
			DetectConditionX86:   vcRedistDetectX86,
			DetectConditionX64:   vcRedistDetectX64,
			DetectConditionArm64: vcRedistDetectArm64,
			InstallArgs:          "/install /quiet /norestart",
			PerMachine:           true,
		},
	},
	"netfx": {
		"4.8.1": {
			DisplayName:     "Microsoft .NET Framework 4.8.1",
			Source:          "ndp481-x86-x64-allos-enu.exe",
			DetectCondition: "NETFRAMEWORK45 >= 533320",
			InstallArgs:     "/passive /norestart",
			PerMachine:      true,
		},
		"4.8": {
			DisplayName:     "Microsoft .NET Framework 4.8",
			Source:          "ndp48-x86-x64-allos-enu.exe",
			DetectCondition: "NETFRAMEWORK45 >= 528040",
			InstallArgs:     "/passive /norestart",
			PerMachine:      true,
		},
		"4.7.2": {
			DisplayName:     "Microsoft .NET Framework 4.7.2",
			Source:          "ndp472-kb4054530-x86-x64-allos-enu.exe",
			DetectCondition: "NETFRAMEWORK45 >= 461808",
			InstallArgs:     "/passive /norestart",
			PerMachine:      true,
		},
		"4.7.1": {
			DisplayName:     "Microsoft .NET Framework 4.7.1",
			Source:          "ndp471-kb4033342-x86-x64-allos-enu.exe",
			DetectCondition: "NETFRAMEWORK45 >= 461308",
			InstallArgs:     "/passive /norestart",
			PerMachine:      true,
		},
		"4.7": {
			DisplayName:     "Microsoft .NET Framework 4.7",
			Source:          "ndp47-kb3186497-x86-x64-allos-enu.exe",
			DetectCondition: "NETFRAMEWORK45 >= 460798",
			InstallArgs:     "/passive /norestart",
			PerMachine:      true,
		},
		"4.6.2": {
			DisplayName:     "Microsoft .NET Framework 4.6.2",
			Source:          "ndp462-kb3151800-x86-x64-allos-enu.exe",
			DetectCondition: "NETFRAMEWORK45 >= 394802",
			InstallArgs:     "/passive /norestart",
			PerMachine:      true,
		},
	},
}

// VC++ Redistributable detection conditions
// References Burn variables populated by <util:RegistrySearch> elements in the
// bundle templates (templates/bundle.wxs and templates/bundle-silent.wxs).
// Burn's condition language does NOT support EXISTS() — that's MSI Launch
// Condition syntax. The searches probe HKLM\…\VC\Runtimes\{x64|x86}\Installed
// and set VcppRuntimeX64Installed / VcppRuntimeX86Installed (1 = installed).
// Reference: https://learn.microsoft.com/en-us/cpp/windows/redistributing-visual-cpp-files

// 2022 (14.30+) - same key as 2015-2019, higher version
const vcRedistDetect2022 = `(VersionNT64 AND VcppRuntimeX64Installed) OR (NOT VersionNT64 AND VcppRuntimeX86Installed)`

// Per-architecture forms, for a bundle that chains exactly one architecture.
// Both variables come from the bundle templates' util:RegistrySearch elements.
// VcppRuntimeX86Installed uses Bitness="always32", so on 64-bit Windows it reads
// HKLM\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\14.0\VC\Runtimes\x86 — the key
// vc_redist.x86.exe writes, and the same physical key a 32-bit MSI's own
// launch-condition search reads under WOW64 redirection. Bundle and MSI therefore
// agree on what "installed" means, which is the property the fix turns on.
const vcRedistDetectX86 = `VcppRuntimeX86Installed`
const vcRedistDetectX64 = `VcppRuntimeX64Installed`

// ARM64. Like the two above, this is the same for every 14.x version, because they
// share one registry key family. VcppRuntimeArm64Installed reads
// ...\VC\Runtimes\arm64 with Bitness="always64" — the same key internal/requirements
// gives the MSI's own ARM64 launch condition, so bundle and MSI agree on what
// "installed" means.
//
// Whether msis can DOWNLOAD an ARM64 installer for a given version is unrelated and
// lives in internal/prereqcache; detection must be right either way, including when
// the author supplies their own installer via <requires ... source="..."/>.
const vcRedistDetectArm64 = `VcppRuntimeArm64Installed`

// 2019 (14.20-14.29)
const vcRedistDetect2019 = vcRedistDetect2022 // Same detection, different installer

// 2017 (14.10-14.19)
const vcRedistDetect2017 = vcRedistDetect2022 // Same registry key family

// 2015 (14.0)
const vcRedistDetect2015 = vcRedistDetect2022 // Same registry key family

// LookupPrerequisite finds a prerequisite definition by type and version.
// Returns nil if not found.
func LookupPrerequisite(prereqType, version string) *PrerequisiteDef {
	if versions, ok := Prerequisites[prereqType]; ok {
		if def, ok := versions[version]; ok {
			return &def
		}
	}
	return nil
}

// ValidatePrerequisite checks if a prerequisite type/version is known.
// Returns an error with helpful message if invalid.
func ValidatePrerequisite(prereqType, version string) error {
	if versions, ok := Prerequisites[prereqType]; ok {
		if _, ok := versions[version]; ok {
			return nil // Valid
		}
		// Type exists but version doesn't
		var available []string
		for v := range versions {
			available = append(available, v)
		}
		sort.Strings(available) // map order is random (#73)
		return fmt.Errorf("unknown %s version '%s'; available versions: %s", prereqType, version, strings.Join(available, ", "))
	}
	// Unknown type
	var types []string
	for t := range Prerequisites {
		types = append(types, t)
	}
	sort.Strings(types)
	return fmt.Errorf("unknown prerequisite type '%s'; available types: %s", prereqType, strings.Join(types, ", "))
}

// ExpandArch replaces the {arch} placeholder with x64 or x86.
// It cannot name ARM64; use ExpandArchName for that.
func ExpandArch(s string, is64bit bool) string {
	if is64bit {
		return replaceArch(s, "x64")
	}
	return replaceArch(s, "x86")
}

// ExpandArchName replaces the {arch} placeholder with the given architecture.
// The caller chooses the spelling, which differs between the two uses: the
// download is "vc_redist.arm64.exe" while the display name reads "(ARM64)".
func ExpandArchName(s, arch string) string {
	return replaceArch(s, arch)
}

func replaceArch(s, arch string) string {
	result := s
	for i := 0; i < len(result); i++ {
		if i+6 <= len(result) && result[i:i+6] == "{arch}" {
			result = result[:i] + arch + result[i+6:]
		}
	}
	return result
}
