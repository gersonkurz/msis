package generator

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// refsFor returns every ComponentRef id in the generated feature XML.
func refsFor(t *testing.T, output *GeneratedOutput) []string {
	t.Helper()
	var ids []string
	for _, line := range strings.Split(output.FeatureXML, "\n") {
		const marker = "<ComponentRef Id='"
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		rest := line[i+len(marker):]
		if j := strings.Index(rest, "'"); j >= 0 {
			ids = append(ids, rest[:j])
		}
	}
	return ids
}

// componentIDs returns every Component id declared across the directory trees.
func componentIDs(t *testing.T, output *GeneratedOutput) []string {
	t.Helper()
	all := output.DirectoryXML + output.AppDataDirXML + output.RegistryXML + output.RemoveOnUninstallXML
	var ids []string
	for _, line := range strings.Split(all, "\n") {
		const marker = "<Component Id='"
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		rest := line[i+len(marker):]
		if j := strings.Index(rest, "'"); j >= 0 {
			ids = append(ids, rest[:j])
		}
	}
	return ids
}

// TestTopLevelItemsAreReferencedWhenFeaturesExist guards issue #15. docs/msis.xsd
// permits items directly under <setup>, but their components were emitted with no
// ComponentRef, and WiX rejected the build with "WIX0267: Found orphaned Component" as
// soon as the package also declared a feature of its own.
//
// It went unnoticed because WiX invents a default feature for a package that declares
// none and adopts the loose components into it; the failure only appears once an author
// writes a single <feature>. That combination is the common case.
func TestTopLevelItemsAreReferencedWhenFeaturesExist(t *testing.T) {
	setup := &ir.Setup{
		Items: []ir.Item{
			ir.SetEnv{Name: "TOP_VAR", Value: "x"},
		},
		Features: []ir.Feature{{
			Name:  "Main",
			Items: []ir.Item{ir.SetEnv{Name: "FEATURE_VAR", Value: "y"}},
		}},
	}
	ctx := NewContext(setup, variables.New(), t.TempDir())
	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(componentIDs(t, output)) == 0 {
		t.Fatal("expected at least one component to be declared")
	}

	// Every component the TOP-LEVEL item produced must be referenced. Scoped to those
	// deliberately: a package whose feature contains no <files> also orphans the
	// INSTALLDIR permission component, but that is a separate pre-existing defect with
	// a different cause (markDirectoryFeature is only called from the file paths) and
	// it reproduces without any top-level item at all. Asserting "every component is
	// referenced" here would fail on that instead, and hide what this test is for.
	refSet := make(map[string]bool)
	for _, id := range refsFor(t, output) {
		refSet[id] = true
	}
	for _, id := range ctx.FeatureComponents[packageItemsFeatureID] {
		if !refSet[id] {
			t.Errorf("top-level component %s is declared but never referenced by a feature (WIX0267)", id)
		}
	}

	if !strings.Contains(output.FeatureXML, packageItemsFeatureID) {
		t.Errorf("expected a package-items feature, got:\n%s", output.FeatureXML)
	}
	// Package-level items are not something to deselect, and must not inherit the
	// level or conditions of whichever feature happened to be declared first.
	if !strings.Contains(output.FeatureXML, "Display='hidden'") {
		t.Errorf("the package-items feature should be hidden, got:\n%s", output.FeatureXML)
	}
	if !strings.Contains(output.FeatureXML, "AllowAbsent='no'") {
		t.Errorf("the package-items feature should not be optional, got:\n%s", output.FeatureXML)
	}
}

// TestTopLevelItemsWithoutFeaturesAreUnchanged: a package declaring no features already
// builds, because WiX adopts the loose components into a default feature of its own.
// That path is deliberately left alone — emitting a feature there would change the
// feature identity of packages already in the field for no benefit.
func TestTopLevelItemsWithoutFeaturesAreUnchanged(t *testing.T) {
	setup := &ir.Setup{
		Items: []ir.Item{ir.SetEnv{Name: "TOP_VAR", Value: "x"}},
	}
	ctx := NewContext(setup, variables.New(), t.TempDir())
	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if strings.Contains(output.FeatureXML, packageItemsFeatureID) {
		t.Errorf("no package-items feature should be emitted without declared features, got:\n%s", output.FeatureXML)
	}
}

// TestFeatureItemsUnaffected: items inside a feature must keep referencing that
// feature, not the package-items one.
func TestFeatureItemsUnaffected(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{{
			Name:  "Main",
			Items: []ir.Item{ir.SetEnv{Name: "FEATURE_VAR", Value: "y"}},
		}},
	}
	ctx := NewContext(setup, variables.New(), t.TempDir())
	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if strings.Contains(output.FeatureXML, packageItemsFeatureID) {
		t.Errorf("a package with no top-level items needs no package-items feature, got:\n%s", output.FeatureXML)
	}
	if len(refsFor(t, output)) == 0 {
		t.Error("the feature's own component should still be referenced")
	}
}

// TestTopLevelCleanupItemsAreReferenced covers the two forms this fix makes
// INSTALLABLE for the first time, which is the part that matters most: a top-level
// <remove-on-uninstall> previously failed the build, so its component never ran. Both
// are destructive at uninstall — a recursive folder delete and a registry key removal —
// so their feature association is worth asserting directly rather than inferring from
// a successful link.
//
// Their ComponentRefs are registered later than <set-env>'s, during XML generation
// rather than item processing, so they exercise a different path.
func TestTopLevelCleanupItemsAreReferenced(t *testing.T) {
	setup := &ir.Setup{
		Items: []ir.Item{
			ir.RemoveOnUninstall{Folder: `[APPDATADIR]Vendor\App\logs`},
			ir.RemoveOnUninstall{Registry: `HKLM\Software\Vendor\App`},
		},
		Features: []ir.Feature{{
			Name:  "Main",
			Items: []ir.Item{ir.SetEnv{Name: "FEATURE_VAR", Value: "y"}},
		}},
	}
	ctx := NewContext(setup, variables.New(), t.TempDir())
	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(ctx.RemoveOnUninstallItems) != 2 {
		t.Fatalf("expected two cleanup items, got %d", len(ctx.RemoveOnUninstallItems))
	}

	refSet := make(map[string]bool)
	for _, id := range refsFor(t, output) {
		refSet[id] = true
	}
	for _, item := range ctx.RemoveOnUninstallItems {
		if item.FeatureID != packageItemsFeatureID {
			t.Errorf("cleanup item %s should belong to the package-items feature, got %q", item.ID, item.FeatureID)
		}
		compID := "C_" + item.ID
		if !refSet[compID] {
			t.Errorf("cleanup component %s is declared but never referenced by a feature (WIX0267)", compID)
		}
	}

	// Both forms must actually be emitted, not silently skipped.
	if !strings.Contains(output.RemoveOnUninstallXML, "RemoveFolderEx") {
		t.Errorf("expected a folder cleanup element, got:\n%s", output.RemoveOnUninstallXML)
	}
	if !strings.Contains(output.RemoveOnUninstallXML, "RemoveRegistryKey") {
		t.Errorf("expected a registry cleanup element, got:\n%s", output.RemoveOnUninstallXML)
	}
}
