package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/variables"
)

func TestGetLCID(t *testing.T) {
	vars := variables.New()
	r := NewRenderer(vars, "", "", nil)

	tests := []struct {
		language string
		wantLCID string
	}{
		{"en-us", "1033"},
		{"English", "1033"},
		{"en-gb", "2057"},
		{"de-de", "1031"},
		{"German", "1031"},
		{"fr-fr", "1036"},
		{"fr-ca", "3084"},
		{"es-es", "3082"},
		{"ja-jp", "1041"},
		{"zh-cn", "2052"},
		{"ru-ru", "1049"},
		{"pl-pl", "1045"},
		{"unknown", "1033"}, // Default to English
	}

	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			vars["LANGUAGE"] = tt.language
			got := r.getLCID()
			if got != tt.wantLCID {
				t.Errorf("getLCID(%q) = %q, want %q", tt.language, got, tt.wantLCID)
			}
		})
	}
}

func TestGetCodepage(t *testing.T) {
	vars := variables.New()
	r := NewRenderer(vars, "", "", nil)

	tests := []struct {
		language     string
		wantCodepage string
	}{
		{"en-us", "1252"},   // Western European
		{"de-de", "1252"},   // Western European
		{"pl-pl", "1250"},   // Central European
		{"ru-ru", "1251"},   // Cyrillic
		{"ja-jp", "932"},    // Japanese Shift-JIS
		{"zh-cn", "936"},    // Simplified Chinese GBK
		{"ko-kr", "949"},    // Korean
		{"unknown", "1252"}, // Default
	}

	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			vars["LANGUAGE"] = tt.language
			got := r.getCodepage()
			if got != tt.wantCodepage {
				t.Errorf("getCodepage(%q) = %q, want %q", tt.language, got, tt.wantCodepage)
			}
		})
	}
}

func TestGetTemplatePath(t *testing.T) {
	// Create temp directory to avoid platform-specific path issues
	tmpDir, err := os.MkdirTemp("", "template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	vars := variables.New()
	r := NewRenderer(vars, tmpDir, "", nil)

	tests := []struct {
		platform   string
		silent     bool
		wantSuffix string
	}{
		{"x64", false, "x64/template.wxs"},
		{"x64", true, "x64/template-silent.wxs"},
		{"x86", false, "x86/template.wxs"},
		{"X64", false, "x64/template.wxs"},         // Case insensitive
		{"arm64", false, "x64/template.wxs"},       // arm64 is 64-bit -> ProgramFiles64Folder
		{"ARM64", true, "x64/template-silent.wxs"}, // case insensitive
		{"", false, "x64/template.wxs"},            // default to 64-bit, matching msis's x64 default
	}

	for _, tt := range tests {
		got := r.getTemplatePath(tt.platform, tt.silent)
		// Normalize path separators for comparison
		got = filepath.ToSlash(got)
		if !strings.HasSuffix(got, tt.wantSuffix) {
			t.Errorf("getTemplatePath(%q, %v) = %q, want suffix %q", tt.platform, tt.silent, got, tt.wantSuffix)
		}
	}
}

func TestBuildContext(t *testing.T) {
	vars := variables.New()
	vars["PRODUCT_NAME"] = "Test Product"
	vars["PRODUCT_VERSION"] = "1.0.0"
	vars["LANGUAGE"] = "en-us"

	data := &generator.GeneratedOutput{
		DirectoryXML: "<Directory Id='TEST'/>",
		FeatureXML:   "<Feature Id='FEATURE_00000'/>",
	}

	r := NewRenderer(vars, "/templates", "", data)
	ctx := r.buildContext()

	// Check variables are copied
	if ctx["PRODUCT_NAME"] != "Test Product" {
		t.Error("expected PRODUCT_NAME to be copied to context")
	}

	// Check LCID
	if ctx["LCID"] != "1033" {
		t.Errorf("LCID = %v, want 1033", ctx["LCID"])
	}

	// Check generated content
	if ctx["FEATURES"] != "<Feature Id='FEATURE_00000'/>" {
		t.Errorf("FEATURES = %v, want feature XML", ctx["FEATURES"])
	}

	if ctx["INSTALLDIR_FILES"] != "<Directory Id='TEST'/>" {
		t.Errorf("INSTALLDIR_FILES = %v, want directory XML", ctx["INSTALLDIR_FILES"])
	}
}

func TestLogoDefaultsNoPrefix(t *testing.T) {
	// Create temp directory to avoid platform-specific path issues
	tmpDir, err := os.MkdirTemp("", "logo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	vars := variables.New()
	// No LOGO_BANNER, LOGO_DIALOG, LOGO_BOOTSTRAP set
	// No LOGO_PREFIX set - logos should be empty (WiX uses its defaults)

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, tmpDir, "", data)
	ctx := r.buildContext()

	// Without LOGO_PREFIX, no logo defaults should be set
	if ctx["LOGO_BANNER"] != nil {
		t.Errorf("LOGO_BANNER = %v, want nil (no default)", ctx["LOGO_BANNER"])
	}
	if ctx["LOGO_DIALOG"] != nil {
		t.Errorf("LOGO_DIALOG = %v, want nil (no default)", ctx["LOGO_DIALOG"])
	}
	if ctx["LOGO_BOOTSTRAP"] != nil {
		t.Errorf("LOGO_BOOTSTRAP = %v, want nil (no default)", ctx["LOGO_BOOTSTRAP"])
	}
}

func TestLogoDefaultsWithPrefix(t *testing.T) {
	// Create temp directory and logo files
	tmpDir, err := os.MkdirTemp("", "logo-prefix-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the logo files so they can be found
	logoFiles := []string{
		"CUSTOM_WixUiBanner.bmp",
		"CUSTOM_WixUiDialog.bmp",
		"CUSTOM_LogoBootstrap.bmp",
	}
	for _, f := range logoFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create logo file %s: %v", f, err)
		}
	}

	vars := variables.New()
	vars["LOGO_PREFIX"] = "CUSTOM"

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, tmpDir, "", data)
	ctx := r.buildContext()

	// Should use custom prefix - check suffix since path is absolute
	bannerStr, _ := ctx["LOGO_BANNER"].(string)
	if !strings.HasSuffix(filepath.ToSlash(bannerStr), "CUSTOM_WixUiBanner.bmp") {
		t.Errorf("LOGO_BANNER = %v, want suffix CUSTOM_WixUiBanner.bmp", bannerStr)
	}
}

