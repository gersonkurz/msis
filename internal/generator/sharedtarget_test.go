package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// #79 (D24): two <files> installing different sources to one target. Across features that is
// deprecated - on the VM (T79) removing either feature deleted the file the other still
// installed - so it builds with a warning, and Strict refuses it. Within one feature it is sound
// and relied on (ProAKT's core-then-NG override), so it stays silent.
func TestSharedFileTargets(t *testing.T) {
	// Sources are host paths (native separators, so the fixture works off Windows too); targets
	// are MSI notation. n turns a slash path into the host form the generator reports.
	n := filepath.FromSlash
	dir := t.TempDir()
	for _, rel := range []string{"a/config.json", "b/config.json", "core/CONFIG/CURRENCY.TXT", "ng/CONFIG/CURRENCY.TXT", "svc/svc.exe", "svc2/svc.exe"} {
		path := filepath.Join(dir, n(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	feature := func(name string, items ...ir.Item) ir.Feature {
		return ir.Feature{Name: name, Enabled: true, Items: items}
	}
	cases := []struct {
		name       string
		features   []ir.Feature
		deprecated bool
		mustSay    []string
	}{
		{"two features, one target (the issue's repro): files named directly", []ir.Feature{
			feature("Standard", ir.Files{Source: n("a/config.json"), Target: "[INSTALLDIR]"}),
			feature("Variant", ir.Files{Source: n("b/config.json"), Target: "[INSTALLDIR]"}),
		}, true, []string{"#79", "[INSTALLDIR]config.json", `feature "Standard" from ` + n("a/config.json"),
			`feature "Variant" from ` + n("b/config.json"), "deprecated", "/STRICT", "installs and removes them together",
			// <exclude> does not apply to a file a <files> names, so the advice is to remove that <files>.
			`(remove the <files source="` + n("a/config.json") + `" .../> that names it)`,
			`(remove the <files source="` + n("b/config.json") + `" .../> that names it)`}},
		{"two features, one target in a walked folder", []ir.Feature{
			feature("Core", ir.Files{Source: n("core/CONFIG"), Target: `[INSTALLDIR]CONFIG`}),
			feature("NG", ir.Files{Source: n("ng/CONFIG"), Target: `[INSTALLDIR]CONFIG`}),
		}, true, []string{`[INSTALLDIR]CONFIG\CURRENCY.TXT`,
			`<exclude folder="` + n("core/CONFIG/CURRENCY.TXT") + `"/>`, `<exclude folder="` + n("ng/CONFIG/CURRENCY.TXT") + `"/>`}},
		{"the <exclude> the warning proposes, applied (Poste Italiane's desktop.ini)", []ir.Feature{
			feature("Core", ir.Files{Source: n("core/CONFIG"), Target: `[INSTALLDIR]CONFIG`}),
			feature("NG", ir.Files{Source: n("ng/CONFIG"), Target: `[INSTALLDIR]CONFIG`},
				ir.Exclude{Folder: n("ng/CONFIG/CURRENCY.TXT")}),
		}, false, nil},
		{"one feature, two sources (ProAKT's override)", []ir.Feature{
			feature("Main", ir.Files{Source: n("core/CONFIG"), Target: `[INSTALLDIR]CONFIG`},
				ir.Files{Source: n("ng/CONFIG"), Target: `[INSTALLDIR]CONFIG`}),
		}, false, nil},
		{"two features, distinct targets", []ir.Feature{
			feature("Standard", ir.Files{Source: n("a/config.json"), Target: "[INSTALLDIR]"}),
			feature("Variant", ir.Files{Source: n("b/config.json"), Target: `[INSTALLDIR]variant`}),
		}, false, nil},
		{"a service's file is #77's to report, not #79's", []ir.Feature{
			feature("App", ir.Files{Source: n("svc/svc.exe"), Target: "[INSTALLDIR]"},
				ir.Service{FileName: "svc.exe", ServiceName: "Svc79"}),
			feature("Other", ir.Files{Source: n("svc2/svc.exe"), Target: "[INSTALLDIR]"}),
		}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := NewContext(&ir.Setup{Features: tc.features}, variables.New(), dir).Generate()
			if err != nil {
				t.Fatalf("Generate failed: %v", err)
			}
			var warnings []string
			for _, w := range output.Warnings {
				if strings.Contains(w, "#79") {
					warnings = append(warnings, w)
				}
			}
			if want := map[bool]int{true: 1, false: 0}[tc.deprecated]; len(warnings) != want {
				t.Fatalf("%d #79 warnings, want %d: %q", len(warnings), want, output.Warnings)
			}
			for _, want := range tc.mustSay {
				if !strings.Contains(warnings[0], want) {
					t.Errorf("the warning lacks %q:\n%s", want, warnings[0])
				}
			}

			strict := NewContext(&ir.Setup{Features: tc.features}, variables.New(), dir)
			strict.Strict = true
			_, err = strict.Generate()
			switch {
			case tc.deprecated && (err == nil || !strings.Contains(err.Error(), "#79")):
				t.Errorf("Strict did not refuse the layout: %v", err)
			case !tc.deprecated && err != nil && strings.Contains(err.Error(), "#79"):
				t.Errorf("Strict refused a sound layout: %v", err)
			}
		})
	}
}

// #84: the deprecation warnings (#77, #79) name a feature as the package's Title shows it,
// resolved, not as the script wrote it - probuiknoba saw "{{PRODUCT_NAME}}" in the #77 one.
func TestDeprecationWarningsNameResolvedFeatures(t *testing.T) {
	dir := t.TempDir()
	n := filepath.FromSlash
	for _, rel := range []string{"a/svc.exe", "b/config.json", "c/config.json"} {
		path := filepath.Join(dir, n(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vars := variables.New()
	vars["PRODUCT_NAME"] = "ProAKT Standard"
	setup := &ir.Setup{Features: []ir.Feature{
		{Name: "{{PRODUCT_NAME}}", Enabled: true, Items: []ir.Item{
			ir.Files{Source: n("a/svc.exe"), Target: "[INSTALLDIR]"},
			ir.Files{Source: n("b/config.json"), Target: "[INSTALLDIR]"},
		}},
		{Name: "Install as service", Enabled: true, Items: []ir.Item{
			ir.Service{FileName: "svc.exe", ServiceName: "PASERVER"},
			ir.Files{Source: n("c/config.json"), Target: "[INSTALLDIR]"},
		}},
	}}
	output, err := NewContext(setup, vars, dir).Generate()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, w := range output.Warnings {
		for _, issue := range []string{"#77", "#79"} {
			if !strings.Contains(w, issue) {
				continue
			}
			found[issue] = true
			if strings.Contains(w, "{{") || !strings.Contains(w, `"ProAKT Standard"`) {
				t.Errorf("the %s warning does not name the resolved feature:\n%s", issue, w)
			}
		}
	}
	if !found["#77"] || !found["#79"] {
		t.Errorf("expected a #77 and a #79 warning, got %q", output.Warnings)
	}
}
