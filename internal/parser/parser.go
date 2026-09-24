// Package parser reads .msis XML files into the IR representation.
package parser

import (
	"encoding/xml"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/gersonkurz/msis/internal/contact"
	"github.com/gersonkurz/msis/internal/ir"
)

// Parse reads an .msis file and returns the parsed Setup structure.
func Parse(filename string) (*ir.Setup, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return ParseBytes(data)
}

// ParseBytes parses .msis XML from a byte slice.
func ParseBytes(data []byte) (*ir.Setup, error) {
	var raw xmlSetup
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing XML: %w", err)
	}

	return convertSetup(&raw)
}

// XML intermediate types for unmarshaling

type xmlSetup struct {
	XMLName xml.Name `xml:"setup"`
	Silent  string   `xml:"silent,attr"`
	// Children captured in document order via custom UnmarshalXML
	Sets     []xmlSet
	Requires []xmlRequires // Top-level runtime requirements
	SBOMs    []xmlSBOM     // Supplied component SBOMs (#36)
	// Components are facts the script declares about payload files (#64)
	Components []xmlComponent
	VEX        *xmlVEX // The VEX document annotating this product (#37)
	Features   []xmlFeature
	Items      []xmlItem // Preserves document order
	Bundle     *xmlBundle
}

// xmlItem holds any item type with its original position
type xmlItem struct {
	Type              string // "files", "registry", "set-env", etc.
	Files             *xmlFiles
	Registry          *xmlRegistry
	SetEnv            *xmlSetEnv
	Shortcut          *xmlShortcut
	Service           *xmlService
	Exclude           *xmlExclude
	Execute           *xmlExecute
	CreateFolder      *xmlCreateFolder
	RemoveOnUninstall *xmlRemoveOnUninstall
}

type xmlSet struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlFeature struct {
	Name        string `xml:"name,attr"`
	Enabled     string `xml:"enabled,attr"`
	Condition   string `xml:"condition,attr"`
	Allowed     string `xml:"allowed,attr"`
	SubFeatures []xmlFeature
	Items       []xmlItem // Preserves document order
}

type xmlFiles struct {
	Source         string `xml:"source,attr"`
	Target         string `xml:"target,attr"`
	DoNotOverwrite string `xml:"do-not-overwrite,attr"`
}

type xmlRegistry struct {
	File      string `xml:"file,attr"`
	SDDL      string `xml:"sddl,attr"`
	Preserve  string `xml:"preserve,attr"`
	Permanent string `xml:"permanent,attr"`
	Condition string `xml:"condition,attr"`
}

type xmlSetEnv struct {
	Name      string `xml:"name,attr"`
	Value     string `xml:"value,attr"`
	Permanent string `xml:"permanent,attr"`
}

type xmlShortcut struct {
	Name        string `xml:"name,attr"`
	Target      string `xml:"target,attr"`
	File        string `xml:"file,attr"`
	Description string `xml:"description,attr"`
	Icon        string `xml:"icon,attr"`
}

type xmlService struct {
	FileName           string `xml:"file-name,attr"`
	ServiceName        string `xml:"service-name,attr"`
	ServiceDisplayName string `xml:"service-display-name,attr"`
	Start              string `xml:"start,attr"`
	Description        string `xml:"description,attr"`
	ServiceType        string `xml:"service-type,attr"`
	ErrorControl       string `xml:"error-control,attr"`
	Restart            string `xml:"restart,attr"`
	StartAfterInstall  string `xml:"start-after-install,attr"`
}

type xmlExclude struct {
	Folder string `xml:"folder,attr"`
}

type xmlExecute struct {
	Cmd         string `xml:"cmd,attr"`
	When        string `xml:"when,attr"`
	Directory   string `xml:"directory,attr"`
	FailOnError string `xml:"fail-on-error,attr"`
	Quiet       string `xml:"quiet,attr"`
}

type xmlCreateFolder struct {
	Target string `xml:"target,attr"`
}

type xmlRemoveOnUninstall struct {
	Registry string `xml:"registry,attr"`
	Folder   string `xml:"folder,attr"`
}

type xmlBundle struct {
	// Legacy shorthand attributes
	Source64bit string `xml:"source_64bit,attr"`
	Source32bit string `xml:"source_32bit,attr"`
	SourceArm64 string `xml:"source_arm64,attr"`

	// New nested elements
	Prerequisites []xmlPrerequisite
	MSI           *xmlBundleMSI
	ExePackages   []xmlExePackage
}

