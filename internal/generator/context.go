// Package generator produces WiX XML from the IR representation.
package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// Context holds state during WXS generation.
type Context struct {
	Setup     *ir.Setup
	Variables variables.Dictionary
	WorkDir   string // Directory containing the .msis file

	// ID counters for deterministic generation
	nextDirectoryID int
	nextFileID      int
	nextShortcutID  int
	nextEnvID       int
	nextServiceID   int
	nextFeatureID   int

	// Component tracking (for uniqueness)
	componentIDs map[string]bool

	// Directory trees by root key (INSTALLDIR, APPDATADIR, etc.)
	DirectoryTrees map[string]*Directory

	// Excluded folders (lowercase paths, normalized to source-relative)
	ExcludedFolders map[string]bool

	// Feature component references (for ComponentRef in features)
	FeatureComponents map[string][]string // feature name -> component IDs
}

// NewContext creates a new generation context.
func NewContext(setup *ir.Setup, vars variables.Dictionary, workDir string) *Context {
	return &Context{
		Setup:             setup,
		Variables:         vars,
		WorkDir:           workDir,
		componentIDs:      make(map[string]bool),
		DirectoryTrees:    make(map[string]*Directory),
		ExcludedFolders:   make(map[string]bool),
		FeatureComponents: make(map[string][]string),
	}
}

// Directory represents a directory in the installation tree.
type Directory struct {
	ID             string
	Name           string
	CustomID       string // e.g., "INSTALLDIR"
	Parent         *Directory
	Children       map[string]*Directory // key is lowercase name
	Components     []*Component
	DoNotOverwrite bool
}

// Component represents a WiX component containing files or other resources.
type Component struct {
	ID          string
	GUID        string
	Files       []*File
	Environment *Environment
	Service     *Service
	CreateFolder bool
}

// File represents a file to be installed.
type File struct {
	ID         string
	Name       string
	SourcePath string
	KeyPath    bool
}

// Environment represents an environment variable.
type Environment struct {
	ID    string
	Name  string
	Value string
}

// Service represents a Windows service.
type Service struct {
	ID          string
	Name        string
	DisplayName string
	Description string
	Start       string
	Type        string
	ErrorControl string
	FileName    string
}

// NextDirectoryID returns a unique directory ID.
func (c *Context) NextDirectoryID() string {
	id := fmt.Sprintf("DIR_ID%05d", c.nextDirectoryID)
	c.nextDirectoryID++
	return id
}

// NextFileID returns a unique file ID.
func (c *Context) NextFileID() string {
	id := fmt.Sprintf("FILE_ID%05d", c.nextFileID)
	c.nextFileID++
	return id
}

// NextComponentID returns a unique component ID based on path.
func (c *Context) NextComponentID(path string) string {
	// Generate deterministic ID from path hash
	hash := sha256.Sum256([]byte(path))
	hashStr := hex.EncodeToString(hash[:8])
	baseID := fmt.Sprintf("CID_%s", hashStr)

	// Ensure uniqueness
	id := baseID
	counter := 0
	for c.componentIDs[id] {
		counter++
		id = fmt.Sprintf("%s_%d", baseID, counter)
	}
	c.componentIDs[id] = true
	return id
}

// NextShortcutID returns a unique shortcut ID.
func (c *Context) NextShortcutID() string {
	id := fmt.Sprintf("SHORTCUT_ID%04d", c.nextShortcutID)
	c.nextShortcutID++
	return id
}

// NextEnvID returns a unique environment variable ID.
func (c *Context) NextEnvID() string {
	id := fmt.Sprintf("ENV_ID%04d", c.nextEnvID)
	c.nextEnvID++
	return id
}

// NextServiceID returns a unique service ID.
func (c *Context) NextServiceID() string {
	id := fmt.Sprintf("SVC_ID%04d", c.nextServiceID)
	c.nextServiceID++
	return id
}

// NextFeatureID returns a unique feature ID (matches msis-2.x format).
func (c *Context) NextFeatureID() string {
	id := fmt.Sprintf("FEATURE_%05d", c.nextFeatureID)
	c.nextFeatureID++
	return id
}

// GenerateGUID creates a deterministic GUID from a path.
func GenerateGUID(path string) string {
	hash := sha256.Sum256([]byte(path))
	// Format as GUID: 8-4-4-4-12
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(hash[0:4]),
		hex.EncodeToString(hash[4:6]),
		hex.EncodeToString(hash[6:8]),
		hex.EncodeToString(hash[8:10]),
		hex.EncodeToString(hash[10:16]))
}

