package sbom

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

func extensionRecord(t *testing.T, sha256 string) *buildrecord.Record {
	t.Helper()
	rec := recordFor(t)
	rec.Extensions = []buildrecord.ExtensionFile{{
		Package: "WixToolset.Util.wixext", Version: "7.0.0", Entry: "wix-ir/utilca.dll-1", SHA256: sha256,
		Authors: "WiX Toolset Team", Repository: "https://github.com/wixtoolset/wix",
		License: "LicenseRef-scancode-os-maintenance-fee-eula",
	}}
	return rec
}

// #67, D18: a Binary-table stream whose bytes are a file of an extension the build loaded is
// that package's file - creator, licence and version are what the package declares - and the
// document says which file of which package it matched.
func TestAStreamMatchingAnExtensionFileIsAttributed(t *testing.T) {
	stream := syntheticPackage().Binaries[0]
	doc, err := docWith(t, extensionRecord(t, stream.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	c := componentByRef(doc, refWithName(doc, stream.Name))
	if c.Version != "7.0.0" {
		t.Errorf("version %q, want the package's", c.Version)
	}
	if !reflect.DeepEqual(c.Manufacturer, &OrganizationalEntity{Name: "WiX Toolset Team", URL: []string{"https://github.com/wixtoolset/wix"}}) {
		t.Errorf("creator %+v", c.Manufacturer)
	}
	if !reflect.DeepEqual(c.Licenses, []LicenseChoice{{Expression: "LicenseRef-scancode-os-maintenance-fee-eula", Acknowledgement: "concluded"}}) {
		t.Errorf("licences %+v, want ScanCode's id as the one concluded expression BSI §6.1 allows", c.Licenses)
	}
	if got := propertyValueOf(c.Properties, propBuildExtension); got != "WixToolset.Util.wixext 7.0.0, wix-ir/utilca.dll-1" {
		t.Errorf("%s = %q", propBuildExtension, got)
	}
	if got := propertyValueOf(c.Properties, propIdentityUnknown); !strings.Contains(got, "matched by SHA-256") {
		t.Errorf("%s = %q, want the match stated", propIdentityUnknown, got)
	}
	conforms(t, doc, conformance.Expected{})
}

// A stream with WiX's name but other bytes is not WiX's: nothing is attributed.
func TestAStreamWithOtherBytesIsNotAttributed(t *testing.T) {
	stream := syntheticPackage().Binaries[0]
	doc, err := docWith(t, extensionRecord(t, strings.Repeat("d", 64)))
	if err != nil {
		t.Fatal(err)
	}
	c := componentByRef(doc, refWithName(doc, stream.Name))
	if c.Version != "" || c.Manufacturer != nil || c.Licenses != nil || propertyValueOf(c.Properties, propBuildExtension) != "" {
		t.Errorf("a stream the extension does not carry was attributed: %+v", c)
	}
}
