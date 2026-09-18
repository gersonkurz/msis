package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aymerick/raymond"
	"github.com/gersonkurz/msis/internal/generator"
)

// msiTemplates are the shipped templates rendered through Render/RenderSilent. The bundle
// templates are deliberately absent: they go through renderBundleTemplate with a different
// context and consume none of these placeholders.
var msiTemplates = []string{
	"../../templates/x64/template.wxs",
	"../../templates/x86/template.wxs",
	"../../templates/x86/template-silent.wxs",
	"../../templates/minimal/template.wxs",
	"../../templates/minimal-x86/template.wxs",
}

// generatedContentKeys lists the same placeholders generatedContent supplies. It is written
// out rather than derived from the map so that the two have to agree: a key added to the
// renderer without being added to every template fails here, which is the drift that caused
// issue #19.
var generatedContentKeys = []string{
	"APPDATADIR_FILES",
	"COMMONFILESDIR_FILES",
	"CUSTOM_ACTIONS",
	"DESKTOP_FILES",
	"FEATURES",
	"INSTALLDIR_FILES",
	"INSTALL_EXECUTE_SEQUENCE",
	"LAUNCH_CONDITIONS",
	"LAUNCH_CONDITION_SEARCHES",
	"LOCALAPPDATADIR_FILES",
	"PRESERVATION_PROPERTIES",
	"REGISTRY_ENTRIES",
	"REMOVE_ON_UNINSTALL",
	"ROAMINGAPPDATADIR_FILES",
	"STARTMENU_FILES",
	"SYSTEMDIR_FILES",
	"WINDOWSDIR_FILES",
}

// allGenerated is a generatedContent map where every key has content, i.e. the worst case a
// template has to be able to host.
func allGenerated() map[string]string {
	m := make(map[string]string, len(generatedContentKeys))
	for _, key := range generatedContentKeys {
		m[key] = "<Generated/>"
	}
	return m
}

// TestGeneratedContentKeysMatchRenderer keeps the list above honest. Without it, a key added
// to generatedContent and to no template would still pass the coverage test below, because
// the test would not know to look for it.
func TestGeneratedContentKeysMatchRenderer(t *testing.T) {
	r := &Renderer{GeneratedData: &generator.GeneratedOutput{}}

	actual := make(map[string]bool)
	for key := range r.generatedContent() {
		actual[key] = true
	}
	expected := make(map[string]bool)
	for _, key := range generatedContentKeys {
		expected[key] = true
	}

	for key := range actual {
		if !expected[key] {
			t.Errorf("generatedContent supplies %q, which generatedContentKeys does not list - "+
				"add it there and to every template in msiTemplates", key)
		}
	}
	for key := range expected {
		if !actual[key] {
			t.Errorf("generatedContentKeys lists %q, which generatedContent no longer supplies", key)
		}
	}
}

// TestShippedTemplatesCoverGeneratedContent is the invariant behind issue #19: a template with
// nowhere to put generated content discards it in silence, and wix build succeeds on the
// truncated package. Every shipped MSI template must be able to host every kind of generated
// content, because which kinds a build produces depends on the .msis script, not the template.
//
// It runs the production check rather than re-deriving one, so the two cannot disagree about
// what counts as covered.
func TestShippedTemplatesCoverGeneratedContent(t *testing.T) {
	for _, tmpl := range msiTemplates {
		data, err := os.ReadFile(tmpl)
		if err != nil {
			t.Fatalf("reading %s: %v", tmpl, err)
		}
		if err := checkTemplateCoverage(filepath.Base(tmpl), string(data), shippedTemplateContext(), allGenerated()); err != nil {
			t.Errorf("%v", err)
		}
	}
}