type xmlPrerequisite struct {
	Type    string `xml:"type,attr"`
	Version string `xml:"version,attr"`
	Source  string `xml:"source,attr"`
	SHA256  string `xml:"sha256,attr"`
}

type xmlBundleMSI struct {
	Source      string `xml:"source,attr"`
	Source64bit string `xml:"source_64bit,attr"`
	Source32bit string `xml:"source_32bit,attr"`
	SourceArm64 string `xml:"source_arm64,attr"`
}

type xmlExePackage struct {
	ID              string `xml:"id,attr"`
	Source          string `xml:"source,attr"`
	DetectCondition string `xml:"detect,attr"`
	InstallArgs     string `xml:"args,attr"`
}

// xmlSBOM represents a supplied component SBOM: <sbom source="..." for="..."/>
type xmlSBOM struct {
	Source string `xml:"source,attr"`
	For    string `xml:"for,attr"`
}

// UnmarshalXML for xmlSBOM - both attributes are required, and an unknown one is an error
// rather than silence: a typo in `for` would otherwise mean the document is merged onto
// nothing at all.
func (s *xmlSBOM) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "source":
			s.Source = attr.Value
		case "for":
			s.For = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <sbom>", attr.Name.Local)
		}
	}
	if s.Source == "" {
		return fmt.Errorf("<sbom> requires a source attribute")
	}
	if s.For == "" {
		return fmt.Errorf("<sbom> requires a for attribute naming the file it describes")
	}
	return d.Skip()
}

// xmlComponent represents facts declared about one payload file (#64):
// <component for="[INSTALLDIR]libfoo.dll" name= version= creator= license= purl= cpe=/>
type xmlComponent struct {
	ir.DeclaredComponent
}

// UnmarshalXML for xmlComponent - `for` is required, an unknown attribute is an error, and each
// value is checked for the shape it must have. A declaration is published as a fact about the
// file, so a malformed one is refused here, where the author can see which element it came from.
func (c *xmlComponent) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		v := strings.TrimSpace(attr.Value)
		switch attr.Name.Local {
		case "for":
			c.For = v
		case "name":
			c.Name = v
		case "version":
			c.Version = v
		case "creator":
			c.Creator = v
		case "license":
			c.License = v
		case "purl":
			c.PURL = v
		case "cpe":
			c.CPE = v
		default:
			return fmt.Errorf("unknown attribute '%s' on <component>", attr.Name.Local)
		}
	}
	if c.For == "" {
		return fmt.Errorf("<component> requires a for attribute naming the file it describes")
	}
	if c.Name == "" && c.Version == "" && c.Creator == "" && c.License == "" && c.PURL == "" && c.CPE == "" {
		return fmt.Errorf("<component for=%q> declares nothing", c.For)
	}
	if c.Creator != "" && !contact.IsEmail(c.Creator) && !contact.IsURL(c.Creator) {
		return fmt.Errorf("<component for=%q>: creator %q is neither an email address nor an absolute http(s) URL", c.For, c.Creator)
	}
	if c.License != "" {
		if err := validSPDX(c.License); err != nil {
			return fmt.Errorf("<component for=%q>: license %q is not an SPDX licence expression: %v", c.For, c.License, err)
		}
	}
	if c.PURL != "" && !purlShape.MatchString(c.PURL) {
		return fmt.Errorf("<component for=%q>: purl %q is not a package URL (pkg:type/name@version)", c.For, c.PURL)
	}
	if c.CPE != "" && !strings.HasPrefix(c.CPE, "cpe:2.3:") && !strings.HasPrefix(c.CPE, "cpe:/") {
		return fmt.Errorf("<component for=%q>: cpe %q is neither a CPE 2.3 formatted string nor a CPE 2.2 URI", c.For, c.CPE)
	}
	return d.Skip()
}

var (
	purlShape = regexp.MustCompile(`^pkg:[a-zA-Z][a-zA-Z0-9.+-]*/[^@\s]+(@[^\s]+)?$`)
)

// xmlVEX represents the VEX document annotating this product: <vex source="..."/>
type xmlVEX struct {
	Source string `xml:"source,attr"`
}

