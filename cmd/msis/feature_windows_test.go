//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The script from #75, trimmed to what it exercises: every value a {{VAR}}, supplied by /SET.
const featureNameScript = `<?xml version='1.0' encoding='utf-8'?>
<setup>
    <set name="PRODUCT_NAME" value="{{PRODUCT_NAME}}"/>
    <set name="PRODUCT_VERSION" value="{{PRODUCT_VERSION}}"/>
    <set name="LANGUAGE" value="en-us"/>
    <set name="BUILD_TARGET" value="{{TARGET}}"/>
    <set name="UPGRADE_CODE" value="487D3C22-8A3F-4198-9EA3-8E041144D11D"/>
    <set name="MANUFACTURER" value="Probe"/>
    <set name="PLATFORM" value="x86" />
    <feature name="{{PRODUCT_NAME}}" enabled="yes">
        <files source="app.txt" target="INSTALLDIR"/>
    </feature>
    <feature name="Tools &amp; 'Extras'">
        <files source="tool.txt" target="[INSTALLDIR]tools"/>
    </feature>
</setup>`

// #75: a {{VAR}} in <feature name> is resolved - with /SET overrides, as msis-2.x's
// DescriptionReader translated it - and the title is XML-escaped, so a quote or an ampersand
// in a name cannot break the WXS.
func TestAFeatureNameIsResolvedAndEscaped(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	write(t, filepath.Join(dir, "tool.txt"), "a tool\n")
	script := scriptFor(t, dir, "probe.msi", featureNameScript)
	err := processFile(script, &cliArgs{templateFolder: repoTemplates(t), setOverrides: map[string]string{
		"PRODUCT_NAME": "ProAKT Standard", "PRODUCT_VERSION": "3.6.0.73",
	}})
	if err != nil {
		t.Fatal(err)
	}
	wxs, err := os.ReadFile(filepath.Join(dir, "probe.wxs"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(wxs)
	if !strings.Contains(text, "Title='ProAKT Standard'") {
		t.Errorf("the feature title is not the resolved PRODUCT_NAME")
	}
	if strings.Contains(text, "{{PRODUCT_NAME}}") {
		t.Errorf("the literal reference reached the WXS")
	}
	if !strings.Contains(text, "Title='Tools &amp; &apos;Extras&apos;'") {
		t.Errorf("a title with an ampersand and quotes is not escaped:\n%s", featureLines(text))
	}
}

func featureLines(wxs string) string {
	var out []string
	for _, l := range strings.Split(wxs, "\n") {
		if strings.Contains(l, "<Feature ") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return strings.Join(out, "\n")
}
