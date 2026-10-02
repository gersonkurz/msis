//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #90 (D30): a bundle's <search> elements and an <exe per-machine="yes"> reach the bundle the
// real wix builds, in both shipped bundle templates; and a custom bundle template without
// {{{SEARCHES}}} fails the build instead of dropping the searches.

const searchBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup{{SILENT}}>
  <set name="PRODUCT_NAME" value="SearchBundle"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{5E2B8D41-7A3C-4F96-B0D2-9C1E6A4F8B90}"/>
  <set name="LICENSE_URL" value="https://example.com/license"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <search variable="WebView2Machine" root="HKLM" bitness="32" value="pv" result="value"
            key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"/>
    <exe id="WebView2" source="setup-stand-in.exe" args="/silent /install" per-machine="yes"
         detect="WebView2Machine &gt; v0.0.0.0"/>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

// buildInnerMSI builds the MSI the bundle chains, with the real wix.
func buildInnerMSI(t *testing.T, dir string) {
	t.Helper()
	inner := filepath.Join(dir, "inner")
	write(t, filepath.Join(inner, "app.txt"), "the application\n")
	write(t, filepath.Join(inner, "lib", "one.txt"), "library one\n")
	write(t, filepath.Join(inner, "lib", "deep", "two.txt"), "library two, nested\n")
	script := scriptFor(t, inner, "inner.msi", payloadScript)
	if err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("building the inner MSI: %v", err)
	}
}

// standInExe copies a real PE so that wix can harvest it; it is never run.
func standInExe(t *testing.T, path string) {
	t.Helper()
	src, err := os.Open(filepath.Join(os.Getenv("SystemRoot"), "System32", "whoami.exe"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
}

func TestBundleSearchesReachTheBuiltBundle(t *testing.T) {
	for name, silent := range map[string]string{"regular": "", "silent": ` silent="yes"`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			buildInnerMSI(t, dir)
			standInExe(t, filepath.Join(dir, "setup-stand-in.exe"))
			script := scriptFor(t, dir, "suite.exe", strings.ReplaceAll(searchBundleScript, "{{SILENT}}", silent))
			if err := processFile(script, &cliArgs{build: true, retainWxs: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
				t.Fatalf("building the bundle: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "suite.exe")); err != nil {
				t.Fatalf("no bundle: %v", err)
			}
			wxs, err := os.ReadFile(filepath.Join(dir, "suite-bundle.wxs"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				`<util:RegistrySearch Id='MSIS_Search_WebView2Machine' Variable='WebView2Machine' Root='HKLM' Key='SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}' Value='pv' Result='value' Bitness='always32'/>`,
				`DetectCondition='WebView2Machine &gt; v0.0.0.0'`,
				`PerMachine='yes'`,
			} {
				if !strings.Contains(string(wxs), want) {
					t.Errorf("the bundle WXS lacks %s", want)
				}
			}
		})
	}
}

func TestACustomBundleTemplateWithoutSearchesFails(t *testing.T) {
	dir := t.TempDir()
	buildInnerMSI(t, dir)
	standInExe(t, filepath.Join(dir, "setup-stand-in.exe"))
	shipped, err := os.ReadFile(filepath.Join(repoTemplates(t), "bundle.wxs"))
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dir, "custom")
	write(t, filepath.Join(custom, "bundle.wxs"), strings.ReplaceAll(string(shipped), "{{{SEARCHES}}}", ""))
	script := scriptFor(t, dir, "suite.exe", strings.ReplaceAll(searchBundleScript, "{{SILENT}}", ""))
	err = processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t), customTemplates: custom})
	if err == nil || !strings.Contains(err.Error(), "SEARCHES") {
		t.Fatalf("a custom bundle template without {{{SEARCHES}}} must fail: %v", err)
	}
}