// sanitizePath converts a path to a valid WiX ID component.
func sanitizePath(path string) string {
	var sb strings.Builder
	for _, c := range path {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			sb.WriteRune(c)
		} else {
			sb.WriteRune('_')
		}
	}
	result := sb.String()
	// Truncate if too long (MSI has limits)
	if len(result) > 60 {
		result = result[:60]
	}
	return result
}

// GetOrCreateDirectory finds or creates a directory in the tree.
func (c *Context) GetOrCreateDirectory(rootKey string, subPath string, doNotOverwrite bool) *Directory {
	// Get or create root
	root, ok := c.DirectoryTrees[rootKey]
	if !ok {
		// Get the directory name from variables (e.g., INSTALLDIR -> "NG1")
		rootName := c.Variables[rootKey]
		root = &Directory{
			ID:       c.NextDirectoryID(),
			Name:     rootName, // Will be empty if variable not set, which is fine
			CustomID: rootKey,
			Children: make(map[string]*Directory),
		}
		c.DirectoryTrees[rootKey] = root
	}

	if subPath == "" {
		return root
	}

	// Navigate/create path
	current := root
	parts := strings.Split(subPath, "\\")
	for _, part := range parts {
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		child, ok := current.Children[key]
		if !ok {
			child = &Directory{
				ID:             c.NextDirectoryID(),
				Name:           part,
				Parent:         current,
				Children:       make(map[string]*Directory),
				DoNotOverwrite: doNotOverwrite,
			}
			current.Children[key] = child
		}
		current = child
	}
	return current
}

// ParseTarget parses a target like "[INSTALLDIR]subfolder" into rootKey and subPath.
// Handles bracketed form: [INSTALLDIR]path -> rootKey=INSTALLDIR, subPath=path
// Handles bare root keys: INSTALLDIR, APPDATADIR -> rootKey=<name>, subPath=""
func ParseTarget(target string) (rootKey, subPath string) {
	if strings.HasPrefix(target, "[") {
		idx := strings.Index(target, "]")
		if idx > 0 {
			rootKey = target[1:idx]
			subPath = target[idx+1:]
			// Normalize path separators
			subPath = strings.ReplaceAll(subPath, "/", "\\")
			subPath = strings.TrimPrefix(subPath, "\\")
			return rootKey, subPath
		}
	}
	// Check for bare root keys (msis-2.x compatible)
	bareKey := strings.ToUpper(target)
	if bareKey == "INSTALLDIR" || bareKey == "APPDATADIR" || bareKey == "PROGRAMFILESDIR" ||
		bareKey == "COMMONFILESDIR" || bareKey == "WINDOWSDIR" || bareKey == "SYSTEMDIR" {
		return target, ""
	}
	// Default: treat as subpath under INSTALLDIR
	return "INSTALLDIR", target
}

// Generate produces the WXS content for the setup.
func (c *Context) Generate() (*GeneratedOutput, error) {
	// First pass: collect excludes
	c.collectExcludes(c.Setup.Items)
	for _, feature := range c.Setup.Features {
		c.collectExcludesFromFeature(&feature)
	}

	// Second pass: process features and items
	for _, feature := range c.Setup.Features {
		if err := c.processFeature(&feature, ""); err != nil {
			return nil, err
		}
	}

	// Process top-level items
	for _, item := range c.Setup.Items {
		if err := c.processItem(item, nil, ""); err != nil {
			return nil, err
		}
	}

	// Generate output
	output := &GeneratedOutput{
		DirectoryXML: c.generateAllDirectoryXML(),
		FeatureXML:   c.generateAllFeatureXML(),
	}

	return output, nil
}

// GeneratedOutput holds the generated WiX XML fragments.
type GeneratedOutput struct {
	DirectoryXML string
	FeatureXML   string
}

func (c *Context) collectExcludes(items []ir.Item) {
	for _, item := range items {
		if exc, ok := item.(ir.Exclude); ok {
			// Normalize the exclude path
			folder := exc.Folder
			// Convert to forward slashes for consistency, then normalize
			folder = strings.ReplaceAll(folder, "/", "\\")
			// Store both relative and absolute forms for matching
			c.ExcludedFolders[strings.ToLower(folder)] = true
			// Also store the absolute path if it's relative
			if !filepath.IsAbs(folder) {
				absPath := filepath.Join(c.WorkDir, folder)
				c.ExcludedFolders[strings.ToLower(absPath)] = true
			}
		}
	}
}

