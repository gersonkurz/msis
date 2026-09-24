package main

// The release check for msis's pinned WiX extension package facts (#67, decisions D18). The
// extension cache a build reads keeps only the DLL, so what each package declares - authors,
// repository, licence file - is pinned in internal/wix. This compares every pin with the
// .nuspec and the licence file nuget.org serves for that exact version, and fails on any
// difference: a changed licence text is exactly when the licence id has to be looked at again.

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/wix"
)

const nugetFlat = "https://api.nuget.org/v3-flatcontainer/"

type nuspec struct {
	Metadata struct {
		Authors string `xml:"authors"`
		License struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"license"`
		Repository struct {
			URL string `xml:"url,attr"`
		} `xml:"repository"`
	} `xml:"metadata"`
}

var httpClient = &http.Client{Timeout: 2 * time.Minute}

func fetch(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// checkWixPackage compares one pin with what nuget.org serves; the differences, if any.
func checkWixPackage(id, version string, pin wix.ExtensionPackage, get func(string) ([]byte, error)) ([]string, error) {
	lower := strings.ToLower(id)
	base := nugetFlat + lower + "/" + version + "/" + lower
	raw, err := get(base + ".nuspec")
	if err != nil {
		return nil, err
	}
	var spec nuspec
	if err := xml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("%s %s: reading the .nuspec: %w", id, version, err)
	}
	var diffs []string
	differ := func(what, pinned, served string) {
		if pinned != served {
			diffs = append(diffs, fmt.Sprintf("%s: pinned %q, nuget.org declares %q", what, pinned, served))
		}
	}
	m := spec.Metadata
	differ("authors", pin.Authors, strings.TrimSpace(m.Authors))
	differ("repository", pin.Repository, m.Repository.URL)
	differ("licence", "file:"+pin.LicenseFile, m.License.Type+":"+strings.TrimSpace(m.License.Value))

	pkg, err := get(base + "." + version + ".nupkg")
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		return nil, fmt.Errorf("%s %s: reading the package: %w", id, version, err)
	}
	f, err := z.Open(pin.LicenseFile)
	if err != nil {
		return append(diffs, fmt.Sprintf("the package carries no %s", pin.LicenseFile)), nil
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	differ(pin.LicenseFile+" SHA-256", pin.LicenseSHA256, hex.EncodeToString(h.Sum(nil)))
	return diffs, nil
}

// checkWixPackages checks every pin. A network failure fails too: an unchecked pin must not
// pass for a checked one.
func checkWixPackages(get func(string) ([]byte, error)) error {
	pins := wix.PinnedExtensionPackages()
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var problems []string
	for _, k := range keys {
		id, version, _ := strings.Cut(k, "/")
		diffs, err := checkWixPackage(id, version, pins[k], get)
		if err != nil {
			return fmt.Errorf("checking %s: %w", k, err)
		}
		for _, d := range diffs {
			problems = append(problems, k+": "+d)
		}
		if len(diffs) == 0 {
			fmt.Printf("%s: as pinned\n", k)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("the pinned WiX package facts (internal/wix/extensions.go) no longer match nuget.org:\n  %s\n"+
			"a changed licence text needs its licence id re-read against BSI TR-03183-2 §6.1 (decisions D18)",
			strings.Join(problems, "\n  "))
	}
	return nil
}