// UnmarshalXML for xmlVEX - source is required, and an unknown attribute is an error.
func (v *xmlVEX) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "source":
			v.Source = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <vex>", attr.Name.Local)
		}
	}
	if v.Source == "" {
		return fmt.Errorf("<vex> requires a source attribute")
	}
	return d.Skip()
}

// xmlRequires represents a top-level runtime requirement
type xmlRequires struct {
	Type    string `xml:"type,attr"`
	Version string `xml:"version,attr"`
	Source  string `xml:"source,attr"`
	SHA256  string `xml:"sha256,attr"`
}

// sha256Hex is what a sha256= attribute must be once trimmed and lower-cased.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// checkSuppliedDigest validates a sha256= attribute on <requires> or <prerequisite> (#50) and
// returns it normalised to lowercase. It belongs to a SUPPLIED source: a download msis performs
// is pinned by msis itself (D5), so a digest without a source has nothing to apply to and is
// refused rather than ignored - an attribute that is silently dropped is one the author
// believes is protecting them.
func checkSuppliedDigest(element, source, digest string) (string, error) {
	if digest == "" {
		return "", nil
	}
	if source == "" {
		return "", fmt.Errorf("<%s> sha256 applies to a supplied source; a download msis performs is pinned by msis itself (see docs/prerequisites.md)", element)
	}
	norm := strings.ToLower(strings.TrimSpace(digest))
	if !sha256Hex.MatchString(norm) {
		return "", fmt.Errorf("<%s> sha256 must be 64 hexadecimal characters, got %q", element, digest)
	}
	return norm, nil
}

// UnmarshalXML for xmlSet - validates attributes
func (s *xmlSet) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasName, hasValue := false, false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "name":
			s.Name = attr.Value
			hasName = true
		case "value":
			s.Value = attr.Value
			hasValue = true
		default:
			return fmt.Errorf("unknown attribute '%s' on <set>", attr.Name.Local)
		}
	}
	if !hasName {
		return fmt.Errorf("<set> requires 'name' attribute")
	}
	if !hasValue {
		return fmt.Errorf("<set> requires 'value' attribute")
	}
	// Consume any content (should be empty)
	return d.Skip()
}

// UnmarshalXML for xmlFiles - validates attributes
func (f *xmlFiles) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasSource, hasTarget := false, false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "source":
			f.Source = attr.Value
			hasSource = true
		case "target":
			f.Target = attr.Value
			hasTarget = true
		case "do-not-overwrite":
			f.DoNotOverwrite = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <files>", attr.Name.Local)
		}
	}
	if !hasSource {
		return fmt.Errorf("<files> requires 'source' attribute")
	}
	if !hasTarget {
		return fmt.Errorf("<files> requires 'target' attribute")
	}
	return d.Skip()
}

// UnmarshalXML for xmlRegistry - validates attributes
func (r *xmlRegistry) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasFile := false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "file":
			r.File = attr.Value
			hasFile = true
		case "sddl":
			r.SDDL = attr.Value
		case "preserve":
			r.Preserve = attr.Value
		case "permanent":
			r.Permanent = attr.Value
		case "condition":
			r.Condition = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <registry>", attr.Name.Local)
		}
	}
	if !hasFile {
		return fmt.Errorf("<registry> requires 'file' attribute")
	}
	return d.Skip()
}

// UnmarshalXML for xmlSetEnv - validates attributes
func (s *xmlSetEnv) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasName, hasValue := false, false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "name":
			s.Name = attr.Value
			hasName = true
		case "value":
			s.Value = attr.Value
			hasValue = true
		default:
			return fmt.Errorf("unknown attribute '%s' on <set-env>", attr.Name.Local)
		}
	}
	if !hasName {
		return fmt.Errorf("<set-env> requires 'name' attribute")
	}
	if !hasValue {
		return fmt.Errorf("<set-env> requires 'value' attribute")
	}
	return d.Skip()
}

// UnmarshalXML for xmlShortcut - validates attributes
func (s *xmlShortcut) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasName, hasTarget, hasFile := false, false, false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "name":
			s.Name = attr.Value
			hasName = true
		case "target":
			s.Target = attr.Value
			hasTarget = true
		case "file":
			s.File = attr.Value
			hasFile = true
		case "description":
			s.Description = attr.Value
		case "icon":
			s.Icon = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <shortcut>", attr.Name.Local)
		}
	}
	if !hasName {
		return fmt.Errorf("<shortcut> requires 'name' attribute")
	}
	if !hasTarget {
		return fmt.Errorf("<shortcut> requires 'target' attribute")
	}
	if !hasFile {
		return fmt.Errorf("<shortcut> requires 'file' attribute")
	}
	return d.Skip()
}

