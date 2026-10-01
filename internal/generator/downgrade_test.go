package generator

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// TestComparableVersionOrdersLikeTheVersions: Windows Installer compares the guard's strings
// lexicographically, so the padding must make that order the numeric one - the fourth field
// included, and across a change in digit count (4.2.0.9 < 4.2.0.10), which unpadded strings
// get wrong.
func TestComparableVersionOrdersLikeTheVersions(t *testing.T) {
	for in, want := range map[string]string{
		"4.2.0.90":            "00004.00002.00000.00090",
		"4.2.0":               "00004.00002.00000.00000",
		"1":                   "00001.00000.00000.00000",
		"255.255.65535.65535": "00255.00255.65535.65535",
	} {
		got, err := comparableVersion(in)
		if err != nil || got != want {
			t.Errorf("comparableVersion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	ascending := []string{"4.2.0.9", "4.2.0.10", "4.2.0.82", "4.2.0.90", "4.2.1", "4.10.0.0", "10.0"}
	padded := make([]string, len(ascending))
	for i, v := range ascending {
		var err error
		if padded[i], err = comparableVersion(v); err != nil {
			t.Fatal(err)
		}
	}
	if !sort.StringsAreSorted(padded) {
		t.Errorf("padded versions do not sort as the versions do: %v", padded)
	}

	for _, bad := range []string{"4.2.0.90.1", "4.2.x", "4.2.0.65536", "-1", "4..2"} {
		if _, err := comparableVersion(bad); err == nil {
			t.Errorf("comparableVersion(%q) accepted it", bad)
		}
	}
}

// generateGuarded builds a package with the given features, each installing one file, at
// PRODUCT_VERSION version.
func generateGuarded(t *testing.T, version string, features ...string) (*GeneratedOutput, *Context, error) {
	t.Helper()
	work := t.TempDir()
	setup := &ir.Setup{}
	for _, name := range features {
		if err := os.WriteFile(filepath.Join(work, name+".txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		setup.Features = append(setup.Features, ir.Feature{Name: name, Enabled: true, Allowed: true,
			Items:       []ir.Item{ir.Files{Source: name + ".txt", Target: "[INSTALLDIR]"}},
			SubFeatures: []ir.Feature{{Name: name + " child", Enabled: true, Allowed: true}}})
	}
	vars := variables.New()
	vars["PRODUCT_VERSION"] = version
	vars["UPGRADE_CODE"] = "{a54ab31f-b689-458b-a82b-c0d3c17709ac}"
	ctx := NewContext(setup, vars, work)
	out, err := ctx.Generate()
	return out, ctx, err
}

// TestTheDowngradeGuardIsPinned (#89, D27): the search, the condition and the recorded value,
// as literals, since a guard that drifts between two releases would compare one release's
// value against another's format.
func TestTheDowngradeGuardIsPinned(t *testing.T) {
	out, _, err := generateGuarded(t, "4.2.0.90", "Main")
	if err != nil {
		t.Fatal(err)
	}
	key := `Software\msis\Packages\A54AB31F-B689-458B-A82B-C0D3C17709AC`
	for _, want := range []string{
		`<RegistrySearch Id='MSIS_INSTALLED_VERSION_SEARCH' Root='HKLM' Key='` + key + `' Name='Version' Type='raw'/>`,
	} {
		if !strings.Contains(out.LaunchConditionSearchXML, want) {
			t.Errorf("search: want %s in\n%s", want, out.LaunchConditionSearchXML)
		}
	}
	cond := `<Launch Condition='Installed OR NOT MSIS_INSTALLED_VERSION OR MSIS_INSTALLED_VERSION &lt;= &quot;00004.00002.00000.00090&quot;'`
	if !strings.Contains(out.LaunchConditionsXML, cond) {
		t.Errorf("condition: want %s in\n%s", cond, out.LaunchConditionsXML)
	}
	value := `<RegistryValue Root='HKLM' Key='` + key + `' Name='Version' Type='string' Value='00004.00002.00000.00090' KeyPath='yes'/>`
	if !strings.Contains(out.RegistryXML, value) {
		t.Errorf("component: want %s in\n%s", value, out.RegistryXML)
	}
}

// TestTheGuardComponentJoinsEveryTopLevelFeature: whichever features an install selects, the
// version is recorded, so the next package can read it. Sub-features need no reference: a
// sub-feature is installed only with its parent.
func TestTheGuardComponentJoinsEveryTopLevelFeature(t *testing.T) {
	out, ctx, err := generateGuarded(t, "1.2.3.4", "Main", "Extra", "Debug")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(out.RegistryXML, "<Component Id='")
	if start < 0 {
		t.Fatalf("no guard component in %s", out.RegistryXML)
	}
	id, _ := attrValue(out.RegistryXML[start:], "<Component Id='")
	for _, index := range []string{"0", "1", "2"} {
		if feature := ctx.featureIDs[index]; !slices.Contains(ctx.FeatureComponents[feature], id) {
			t.Errorf("top-level feature %s does not reference the guard %s", feature, id)
		}
	}
	if n := strings.Count(out.FeatureXML, "<ComponentRef Id='"+id+"'/>"); n != 3 {
		t.Errorf("the guard is referenced %d times, want once per top-level feature (3)", n)
	}
}

// TestTheGuardJoinsThePackageItemsFeature: items written directly under <setup> install through
// the generated MSIS_PACKAGE_ITEMS feature, and with every authored feature disabled that is
// all an install installs - the version must be recorded then too (review of #89). Both ways
// the feature comes about: a top-level <files>, and a top-level <remove-on-uninstall>, whose
// component is only registered by the remove-on-uninstall pass after item processing.
func TestTheGuardJoinsThePackageItemsFeature(t *testing.T) {
	for name, item := range map[string]ir.Item{
		"files":               ir.Files{Source: "top.txt", Target: "[INSTALLDIR]"},
		"remove-on-uninstall": ir.RemoveOnUninstall{Folder: "[APPDATADIR]cache"},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			if err := os.WriteFile(filepath.Join(work, "top.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			setup := &ir.Setup{
				Items:    []ir.Item{item},
				Features: []ir.Feature{{Name: "Optional", Enabled: false, Allowed: true}},
			}
			vars := variables.New()
			vars["PRODUCT_VERSION"] = "1.0.0.5"
			vars["INSTALLDIR"] = "App"
			ctx := NewContext(setup, vars, work)
			out, err := ctx.Generate()
			if err != nil {
				t.Fatal(err)
			}
			start := strings.Index(out.RegistryXML, "<Component Id='")
			id, _ := attrValue(out.RegistryXML[start:], "<Component Id='")
			items := out.FeatureXML[strings.Index(out.FeatureXML, "<Feature Id='"+packageItemsFeatureID+"'"):]
			if !strings.Contains(items[:strings.Index(items, "</Feature>")], "<ComponentRef Id='"+id+"'/>") {
				t.Errorf("the package-items feature does not reference the guard %s:\n%s", id, out.FeatureXML)
			}
		})
	}
}

// TestAPackageWithoutFeaturesIsGuardedToo: its components belong to WiX's default feature, and
// so does the guard's.
func TestAPackageWithoutFeaturesIsGuardedToo(t *testing.T) {
	out, _, err := generateGuarded(t, "2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.LaunchConditionsXML, "00002.00000.00000.00000") ||
		!strings.Contains(out.RegistryXML, "Value='00002.00000.00000.00000'") {
		t.Errorf("no guard:\n%s\n%s", out.LaunchConditionsXML, out.RegistryXML)
	}
	if strings.Contains(out.FeatureXML, "ComponentRef") {
		t.Errorf("a featureless package got feature XML: %s", out.FeatureXML)
	}
}

// TestAnUnreadableVersionFailsTheBuild: the guard cannot order a version it cannot read, and a
// guard that silently went missing would bring back the broken downgrade.
func TestAnUnreadableVersionFailsTheBuild(t *testing.T) {
	if _, _, err := generateGuarded(t, "4.2.0.beta", "Main"); err == nil || !strings.Contains(err.Error(), "PRODUCT_VERSION") {
		t.Fatalf("want a PRODUCT_VERSION error, got %v", err)
	}
}
