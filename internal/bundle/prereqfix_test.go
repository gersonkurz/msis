package bundle

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// TestANetfxPrerequisiteBringsItsSearch (#93, D32): a bundle chaining a netfx prerequisite - explicit
// or auto - declares the NETFRAMEWORK45 search its detect condition tests; one without does not.
func TestANetfxPrerequisiteBringsItsSearch(t *testing.T) {
	const search = "Variable='NETFRAMEWORK45' Root='HKLM' Key='SOFTWARE\\Microsoft\\NET Framework Setup\\NDP\\v4\\Full' Value='Release' Result='value' Bitness='always32'"
	for name, prereqs := range map[string][]ir.Prerequisite{
		"netfx":    {{Type: "netfx", Version: "4.8"}},
		"both":     {{Type: "vcredist", Version: "2022"}, {Type: "netfx", Version: "4.8.1"}},
		"vcredist": {{Type: "vcredist", Version: "2022"}},
	} {
		want := 0
		if name != "vcredist" {
			want = 1
		}
		explicit, err := NewGenerator(&ir.Setup{Bundle: &ir.Bundle{Prerequisites: prereqs, MSI: &ir.BundleMSI{Source: "a.msi"}}}, variables.New(), ".").Generate()
		if err != nil {
			t.Fatal(err)
		}
		auto, err := NewAutoBundleGenerator(variables.New(), ".", "a.msi", prereqs).Generate()
		if err != nil {
			t.Fatal(err)
		}
		for path, out := range map[string]*GeneratedBundle{"explicit": explicit, "auto": auto} {
			if got := strings.Count(out.SearchXML, search); got != want {
				t.Errorf("%s, %s bundle: the NETFRAMEWORK45 search appears %d times, want %d:\n%s", name, path, got, want, out.SearchXML)
			}
		}
	}
}
