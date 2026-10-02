package template

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// balNamespace is the WiX bootstrapper application extension's namespace (WiX 4 and later).
const balNamespace = "http://wixtoolset.org/schemas/v4/wxs/bal"

// CheckBundleBootstrapper refuses a rendered bundle whose WixStandardBootstrapperApplication has
// Theme="none" (#95, decisions D31).
//
// WiX accepts that without a word, and the bundle cannot start: "none" references no built-in
// theme payloads, and WixStdBA fails while it is being created ("BootstrapperApplication.xml
// manifest is missing wixstdba information", 0x80070490). msis's own silent template shipped it
// until 3.0.7, so every custom template copied from it carries it too.
//
// This is msis policy, stated as such: from the rendered document alone msis cannot prove what a
// ThemeFile or payloads defined elsewhere would supply, so it does not try. A template keeps a
// built-in theme (and may add a ThemeFile to restyle it). The check reads the XML with namespaces,
// so comments and any prefix bound to the bal namespace are handled, and other bootstrapper
// applications are left alone. A document that does not parse is left to wix to report.
func CheckBundleBootstrapper(templatePath, rendered string) error {
	dec := xml.NewDecoder(strings.NewReader(rendered))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return nil
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Space != balNamespace || el.Name.Local != "WixStandardBootstrapperApplication" {
			continue
		}
		for _, attr := range el.Attr {
			if attr.Name.Space == "" && attr.Name.Local == "Theme" && strings.EqualFold(attr.Value, "none") {
				return fmt.Errorf(`template %s gives WixStandardBootstrapperApplication Theme="none": that theme ships no theme payloads, `+
					`and a bundle built with it does not start (0x80070490, "manifest is missing wixstdba information"; #95, decisions D31). `+
					`msis refuses it; use a built-in theme such as Theme="hyperlinkLicense", as the shipped bundle templates do`, templatePath)
			}
		}
	}
}
