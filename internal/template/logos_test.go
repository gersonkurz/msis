package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
)

// writeLogo creates an empty file at dir/name, failing the test on error.
func writeLogo(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("bmp"), 0644); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

// TestResolveLogosPrefixAllThree: LOGO_PREFIX resolves each conventional file when present.
func TestResolveLogosPrefixAllThree(t *testing.T) {
	dir := t.TempDir()
	writeLogo(t, dir, "Acme_WixUiBanner.bmp")
	writeLogo(t, dir, "Acme_WixUiDialog.bmp")
	writeLogo(t, dir, "Acme_LogoBootstrap.bmp")

	vars := variables.New()
	vars["LOGO_PREFIX"] = "Acme"

	all := []string{LogoBanner, LogoDialog, LogoBootstrap}
	res := ResolveLogos(vars, all, dir, "", "")

	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
	for _, name := range all {
		got, ok := res.Values[name]
		if !ok {
			t.Errorf("%s: not resolved", name)
			continue
		}
		if filepath.Dir(got) != dir {
			t.Errorf("%s = %q, want under %q", name, got, dir)
		}
	}
}

// TestResolveLogosSourceDirPreferred: the source dir wins over custom/base when the same
// conventional file exists in more than one root.
func TestResolveLogosSourceDirPreferred(t *testing.T) {
	src := t.TempDir()
	tmpl := t.TempDir()
	writeLogo(t, src, "Acme_LogoBootstrap.bmp")
	writeLogo(t, tmpl, "Acme_LogoBootstrap.bmp")

	vars := variables.New()
	vars["LOGO_PREFIX"] = "Acme"

	res := ResolveLogos(vars, BundleLogoVars, src, "", tmpl)
	got := res.Values[LogoBootstrap]
	if filepath.Dir(got) != src {
		t.Errorf("LOGO_BOOTSTRAP = %q, want under source dir %q", got, src)
	}
}

// TestResolveLogosPrefixMissWarns: a prefix-derived file that does not exist warns and is omitted
// (so the template's {{#if}} guard falls back to the WiX default).
func TestResolveLogosPrefixMissWarns(t *testing.T) {
	dir := t.TempDir() // empty

	vars := variables.New()
	vars["LOGO_PREFIX"] = "Acme"

	res := ResolveLogos(vars, []string{LogoBanner}, dir, "", "")
	if _, ok := res.Values[LogoBanner]; ok {
		t.Errorf("LOGO_BANNER should be omitted when the prefixed file is missing")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Acme_WixUiBanner.bmp") {
		t.Errorf("expected a warning naming the missing file, got %v", res.Warnings)
	}
}

// TestResolveLogosExplicitMissWarns: an explicit value is kept verbatim but warns if not found,
// so a typo no longer silently brands with the WiX default.
func TestResolveLogosExplicitMissWarns(t *testing.T) {
	dir := t.TempDir() // empty

	vars := variables.New()
	vars["LOGO_BOOTSTRAP"] = "does-not-exist.png"

	res := ResolveLogos(vars, BundleLogoVars, dir, "", "")
	if res.Values[LogoBootstrap] != "does-not-exist.png" {
		t.Errorf("explicit value should be preserved verbatim, got %q", res.Values[LogoBootstrap])
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "does-not-exist.png") {
		t.Errorf("expected a warning for the missing explicit logo, got %v", res.Warnings)
	}
}

// TestResolveLogosExplicitFoundNoWarn: an explicit value that resolves under a search root is kept
// and produces no warning. Explicit also wins over LOGO_PREFIX.
func TestResolveLogosExplicitFoundNoWarn(t *testing.T) {
	dir := t.TempDir()
	writeLogo(t, dir, "logo.png")
	writeLogo(t, dir, "Acme_LogoBootstrap.bmp")

	vars := variables.New()
	vars["LOGO_PREFIX"] = "Acme"
	vars["LOGO_BOOTSTRAP"] = "logo.png" // explicit, relative to the source root

	res := ResolveLogos(vars, BundleLogoVars, dir, "", "")
	if res.Values[LogoBootstrap] != "logo.png" {
		t.Errorf("explicit value should win over prefix, got %q", res.Values[LogoBootstrap])
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no warning expected when explicit file resolves, got %v", res.Warnings)
	}
}

// TestBuildBundleContextPrefix: a bundle context picks up LOGO_BOOTSTRAP from LOGO_PREFIX, and the
// real bundle template then emits the LogoFile attribute. This is the regression guard for the bug
// where LOGO_PREFIX only affected the MSI.
func TestBuildBundleContextPrefix(t *testing.T) {
	src := t.TempDir()
	writeLogo(t, src, "Acme_LogoBootstrap.bmp")

	vars := variables.New()
	vars["LOGO_PREFIX"] = "Acme"

	ctx, warnings := BuildBundleContext(vars, "<MsiPackage SourceFile='x.msi'/>", src, "", "")
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	bootstrap, _ := ctx[LogoBootstrap].(string)
	if filepath.Dir(bootstrap) != src {
		t.Fatalf("LOGO_BOOTSTRAP in context = %q, want under %q", bootstrap, src)
	}

	content, err := os.ReadFile(filepath.Join("..", "..", "templates", "bundle.wxs"))
	if err != nil {
		t.Fatalf("reading bundle template: %v", err)
	}
	// Supply the minimum the bundle template needs alongside the resolved logo.
	ctx["PRODUCT_NAME"] = "App"
	ctx["PRODUCT_VERSION"] = "1.0.0"
	ctx["MANUFACTURER"] = "ACME"
	ctx["UPGRADE_CODE"] = "{00000000-0000-0000-0000-000000000000}"
	out, err := RenderString(string(content), ctx)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `LogoFile="`+bootstrap+`"`) {
		t.Errorf("expected LogoFile=%q in rendered bundle:\n%s", bootstrap, out)
	}
}

// TestBuildBundleContextExplicitBootstrap: an explicit LOGO_BOOTSTRAP flows into the bundle context
// and template verbatim.
func TestBuildBundleContextExplicitBootstrap(t *testing.T) {
	src := t.TempDir()
	writeLogo(t, src, "brand.png")

	vars := variables.New()
	vars["LOGO_BOOTSTRAP"] = "brand.png"

	ctx, warnings := BuildBundleContext(vars, "", src, "", "")
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if ctx[LogoBootstrap] != "brand.png" {
		t.Errorf("LOGO_BOOTSTRAP = %v, want brand.png", ctx[LogoBootstrap])
	}
}

// TestBundleTemplatesExist guards that the dead bootstrap*.wxs templates stay deleted and the live
// bundle templates remain.
func TestBundleTemplatesExist(t *testing.T) {
	dir := filepath.Join("..", "..", "templates")
	for _, gone := range []string{"bootstrap.wxs", "bootstrap-silent.wxs"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s should have been deleted (dead template)", gone)
		}
	}
	for _, live := range []string{"bundle.wxs", "bundle-silent.wxs"} {
		if _, err := os.Stat(filepath.Join(dir, live)); err != nil {
			t.Errorf("%s should exist: %v", live, err)
		}
	}
}
