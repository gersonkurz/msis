package bundle

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

func generateWith(t *testing.T, searches []ir.BundleSearch, exes ...ir.ExePackage) (*GeneratedBundle, error) {
	t.Helper()
	setup := &ir.Setup{Bundle: &ir.Bundle{Searches: searches, ExePackages: exes, MSI: &ir.BundleMSI{Source: "app.msi"}}}
	return NewGenerator(setup, variables.New(), ".").Generate()
}

var webView2Machine = ir.BundleSearch{Variable: "WebView2Machine", Root: "HKLM", Bitness: "32", Result: "value", Value: "pv",
	Key: `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`}

// TestSearchesArePinned (#90, D30): Result and Bitness are always written out - WiX's own
// defaults are Result="value" and the engine's view - and a search without a value name omits
// Value. Literal, so a change to the emitted form is a visible change.
func TestSearchesArePinned(t *testing.T) {
	key := ir.BundleSearch{Variable: "AppKey", Root: "HKCU", Key: `Software\App`, Result: "exists", Bitness: "64"}
	out, err := generateWith(t, []ir.BundleSearch{webView2Machine, key},
		ir.ExePackage{Source: "a.exe", DetectCondition: "WebView2Machine > v0.0.0.0 OR AppKey"})
	if err != nil {
		t.Fatal(err)
	}
	want := `    <util:RegistrySearch Id='MSIS_Search_WebView2Machine' Variable='WebView2Machine' Root='HKLM' Key='SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}' Value='pv' Result='value' Bitness='always32'/>
    <util:RegistrySearch Id='MSIS_Search_AppKey' Variable='AppKey' Root='HKCU' Key='Software\App' Result='exists' Bitness='always64'/>
`
	if out.SearchXML != want {
		t.Errorf("searches:\n%s\nwant:\n%s", out.SearchXML, want)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("both variables are used, yet: %v", out.Warnings)
	}
}

// TestASearchMayNotWriteAReservedVariable: Burn built-ins, the Wix namespace, and what the
// templates and prerequisites use - case-insensitively, as Burn compares names - and no two
// searches may set one variable.
func TestASearchMayNotWriteAReservedVariable(t *testing.T) {
	for variable, want := range map[string]string{
		"VersionNT64":             "Burn built-in",
		"versionnt64":             "Burn built-in",
		"WixBundleName":           "reserved for Burn",
		"VcppRuntimeX64Installed": "templates or msis's prerequisites",
		"NETFRAMEWORK45":          "templates or msis's prerequisites",
		"InstallFolder":           "templates or msis's prerequisites",
	} {
		s := webView2Machine
		s.Variable = variable
		if _, err := generateWith(t, []ir.BundleSearch{s}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error saying %q, got %v", variable, want, err)
		}
	}
	twice := webView2Machine
	twice.Variable = "webview2machine"
	if _, err := generateWith(t, []ir.BundleSearch{webView2Machine, twice}); err == nil || !strings.Contains(err.Error(), "two searches set it") {
		t.Errorf("want the duplicate refused, got %v", err)
	}
}

// TestAnUnreferencedSearchWarns: a search no <exe> mentions in detect or args is probably a
// typo. Matched as a whole word, so a longer name does not count as a use.
func TestAnUnreferencedSearchWarns(t *testing.T) {
	out, err := generateWith(t, []ir.BundleSearch{webView2Machine}, ir.ExePackage{Source: "a.exe", DetectCondition: "WebView2MachineX"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "no <exe> in this script refers to it") {
		t.Errorf("warnings = %v", out.Warnings)
	}
	out, err = generateWith(t, []ir.BundleSearch{webView2Machine}, ir.ExePackage{Source: "a.exe", InstallArgs: "/v [WebView2Machine]"})
	if err != nil || len(out.Warnings) != 0 {
		t.Errorf("a use in args must count: %v %v", out.Warnings, err)
	}
}

// TestPerMachineIsEmittedOnlyWhenAsked: an <exe> without per-machine= is exactly what it was.
func TestPerMachineIsEmittedOnlyWhenAsked(t *testing.T) {
	out, err := generateWith(t, nil, ir.ExePackage{ID: "Machine", Source: "m.exe", PerMachine: true}, ir.ExePackage{ID: "User", Source: "u.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.ChainXML, "Id='Machine' SourceFile='m.exe' PerMachine='yes' Permanent='yes'") {
		t.Errorf("no PerMachine on the per-machine exe:\n%s", out.ChainXML)
	}
	if strings.Contains(out.ChainXML, "Id='User' SourceFile='u.exe' PerMachine") {
		t.Errorf("PerMachine emitted on an exe that did not ask:\n%s", out.ChainXML)
	}
}
