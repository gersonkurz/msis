package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/variables"
)

// rendererForTemplateSelection builds a renderer over a temp template folder holding a
// distinguishable regular and silent template for the given platform, plus a third file
// outside that folder to be selected with /TEMPLATE.
func rendererForTemplateSelection(t *testing.T, platform string) (*Renderer, string) {
	t.Helper()

	base := t.TempDir()
	folder := platform
	if !strings.EqualFold(platform, "x86") {
		folder = "x64"
	}
	if err := os.MkdirAll(filepath.Join(base, folder), 0755); err != nil {
		t.Fatal(err)
	}

	write := func(path, marker string) string {
		full := filepath.Join(base, path)
		if err := os.WriteFile(full, []byte("<Package>"+marker+"</Package>"), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	write(filepath.Join(folder, "template.wxs"), "REGULAR")
	write(filepath.Join(folder, "template-silent.wxs"), "STOCK-SILENT")
	custom := write("chosen.wxs", "CHOSEN")

	vars := variables.New()
	vars["PLATFORM"] = platform
	r := NewRenderer(vars, base, "", &generator.GeneratedOutput{})
	return r, custom
}

// TestCustomTemplateAppliesToSilentPackages guards issue #20. RenderSilent never looked at
// CustomTemplate, so `/TEMPLATE:<path>` was silently ignored for any script with
// silent="yes": the build used the stock silent template and said nothing, producing a
// package that was not the one the user configured.
func TestCustomTemplateAppliesToSilentPackages(t *testing.T) {
	r, custom := rendererForTemplateSelection(t, "x86")
	r.SetCustomTemplate(custom)

	out, err := r.RenderSilent()
	if err != nil {
		t.Fatalf("RenderSilent: %v", err)
	}
	if !strings.Contains(out, "CHOSEN") {
		t.Errorf("RenderSilent ignored the /TEMPLATE selection, rendering %q instead", out)
	}
}

// TestCustomTemplateAppliesToRegularPackages is the case that already worked, kept so a
// future change cannot fix one path by breaking the other.
func TestCustomTemplateAppliesToRegularPackages(t *testing.T) {
	r, custom := rendererForTemplateSelection(t, "x86")
	r.SetCustomTemplate(custom)

	out, err := r.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "CHOSEN") {
		t.Errorf("Render ignored the /TEMPLATE selection, rendering %q instead", out)
	}
}

// TestSilentFallsBackWhenPlatformHasNoSilentTemplate keeps the behaviour the fix must not
// disturb: x64 ships no silent template, and RenderSilent reports that by returning "" so
// main.go falls back to the regular one.
func TestSilentFallsBackWhenPlatformHasNoSilentTemplate(t *testing.T) {
	r, _ := rendererForTemplateSelection(t, "x64")
	if err := os.Remove(filepath.Join(r.TemplateFolder, "x64", "template-silent.wxs")); err != nil {
		t.Fatal(err)
	}

	out, err := r.RenderSilent()
	if err != nil {
		t.Fatalf("RenderSilent: %v", err)
	}
	if out != "" {
		t.Errorf("expected an empty result to signal the fallback, got %q", out)
	}
}

// TestSilentUsesStockTemplateWithoutAnOverride is the other half of #20: without /TEMPLATE
// the stock silent template must still be chosen, not the regular one.
func TestSilentUsesStockTemplateWithoutAnOverride(t *testing.T) {
	r, _ := rendererForTemplateSelection(t, "x86")

	out, err := r.RenderSilent()
	if err != nil {
		t.Fatalf("RenderSilent: %v", err)
	}
	if !strings.Contains(out, "STOCK-SILENT") {
		t.Errorf("expected the stock silent template, rendered %q", out)
	}
}

// TestMissingCustomTemplateIsAnError covers the judgement call in the fix: a /TEMPLATE the
// user named and that does not exist must fail, rather than being treated like an absent
// stock silent template and quietly falling back to a different one.
func TestMissingCustomTemplateIsAnError(t *testing.T) {
	r, custom := rendererForTemplateSelection(t, "x86")
	r.SetCustomTemplate(filepath.Join(filepath.Dir(custom), "does-not-exist.wxs"))

	for name, render := range map[string]func() (string, error){
		"Render":       r.Render,
		"RenderSilent": r.RenderSilent,
	} {
		out, err := render()
		if err == nil {
			t.Errorf("%s: expected an error for a missing /TEMPLATE file, got %q", name, out)
			continue
		}
		if !strings.Contains(err.Error(), "does-not-exist.wxs") {
			t.Errorf("%s: error does not name the missing file: %v", name, err)
		}
	}
}
