package wix

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// DefaultVersion is the WiX version that SETUP-WIX installs when no override is given.
// msis itself works with whatever WiX 6/7 is installed (see GetWixMajorVersion); this is
// only the version provisioned on request.
const DefaultVersion = "7.0.0"

// WiX extension package IDs. These are the single source of truth: both the build path
// (runWixBuild) and the provisioning path (EnsureWix) reference them, so they cannot drift.
const (
	extUI           = "WixToolset.UI.wixext"
	extUtil         = "WixToolset.Util.wixext"
	extBootstrapper = "WixToolset.BootstrapperApplications.wixext"
	extNetfx        = "WixToolset.Netfx.wixext"
)

// msiExtensions are loaded for MSI builds.
var msiExtensions = []string{extUI, extUtil}

// bundleExtensions are loaded for bundle (bootstrapper) builds.
var bundleExtensions = []string{extBootstrapper, extUtil, extNetfx}

// AllExtensions is the union of every extension msis can use; EnsureWix installs all of them.
var AllExtensions = []string{extUI, extUtil, extBootstrapper, extNetfx}

// extArgs turns a list of extension IDs into `-ext <id>` build arguments.
func extArgs(exts []string) []string {
	args := make([]string, 0, len(exts)*2)
	for _, e := range exts {
		args = append(args, "-ext", e)
	}
	return args
}

// EnsureWix installs/updates the WiX dotnet global tool to the given version and registers
// the extensions msis needs, pinned to that version. If clean is true, other-version copies
// of the managed extensions are removed from the shared global store (silences WIX6101
// "compatible with WiX vN?" warnings). progress receives human-readable status lines.
//
// WiX extensions live in a single global per-user store shared across WiX major versions and
// tagged by version; pinning every add to one version is what avoids the mixed-version mess
// that otherwise plagues manual installs.
func EnsureWix(version string, clean bool, progress func(string)) error {
	if version == "" {
		version = DefaultVersion
	}
	if progress == nil {
		progress = func(string) {}
	}

	// 1. .NET SDK present?
	if _, err := exec.LookPath("dotnet"); err != nil {
		return fmt.Errorf(".NET SDK not found; install it from https://dotnet.microsoft.com/download then retry")
	}

	// 2. Install or update the dotnet global tool (the wix.exe msis resolves).
	current, _ := dotnetToolVersion("wix")
	switch {
	case current == "":
		progress(fmt.Sprintf("installing wix %s", version))
		if err := runQuiet("dotnet", "tool", "install", "--global", "wix", "--version", version); err != nil {
			return fmt.Errorf("installing wix: %w", err)
		}
	case current != version:
		progress(fmt.Sprintf("updating wix %s -> %s", current, version))
		if err := runQuiet("dotnet", "tool", "update", "--global", "wix", "--version", version); err != nil {
			return fmt.Errorf("updating wix: %w", err)
		}
		// `tool update --version` should land exactly that version; fall back hard if not.
		if v, _ := dotnetToolVersion("wix"); v != version {
			progress("update did not land target; reinstalling")
			_ = runQuiet("dotnet", "tool", "uninstall", "--global", "wix")
			if err := runQuiet("dotnet", "tool", "install", "--global", "wix", "--version", version); err != nil {
				return fmt.Errorf("reinstalling wix: %w", err)
			}
		}
	default:
		progress(fmt.Sprintf("wix %s already installed", current))
	}

	wixPath := GetWixPath()

	// 3. Accept the EULA persistently (WiX 7+). Required before `extension add` and
	//    `build` will run; writes a per-user acceptance file so it's a one-time step.
	if major := parseMajorVersion(version); major >= 7 {
		eulaID := fmt.Sprintf("wix%d", major)
		progress("accepting WiX EULA (" + eulaID + ")")
		if err := runQuiet(wixPath, "eula", "accept", eulaID); err != nil {
			return fmt.Errorf("accepting WiX %d EULA: %w", major, err)
		}
	}

	// 4. Register extensions, pinned to the matching version.
	for _, ext := range AllExtensions {
		spec := ext + "/" + version
		progress("adding extension " + spec)
		if err := runQuiet(wixPath, "extension", "add", "-g", spec); err != nil {
			return fmt.Errorf("adding extension %s (check network / nuget access): %w", spec, err)
		}
	}

	// 5. Optionally remove mismatched-version copies of the managed extensions.
	if clean {
		cleanMismatchedExtensions(wixPath, version, progress)
	}

	progress(fmt.Sprintf("WiX %s ready (%s)", version, wixPath))
	return nil
}

// dotnetToolVersion returns the installed version of a dotnet global tool, or "".
func dotnetToolVersion(pkg string) (string, error) {
	out, err := exec.Command("dotnet", "tool", "list", "--global").Output()
	if err != nil {
		return "", err
	}
	return parseDotnetToolVersion(string(out), pkg), nil
}

// parseDotnetToolVersion extracts the version column for pkg from `dotnet tool list` output.
func parseDotnetToolVersion(output, pkg string) string {
	pkg = strings.ToLower(pkg)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.ToLower(fields[0]) == pkg {
			return fields[1]
		}
	}
	return ""
}

var extListLine = regexp.MustCompile(`^\s*(WixToolset\.\S+)\s+(\d[\w.\-+]*)`)

// cleanMismatchedExtensions removes copies of the managed extensions whose version differs
// from the target, leaving the global store free of the noise that triggers WIX6101 warnings.
func cleanMismatchedExtensions(wixPath, version string, progress func(string)) {
	out, err := exec.Command(wixPath, "extension", "list", "-g").Output()
	if err != nil {
		progress("warning: could not list extensions for cleanup: " + err.Error())
		return
	}
	managed := make(map[string]bool, len(AllExtensions))
	for _, e := range AllExtensions {
		managed[e] = true
	}
	for _, line := range strings.Split(string(out), "\n") {
		m := extListLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, ver := m[1], m[2]
		if managed[name] && ver != version {
			spec := name + "/" + ver
			if err := runQuiet(wixPath, "extension", "remove", "-g", spec); err != nil {
				progress("warning: could not remove " + spec)
			} else {
				progress("removed " + spec)
			}
		}
	}
}

// runQuiet runs a command, suppressing its output on success and surfacing it on failure.
func runQuiet(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("%v\n%s", err, trimmed)
		}
		return err
	}
	return nil
}
