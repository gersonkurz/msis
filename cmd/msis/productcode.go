package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/variables"
	"github.com/gersonkurz/msis/internal/wix"
)

// productCodePlaceholder stands in for the ProductCode while the WXS is rendered, so the WXS can
// be hashed before the code it will carry is known (#66).
const productCodePlaceholder = "MSIS-PRODUCT-CODE-PLACEHOLDER"

// fileWixVariables are the WixVariables whose value is a file WiX packages: the UI's bitmaps,
// icons and licence text.
var fileWixVariables = map[string]bool{
	"WixUIBannerBmp": true, "WixUIDialogBmp": true, "WixUILicenseRtf": true,
	"WixUIExclamationIco": true, "WixUIInfoIco": true, "WixUINewIco": true, "WixUIUpIco": true,
}

// productCode derives the ProductCode for a package from everything it is built from (#66,
// decisions D20): the UpgradeCode, version and platform; msis's, WiX's and the extensions'
// versions; the WXS as rendered with the placeholder; and the SHA-256 of every file that WXS
// references, resolved through the bind paths as WiX resolves them, and of the -loc file.
// Identical inputs give the same code, any change gives a new one - so two builds of one
// script match, and a rebuild with a changed file still major-upgrades.
//
// It returns "" and the reason when a referenced file cannot be resolved and hashed: a code that
// could not see a file must not stay the same when that file changes, so WiX then generates one,
// as it always did.
func productCode(wxs string, vars variables.Dictionary, rec *buildrecord.Record, templateFolder string, toolchain []string) (string, string) {
	// WiX's preprocessor can add inputs the hash never sees - an <?include?>d file, a $(env.X)
	// value - so a WXS that uses it gets no derived code (#66's review); referencedFiles says so.
	refs, err := referencedFiles(wxs)
	if err != nil {
		return "", "the WXS " + err.Error()
	}
	h := sha256.New()
	fmt.Fprintf(h, "msis product code v1\x00%s\x00%s\x00%s\x00", vars.UpgradeCode(), vars["PRODUCT_VERSION"], vars.Platform())
	for _, t := range toolchain {
		fmt.Fprintf(h, "%s\x00", t)
	}
	fmt.Fprintf(h, "%d\x00%s", len(wxs), wxs)
	for _, ref := range refs {
		path, ok := rec.Locate(ref)
		if !ok {
			return "", fmt.Sprintf("%s, which the WXS references, is in none of the build's bind paths", ref)
		}
		sum, err := sha256File(path)
		if err != nil {
			return "", err.Error()
		}
		fmt.Fprintf(h, "%s\x00%s\x00", ref, sum)
	}
	if loc := wix.LocalizationFile(templateFolder, vars["LANGUAGE"]); loc != "" {
		sum, err := sha256File(loc)
		if err != nil {
			return "", err.Error()
		}
		fmt.Fprintf(h, "loc\x00%s\x00", sum)
	}
	return guidOf(h.Sum(nil)), ""
}

// referencedFiles is every file the WXS names for WiX to package, sorted and without repeats.
func referencedFiles(wxs string) ([]string, error) {
	seen := map[string]bool{}
	d := xml.NewDecoder(strings.NewReader(wxs))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("is not readable XML: %w", err)
		}
		// The preprocessor works on the DECODED document, so it is looked for there: a
		// character reference such as &#36;(env.X) is a preprocessor variable to WiX too.
		switch t := tok.(type) {
		case xml.ProcInst:
			if t.Target != "xml" {
				return nil, fmt.Errorf("uses the WiX preprocessor (<?%s ...?>), whose inputs msis cannot see", t.Target)
			}
		case xml.CharData:
			if preprocessorVariable(string(t)) {
				return nil, errPreprocessorVariable
			}
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		for _, a := range el.Attr {
			if preprocessorVariable(a.Value) {
				return nil, errPreprocessorVariable
			}
		}
		fileVar := el.Name.Local == "WixVariable"
		if fileVar {
			fileVar = false
			for _, a := range el.Attr {
				if a.Name.Local == "Id" && fileWixVariables[a.Value] {
					fileVar = true
				}
			}
		}
		for _, a := range el.Attr {
			switch {
			case a.Name.Local == "Source" || a.Name.Local == "SourceFile", fileVar && a.Name.Local == "Value":
				if a.Value != "" {
					// "$$" is WiX's escaped "$" (the generator writes a file name's "$" so);
					// the file WiX looks for has the single one.
					seen[strings.ReplaceAll(a.Value, "$$", "$")] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out, nil
}

var errPreprocessorVariable = errors.New("uses a WiX preprocessor variable $(...), whose value msis cannot see")

// preprocessorVariable reports whether a decoded value holds a $(...) reference; WiX's escaped
// "$$" is a plain dollar (the generator writes a file name's "$" so), not one.
func preprocessorVariable(v string) bool {
	return strings.Contains(strings.ReplaceAll(v, "$$", ""), "$(")
}

// guidOf formats a digest as a Windows Installer GUID, marked as a name-based UUID (version 8,
// RFC 9562) so it cannot be mistaken for a random one.
func guidOf(sum []byte) string {
	b := append([]byte(nil), sum[:16]...)
	b[6] = b[6]&0x0f | 0x80
	b[8] = b[8]&0x3f | 0x80
	x := strings.ToUpper(hex.EncodeToString(b))
	return "{" + x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32] + "}"
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// applyProductCode puts the derived ProductCode into a WXS rendered with the placeholder (#66),
// and says where it came from. A template without the attribute is left alone. When no code
// can be derived, the attribute is dropped and WiX generates one, as it always did.
func applyProductCode(wxs string, vars variables.Dictionary, rec *buildrecord.Record, templateFolder, wixDir string) (string, string, error) {
	if !strings.Contains(wxs, productCodePlaceholder) {
		return wxs, "", nil
	}
	toolchain := make([]string, 0, len(rec.Toolchain)+2)
	for _, t := range rec.Toolchain {
		toolchain = append(toolchain, t.Name+"@"+t.Version)
	}
	sort.Strings(toolchain)
	code, why := "", ""
	for _, e := range wix.ResolveExtensions(wixDir, wix.MSIExtensions(), wix.GetWixMajorVersion()) {
		if e.Path == "" {
			why = "the WiX extension " + e.ID + " is not in a cache msis resolves, so its content is unknown"
			break
		}
		toolchain = append(toolchain, e.ID+"@"+e.Version)
	}
	if why == "" {
		code, why = productCode(wxs, vars, rec, templateFolder, toolchain)
	}
	if code == "" {
		// The attribute goes, however the template wrote it, and WiX generates the code. A
		// placeholder left anywhere else - a template using {{PRODUCT_CODE}} outside <Package> -
		// has no code to become, and stops the build rather than reach WiX.
		out := placeholderAttribute.ReplaceAllString(wxs, "")
		if strings.Contains(out, productCodePlaceholder) {
			return "", "", fmt.Errorf("the template uses {{PRODUCT_CODE}} outside <Package ProductCode=...>, and no "+
				"ProductCode could be derived (%s); set PRODUCT_CODE in the script", why)
		}
		return out, "ProductCode: generated by WiX - " + why, nil
	}
	return strings.ReplaceAll(wxs, productCodePlaceholder, code),
		"ProductCode: " + code + " (derived from everything the package is built from)", nil
}

// placeholderAttribute is ProductCode="<placeholder>" in any quote style and spacing.
var placeholderAttribute = regexp.MustCompile(`\s+ProductCode\s*=\s*(?:"` + productCodePlaceholder + `"|'` + productCodePlaceholder + `')`)
