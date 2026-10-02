package bundle

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// TestASilentBundleShowsNoMSIUI (#95, D31): in a silent bundle no MsiPackage carries
// bal:DisplayInternalUICondition, in every way the chain can name its MSIs - one MSI, per-
// architecture <msi>, and the legacy shorthand - while a regular bundle keeps it on every one.
func TestASilentBundleShowsNoMSIUI(t *testing.T) {
	for name, b := range map[string]ir.Bundle{
		"single":   {MSI: &ir.BundleMSI{Source: "app.msi"}},
		"per-arch": {MSI: &ir.BundleMSI{Source64bit: "a64.msi", Source32bit: "a86.msi", SourceArm64: "aarm.msi"}},
		"legacy":   {Source64bit: "a64.msi", Source32bit: "a86.msi", SourceArm64: "aarm.msi"},
	} {
		for _, silent := range []bool{true, false} {
			bundle := b
			out, err := NewGenerator(&ir.Setup{Silent: silent, Bundle: &bundle}, variables.New(), ".").Generate()
			if err != nil {
				t.Fatal(err)
			}
			packages := strings.Count(out.ChainXML, "<MsiPackage ")
			ui := strings.Count(out.ChainXML, "bal:DisplayInternalUICondition='1'")
			if silent && ui != 0 {
				t.Errorf("%s, silent: %d packages still show the MSI's UI:\n%s", name, ui, out.ChainXML)
			}
			if !silent && ui != packages {
				t.Errorf("%s, regular: %d of %d packages show the MSI's UI", name, ui, packages)
			}
		}
	}
}

// TestASilentAutoBundleShowsNoMSIUI: the auto-bundle wrapper follows <setup silent> the same way.
func TestASilentAutoBundleShowsNoMSIUI(t *testing.T) {
	for _, silent := range []bool{true, false} {
		gen := NewAutoBundleGenerator(variables.New(), ".", "app.msi", nil)
		gen.Silent = silent
		out, err := gen.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(out.ChainXML, "DisplayInternalUICondition"); has == silent {
			t.Errorf("silent=%v: DisplayInternalUICondition present=%v\n%s", silent, has, out.ChainXML)
		}
	}
}