// UnmarshalXML for xmlService - validates attributes
func (s *xmlService) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasFileName, hasServiceName := false, false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "file-name":
			s.FileName = attr.Value
			hasFileName = true
		case "service-name":
			s.ServiceName = attr.Value
			hasServiceName = true
		case "service-display-name":
			s.ServiceDisplayName = attr.Value
		case "start":
			s.Start = attr.Value
		case "description":
			s.Description = attr.Value
		case "service-type":
			s.ServiceType = attr.Value
		case "error-control":
			s.ErrorControl = attr.Value
		case "restart":
			s.Restart = attr.Value
		case "start-after-install":
			s.StartAfterInstall = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <service>", attr.Name.Local)
		}
	}
	if !hasFileName {
		return fmt.Errorf("<service> requires 'file-name' attribute")
	}
	if !hasServiceName {
		return fmt.Errorf("<service> requires 'service-name' attribute")
	}
	return d.Skip()
}

// UnmarshalXML for xmlExclude - validates attributes
func (e *xmlExclude) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasFolder := false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "folder":
			e.Folder = attr.Value
			hasFolder = true
		default:
			return fmt.Errorf("unknown attribute '%s' on <exclude>", attr.Name.Local)
		}
	}
	if !hasFolder {
		return fmt.Errorf("<exclude> requires 'folder' attribute")
	}
	return d.Skip()
}

// UnmarshalXML for xmlExecute - validates attributes
func (e *xmlExecute) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasCmd, hasWhen := false, false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "cmd":
			e.Cmd = attr.Value
			hasCmd = true
		case "when":
			e.When = attr.Value
			hasWhen = true
		case "directory":
			e.Directory = attr.Value
		case "fail-on-error":
			e.FailOnError = attr.Value
		case "quiet":
			e.Quiet = strings.ToLower(attr.Value)
		default:
			return fmt.Errorf("unknown attribute '%s' on <execute>", attr.Name.Local)
		}
	}
	if !hasCmd {
		return fmt.Errorf("<execute> requires 'cmd' attribute")
	}
	if !hasWhen {
		return fmt.Errorf("<execute> requires 'when' attribute")
	}
	switch e.Quiet {
	case "", "no", "yes", "auto":
		// valid
	default:
		return fmt.Errorf("invalid quiet value %q on <execute>: must be one of no, yes, auto", e.Quiet)
	}
	return d.Skip()
}

// UnmarshalXML for xmlRequires - validates attributes
func (r *xmlRequires) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	hasType := false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "type":
			r.Type = strings.ToLower(attr.Value) // Normalize to lowercase
			hasType = true
		case "version":
			r.Version = attr.Value
		case "source":
			r.Source = attr.Value
		case "sha256":
			r.SHA256 = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <requires>", attr.Name.Local)
		}
	}
	if !hasType {
		return fmt.Errorf("<requires> requires 'type' attribute")
	}
	if r.Version == "" && r.Source == "" {
		return fmt.Errorf("<requires> requires 'version' or 'source' attribute")
	}
	digest, err := checkSuppliedDigest("requires", r.Source, r.SHA256)
	if err != nil {
		return err
	}
	r.SHA256 = digest
	return d.Skip()
}

// UnmarshalXML for xmlBundle - supports both legacy shorthand and nested elements
func (b *xmlBundle) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	// Parse attributes (legacy shorthand)
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "source_64bit":
			b.Source64bit = attr.Value
		case "source_32bit":
			b.Source32bit = attr.Value
		case "source_arm64":
			b.SourceArm64 = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <bundle>", attr.Name.Local)
		}
	}

	// Parse nested elements
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "prerequisite":
				var prereq xmlPrerequisite
				if err := d.DecodeElement(&prereq, &t); err != nil {
					return err
				}
				if prereq.Type == "" {
					return fmt.Errorf("<prerequisite> requires 'type' attribute")
				}
				if prereq.Version == "" && prereq.Source == "" {
					return fmt.Errorf("<prerequisite> requires 'version' or 'source' attribute")
				}
				digest, err := checkSuppliedDigest("prerequisite", prereq.Source, prereq.SHA256)
				if err != nil {
					return err
				}
				prereq.SHA256 = digest
				b.Prerequisites = append(b.Prerequisites, prereq)
			case "msi":
				var msi xmlBundleMSI
				if err := d.DecodeElement(&msi, &t); err != nil {
					return err
				}
				if msi.Source == "" && msi.Source64bit == "" && msi.Source32bit == "" && msi.SourceArm64 == "" {
					return fmt.Errorf("<msi> requires 'source', 'source_64bit', 'source_32bit', or 'source_arm64' attribute")
				}
				b.MSI = &msi
			case "exe":
				var exe xmlExePackage
				if err := d.DecodeElement(&exe, &t); err != nil {
					return err
				}
				if exe.Source == "" {
					return fmt.Errorf("<exe> requires 'source' attribute")
				}
				b.ExePackages = append(b.ExePackages, exe)
			default:
				return fmt.Errorf("unknown element <%s> in <bundle>", t.Name.Local)
			}
		case xml.EndElement:
			return nil
		}
	}
}

