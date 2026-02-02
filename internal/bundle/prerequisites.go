// Package bundle provides WiX Bundle (bootstrapper) generation.
package bundle

// PrerequisiteDef defines a well-known prerequisite package.
type PrerequisiteDef struct {
	// DisplayName shown in bootstrapper UI
	DisplayName string

	// Source file name (use {arch} placeholder for x86/x64 variants)
	Source string

	// DetectCondition is a WiX burn condition to check if already installed
	DetectCondition string

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
			DisplayName:     "Microsoft Visual C++ 2015-2022 Redistributable ({arch})",
			Source:          "vc_redist.{arch}.exe",
			DetectCondition: vcRedistDetect2022,
			InstallArgs:     "/install /quiet /norestart",
			PerMachine:      true,
		},
		"2019": {
			DisplayName:     "Microsoft Visual C++ 2015-2019 Redistributable ({arch})",
			Source:          "vc_redist.{arch}.exe",
			DetectCondition: vcRedistDetect2019,
			InstallArgs:     "/install /quiet /norestart",
			PerMachine:      true,
		},
		"2017": {
			DisplayName:     "Microsoft Visual C++ 2017 Redistributable ({arch})",
			Source:          "vc_redist.{arch}.exe",
			DetectCondition: vcRedistDetect2017,
			InstallArgs:     "/install /quiet /norestart",
			PerMachine:      true,
		},
		"2015": {
			DisplayName:     "Microsoft Visual C++ 2015 Redistributable ({arch})",
			Source:          "vc_redist.{arch}.exe",
			DetectCondition: vcRedistDetect2015,
			InstallArgs:     "/install /quiet /norestart",
			PerMachine:      true,
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
// These check the Installed DWORD value in the appropriate registry key
// Reference: https://learn.microsoft.com/en-us/cpp/windows/redistributing-visual-cpp-files

// 2022 (14.30+) - same key as 2015-2019, higher version
const vcRedistDetect2022 = `(VersionNT64 AND EXISTS("HKLM\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64\Installed")) OR (NOT VersionNT64 AND EXISTS("HKLM\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x86\Installed"))`

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

// ExpandArch replaces {arch} placeholder with x64 or x86.
func ExpandArch(s string, is64bit bool) string {
	if is64bit {
		return replaceArch(s, "x64")
	}
	return replaceArch(s, "x86")
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
