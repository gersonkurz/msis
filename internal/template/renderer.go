// Package template renders WiX templates using Handlebars.
package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aymerick/raymond"
	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/variables"
)

// Renderer fills WiX templates with generated content.
type Renderer struct {
	Variables          variables.Dictionary
	TemplateFolder     string // Base template folder (public defaults)
	CustomTemplates    string // Overlay folder (private overrides, takes precedence)
	GeneratedData      *generator.GeneratedOutput
	CustomTemplate     string // Optional: explicit template file path
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

	// Map platform to folder
	platformFolder := "x86"
	if strings.EqualFold(platform, "x64") {
		platformFolder = "x64"
	}

	// Use overlay resolution
	return r.resolveTemplatePath(filepath.Join(platformFolder, templateName))
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

	// Apply logo defaults if not set (msis-2.x compatible)
	// Use overlay resolution to find logos in custom folder first, then base
	logoPrefix := r.Variables["LOGO_PREFIX"]
	if logoPrefix == "" {
		logoPrefix = "NGBT"
	}

	if r.Variables["LOGO_BANNER"] == "" {
		ctx["LOGO_BANNER"] = r.resolveTemplatePath(logoPrefix + "_WixUiBanner.bmp")
	}
	if r.Variables["LOGO_DIALOG"] == "" {
		ctx["LOGO_DIALOG"] = r.resolveTemplatePath(logoPrefix + "_WixUiDialog.bmp")
	}
	if r.Variables["LOGO_BOOTSTRAP"] == "" {
		ctx["LOGO_BOOTSTRAP"] = r.resolveTemplatePath(logoPrefix + "_LogoBootstrap.bmp")
	}

	// Add generated content (triple-braced in template for unescaped output)
	ctx["FEATURES"] = r.GeneratedData.FeatureXML
	ctx["INSTALLDIR_FILES"] = r.buildInstallDirFiles()
	ctx["APPDATADIR_FILES"] = r.buildAppDataDirFiles()
	ctx["DESKTOP_FILES"] = r.buildDesktopFiles()
	ctx["STARTMENU_FILES"] = r.buildStartMenuFiles()
	ctx["REGISTRY_ENTRIES"] = "" // TODO: Phase 4
	ctx["CUSTOM_ACTIONS"] = r.buildCustomActions()
	ctx["INSTALL_EXECUTE_SEQUENCE"] = r.buildInstallExecuteSequence()

	// Add boolean flags for conditional rendering
	ctx["SETUP_ICON"] = r.Variables["SETUP_ICON"]
	ctx["DLL_CUSTOM"] = r.Variables["DLL_CUSTOM"]
	ctx["REPAIR_ENABLED"] = r.Variables.GetBool("REPAIR_ENABLED")
	ctx["REMOVE_ENABLED"] = r.Variables.GetBool("REMOVE_ENABLED")
	ctx["REMOVE_REGISTRY_TREE"] = r.Variables["REMOVE_REGISTRY_TREE"]
	ctx["REMOVE_FOLDERS_ON_UNINSTALL"] = r.Variables.GetBool("REMOVE_FOLDERS_ON_UNINSTALL")
	ctx["DO_NOT_UPGRADE_FROM"] = r.Variables["DO_NOT_UPGRADE_FROM"]
	ctx["DO_NOT_UPGRADE_MESSAGE"] = r.Variables["DO_NOT_UPGRADE_MESSAGE"]
	ctx["START_EXE"] = r.Variables["START_EXE"]
	ctx["SCHEDULE_REBOOT"] = r.Variables.GetBool("SCHEDULE_REBOOT")

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
	"en-us":    {"1033", "1252"},
	"en-gb":    {"2057", "1252"},
	"en-au":    {"3081", "1252"},
	"en-ca":    {"4105", "1252"},
	"english":  {"1033", "1252"},
	// German variants
	"de-de":    {"1031", "1252"},
	"de-at":    {"3079", "1252"},
	"de-ch":    {"2055", "1252"},
	"german":   {"1031", "1252"},
	// French variants
	"fr-fr":    {"1036", "1252"},
	"fr-ca":    {"3084", "1252"},
	"fr-ch":    {"4108", "1252"},
	"french":   {"1036", "1252"},
	// Spanish variants
	"es-es":    {"3082", "1252"},
	"es-mx":    {"2058", "1252"},
	"spanish":  {"3082", "1252"},
	// Italian
	"it-it":    {"1040", "1252"},
	"italian":  {"1040", "1252"},
	// Portuguese
	"pt-br":    {"1046", "1252"},
	"pt-pt":    {"2070", "1252"},
	"portuguese": {"1046", "1252"},
	// Dutch
	"nl-nl":    {"1043", "1252"},
	"dutch":    {"1043", "1252"},
	// Polish
	"pl-pl":    {"1045", "1250"},
	"polish":   {"1045", "1250"},
	// Russian
	"ru-ru":    {"1049", "1251"},
	"russian":  {"1049", "1251"},
	// Japanese
	"ja-jp":    {"1041", "932"},
	"japanese": {"1041", "932"},
	// Chinese
	"zh-cn":    {"2052", "936"},
	"zh-tw":    {"1028", "950"},
	"chinese":  {"2052", "936"},
	// Korean
	"ko-kr":    {"1042", "949"},
	"korean":   {"1042", "949"},
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
	// TODO: Extract APPDATADIR content from generator
	return ""
}

func (r *Renderer) buildDesktopFiles() string {
	// TODO: Extract DesktopFolder content from generator
	return ""
}

func (r *Renderer) buildStartMenuFiles() string {
	// TODO: Extract StartMenu content from generator
	return ""
}

func (r *Renderer) buildCustomActions() string {
	// TODO: Build custom actions from execute elements
	return ""
}

func (r *Renderer) buildInstallExecuteSequence() string {
	// TODO: Build install execute sequence from execute elements
	return ""
}