// UnmarshalXML for xmlSetup to preserve item order
func (s *xmlSetup) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	// Parse and validate attributes
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "silent":
			s.Silent = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <setup>", attr.Name.Local)
		}
	}

	// Parse child elements in order
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "set":
				var set xmlSet
				if err := d.DecodeElement(&set, &t); err != nil {
					return err
				}
				s.Sets = append(s.Sets, set)

			case "feature":
				var feat xmlFeature
				if err := d.DecodeElement(&feat, &t); err != nil {
					return err
				}
				s.Features = append(s.Features, feat)

			case "bundle":
				var bundle xmlBundle
				if err := d.DecodeElement(&bundle, &t); err != nil {
					return err
				}
				s.Bundle = &bundle

			case "requires":
				var req xmlRequires
				if err := d.DecodeElement(&req, &t); err != nil {
					return err
				}
				s.Requires = append(s.Requires, req)

			case "sbom":
				var sb xmlSBOM
				if err := d.DecodeElement(&sb, &t); err != nil {
					return err
				}
				s.SBOMs = append(s.SBOMs, sb)

			case "component":
				var c xmlComponent
				if err := d.DecodeElement(&c, &t); err != nil {
					return err
				}
				s.Components = append(s.Components, c)

			case "vex":
				if s.VEX != nil {
					return fmt.Errorf("<vex> is given twice; one VEX document covers the " +
						"product, and two would each have to say which statements win")
				}
				var vx xmlVEX
				if err := d.DecodeElement(&vx, &t); err != nil {
					return err
				}
				s.VEX = &vx

			case "files":
				var files xmlFiles
				if err := d.DecodeElement(&files, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "files", Files: &files})

			case "registry":
				var reg xmlRegistry
				if err := d.DecodeElement(&reg, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "registry", Registry: &reg})

			case "set-env":
				var env xmlSetEnv
				if err := d.DecodeElement(&env, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "set-env", SetEnv: &env})

			case "shortcut":
				var sc xmlShortcut
				if err := d.DecodeElement(&sc, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "shortcut", Shortcut: &sc})

			case "service":
				var svc xmlService
				if err := d.DecodeElement(&svc, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "service", Service: &svc})

			case "exclude":
				var exc xmlExclude
				if err := d.DecodeElement(&exc, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "exclude", Exclude: &exc})

			case "create-folder":
				var cf xmlCreateFolder
				if err := d.DecodeElement(&cf, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "create-folder", CreateFolder: &cf})

			case "execute":
				var exec xmlExecute
				if err := d.DecodeElement(&exec, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "execute", Execute: &exec})

			case "remove-on-uninstall":
				var rem xmlRemoveOnUninstall
				if err := d.DecodeElement(&rem, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "remove-on-uninstall", RemoveOnUninstall: &rem})

			default:
				return fmt.Errorf("unknown element <%s> in <setup>", t.Name.Local)
			}

		case xml.EndElement:
			if t.Name == start.Name {
				return nil
			}
		}
	}
}

