package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
)

// TestThemeNoneIsRefused (#95, D31): WixStdBA with Theme="none" fails the build, whatever prefix
// the bal namespace has and whatever case the value is in.
func TestThemeNoneIsRefused(t *testing.T) {
	for name, doc := range map[string]string{
		"bal prefix":   `<Wix xmlns:bal="http://wixtoolset.org/schemas/v4/wxs/bal"><Bundle><BootstrapperApplication><bal:WixStandardBootstrapperApplication LicenseUrl="" Theme="none"/></BootstrapperApplication></Bundle></Wix>`,
		"other prefix": `<Wix xmlns:b="http://wixtoolset.org/schemas/v4/wxs/bal"><b:WixStandardBootstrapperApplication Theme="None"/></Wix>`,
	} {
		if err := CheckBundleBootstrapper("t.wxs", doc); err == nil || !strings.Contains(err.Error(), "#95") {
			t.Errorf("%s: want the refusal, got %v", name, err)
		}
	}
}

// TestOtherBootstrappersAreLeftAlone: a built-in theme, a commented-out Theme="none", an element
// of that name in another namespace, and a document that does not parse all pass.
func TestOtherBootstrappersAreLeftAlone(t *testing.T) {
	for name, doc := range map[string]string{
		"built-in theme": `<Wix xmlns:bal="http://wixtoolset.org/schemas/v4/wxs/bal"><bal:WixStandardBootstrapperApplication Theme="hyperlinkLicense"/></Wix>`,
		"commented":      `<Wix xmlns:bal="http://wixtoolset.org/schemas/v4/wxs/bal"><!-- <bal:WixStandardBootstrapperApplication Theme="none"/> --><bal:WixStandardBootstrapperApplication Theme="rtfLicense"/></Wix>`,
		"other ns":       `<Wix xmlns:x="urn:other"><x:WixStandardBootstrapperApplication Theme="none"/></Wix>`,
		"not xml":        `<Wix <<< Theme="none"`,
	} {
		if err := CheckBundleBootstrapper("t.wxs", doc); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

// TestShippedBundleTemplatesHaveATheme: both shipped bundle templates, rendered, pass the guard -
// the silent one used to carry Theme="none" (#95).
func TestShippedBundleTemplatesHaveATheme(t *testing.T) {
	for _, name := range []string{"bundle.wxs", "bundle-silent.wxs"} {
		content, err := os.ReadFile(filepath.Join("..", "..", "templates", name))
		if err != nil {
			t.Fatal(err)
		}
		ctx, _ := BuildBundleContext(variables.New(), "<MsiPackage SourceFile='a.msi'/>", "", ".", "", "")
		out, err := RenderString(string(content), ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckBundleBootstrapper(name, out); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !strings.Contains(out, `Theme="hyperlinkLicense"`) {
			t.Errorf("%s: no hyperlinkLicense theme", name)
		}
	}
}
