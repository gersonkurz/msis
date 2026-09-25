//go:build windows

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const overridesScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Overrides"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{9E4D3A86-5F7C-4B03-9D29-4A8C6E0F3B87}"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// #73: /SET: overrides are printed in a defined order - by name - not in Go's random map
// order. Eight of them, so the old code would print them sorted by chance once in 40320 runs.
func TestOverridesPrintInADefinedOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	script := scriptFor(t, dir, "overrides.msi", overridesScript)
	overrides := map[string]string{}
	for _, name := range []string{"ZETA", "ALPHA", "MU", "PLATFORM", "BETA", "OMEGA", "GAMMA", "PRODUCT_VERSION"} {
		overrides[name] = "v-" + strings.ToLower(name)
	}
	overrides["PLATFORM"] = "x64"
	overrides["PRODUCT_VERSION"] = "2.0.0"

	out := capture(t, func() error {
		return processFile(script, &cliArgs{dryRun: true, setOverrides: overrides, templateFolder: repoTemplates(t)})
	})
	var printed []string
	for _, line := range strings.Split(out, "\n") {
		if _, rest, ok := strings.Cut(line, "Override: "); ok {
			name, _, _ := strings.Cut(rest, "=")
			printed = append(printed, name)
		}
	}
	if len(printed) != len(overrides) || !slices.IsSorted(printed) {
		t.Errorf("overrides printed as %v, want all %d sorted by name", printed, len(overrides))
	}
}
