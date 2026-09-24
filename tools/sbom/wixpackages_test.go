package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/wix"
)

// served is what a fake nuget.org returns for one package version.
func served(t *testing.T, authors, licence, repo, licenceText string) func(string) ([]byte, error) {
	t.Helper()
	var pkg bytes.Buffer
	z := zip.NewWriter(&pkg)
	f, _ := z.Create("OSMFEULA.txt")
	f.Write([]byte(licenceText))
	z.Close()
	spec := fmt.Sprintf(`<?xml version="1.0"?><package xmlns="http://schemas.microsoft.com/packaging/2011/08/nuspec.xsd">
<metadata><authors>%s</authors><license type="file">%s</license><repository type="git" url="%s"/></metadata></package>`,
		authors, licence, repo)
	return func(url string) ([]byte, error) {
		switch {
		case strings.HasSuffix(url, ".nuspec"):
			return []byte(spec), nil
		case strings.HasSuffix(url, ".nupkg"):
			return pkg.Bytes(), nil
		}
		return nil, fmt.Errorf("unexpected %s", url)
	}
}

// #67, D18: a pin that matches passes; a changed author, licence file or licence TEXT fails,
// the last because it is when the licence id has to be read again.
func TestTheWixPackageCheckNoticesEveryDrift(t *testing.T) {
	text := "End User License Agreement ..."
	h := sha256.Sum256([]byte(text))
	pin := wix.ExtensionPackage{Authors: "WiX Toolset Team", Repository: "https://github.com/wixtoolset/wix",
		LicenseFile: "OSMFEULA.txt", LicenseSHA256: hex.EncodeToString(h[:]), License: "LicenseRef-x"}

	check := func(get func(string) ([]byte, error)) []string {
		t.Helper()
		diffs, err := checkWixPackage("WixToolset.UI.wixext", "7.0.0", pin, get)
		if err != nil {
			t.Fatal(err)
		}
		return diffs
	}
	if d := check(served(t, pin.Authors, "OSMFEULA.txt", pin.Repository, text)); len(d) != 0 {
		t.Errorf("a matching pin: %v", d)
	}
	for what, get := range map[string]func(string) ([]byte, error){
		"authors":      served(t, "Someone Else", "OSMFEULA.txt", pin.Repository, text),
		"licence":      served(t, pin.Authors, "LICENSE.txt", pin.Repository, text),
		"repository":   served(t, pin.Authors, "OSMFEULA.txt", "https://example.invalid/wix", text),
		"OSMFEULA.txt": served(t, pin.Authors, "OSMFEULA.txt", pin.Repository, text+" amended"),
	} {
		if d := check(get); len(d) != 1 || !strings.HasPrefix(d[0], what) {
			t.Errorf("%s drift: got %v", what, d)
		}
	}
}