func (c *Context) collectExcludesFromFeature(feature *ir.Feature) {
	c.collectExcludes(feature.Items)
	for _, sf := range feature.SubFeatures {
		c.collectExcludesFromFeature(&sf)
	}
}

// isExcluded checks if a path should be excluded, matching against both absolute
// and relative forms (relative to basePath or WorkDir).
func (c *Context) isExcluded(path, basePath string) bool {
	lowerPath := strings.ToLower(path)
	// Check absolute path
	if c.ExcludedFolders[lowerPath] {
		return true
	}
	// Check path relative to basePath
	if basePath != "" {
		relPath, err := filepath.Rel(basePath, path)
		if err == nil {
			if c.ExcludedFolders[strings.ToLower(relPath)] {
				return true
			}
		}
	}
	// Check path relative to WorkDir
	relPath, err := filepath.Rel(c.WorkDir, path)
	if err == nil {
		if c.ExcludedFolders[strings.ToLower(relPath)] {
			return true
		}
	}
	return false
}

func (c *Context) processFeature(feature *ir.Feature, parentPath string) error {
	featurePath := feature.Name
	if parentPath != "" {
		featurePath = parentPath + "/" + feature.Name
	}

	// Process items
	for _, item := range feature.Items {
		if err := c.processItem(item, feature, featurePath); err != nil {
			return err
		}
	}

	// Process sub-features
	for _, sf := range feature.SubFeatures {
		if err := c.processFeature(&sf, featurePath); err != nil {
			return err
		}
	}

	return nil
}

func (c *Context) processItem(item ir.Item, feature *ir.Feature, featurePath string) error {
	switch it := item.(type) {
	case ir.Files:
		return c.processFiles(it, feature, featurePath)
	case ir.SetEnv:
		return c.processSetEnv(it, feature, featurePath)
	case ir.Service:
		return c.processService(it, feature, featurePath)
	case ir.Shortcut:
		return c.processShortcut(it, feature, featurePath)
	case ir.Execute:
		return c.processExecute(it, feature, featurePath)
	case ir.Exclude:
		// Already processed in first pass
		return nil
	case ir.Registry:
		// Registry is out of scope for ng1-bmo (Phase 3)
		return nil
	}
	return nil
}

func (c *Context) processFiles(files ir.Files, feature *ir.Feature, featurePath string) error {
	rootKey, subPath := ParseTarget(files.Target)
	dir := c.GetOrCreateDirectory(rootKey, subPath, files.DoNotOverwrite)

	// Resolve source path
	source := files.Source
	if !filepath.IsAbs(source) {
		source = filepath.Join(c.WorkDir, source)
	}

	// Check if source exists
	info, err := os.Stat(source)
	if err != nil {
		// Source doesn't exist - skip silently for dry-run
		return nil
	}

	if info.IsDir() {
		// Enumerate directory recursively
		return c.addDirectoryContents(dir, source, source, featurePath, files.DoNotOverwrite)
	} else {
		// Single file
		return c.addFile(dir, source, info.Name(), featurePath)
	}
}

func (c *Context) addDirectoryContents(dir *Directory, basePath, currentPath, featurePath string, doNotOverwrite bool) error {
	// Check if excluded (check both absolute and relative paths)
	if c.isExcluded(currentPath, basePath) {
		return nil
	}

	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return nil // Skip if can't read
	}

	// Sort entries for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		fullPath := filepath.Join(currentPath, entry.Name())

		if c.isExcluded(fullPath, basePath) {
			continue
		}

		if entry.IsDir() {
			// Create subdirectory
			key := strings.ToLower(entry.Name())
			subDir, ok := dir.Children[key]
			if !ok {
				subDir = &Directory{
					ID:             c.NextDirectoryID(),
					Name:           entry.Name(),
					Parent:         dir,
					Children:       make(map[string]*Directory),
					DoNotOverwrite: doNotOverwrite,
				}
				dir.Children[key] = subDir
			}
			if err := c.addDirectoryContents(subDir, basePath, fullPath, featurePath, doNotOverwrite); err != nil {
				return err
			}
		} else {
			if err := c.addFile(dir, fullPath, entry.Name(), featurePath); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Context) addFile(dir *Directory, sourcePath, fileName, featurePath string) error {
	// Create component for file
	compPath := dir.getFullPath() + "\\" + fileName
	compID := c.NextComponentID(compPath)
	fileID := c.NextFileID()

	comp := &Component{
		ID:   compID,
		GUID: "*", // Auto-generate
		Files: []*File{
			{
				ID:         fileID,
				Name:       fileName,
				SourcePath: sourcePath,
				KeyPath:    true,
			},
		},
	}

	dir.Components = append(dir.Components, comp)

	// Track component for feature
	if featurePath != "" {
		c.FeatureComponents[featurePath] = append(c.FeatureComponents[featurePath], compID)
	}

	return nil
}