// UnmarshalXML for xmlFeature to preserve item order
func (f *xmlFeature) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	// Parse and validate attributes (track presence, not value)
	hasName := false
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "name":
			f.Name = attr.Value
			hasName = true
		case "enabled":
			f.Enabled = attr.Value
		case "condition":
			f.Condition = attr.Value
		case "allowed":
			f.Allowed = attr.Value
		default:
			return fmt.Errorf("unknown attribute '%s' on <feature>", attr.Name.Local)
		}
	}

	// Validate required attributes by presence
	if !hasName {
		return fmt.Errorf("<feature> requires 'name' attribute")
	}

	// Parse child elements in order
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "feature":
				var feat xmlFeature
				if err := d.DecodeElement(&feat, &t); err != nil {
					return err
				}
				f.SubFeatures = append(f.SubFeatures, feat)

			case "files":
				var files xmlFiles
				if err := d.DecodeElement(&files, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "files", Files: &files})

			case "registry":
				var reg xmlRegistry
				if err := d.DecodeElement(&reg, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "registry", Registry: &reg})

			case "set-env":
				var env xmlSetEnv
				if err := d.DecodeElement(&env, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "set-env", SetEnv: &env})

			case "shortcut":
				var sc xmlShortcut
				if err := d.DecodeElement(&sc, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "shortcut", Shortcut: &sc})

			case "service":
				var svc xmlService
				if err := d.DecodeElement(&svc, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "service", Service: &svc})

			case "exclude":
				var exc xmlExclude
				if err := d.DecodeElement(&exc, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "exclude", Exclude: &exc})

			case "create-folder":
				var cf xmlCreateFolder
				if err := d.DecodeElement(&cf, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "create-folder", CreateFolder: &cf})

			case "execute":
				var exec xmlExecute
				if err := d.DecodeElement(&exec, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "execute", Execute: &exec})

			case "remove-on-uninstall":
				var rem xmlRemoveOnUninstall
				if err := d.DecodeElement(&rem, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "remove-on-uninstall", RemoveOnUninstall: &rem})

			default:
				return fmt.Errorf("unknown element <%s> in <feature>", t.Name.Local)
			}

		case xml.EndElement:
			if t.Name == start.Name {
				return nil
			}
		}
	}
}

// Conversion functions

func convertSetup(raw *xmlSetup) (*ir.Setup, error) {
	setup := &ir.Setup{
		Silent: parseMsisBool(raw.Silent),
	}

	// Convert sets
	for _, s := range raw.Sets {
		setup.Sets = append(setup.Sets, ir.Set{
			Name:  s.Name,
			Value: s.Value,
		})
	}

	// Convert requirements
	for _, r := range raw.Requires {
		setup.Requires = append(setup.Requires, ir.Requirement{
			Type:    r.Type,
			Version: r.Version,
			Source:  r.Source,
			SHA256:  r.SHA256,
		})
	}

	// Convert the VEX document
	if raw.VEX != nil {
		setup.VEX = raw.VEX.Source
	}

	// Convert supplied SBOMs
	for _, sb := range raw.SBOMs {
		setup.SBOMs = append(setup.SBOMs, ir.SuppliedSBOM{Source: sb.Source, For: sb.For})
	}
	for _, c := range raw.Components {
		setup.Components = append(setup.Components, c.DeclaredComponent)
	}

	// Convert features
	for _, f := range raw.Features {
		feature, err := convertFeature(&f)
		if err != nil {
			return nil, err
		}
		setup.Features = append(setup.Features, *feature)
	}

	// Convert top-level items (preserves document order)
	items, err := convertItems(raw.Items)
	if err != nil {
		return nil, err
	}
	setup.Items = items

	// Convert bundle
	if raw.Bundle != nil {
		bundle := &ir.Bundle{
			Source64bit: raw.Bundle.Source64bit,
			Source32bit: raw.Bundle.Source32bit,
			SourceArm64: raw.Bundle.SourceArm64,
		}

		// Convert prerequisites
		for _, p := range raw.Bundle.Prerequisites {
			bundle.Prerequisites = append(bundle.Prerequisites, ir.Prerequisite{
				Type:    p.Type,
				Version: p.Version,
				Source:  p.Source,
				SHA256:  p.SHA256,
			})
		}

		// Convert MSI element
		if raw.Bundle.MSI != nil {
			bundle.MSI = &ir.BundleMSI{
				Source:      raw.Bundle.MSI.Source,
				Source64bit: raw.Bundle.MSI.Source64bit,
				Source32bit: raw.Bundle.MSI.Source32bit,
				SourceArm64: raw.Bundle.MSI.SourceArm64,
			}
		}

		// Convert exe packages
		for _, e := range raw.Bundle.ExePackages {
			bundle.ExePackages = append(bundle.ExePackages, ir.ExePackage{
				ID:              e.ID,
				Source:          e.Source,
				DetectCondition: e.DetectCondition,
				InstallArgs:     e.InstallArgs,
			})
		}

		setup.Bundle = bundle
	}

	return setup, nil
}

