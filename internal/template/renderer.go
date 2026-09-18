// Package template renders WiX templates using Handlebars.
package template

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aymerick/raymond"
	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/variables"
)

// Renderer fills WiX templates with generated content.
type Renderer struct {
	Variables       variables.Dictionary
	TemplateFolder  string // Base template folder (public defaults)
	CustomTemplates string // Overlay folder (private overrides, takes precedence)
	GeneratedData   *generator.GeneratedOutput
	CustomTemplate  string // Optional: explicit template file path
	SourceDir       string // Directory of the .msis script; first logo search root (matches WiX bind paths)

	// LogoWarnings collects diagnostics from logo resolution during the last buildContext call
	// (e.g. a LOGO_PREFIX-derived or explicit logo file that could not be found). The caller
	// surfaces these to the user so a missing asset is not silently replaced by the WiX default.
	LogoWarnings []string
}

// NewRenderer creates a template renderer.
// customTemplates is an optional overlay folder that takes precedence over templateFolder.
func NewRenderer(vars variables.Dictionary, templateFolder, customTemplates string, data *generator.GeneratedOutput) *Renderer {
	// Convert template folders to absolute paths for consistent WXS output
	absTemplateFolder, err := filepath.Abs(templateFolder)
	if err != nil {
		absTemplateFolder = templateFolder
	}

	absCustomTemplates := ""
	if customTemplates != "" {
		absCustomTemplates, err = filepath.Abs(customTemplates)
		if err != nil {
			absCustomTemplates = customTemplates
		}
	}

	return &Renderer{
		Variables:       vars,
		TemplateFolder:  absTemplateFolder,
		CustomTemplates: absCustomTemplates,
		GeneratedData:   data,
	}
}

// resolveTemplatePath finds a file in the overlay folder first, then base folder.
func (r *Renderer) resolveTemplatePath(relativePath string) string {
	// Check custom templates folder first (overlay)
	if r.CustomTemplates != "" {
		customPath := filepath.Join(r.CustomTemplates, relativePath)
		if _, err := os.Stat(customPath); err == nil {
			return customPath
		}
	}
	// Fall back to base template folder
	return filepath.Join(r.TemplateFolder, relativePath)
}

// SetCustomTemplate sets an explicit template file to use instead of the default.
func (r *Renderer) SetCustomTemplate(templatePath string) {
	r.CustomTemplate = templatePath
}

// Render processes the template and returns the complete WXS content.
func (r *Renderer) Render() (string, error) {
	// Determine template path
	var templatePath string
	if r.CustomTemplate != "" {
		templatePath = r.CustomTemplate
	} else {
		platform := r.Variables.Platform()
		templatePath = r.getTemplatePath(platform, false)
	}

	// Read template
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading template: %w", err)
	}

	// Build context for Handlebars
	ctx := r.buildContext()

	if err := checkTemplateCoverage(templatePath, string(templateContent), ctx, r.generatedContent()); err != nil {
		return "", err
	}

	// Render template
	result, err := raymond.Render(string(templateContent), ctx)
	if err != nil {
		return "", fmt.Errorf("rendering template: %w", err)
	}

	return result, nil
}

// RenderSilent processes the silent template if available.
func (r *Renderer) RenderSilent() (string, error) {
	platform := r.Variables.Platform()
	templatePath := r.getTemplatePath(platform, true)

	// Check if silent template exists
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		return "", nil // No silent template
	}

	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading silent template: %w", err)
	}

	ctx := r.buildContext()

	if err := checkTemplateCoverage(templatePath, string(templateContent), ctx, r.generatedContent()); err != nil {
		return "", err
	}

	result, err := raymond.Render(string(templateContent), ctx)
	if err != nil {
		return "", fmt.Errorf("rendering silent template: %w", err)
	}

	return result, nil
}

func (r *Renderer) getTemplatePath(platform string, silent bool) string {
	var templateName string
	if silent {
		templateName = "template-silent.wxs"
	} else {
		templateName = "template.wxs"
	}

	// Map platform to template folder. x64 and arm64 are both 64-bit and install under
	// ProgramFiles64Folder (the x64 template); only x86 uses the 32-bit ProgramFilesFolder
	// template. Mapping arm64 to x86 here would install an ARM64 MSI under Program Files (x86),
	// disagreeing with the bundle's native [InstallFolder] (and breaking the Launch button).
	platformFolder := "x64"
	if strings.EqualFold(platform, "x86") {
		platformFolder = "x86"
	}

	// Use overlay resolution
	return r.resolveTemplatePath(filepath.Join(platformFolder, templateName))
}

