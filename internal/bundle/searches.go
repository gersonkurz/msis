package bundle

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gersonkurz/msis/internal/ir"
)

// Registry searches (#90, decisions D30).
//
// An <exe detect> is a Burn condition, and a Burn condition can only test variables. A
// <search> sets one from the registry: it becomes a util:RegistrySearch under <Bundle>, in the
// templates' {{{SEARCHES}}} placeholder. msis emits Result and Bitness explicitly, never WiX's
// defaults: WiX defaults Result to "value" where msis defaults it to "exists", and the default
// Bitness follows the engine's own bitness (an x86 Burn), which is exactly the trap a required
// bitness is there to avoid.
//
// Searches here are independent: none may depend on another's result, since document order
// is not an ordering contract in Burn (that would need After=, which msis does not offer).

// burnBuiltins are Burn's built-in variables (docs.firegiant.com/wix/tools/burn/builtin-variables).
// Burn refuses a search that writes most of them at run time; msis refuses them all at build
// time, with a message. Every name starting "Wix" is reserved too (engine and BA variables).
var burnBuiltins = []string{
	"AdminToolsFolder", "AppDataFolder", "CommonAppDataFolder", "CommonFiles64Folder",
	"CommonFiles6432Folder", "CommonFilesFolder", "CompatibilityMode", "ComputerName", "Date",
	"DesktopFolder", "FavoritesFolder", "FontsFolder", "InstallerInformationalVersion",
	"InstallerName", "InstallerVersion", "LocalAppDataFolder", "LogonUser", "MyPicturesFolder",
	"NativeMachine", "NTProductType", "NTSuiteBackOffice", "NTSuiteDataCenter",
	"NTSuiteEnterprise", "NTSuitePersonal", "NTSuiteSmallBusiness",
	"NTSuiteSmallBusinessRestricted", "NTSuiteWebServer", "PersonalFolder", "Privileged",
	"ProcessorArchitecture", "ProgramFiles64Folder", "ProgramFiles6432Folder",
	"ProgramFilesFolder", "ProgramMenuFolder", "RebootPending", "SendToFolder",
	"ServicePackLevel", "StartMenuFolder", "StartupFolder", "System64Folder", "SystemFolder",
	"SystemLanguageID", "TempFolder", "TemplateFolder", "TerminalServer", "UserLanguageID",
	"UserUILanguageID", "VersionMsi", "VersionNT", "VersionNT64", "WindowsBuildNumber",
	"WindowsFolder", "WindowsVolume",
}

// templateVariables are the variables the shipped bundle templates and msis's prerequisites
// define or consume, plus the WixStdBA launch variables a template may configure. A search
// writing one would silently change what the template or a prerequisite's detect means.
var templateVariables = []string{
	"InstallFolder", "ProgramFiles64Dir", "PfRoot", "LaunchTarget", "LaunchArguments",
	"LaunchHidden", "LaunchTargetElevatedId", "LaunchWorkingFolder",
	"VcppRuntimeX64Installed", "VcppRuntimeX86Installed", "VcppRuntimeArm64Installed",
	"NETFRAMEWORK45",
}

// checkSearches refuses a search whose variable is reserved or set twice, and warns about one
// that no <exe> in this script refers to - probably a typo, though a custom template may use it.
// Burn variable names are case-insensitive, so both checks are.
func checkSearches(bundle *ir.Bundle) (warnings []string, err error) {
	seen := map[string]bool{}
	var problems []string
	for _, s := range bundle.Searches {
		name := strings.ToLower(s.Variable)
		switch {
		case strings.HasPrefix(name, "wix"):
			problems = append(problems, fmt.Sprintf("<search variable=%q>: names starting with Wix are reserved for Burn and its bootstrapper application", s.Variable))
		case slices.ContainsFunc(burnBuiltins, func(b string) bool { return strings.EqualFold(b, s.Variable) }):
			problems = append(problems, fmt.Sprintf("<search variable=%q>: that is a Burn built-in variable", s.Variable))
		case slices.ContainsFunc(templateVariables, func(b string) bool { return strings.EqualFold(b, s.Variable) }):
			problems = append(problems, fmt.Sprintf("<search variable=%q>: the bundle templates or msis's prerequisites already use that variable", s.Variable))
		case seen[name]:
			problems = append(problems, fmt.Sprintf("<search variable=%q>: two searches set it", s.Variable))
		}
		seen[name] = true

		word := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(s.Variable) + `\b`)
		used := slices.ContainsFunc(bundle.ExePackages, func(e ir.ExePackage) bool {
			return word.MatchString(e.DetectCondition) || word.MatchString(e.InstallArgs)
		})
		if !used {
			warnings = append(warnings, fmt.Sprintf("<search variable=%q>: no <exe> in this script refers to it "+
				"(in detect or args); a misspelt name would leave the package undetected", s.Variable))
		}
	}
	if len(problems) > 0 {
		return warnings, fmt.Errorf("bundle searches (#90, decisions D30):\n- %s", strings.Join(problems, "\n- "))
	}
	return warnings, nil
}

// generateSearches renders the searches, in document order.
func generateSearches(bundle *ir.Bundle) string {
	var sb strings.Builder
	for _, s := range bundle.Searches {
		value := ""
		if s.Value != "" {
			value = fmt.Sprintf(" Value='%s'", escapeXMLAttr(s.Value))
		}
		fmt.Fprintf(&sb, "    <util:RegistrySearch Id='MSIS_Search_%s' Variable='%s' Root='%s' Key='%s'%s Result='%s' Bitness='always%s'/>\n",
			s.Variable, s.Variable, s.Root, escapeXMLAttr(s.Key), value, s.Result, s.Bitness)
	}
	return sb.String()
}