func convertFeature(raw *xmlFeature) (*ir.Feature, error) {
	feature := &ir.Feature{
		Name:      raw.Name,
		Enabled:   parseMsisBoolDefault(raw.Enabled, true),
		Condition: raw.Condition,
		Allowed:   parseMsisBoolDefault(raw.Allowed, true),
	}

	// Convert items (preserves document order)
	items, err := convertItems(raw.Items)
	if err != nil {
		return nil, err
	}
	feature.Items = items

	// Convert nested features
	for _, sf := range raw.SubFeatures {
		subFeature, err := convertFeature(&sf)
		if err != nil {
			return nil, err
		}
		feature.SubFeatures = append(feature.SubFeatures, *subFeature)
	}

	return feature, nil
}

func convertItems(rawItems []xmlItem) ([]ir.Item, error) {
	var items []ir.Item

	for _, raw := range rawItems {
		switch raw.Type {
		case "files":
			items = append(items, ir.Files{
				Source:         raw.Files.Source,
				Target:         raw.Files.Target,
				DoNotOverwrite: parseMsisBool(raw.Files.DoNotOverwrite),
			})

		case "registry":
			items = append(items, ir.Registry{
				File:      raw.Registry.File,
				SDDL:      raw.Registry.SDDL,
				Preserve:  parseMsisBool(raw.Registry.Preserve),
				Permanent: parseMsisBool(raw.Registry.Permanent),
				Condition: raw.Registry.Condition,
			})

		case "set-env":
			items = append(items, ir.SetEnv{
				Name:      raw.SetEnv.Name,
				Value:     raw.SetEnv.Value,
				Permanent: parseMsisBool(raw.SetEnv.Permanent),
			})

		case "shortcut":
			items = append(items, ir.Shortcut{
				Name:        raw.Shortcut.Name,
				Target:      raw.Shortcut.Target,
				File:        raw.Shortcut.File,
				Description: raw.Shortcut.Description,
				Icon:        raw.Shortcut.Icon,
			})

		case "service":
			items = append(items, ir.Service{
				FileName:           raw.Service.FileName,
				ServiceName:        raw.Service.ServiceName,
				ServiceDisplayName: raw.Service.ServiceDisplayName,
				Start:              raw.Service.Start,
				Description:        raw.Service.Description,
				ServiceType:        raw.Service.ServiceType,
				ErrorControl:       raw.Service.ErrorControl,
				Restart:            raw.Service.Restart,
				StartAfterInstall:  raw.Service.StartAfterInstall,
			})

		case "exclude":
			items = append(items, ir.Exclude{
				Folder: raw.Exclude.Folder,
			})

		case "create-folder":
			items = append(items, ir.CreateFolder{
				Target: raw.CreateFolder.Target,
			})

		case "execute":
			quiet := raw.Execute.Quiet
			if quiet == "" {
				quiet = "no"
			}
			items = append(items, ir.Execute{
				Cmd:         raw.Execute.Cmd,
				When:        raw.Execute.When,
				Directory:   raw.Execute.Directory,
				FailOnError: parseMsisBool(raw.Execute.FailOnError),
				Quiet:       quiet,
			})

		case "remove-on-uninstall":
			items = append(items, ir.RemoveOnUninstall{
				Registry: raw.RemoveOnUninstall.Registry,
				Folder:   raw.RemoveOnUninstall.Folder,
			})
		}
	}

	return items, nil
}

// parseMsisBool parses msis-style boolean values.
// Valid values: true, false, yes, no, on, off, 1, 0 (case-insensitive)
// Empty string or unrecognized values return false.
func parseMsisBool(s string) bool {
	if s == "" {
		return false
	}
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true
	default:
		return false
	}
}

// parseMsisBoolDefault parses msis-style boolean with a default for empty/missing values.
// IMPORTANT: Default only applies when attribute is empty/missing.
// Invalid values (e.g., "maybe") resolve to false, NOT the default.
func parseMsisBoolDefault(s string, defaultValue bool) bool {
	if s == "" {
		return defaultValue
	}
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true
	default:
		// Invalid values resolve to false, not the default
		return false
	}
}