// generatedContent returns the template placeholders that carry XML msis generated from the
// .msis script, as opposed to plain variables the user set. It is the single source of truth
// for that set: buildContext fills the render context from it, and checkTemplateCoverage
// requires the template to have a placeholder for every entry that produced content.
//
// The two belong together because a template that omits one of these fails silently.
// Handlebars renders an unknown placeholder as nothing, so the generated XML is discarded and
// `wix build` still succeeds - the package simply ships without the files, registry values or
// cleanup the script asked for. That is issue #19: templates/x86/template-silent.wxs carried
// no {{{PRESERVATION_PROPERTIES}}}, so a preserve="yes" value compiled into a registry write
// of [PS_RV_n] with no property behind it, and Windows Installer formats an undefined property
// to the empty string - overwriting the value the user asked to keep.
func (r *Renderer) generatedContent() map[string]string {
	return map[string]string{
		"FEATURES":                  r.GeneratedData.FeatureXML,
		"INSTALLDIR_FILES":          r.buildInstallDirFiles(),
		"APPDATADIR_FILES":          r.buildAppDataDirFiles(),
		"ROAMINGAPPDATADIR_FILES":   r.GeneratedData.RoamingAppDataDirXML,
		"LOCALAPPDATADIR_FILES":     r.GeneratedData.LocalAppDataDirXML,
		"COMMONFILESDIR_FILES":      r.GeneratedData.CommonFilesDirXML,
		"WINDOWSDIR_FILES":          r.GeneratedData.WindowsDirXML,
		"SYSTEMDIR_FILES":           r.GeneratedData.SystemDirXML,
		"DESKTOP_FILES":             r.buildDesktopFiles(),
		"STARTMENU_FILES":           r.buildStartMenuFiles(),
		"REGISTRY_ENTRIES":          r.GeneratedData.RegistryXML,
		"PRESERVATION_PROPERTIES":   r.GeneratedData.PreservationPropertiesXML,
		"CUSTOM_ACTIONS":            r.buildCustomActions(),
		"INSTALL_EXECUTE_SEQUENCE":  r.buildInstallExecuteSequence(),
		"REMOVE_ON_UNINSTALL":       r.GeneratedData.RemoveOnUninstallXML,
		"LAUNCH_CONDITION_SEARCHES": r.GeneratedData.LaunchConditionSearchXML,
		"LAUNCH_CONDITIONS":         r.GeneratedData.LaunchConditionsXML,
	}
}

// generatedContentHint gives the user something concrete per placeholder. Message text only:
// every entry of generatedContent is equally mandatory, so a gap here cannot change what the
// check does - only how well it explains itself.
var generatedContentHint = map[string]string{
	"PRESERVATION_PROPERTIES": `existing registry values marked preserve="yes" would be overwritten with an empty string on install`,
	"REMOVE_ON_UNINSTALL":     "the folders and registry keys named in <remove-on-uninstall> would never be cleaned up",
	"LAUNCH_CONDITIONS":       "the package would install without checking the <requires> prerequisites",
	"REGISTRY_ENTRIES":        "the imported .reg values would be missing from the package",
	"FEATURES":                "the package would contain no features at all",
}

// xmlComment matches an XML comment in rendered output. Content substituted inside one is
// present in the text and absent from the package, so coverage is judged after removing them.
var xmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// coverageSentinel is the probe value substituted for one placeholder. Deliberately made of
// characters HTML escaping leaves alone, so it survives the escaped ({{X}}) spelling as well
// as the raw one.
func coverageSentinel(key string) string {
	return "MSIS_COVERAGE_PROBE_" + key + "_END"
}