// shippedTemplateContext supplies the variables the shipped templates branch on, so a
// placeholder is not reported missing merely because the probe render took an else branch.
// Everything it turns on widens what the templates emit; nothing here can hide a placeholder.
func shippedTemplateContext() map[string]interface{} {
	return map[string]interface{}{
		"USE_INSTALLER_HOOKS":         true,
		"REMOVE_FOLDERS_ON_UNINSTALL": true,
		"REMOVE_REGISTRY_TREE":        `HKLM\Software\Probe`,
		"REPAIR_ENABLED":              true,
		"REMOVE_ENABLED":              true,
		"SCHEDULE_REBOOT":             true,
		"SETUP_ICON":                  "setup.ico",
		"DLL_CUSTOM":                  "custom.dll",
		"DLL_ENTRY":                   "msi-simplica.dll",
		"HOOK_DLL_DIR":                "x64",
		"DO_NOT_UPGRADE_FROM":         "1.0.0",
		"DO_NOT_UPGRADE_MESSAGE":      "no",
		"START_EXE":                   "[INSTALLDIR]app.exe",
		"LICENSE_FILE":                "license.rtf",
	}
}

func TestCheckTemplateCoverage(t *testing.T) {
	const key = "PRESERVATION_PROPERTIES"
	generated := map[string]string{key: "<Property Id='PS_RV_00000'/>"}

	cases := []struct {
		name     string
		template string
		wantErr  bool
	}{
		{
			name:     "missing placeholder is rejected",
			template: `<Package>{{{FEATURES}}}</Package>`,
			wantErr:  true,
		},
		{
			name:     "raw spelling is accepted",
			template: `<Package>{{{PRESERVATION_PROPERTIES}}}</Package>`,
		},
		{
			// Escaped output mangles the XML, but the content IS in the package and WiX
			// reports the damage loudly. This check is only about silence.
			name:     "escaped spelling is accepted",
			template: `<Package>{{PRESERVATION_PROPERTIES}}</Package>`,
		},
		{
			// raymond supports these unescaped spellings (handlebars/basic_test.go:246 and
			// whitespace_test.go:40). A template using one has always worked, and must keep
			// working - the check asks the engine rather than pattern-matching braces.
			name:     "ampersand spelling is accepted",
			template: `<Package>{{&PRESERVATION_PROPERTIES}}</Package>`,
		},
		{
			name:     "whitespace-control spelling is accepted",
			template: `<Package> {{~{PRESERVATION_PROPERTIES}~}} </Package>`,
		},
		{
			name:     "inner whitespace is accepted",
			template: `<Package>{{{ PRESERVATION_PROPERTIES }}}</Package>`,
		},
		{
			// The placeholder is substituted, but into text WiX never reads: the properties
			// are as absent from the package as if the placeholder were missing.
			name:     "placeholder inside an XML comment is rejected",
			template: `<Package><!-- {{{PRESERVATION_PROPERTIES}}} --></Package>`,
			wantErr:  true,
		},
		{
			name:     "placeholder inside a multi-line XML comment is rejected",
			template: "<Package>\n<!-- disabled for now:\n{{{PRESERVATION_PROPERTIES}}}\n-->\n</Package>",
			wantErr:  true,
		},
		{
			// A Handlebars comment emits nothing at all.
			name:     "placeholder inside a Handlebars comment is rejected",
			template: `<Package>{{!-- {{{PRESERVATION_PROPERTIES}}} --}}</Package>`,
			wantErr:  true,
		},
		{
			// A name that appears only in a condition is not a home for the content.
			name:     "conditional reference is not a placeholder",
			template: `<Package>{{#if PRESERVATION_PROPERTIES}}x{{/if}}</Package>`,
			wantErr:  true,
		},
		{
			// A placeholder in a branch this build does not take is not coverage either.
			name:     "placeholder in an untaken branch is rejected",
			template: `<Package>{{#if MISSING_FLAG}}{{{PRESERVATION_PROPERTIES}}}{{/if}}</Package>`,
			wantErr:  true,
		},
		{
			name:     "placeholder in a taken branch is accepted",
			template: `<Package>{{#if SET_FLAG}}{{{PRESERVATION_PROPERTIES}}}{{/if}}</Package>`,
		},
		{
			// LAUNCH_CONDITIONS must not be satisfied by LAUNCH_CONDITION_SEARCHES, whose name
			// it is nearly a prefix of.
			name:     "near-miss placeholder name does not satisfy the check",
			template: `<Package>{{{PRESERVATION_PROPERTIES_EXTRA}}}</Package>`,
			wantErr:  true,
		},
	}

	ctx := map[string]interface{}{"SET_FLAG": true}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkTemplateCoverage("t.wxs", tc.template, ctx, generated)
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("expected an error naming %s, got none", key)
			case tc.wantErr && !strings.Contains(err.Error(), key):
				t.Errorf("error does not name %s: %v", key, err)
			case !tc.wantErr && err != nil:
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestAcceptedSpellingsActuallyEmitTheContent is the other half of the accepted cases above:
// the check must accept a spelling because it works, not merely because it looks familiar.
// Each template here is rendered with the real content and must contain it verbatim.
func TestAcceptedSpellingsActuallyEmitTheContent(t *testing.T) {
	const content = `<Property Id="PS_RV_00000" Value="customer-value"/>`

	for _, tmpl := range []string{
		`<Package>{{{PRESERVATION_PROPERTIES}}}</Package>`,
		`<Package>{{&PRESERVATION_PROPERTIES}}</Package>`,
		`<Package> {{~{PRESERVATION_PROPERTIES}~}} </Package>`,
		`<Package>{{{ PRESERVATION_PROPERTIES }}}</Package>`,
	} {
		ctx := map[string]interface{}{"PRESERVATION_PROPERTIES": content}
		out, err := raymond.Render(tmpl, ctx)
		if err != nil {
			t.Fatalf("rendering %s: %v", tmpl, err)
		}
		if !strings.Contains(out, content) {
			t.Errorf("%s does not emit the content unescaped, so the check should not accept it:\n%s", tmpl, out)
		}
		if err := checkTemplateCoverage("t.wxs", tmpl, map[string]interface{}{},
			map[string]string{"PRESERVATION_PROPERTIES": content}); err != nil {
			t.Errorf("%s emits the content but the check rejected it: %v", tmpl, err)
		}
	}
}

// TestCheckTemplateCoverageEmptyContent covers the rule that keeps the check usable: a
// template is free to omit a placeholder for a feature the .msis script does not use.
func TestCheckTemplateCoverageEmptyContent(t *testing.T) {
	for _, content := range []string{"", "\n   \n"} {
		err := checkTemplateCoverage("t.wxs", `<Package/>`, map[string]interface{}{},
			map[string]string{"PRESERVATION_PROPERTIES": content})
		if err != nil {
			t.Errorf("content %q generated nothing, so the missing placeholder is not a problem: %v", content, err)
		}
	}
}

// TestCheckTemplateCoverageMessageIsDeterministic guards the review focus on diffable output:
// the missing keys come out of a map, so they are sorted before being reported.
func TestCheckTemplateCoverageMessageIsDeterministic(t *testing.T) {
	generated := map[string]string{
		"REMOVE_ON_UNINSTALL":     "<RemoveFolderEx/>",
		"PRESERVATION_PROPERTIES": "<Property/>",
		"APPDATADIR_FILES":        "<Component/>",
	}
	ctx := map[string]interface{}{}

	first := checkTemplateCoverage("t.wxs", "<Package/>", ctx, generated)
	if first == nil {
		t.Fatal("expected an error")
	}
	for range 20 {
		if got := checkTemplateCoverage("t.wxs", "<Package/>", ctx, generated); got.Error() != first.Error() {
			t.Fatalf("message varies between runs:\n%v\nvs\n%v", first, got)
		}
	}

	want := []string{"APPDATADIR_FILES", "PRESERVATION_PROPERTIES", "REMOVE_ON_UNINSTALL"}
	prev := -1
	for _, key := range want {
		at := strings.Index(first.Error(), key)
		if at < 0 {
			t.Fatalf("message does not name %s: %v", key, first)
		}
		if at < prev {
			t.Errorf("keys are not in sorted order: %v", first)
		}
		prev = at
	}
}
