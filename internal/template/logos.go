package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gersonkurz/msis/internal/variables"
)

// Logo variable names. These map to WiX UI images: LOGO_BANNER/LOGO_DIALOG drive the MSI
// WixUIBannerBmp/WixUIDialogBmp, and LOGO_BOOTSTRAP drives the bundle's WixStandardBootstrapperApplication
// LogoFile.
const (
	LogoBanner    = "LOGO_BANNER"
	LogoDialog    = "LOGO_DIALOG"
	LogoBootstrap = "LOGO_BOOTSTRAP"
)

// MSILogoVars are the logos used by the standalone MSI UI; BundleLogoVars are those used by the
// bundle UI. Each render path resolves only the logos it can actually display, so warnings stay
// relevant (an MSI build never warns about a missing bootstrap logo, and vice versa).
var (
	MSILogoVars    = []string{LogoBanner, LogoDialog}
	BundleLogoVars = []string{LogoBootstrap}
)

// logoSuffix is the filename suffix the LOGO_PREFIX convention appends per logo variable
// (e.g. LOGO_PREFIX="MyCompany" => "MyCompany_WixUiBanner.bmp").
var logoSuffix = map[string]string{
	LogoBanner:    "_WixUiBanner.bmp",
	LogoDialog:    "_WixUiDialog.bmp",
	LogoBootstrap: "_LogoBootstrap.bmp",
}

// LogoResolution is the result of resolving logo branding: the context values to apply (only logos
// that resolved are present; an absent entry means "use the WiX default") plus human-readable
// warnings for assets the user clearly asked for but that could not be located.
type LogoResolution struct {
	Values   map[string]string
	Warnings []string
}

// ResolveLogos resolves the requested logo variables (a subset of LOGO_BANNER/LOGO_DIALOG/
// LOGO_BOOTSTRAP) for both MSI and bundle rendering. An explicit LOGO_* value is kept verbatim —
// WiX resolves it against its bind paths at build time — but a warning is emitted if it cannot be
// located, so a typo no longer silently brands with the WiX default. Otherwise, when LOGO_PREFIX is
// set, the conventional "<prefix><suffix>" file is searched in sourceDir -> customDir -> templateDir
// (the same order WiX uses for bind paths); a hit returns its full path, a miss yields a warning.
func ResolveLogos(vars variables.Dictionary, names []string, sourceDir, customDir, templateDir string) LogoResolution {
	res := LogoResolution{Values: map[string]string{}}
	roots := nonEmptyDirs(sourceDir, customDir, templateDir)
	prefix := vars["LOGO_PREFIX"]

	for _, name := range names {
		suffix, known := logoSuffix[name]
		if !known {
			continue
		}
		switch explicit := vars[name]; {
		case explicit != "":
			res.Values[name] = explicit
			if !explicitExists(explicit, roots) {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"%s is set to %q but that file was not found (searched: %s); WiX will use its built-in default if the path does not resolve at build time",
					name, explicit, describeRoots(roots)))
			}
		case prefix != "":
			filename := prefix + suffix
			if path, found := findInRoots(filename, roots); found {
				res.Values[name] = path
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"%s: LOGO_PREFIX=%q expects %q but it was not found (searched: %s); using the WiX default",
					name, prefix, filename, describeRoots(roots)))
			}
		}
	}
	return res
}

// BuildBundleContext builds the Handlebars context for a bundle template: every variable, the
// prerequisite CHAIN, and resolved logo branding (LOGO_BOOTSTRAP, via explicit value or LOGO_PREFIX).
// It returns the context and any logo warnings for the caller to surface. This is the bundle
// counterpart of the MSI's buildContext, and the reason LOGO_PREFIX now works for bundles too.
func BuildBundleContext(vars variables.Dictionary, chainXML, sourceDir, customDir, templateDir string) (map[string]interface{}, []string) {
	ctx := make(map[string]interface{}, len(vars)+2)
	for k, v := range vars {
		ctx[k] = v
	}
	ctx["CHAIN"] = chainXML

	logos := ResolveLogos(vars, BundleLogoVars, sourceDir, customDir, templateDir)
	for name, value := range logos.Values {
		ctx[name] = value
	}
	return ctx, logos.Warnings
}

// findInRoots returns the first existing path for a bare filename across roots, in order.
func findInRoots(filename string, roots []string) (string, bool) {
	for _, root := range roots {
		p := filepath.Join(root, filename)
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// explicitExists reports whether an explicit LOGO_* value can be located — as the absolute or
// current-dir-relative path given, or relative to one of the bind-path search roots.
func explicitExists(value string, roots []string) bool {
	if fileExists(value) {
		return true
	}
	if filepath.IsAbs(value) {
		return false
	}
	_, ok := findInRoots(value, roots)
	return ok
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func nonEmptyDirs(dirs ...string) []string {
	roots := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != "" {
			roots = append(roots, d)
		}
	}
	return roots
}

func describeRoots(roots []string) string {
	if len(roots) == 0 {
		return "no search paths configured"
	}
	return strings.Join(roots, ", ")
}