// checkTemplateCoverage rejects a template that has nowhere to put generated content this
// package actually produced. It fires only when there is content to lose, so a template stays
// free to omit a placeholder for a feature the .msis script does not use.
//
// It answers the question by rendering, not by scanning the template text. Handlebars decides
// what a substitution looks like - {{{X}}}, {{&X}} and {{~{X}~}} are all valid unescaped
// spellings in raymond - so any list of spellings this package kept would be a guess about
// somebody else's grammar, wrong in both directions: rejecting templates that work today, and
// accepting text that never reaches the output. Rendering a probe asks the engine instead.
//
// Substituted-but-discarded placements are then subtracted: a Handlebars comment
// ({{!-- {{{X}}} --}}) never emits the probe at all, and an XML comment (<!-- {{{X}}} -->)
// emits it into text WiX ignores. Either way the generated content does not reach the package,
// which is the thing being checked.
func checkTemplateCoverage(templatePath, templateContent string, ctx map[string]interface{}, generated map[string]string) error {
	probe := make(map[string]interface{}, len(ctx))
	for key, value := range ctx {
		probe[key] = value
	}

	expected := make(map[string]string)
	for key, xml := range generated {
		if strings.TrimSpace(xml) == "" {
			continue
		}
		sentinel := coverageSentinel(key)
		expected[key] = sentinel
		probe[key] = sentinel
	}
	if len(expected) == 0 {
		return nil
	}

	rendered, err := raymond.Render(templateContent, probe)
	if err != nil {
		// The real render runs next on the same template and reports this properly; a
		// template that does not compile is not a coverage problem.
		return nil
	}
	effective := xmlComment.ReplaceAllString(rendered, "")

	var missing []string
	for key, sentinel := range expected {
		if !strings.Contains(effective, sentinel) {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing) // deterministic message; map iteration order is not

	var sb strings.Builder
	fmt.Fprintf(&sb, "template %s discards generated content", templatePath)
	for _, key := range missing {
		fmt.Fprintf(&sb, "\n  nothing emits {{{%s}}}", key)
		if hint, ok := generatedContentHint[key]; ok {
			fmt.Fprintf(&sb, " - %s", hint)
		}
	}
	sb.WriteString("\n\nAdd the placeholder to the template - outside any comment or untaken " +
		"conditional - or build with a template that has it.")
	return errors.New(sb.String())
}

func (r *Renderer) buildContext() map[string]interface{} {
	ctx := make(map[string]interface{})

	// Copy all variables
	for key, value := range r.Variables {
		ctx[key] = value
	}

	// Add template folders (custom takes precedence for bind paths)
	ctx["TEMPLATE_FOLDER"] = r.TemplateFolder
	if r.CustomTemplates != "" {
		ctx["CUSTOM_TEMPLATES"] = r.CustomTemplates
	}

	// Add LCID (language code ID)
	ctx["LCID"] = r.getLCID()
	ctx["CODEPAGE"] = r.getCodepage()

	// Resolve MSI logo branding (explicit LOGO_BANNER/LOGO_DIALOG or the LOGO_PREFIX convention)
	// against the .msis source dir, the custom-templates overlay, then the base template folder —
	// the same order WiX uses for bind paths. A missing asset produces a warning (collected in
	// LogoWarnings) instead of silently falling back to the WiX default.
	logos := ResolveLogos(r.Variables, MSILogoVars, r.SourceDir, r.CustomTemplates, r.TemplateFolder)
	for name, value := range logos.Values {
		ctx[name] = value
	}
	r.LogoWarnings = logos.Warnings

	// Add generated content (triple-braced in template for unescaped output)
	for key, xml := range r.generatedContent() {
		ctx[key] = xml
	}

	// Add boolean flags for conditional rendering
	ctx["SETUP_ICON"] = r.Variables["SETUP_ICON"]
	ctx["DLL_CUSTOM"] = r.Variables["DLL_CUSTOM"]
	ctx["REPAIR_ENABLED"] = r.Variables.GetBool("REPAIR_ENABLED")
	ctx["REMOVE_ENABLED"] = r.Variables.GetBool("REMOVE_ENABLED")
	// REMOVE_REGISTRY_TREE is a comma-separated path list, not a boolean. Normalize false-like
	// values (False/No/Off/0/empty) to "" so {{#if REMOVE_REGISTRY_TREE}} gates correctly
	// (a non-empty "False" would otherwise be truthy in Handlebars and emit the cleanup).
	if r.Variables.RegistryTreeActive() {
		ctx["REMOVE_REGISTRY_TREE"] = r.Variables["REMOVE_REGISTRY_TREE"]
	} else {
		ctx["REMOVE_REGISTRY_TREE"] = ""
	}
	ctx["REMOVE_FOLDERS_ON_UNINSTALL"] = r.Variables.GetBool("REMOVE_FOLDERS_ON_UNINSTALL")
	// Semicolon-separated list of files the native hook DLL must NOT delete during folder cleanup.
	// msis only passes it through as an MSI property; the DLL parses and interprets it.
	ctx["RETAIN_FILES_ON_UNINSTALL"] = r.Variables["RETAIN_FILES_ON_UNINSTALL"]
	ctx["DO_NOT_UPGRADE_FROM"] = r.Variables["DO_NOT_UPGRADE_FROM"]
	ctx["DO_NOT_UPGRADE_MESSAGE"] = r.Variables["DO_NOT_UPGRADE_MESSAGE"]
	ctx["START_EXE"] = r.Variables["START_EXE"]
	// Bundle success-page "Launch" button (consumed by templates/bundle.wxs); the bundle
	// render path forwards all variables, so this also flows through there.
	ctx["LAUNCH_TARGET"] = r.Variables["LAUNCH_TARGET"]
	ctx["SCHEDULE_REBOOT"] = r.Variables.GetBool("SCHEDULE_REBOOT")
	ctx["USE_INSTALLER_HOOKS"] = r.Variables.GetBool("USE_INSTALLER_HOOKS")
	// Arch-native subfolder for the hook DLL Binary SourceFile (x86/x64/arm64). arm64 uses the x64
	// template but must load the arm64 DLL, so this is distinct from template selection.
	ctx["HOOK_DLL_DIR"] = r.Variables.HookDllDir()
	// Note: INCLUDE_VCREDIST is deprecated. Use <requires type="vcredist" version="..."/> instead.
	// Variable is no longer passed to templates; deprecation warning is shown by variables.CheckDeprecated()

	return ctx
}

// languageInfo holds LCID and codepage for a language.
type languageInfo struct {
	LCID     string
	Codepage string
}

// languageMap maps language tags to LCID and codepage (matching Windows CultureInfo).
var languageMap = map[string]languageInfo{
	// English variants
	"en-us":   {"1033", "1252"},
	"en-gb":   {"2057", "1252"},
	"en-au":   {"3081", "1252"},
	"en-ca":   {"4105", "1252"},
	"english": {"1033", "1252"},
	// German variants
	"de-de":  {"1031", "1252"},
	"de-at":  {"3079", "1252"},
	"de-ch":  {"2055", "1252"},
	"german": {"1031", "1252"},
	// French variants
	"fr-fr":  {"1036", "1252"},
	"fr-ca":  {"3084", "1252"},
	"fr-ch":  {"4108", "1252"},
	"french": {"1036", "1252"},
	// Spanish variants
	"es-es":   {"3082", "1252"},
	"es-mx":   {"2058", "1252"},
	"spanish": {"3082", "1252"},
	// Italian
	"it-it":   {"1040", "1252"},
	"italian": {"1040", "1252"},
	// Portuguese
	"pt-br":      {"1046", "1252"},
	"pt-pt":      {"2070", "1252"},
	"portuguese": {"1046", "1252"},
	// Dutch
	"nl-nl": {"1043", "1252"},
	"dutch": {"1043", "1252"},
	// Polish
	"pl-pl":  {"1045", "1250"},
	"polish": {"1045", "1250"},
	// Russian
	"ru-ru":   {"1049", "1251"},
	"russian": {"1049", "1251"},
	// Japanese
	"ja-jp":    {"1041", "932"},
	"japanese": {"1041", "932"},
	// Chinese
	"zh-cn":   {"2052", "936"},
	"zh-tw":   {"1028", "950"},
	"chinese": {"2052", "936"},
	// Korean
	"ko-kr":  {"1042", "949"},
	"korean": {"1042", "949"},
}

func (r *Renderer) getLCID() string {
	lang := strings.ToLower(r.Variables["LANGUAGE"])
	if info, ok := languageMap[lang]; ok {
		return info.LCID
	}
	return "1033" // Default to English (US)
}

func (r *Renderer) getCodepage() string {
	lang := strings.ToLower(r.Variables["LANGUAGE"])
	if info, ok := languageMap[lang]; ok {
		return info.Codepage
	}
	return "1252" // Default Western European
}

func (r *Renderer) buildInstallDirFiles() string {
	return r.GeneratedData.DirectoryXML
}

func (r *Renderer) buildAppDataDirFiles() string {
	return r.GeneratedData.AppDataDirXML
}

func (r *Renderer) buildDesktopFiles() string {
	return r.GeneratedData.DesktopXML
}

func (r *Renderer) buildStartMenuFiles() string {
	return r.GeneratedData.StartMenuXML
}

func (r *Renderer) buildCustomActions() string {
	return r.GeneratedData.CustomActionsXML
}

func (r *Renderer) buildInstallExecuteSequence() string {
	return r.GeneratedData.InstallExecuteSequence
}

// RenderString renders a template string with the given context.
// This is a standalone function for rendering arbitrary templates.
func RenderString(templateContent string, ctx map[string]interface{}) (string, error) {
	tmpl, err := raymond.Parse(templateContent)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	result, err := tmpl.Exec(ctx)
	if err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return result, nil
}
