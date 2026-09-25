//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
)

// #77 (D22): the deprecated layout - a <service> naming another feature's executable - builds by
// default, as scripts in the field need it to, and /STRICT refuses it. This checks the CLI switch
// reaches the generator; the generator tests cover the layouts.
func TestStrictRefusesTheDeprecatedServiceLayout(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "svc.exe"), "never executed\n")
	script := scriptFor(t, dir, "svc.msi", `<?xml version='1.0' encoding='utf-8'?>
<setup>
    <set name="PRODUCT_NAME" value="Deprecated Layout"/>
    <set name="PRODUCT_VERSION" value="1.0.0"/>
    <set name="MANUFACTURER" value="Probe"/>
    <set name="UPGRADE_CODE" value="{2C7A9E51-4D3B-4A86-8F10-6B2E9D5C7A34}"/>
    <set name="BUILD_TARGET" value="{{TARGET}}"/>
    <feature name="Complete"><files source="svc.exe" target="[INSTALLDIR]"/></feature>
    <feature name="Service"><service file-name="svc.exe" service-name="svcD"/></feature>
</setup>`)
	if err := processFile(script, &cliArgs{templateFolder: repoTemplates(t), setOverrides: map[string]string{}}); err != nil {
		t.Fatalf("the deprecated layout does not build by default: %v", err)
	}
	err := processFile(script, &cliArgs{strict: true, templateFolder: repoTemplates(t), setOverrides: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "#77") {
		t.Fatalf("/STRICT did not refuse the deprecated layout: %v", err)
	}
}

// #78: every <service> attribute msis-2.x honoured reaches the package, with 2.x's defaults. Two
// of them used to break the build outright - an omitted service-display-name emitted an
// empty DisplayName (WIX0006), a description a child element WiX does not have (WIX0005) - and
// service-type, error-control and restart were dropped. This drives the real wix build.
func TestServiceAttributesReachThePackage(t *testing.T) {
	cases := []struct {
		name, service string
		wxs           []string // fragments the ServiceInstall/ServiceControl lines must carry
		displayName   string   // as the built MSI's ServiceInstall table holds it
	}{
		{"every attribute set",
			`<service file-name="svc.exe" service-name="svcA" service-display-name="R&amp;D Svc" description="does &lt;things&gt;"
			          service-type="shareProcess" error-control="critical" restart="yes" start="demand"/>`,
			[]string{
				`Name='svcA' DisplayName='R&amp;D Svc' Description='does &lt;things&gt;' Start='demand' Type='shareProcess' ErrorControl='critical'>`,
				`<util:ServiceConfig FirstFailureActionType='restart' SecondFailureActionType='restart' ThirdFailureActionType='restart' RestartServiceDelayInSeconds='30' ResetPeriodInDays='1'/>`,
			},
			"R&D Svc"},
		{"only the required attributes: msis-2.x's defaults",
			`<service file-name="svc.exe" service-name="svcB"/>`,
			[]string{`Name='svcB' DisplayName='svcB' Description='svcB' Start='auto' Type='ownProcess' ErrorControl='normal'>`},
			"svcB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "svc.exe"), "never executed\n")
			script := scriptFor(t, dir, "svc.msi", `<?xml version='1.0' encoding='utf-8'?>
<setup>
    <set name="PRODUCT_NAME" value="Service Attributes"/>
    <set name="PRODUCT_VERSION" value="1.0.0"/>
    <set name="MANUFACTURER" value="Probe"/>
    <set name="UPGRADE_CODE" value="{6A1E9C42-3B7D-4F85-9E20-4C8D1B7A5F93}"/>
    <set name="BUILD_TARGET" value="{{TARGET}}"/>
    <feature name="Main">
        <files source="svc.exe" target="[INSTALLDIR]"/>
        `+tc.service+`
    </feature>
</setup>`)
			err := processFile(script, &cliArgs{build: true, retainWxs: true, templateFolder: repoTemplates(t),
				setOverrides: map[string]string{}})
			if err != nil {
				t.Fatalf("the package does not build: %v", err)
			}
			wxs, err := os.ReadFile(filepath.Join(dir, "svc.wxs"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wxs {
				if !strings.Contains(string(wxs), want) {
					t.Errorf("the WXS lacks %s", want)
				}
			}
			if !strings.Contains(tc.service, `restart="yes"`) && strings.Contains(string(wxs), "util:ServiceConfig") {
				t.Errorf("a service without restart=\"yes\" got a ServiceConfig")
			}
			pkg, err := msiread.Read(filepath.Join(dir, "svc.msi"))
			if err != nil {
				t.Fatal(err)
			}
			if len(pkg.Services) != 1 || pkg.Services[0].DisplayName != tc.displayName {
				t.Errorf("the MSI's services are %+v, want one with DisplayName %q", pkg.Services, tc.displayName)
			}
		})
	}
}