func TestLogoDefaultsNotOverridden(t *testing.T) {
	vars := variables.New()
	vars["LOGO_BANNER"] = "/explicit/path/banner.bmp"
	vars["LOGO_DIALOG"] = "/explicit/path/dialog.bmp"

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, "/templates", "", data)
	ctx := r.buildContext()

	// Explicit values should be preserved
	if ctx["LOGO_BANNER"] != "/explicit/path/banner.bmp" {
		t.Errorf("LOGO_BANNER = %v, should preserve explicit value", ctx["LOGO_BANNER"])
	}
	if ctx["LOGO_DIALOG"] != "/explicit/path/dialog.bmp" {
		t.Errorf("LOGO_DIALOG = %v, should preserve explicit value", ctx["LOGO_DIALOG"])
	}
}

func TestCustomTemplateOverride(t *testing.T) {
	// Create a temp directory with a custom template
	tmpDir, err := os.MkdirTemp("", "msis-template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write custom template
	customTemplate := `CUSTOM:{{PRODUCT_NAME}}`
	customPath := filepath.Join(tmpDir, "custom.wxs")
	if err := os.WriteFile(customPath, []byte(customTemplate), 0644); err != nil {
		t.Fatalf("failed to write custom template: %v", err)
	}

	vars := variables.New()
	vars["PRODUCT_NAME"] = "MyApp"

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, tmpDir, "", data)
	r.SetCustomTemplate(customPath)

	result, err := r.Render()
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if result != "CUSTOM:MyApp" {
		t.Errorf("expected 'CUSTOM:MyApp', got %q", result)
	}
}