func (dir *Directory) getFullPath() string {
	var parts []string
	current := dir
	for current != nil && current.Name != "" {
		parts = append([]string{current.Name}, parts...)
		current = current.Parent
	}
	if len(parts) == 0 {
		return "root"
	}
	return strings.Join(parts, "\\")
}

func (c *Context) processSetEnv(env ir.SetEnv, feature *ir.Feature, featurePath string) error {
	// Environment variables go in INSTALLDIR
	dir := c.GetOrCreateDirectory("INSTALLDIR", "", false)

	envID := c.NextEnvID()
	compID := c.NextComponentID("env_" + env.Name)

	comp := &Component{
		ID:   compID,
		GUID: "*",
		Environment: &Environment{
			ID:    envID,
			Name:  env.Name,
			Value: env.Value,
		},
	}

	dir.Components = append(dir.Components, comp)

	if featurePath != "" {
		c.FeatureComponents[featurePath] = append(c.FeatureComponents[featurePath], compID)
	}

	return nil
}

func (c *Context) processService(svc ir.Service, feature *ir.Feature, featurePath string) error {
	// Services go in INSTALLDIR
	dir := c.GetOrCreateDirectory("INSTALLDIR", "", false)

	svcID := c.NextServiceID()
	compID := c.NextComponentID("svc_" + svc.ServiceName)

	start := "auto"
	if svc.Start != "" {
		start = svc.Start
	}

	comp := &Component{
		ID:   compID,
		GUID: "*",
		Service: &Service{
			ID:          svcID,
			Name:        svc.ServiceName,
			DisplayName: svc.ServiceDisplayName,
			Description: svc.Description,
			Start:       start,
			Type:        svc.ServiceType,
			ErrorControl: svc.ErrorControl,
			FileName:    svc.FileName,
		},
	}

	dir.Components = append(dir.Components, comp)

	if featurePath != "" {
		c.FeatureComponents[featurePath] = append(c.FeatureComponents[featurePath], compID)
	}

	return nil
}

func (c *Context) processShortcut(sc ir.Shortcut, feature *ir.Feature, featurePath string) error {
	// TODO: Implement shortcut generation
	return nil
}

func (c *Context) processExecute(exec ir.Execute, feature *ir.Feature, featurePath string) error {
	// TODO: Implement custom action generation
	return nil
}

func (c *Context) generateAllDirectoryXML() string {
	var sb strings.Builder

	// Sort root keys for deterministic output
	keys := make([]string, 0, len(c.DirectoryTrees))
	for k := range c.DirectoryTrees {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		tree := c.DirectoryTrees[key]
		c.generateDirectoryXML(tree, &sb, 2)
	}

	return sb.String()
}

func (c *Context) generateDirectoryXML(dir *Directory, sb *strings.Builder, depth int) {
	indent := strings.Repeat("    ", depth)

	// Open directory tag
	if dir.CustomID != "" {
		// Root directory with custom ID (e.g., INSTALLDIR)
		if dir.Name != "" {
			sb.WriteString(fmt.Sprintf("%s<Directory Id='%s' Name='%s'>\n", indent, dir.CustomID, dir.Name))
		} else {
			sb.WriteString(fmt.Sprintf("%s<Directory Id='%s'>\n", indent, dir.CustomID))
		}
	} else if dir.Name != "" {
		// Regular subdirectory
		sb.WriteString(fmt.Sprintf("%s<Directory Id='%s' Name='%s'>\n", indent, dir.ID, dir.Name))
	}

	// Generate components
	for _, comp := range dir.Components {
		c.generateComponentXML(comp, sb, depth+1)
	}

	// Sort and generate children
	childKeys := make([]string, 0, len(dir.Children))
	for k := range dir.Children {
		childKeys = append(childKeys, k)
	}
	sort.Strings(childKeys)

	for _, key := range childKeys {
		child := dir.Children[key]
		c.generateDirectoryXML(child, sb, depth+1)
	}

	// Close directory tag
	if dir.Name != "" || dir.CustomID != "" {
		sb.WriteString(fmt.Sprintf("%s</Directory>\n", indent))
	}
}

