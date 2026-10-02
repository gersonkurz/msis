package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
)

const probeSearch = "    <util:RegistrySearch Id='MSIS_Search_X' Variable='X' Root='HKLM' Key='K' Result='exists' Bitness='always32'/>\n"

// TestBothShippedBundleTemplatesPlaceTheSearches (#90, D30): the regular and the silent bundle
// template each put the searches directly under <Bundle>, outside <Chain>, unescaped.
func TestBothShippedBundleTemplatesPlaceTheSearches(t *testing.T) {
	for _, name := range []string{"bundle.wxs", "bundle-silent.wxs"} {
		path := filepath.Join("..", "..", "templates", name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		vars := variables.New()
		vars["LICENSE_URL"] = "https://example.com"
		ctx, _ := BuildBundleContext(vars, "<MsiPackage SourceFile='a.msi'/>", probeSearch, ".", "", "")
		if err := CheckBundleCoverage(path, string(content), ctx, "<MsiPackage SourceFile='a.msi'/>", probeSearch); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		out, err := RenderString(string(content), ctx)
		if err != nil {
			t.Fatal(err)
		}
		at, chain := strings.Index(out, strings.TrimSpace(probeSearch)), strings.Index(out, "<Chain>")
		if at < 0 || chain < 0 || at > chain {
			t.Errorf("%s: the search is not rendered, unescaped, before <Chain>", name)
		}
	}
}

// TestABundleTemplateWithoutSearchesFailsOnlyWhenThereAreSome: a custom template that has no
// {{{SEARCHES}}}, or has it commented out, fails once the script has a <search>; a script
// without searches builds with it as before.
func TestABundleTemplateWithoutSearchesFailsOnlyWhenThereAreSome(t *testing.T) {
	chain := "<MsiPackage SourceFile='a.msi'/>"
	for name, tmpl := range map[string]string{
		"missing":   "<Bundle><Chain>{{{CHAIN}}}</Chain></Bundle>",
		"commented": "<Bundle><!-- {{{SEARCHES}}} --><Chain>{{{CHAIN}}}</Chain></Bundle>",
	} {
		ctx, _ := BuildBundleContext(variables.New(), chain, probeSearch, ".", "", "")
		err := CheckBundleCoverage("custom.wxs", tmpl, ctx, chain, probeSearch)
		if err == nil || !strings.Contains(err.Error(), "SEARCHES") || !strings.Contains(err.Error(), "D30") {
			t.Errorf("%s: want the coverage error, got %v", name, err)
		}
		ctx, _ = BuildBundleContext(variables.New(), chain, "", ".", "", "")
		if err := CheckBundleCoverage("custom.wxs", tmpl, ctx, chain, ""); err != nil {
			t.Errorf("%s: a script without searches must still build: %v", name, err)
		}
	}
	ctx, _ := BuildBundleContext(variables.New(), chain, "", ".", "", "")
	if err := CheckBundleCoverage("custom.wxs", "<Bundle><Chain></Chain></Bundle>", ctx, chain, ""); err == nil || !strings.Contains(err.Error(), "CHAIN") {
		t.Errorf("a template without {{{CHAIN}}} must fail too: %v", err)
	}
}

// TestASearchesVariableCannotReplaceTheGenerated: SEARCHES and CHAIN are set after the user's
// variables, so a /SET:SEARCHES=... cannot replace what the script generated.
func TestASearchesVariableCannotReplaceTheGenerated(t *testing.T) {
	vars := variables.New()
	vars["SEARCHES"], vars["CHAIN"] = "user value", "user value"
	ctx, _ := BuildBundleContext(vars, "chain", "searches", ".", "", "")
	if ctx["SEARCHES"] != "searches" || ctx["CHAIN"] != "chain" {
		t.Errorf("CHAIN=%v SEARCHES=%v", ctx["CHAIN"], ctx["SEARCHES"])
	}
}