func TestRenderWithMinimalTemplate(t *testing.T) {
	// Create a minimal template in a temp directory
	tmpDir, err := os.MkdirTemp("", "msis-template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create x64 directory
	x64Dir := filepath.Join(tmpDir, "x64")
	if err := os.MkdirAll(x64Dir, 0755); err != nil {
		t.Fatalf("failed to create x64 dir: %v", err)
	}

	// Write minimal template
	minimalTemplate := `<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
    <Package Name="{{PRODUCT_NAME}}" Version="{{PRODUCT_VERSION}}">
        {{{FEATURES}}}
        <StandardDirectory Id="ProgramFiles64Folder">
            {{{INSTALLDIR_FILES}}}
        </StandardDirectory>
    </Package>
</Wix>`

	templatePath := filepath.Join(x64Dir, "template.wxs")
	if err := os.WriteFile(templatePath, []byte(minimalTemplate), 0644); err != nil {
		t.Fatalf("failed to write template: %v", err)
	}

	// Create renderer
	vars := variables.New()
	vars["PRODUCT_NAME"] = "Test App"
	vars["PRODUCT_VERSION"] = "2.0.0"
	vars["PLATFORM"] = "x64"

	data := &generator.GeneratedOutput{
		DirectoryXML: "<Directory Id='INSTALLDIR' Name='TestApp'/>",
		FeatureXML:   "<Feature Id='FEATURE_00000' Title='Main'/>",
	}

	r := NewRenderer(vars, tmpDir, "", data)

	result, err := r.Render()
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Verify output
	if !strings.Contains(result, "Name=\"Test App\"") {
		t.Error("expected output to contain product name")
	}
	if !strings.Contains(result, "Version=\"2.0.0\"") {
		t.Error("expected output to contain version")
	}
	if !strings.Contains(result, "FEATURE_00000") {
		t.Error("expected output to contain feature")
	}
	if !strings.Contains(result, "INSTALLDIR") {
		t.Error("expected output to contain install directory")
	}
}

// TestRegularTemplateHookGating renders the real x64/x86 MSI templates and verifies the
// folder-wipe custom action appears only when BOTH REMOVE_FOLDERS_ON_UNINSTALL and
// USE_INSTALLER_HOOKS are set, and that the RETAIN_FILES_ON_UNINSTALL property is emitted
// only when the variable is non-empty.
func TestRegularTemplateHookGating(t *testing.T) {
	for _, arch := range []string{"x64", "x86"} {
		content, err := os.ReadFile(filepath.Join("..", "..", "templates", arch, "template.wxs"))
		if err != nil {
			t.Fatalf("reading %s template: %v", arch, err)
		}
		base := func() map[string]interface{} {
			return map[string]interface{}{
				"PRODUCT_NAME": "App", "PRODUCT_VERSION": "1.0.0", "MANUFACTURER": "ACME",
				"UPGRADE_CODE": "{00000000-0000-0000-0000-000000000000}", "DLL_ENTRY": "msi-simplica.dll",
			}
		}
		render := func(ctx map[string]interface{}) string {
			out, err := RenderString(string(content), ctx)
			if err != nil {
				t.Fatalf("%s render: %v", arch, err)
			}
			return out
		}

		// folders + hooks => action present
		c := base()
		c["REMOVE_FOLDERS_ON_UNINSTALL"] = true
		c["USE_INSTALLER_HOOKS"] = true
		if !strings.Contains(render(c), "RemoveAllFoldersOnUninstall") {
			t.Errorf("%s: expected RemoveAllFoldersOnUninstall when both flags set", arch)
		}

		// folders but no hooks => action absent (the bug that wiped data without hooks)
		c = base()
		c["REMOVE_FOLDERS_ON_UNINSTALL"] = true
		if strings.Contains(render(c), "RemoveAllFoldersOnUninstall") {
			t.Errorf("%s: RemoveAllFoldersOnUninstall must NOT appear without USE_INSTALLER_HOOKS", arch)
		}

		// retain property only when set
		c = base()
		if strings.Contains(render(c), `Id="RETAIN_FILES_ON_UNINSTALL"`) {
			t.Errorf("%s: RETAIN property must be absent when variable unset", arch)
		}
		c["RETAIN_FILES_ON_UNINSTALL"] = `[APPDATADIR]DATABASE\proakt.db`
		out := render(c)
		if !strings.Contains(out, `Id="RETAIN_FILES_ON_UNINSTALL"`) || !strings.Contains(out, `DATABASE\proakt.db`) {
			t.Errorf("%s: expected RETAIN property with value when set:\n%s", arch, out)
		}
	}
}

// TestStartExe renders the real MSI templates and checks the "Launch Product" exit-dialog wiring:
// it appears only when START_EXE is set, and the value is passed into WixShellExecTarget verbatim
// (a Formatted path like [INSTALLDIR]App.exe), NOT wrapped in the old [#FileId] form.
func TestStartExe(t *testing.T) {
	for _, arch := range []string{"x64", "x86"} {
		content, err := os.ReadFile(filepath.Join("..", "..", "templates", arch, "template.wxs"))
		if err != nil {
			t.Fatalf("reading %s template: %v", arch, err)
		}
		base := map[string]interface{}{
			"PRODUCT_NAME": "App", "PRODUCT_VERSION": "1.0.0", "MANUFACTURER": "ACME",
			"UPGRADE_CODE": "{00000000-0000-0000-0000-000000000000}", "DLL_ENTRY": "msi-simplica.dll",
		}
		render := func(ctx map[string]interface{}) string {
			out, err := RenderString(string(content), ctx)
			if err != nil {
				t.Fatalf("%s render: %v", arch, err)
			}
			return out
		}

		// Absent: no launch wiring.
		if got := render(base); strings.Contains(got, "WixShellExecTarget") || strings.Contains(got, "LaunchApplication") {
			t.Errorf("%s: launch wiring must be absent when START_EXE is unset", arch)
		}

		// Present: value passes through verbatim into WixShellExecTarget, no [#...] wrapper.
		c := map[string]interface{}{}
		for k, v := range base {
			c[k] = v
		}
		c["START_EXE"] = `[INSTALLDIR]App.exe`
		out := render(c)
		want := `<Property Id="WixShellExecTarget" Value="[INSTALLDIR]App.exe" />`
		if !strings.Contains(out, want) {
			t.Errorf("%s: expected %q in output:\n%s", arch, want, out)
		}
		if strings.Contains(out, `Value="[#`) {
			t.Errorf("%s: START_EXE must not be wrapped in the legacy [#FileId] form:\n%s", arch, out)
		}
		if !strings.Contains(out, `DllEntry="WixShellExec"`) {
			t.Errorf("%s: expected LaunchApplication custom action when START_EXE is set", arch)
		}
	}
}

// TestHookDllDirInTemplate verifies the hook DLL Binary SourceFile is arch-native, driven by
// HOOK_DLL_DIR (so an arm64 build — which uses the x64 template — loads the arm64 DLL).
func TestHookDllDirInTemplate(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "templates", "x64", "template.wxs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"x64", "arm64"} {
		out, err := RenderString(string(content), map[string]interface{}{
			"PRODUCT_NAME": "App", "PRODUCT_VERSION": "1.0.0", "MANUFACTURER": "ACME",
			"UPGRADE_CODE":        "{00000000-0000-0000-0000-000000000000}",
			"USE_INSTALLER_HOOKS": true, "DLL_ENTRY": "msi-simplica.dll", "HOOK_DLL_DIR": dir,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := `SourceFile="` + dir + `/msi-simplica.dll"`
		if !strings.Contains(out, want) {
			t.Errorf("HOOK_DLL_DIR=%q: expected %q in rendered Binary", dir, want)
		}
	}
}

// TestRegistryCleanupGating renders the real x86 regular AND silent templates and verifies the
// registry-tree cleanup (prep CA that supplies CustomActionData + the deferred CA) is emitted only
// when both REMOVE_REGISTRY_TREE is active and USE_INSTALLER_HOOKS=True.
func TestRegistryCleanupGating(t *testing.T) {
	for _, tpl := range []string{"template.wxs", "template-silent.wxs"} {
		content, err := os.ReadFile(filepath.Join("..", "..", "templates", "x86", tpl))
		if err != nil {
			t.Fatalf("reading %s: %v", tpl, err)
		}
		base := func() map[string]interface{} {
			return map[string]interface{}{
				"PRODUCT_NAME": "App", "PRODUCT_VERSION": "1.0.0", "MANUFACTURER": "ACME",
				"UPGRADE_CODE": "{00000000-0000-0000-0000-000000000000}", "DLL_ENTRY": "msi-simplica.dll",
				"TEMPLATE_FOLDER": "/tpl",
			}
		}
		render := func(ctx map[string]interface{}) string {
			out, err := RenderString(string(content), ctx)
			if err != nil {
				t.Fatalf("%s render: %v", tpl, err)
			}
			return out
		}

		// active + hooks => prep CA (CustomActionData) AND deferred CA present
		c := base()
		c["REMOVE_REGISTRY_TREE"] = `HKLM\Software\X`
		c["USE_INSTALLER_HOOKS"] = true
		out := render(c)
		if !strings.Contains(out, "RemoveRegistryTreeOnUninstallPrep") || !strings.Contains(out, `DllEntry="RemoveRegistryTreeOnUninstall"`) {
			t.Errorf("%s: expected prep + deferred registry CA when active+hooks", tpl)
		}

		// active + NO hooks => no registry cleanup at all
		c = base()
		c["REMOVE_REGISTRY_TREE"] = `HKLM\Software\X`
		if strings.Contains(render(c), "RemoveRegistryTree") {
			t.Errorf("%s: registry cleanup must NOT render without USE_INSTALLER_HOOKS", tpl)
		}

		// inactive (empty, as the renderer normalizes false-like) + hooks => no registry cleanup
		c = base()
		c["REMOVE_REGISTRY_TREE"] = ""
		c["USE_INSTALLER_HOOKS"] = true
		if strings.Contains(render(c), "RemoveRegistryTree") {
			t.Errorf("%s: registry cleanup must NOT render when inactive", tpl)
		}
	}
}

// TestBuildContextRegistryTreeNormalization verifies false-like REMOVE_REGISTRY_TREE values become
// empty in the render context (so {{#if REMOVE_REGISTRY_TREE}} gates correctly), while a real path
// passes through.
func TestBuildContextRegistryTreeNormalization(t *testing.T) {
	data := &generator.GeneratedOutput{}
	for _, falsey := range []string{"False", "no", "OFF", "0", "", "  "} {
		vars := variables.New()
		vars["REMOVE_REGISTRY_TREE"] = falsey
		ctx := NewRenderer(vars, "/t", "", data).buildContext()
		if ctx["REMOVE_REGISTRY_TREE"] != "" {
			t.Errorf("REMOVE_REGISTRY_TREE=%q should normalize to empty, got %q", falsey, ctx["REMOVE_REGISTRY_TREE"])
		}
	}
	vars := variables.New()
	vars["REMOVE_REGISTRY_TREE"] = `HKLM\Software\X`
	ctx := NewRenderer(vars, "/t", "", data).buildContext()
	if ctx["REMOVE_REGISTRY_TREE"] != `HKLM\Software\X` {
		t.Errorf("active path should pass through, got %q", ctx["REMOVE_REGISTRY_TREE"])
	}
}

// TestBundleLaunchTarget renders the real bundle template and checks that the
// success-page "Launch" button (LaunchTarget Burn variable) appears only when
// LAUNCH_TARGET is set, and that a bracketed Formatted path passes through verbatim.
func TestBundleLaunchTarget(t *testing.T) {
	tmplPath := filepath.Join("..", "..", "templates", "bundle.wxs")
	content, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("reading bundle template: %v", err)
	}

	base := map[string]interface{}{
		"PRODUCT_NAME":    "MSIS",
		"PRODUCT_VERSION": "3.0.3",
		"MANUFACTURER":    "ACME",
		"UPGRADE_CODE":    "{00000000-0000-0000-0000-000000000000}",
		"LICENSE_URL":     "",
		"CHAIN":           "<MsiPackage SourceFile='x.msi'/>",
	}

	// Absent: no LaunchTarget variable (the {{#if}} guard).
	absent, err := RenderString(string(content), base)
	if err != nil {
		t.Fatalf("render (absent): %v", err)
	}
	if strings.Contains(absent, `Name="LaunchTarget"`) {
		t.Errorf("LaunchTarget should be absent when LAUNCH_TARGET is unset:\n%s", absent)
	}

	// Present: LaunchTarget with the bracketed path passed through untouched.
	withTarget := map[string]interface{}{}
	for k, v := range base {
		withTarget[k] = v
	}
	withTarget["LAUNCH_TARGET"] = `[InstallFolder]\environ.exe`
	present, err := RenderString(string(content), withTarget)
	if err != nil {
		t.Fatalf("render (present): %v", err)
	}
	want := `<Variable Name="LaunchTarget" Value="[InstallFolder]\environ.exe"/>`
	if !strings.Contains(present, want) {
		t.Errorf("expected %q in output:\n%s", want, present)
	}
}