func (c *Context) generateComponentXML(comp *Component, sb *strings.Builder, depth int) {
	indent := strings.Repeat("    ", depth)

	sb.WriteString(fmt.Sprintf("%s<Component Id='%s' Guid='%s'>\n", indent, comp.ID, comp.GUID))

	// Files
	for _, file := range comp.Files {
		keyPath := ""
		if file.KeyPath {
			keyPath = " KeyPath='yes'"
		}
		sb.WriteString(fmt.Sprintf("%s    <File Id='%s' Name='%s' Source='%s'%s/>\n",
			indent, file.ID, file.Name, file.SourcePath, keyPath))
	}

	// Environment
	if comp.Environment != nil {
		env := comp.Environment
		sb.WriteString(fmt.Sprintf("%s    <Environment Id='%s' Name='%s' Value='%s' Permanent='yes' Part='last' Action='set' System='yes'/>\n",
			indent, env.ID, env.Name, env.Value))
	}

	// Service
	if comp.Service != nil {
		svc := comp.Service
		startType := "auto"
		switch strings.ToLower(svc.Start) {
		case "auto":
			startType = "auto"
		case "demand", "manual":
			startType = "demand"
		case "disabled":
			startType = "disabled"
		}

		sb.WriteString(fmt.Sprintf("%s    <ServiceInstall Id='%s' Name='%s' DisplayName='%s' Start='%s' Type='ownProcess' ErrorControl='normal'>\n",
			indent, svc.ID, svc.Name, svc.DisplayName, startType))
		if svc.Description != "" {
			sb.WriteString(fmt.Sprintf("%s        <Description>%s</Description>\n", indent, svc.Description))
		}
		sb.WriteString(fmt.Sprintf("%s    </ServiceInstall>\n", indent))
		sb.WriteString(fmt.Sprintf("%s    <ServiceControl Id='%s_ctrl' Name='%s' Start='install' Stop='both' Remove='uninstall' Wait='yes'/>\n",
			indent, svc.ID, svc.Name))
	}

	// CreateFolder for empty directories
	if comp.CreateFolder {
		sb.WriteString(fmt.Sprintf("%s    <CreateFolder/>\n", indent))
	}

	sb.WriteString(fmt.Sprintf("%s</Component>\n", indent))
}

func (c *Context) generateAllFeatureXML() string {
	var sb strings.Builder

	for _, feature := range c.Setup.Features {
		c.generateFeatureXML(&feature, &sb, 2, "")
	}

	return sb.String()
}

func (c *Context) generateFeatureXML(feature *ir.Feature, sb *strings.Builder, depth int, parentPath string) {
	indent := strings.Repeat("    ", depth)

	featurePath := feature.Name
	if parentPath != "" {
		featurePath = parentPath + "/" + feature.Name
	}

	// Feature attributes - use unique generated ID (msis-2.x compatible)
	featureID := c.NextFeatureID()

	// Level: 1 for enabled, 32767 for disabled (msis-2.x compatible)
	level := "1"
	if !feature.Enabled {
		level = "32767"
	}

	allowAbsent := "yes"
	if !feature.Allowed {
		allowAbsent = "no"
	}

	sb.WriteString(fmt.Sprintf("%s<Feature Id='%s' Title='%s' Level='%s' AllowAbsent='%s'>\n",
		indent, featureID, feature.Name, level, allowAbsent))

	// Component refs
	if compIDs, ok := c.FeatureComponents[featurePath]; ok {
		for _, compID := range compIDs {
			sb.WriteString(fmt.Sprintf("%s    <ComponentRef Id='%s'/>\n", indent, compID))
		}
	}

	// Sub-features
	for _, sf := range feature.SubFeatures {
		c.generateFeatureXML(&sf, sb, depth+1, featurePath)
	}

	sb.WriteString(fmt.Sprintf("%s</Feature>\n", indent))
}
