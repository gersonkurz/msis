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
// versions; the WXS as rendered with the placeholder, with every file it references replaced by
// that file's SHA-256 - resolved through the bind paths as WiX resolves them - so where the
// sources sit is not an input (#81); and the SHA-256 of the -loc file.
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
	sums := make(map[string]string, len(refs))
	for _, ref := range refs {
		path, ok := rec.Locate(ref)
		if !ok {
			return "", fmt.Sprintf("%s, which the WXS references, is in none of the build's bind paths", ref)
		}
		sum, err := sha256File(path)
		if err != nil {
			return "", err.Error()
		}
		sums[ref] = sum
	}
	h := sha256.New()
	fmt.Fprintf(h, "msis product code v2\x00%s\x00%s\x00%s\x00", vars.UpgradeCode(), vars["PRODUCT_VERSION"], vars.Platform())
	for _, t := range toolchain {
		fmt.Fprintf(h, "%s\x00", t)
	}
	if err := hashLocationFree(h, wxs, sums); err != nil {
		return "", "the WXS " + err.Error()
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
		for _, a := range el.Attr {
			if ref, ok := fileReference(el, a); ok {
				seen[ref] = true
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

// fileReference reports whether attribute a of el names a file for WiX to package - a Source,
// a SourceFile, or the Value of a file-valued WixVariable - and returns the file as WiX looks
// for it: "$$" is WiX's escaped "$" (the generator writes a file name's "$" so).
func fileReference(el xml.StartElement, a xml.Attr) (string, bool) {
	isFile := a.Name.Local == "Source" || a.Name.Local == "SourceFile"
	if !isFile && a.Name.Local == "Value" && el.Name.Local == "WixVariable" {
		for _, id := range el.Attr {
			if id.Name.Local == "Id" && fileWixVariables[id.Value] {
				isFile = true
			}
		}
	}
	if !isFile || a.Value == "" {
		return "", false
	}
	return strings.ReplaceAll(a.Value, "$$", "$"), true
}

// hashLocationFree writes the WXS into h with every file reference replaced by the SHA-256 of
// the file it resolves to (sums, keyed as fileReference returns them) and the reference's file
// name, so the digest binds each file's content and name to its place in the package but not to
// the folder the build ran in (#81): a script with absolute sources, built from another folder,
// gets the same ProductCode.
//
// It writes the decoded token stream, not the text, so an attribute's quoting or escaping cannot
// hide or fake a reference. Every token is written, delimited, comments included.
func hashLocationFree(h io.Writer, wxs string, sums map[string]string) error {
	d := xml.NewDecoder(strings.NewReader(wxs))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("is not readable XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			fmt.Fprintf(h, "<%s:%s\x00", t.Name.Space, t.Name.Local)
			for _, a := range t.Attr {
				v := a.Value
				if ref, ok := fileReference(t, a); ok {
					// The file name stays: WiX installs a <File> without Name under its
					// Source's name, so a rename with identical bytes is a different package.
					v = "sha256:" + sums[ref] + "\x00" + ref[strings.LastIndexAny(ref, `\/`)+1:]
				}
				fmt.Fprintf(h, "@%s:%s=%d\x00%s\x00", a.Name.Space, a.Name.Local, len(v), v)
			}
		case xml.EndElement:
			fmt.Fprintf(h, ">%s:%s\x00", t.Name.Space, t.Name.Local)
		case xml.CharData:
			fmt.Fprintf(h, "t%d\x00%s", len(t), t)
		case xml.Comment:
			fmt.Fprintf(h, "c%d\x00%s", len(t), t)
		case xml.ProcInst:
			fmt.Fprintf(h, "p%s\x00%d\x00%s", t.Target, len(t.Inst), t.Inst)
		case xml.Directive:
			fmt.Fprintf(h, "d%d\x00%s", len(t), t)
		}
	}
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
