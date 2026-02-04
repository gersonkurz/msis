// Package parser reads .msis XML files into the IR representation.
package parser

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"

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
	Features []xmlFeature
	Items    []xmlItem // Preserves document order
	Bundle   *xmlBundle
}

// xmlItem holds any item type with its original position
type xmlItem struct {
	Type    string // "files", "registry", "set-env", etc.
	Files   *xmlFiles
	Registry *xmlRegistry
	SetEnv   *xmlSetEnv
	Shortcut *xmlShortcut
	Service  *xmlService
	Exclude  *xmlExclude
	Execute  *xmlExecute
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
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
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
}

type xmlExclude struct {
	Folder string `xml:"folder,attr"`
}

type xmlExecute struct {
	Cmd       string `xml:"cmd,attr"`
	When      string `xml:"when,attr"`
	Directory string `xml:"directory,attr"`
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

			case "execute":
				var exec xmlExecute
				if err := d.DecodeElement(&exec, &t); err != nil {
					return err
				}
				s.Items = append(s.Items, xmlItem{Type: "execute", Execute: &exec})

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

			case "execute":
				var exec xmlExecute
				if err := d.DecodeElement(&exec, &t); err != nil {
					return err
				}
				f.Items = append(f.Items, xmlItem{Type: "execute", Execute: &exec})

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
				Name:  raw.SetEnv.Name,
				Value: raw.SetEnv.Value,
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
			})

		case "exclude":
			items = append(items, ir.Exclude{
				Folder: raw.Exclude.Folder,
			})

		case "execute":
			items = append(items, ir.Execute{
				Cmd:       raw.Execute.Cmd,
				When:      raw.Execute.When,
				Directory: raw.Execute.Directory,
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
