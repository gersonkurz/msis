// Package generator produces WiX XML from the IR representation.
package generator

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/registry"
	"github.com/gersonkurz/msis/internal/requirements"
	"github.com/gersonkurz/msis/internal/variables"
)

// Context holds state during WXS generation.
type Context struct {
	Setup     *ir.Setup
	Variables variables.Dictionary
	WorkDir   string // Directory containing the .msis file

	// Strict refuses what is deprecated instead of warning about it (/STRICT): today the #77
	// layout, a service sharing its executable with another feature (D22).
	Strict bool

	// warnings collects build-time diagnostics, surfaced on GeneratedOutput and
	// printed by main.go.
	warnings []string

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

	// Feature IDs - maps feature index path to generated ID
	// Index path is built using feature position in parent (e.g., "0/1/0")
	featureIDs map[string]string

	// Feature names as written, by feature ID - for error messages
	featureNames map[string]string

	// Feature component references (keyed by unique feature ID, not name)
	FeatureComponents map[string][]string // feature ID -> component IDs

	// Target file tracking for duplicate detection (key: "dirID:lowercaseFilename")
	// Value is the count of files targeting this location
	targetFileSeen map[string]int

	// File source paths by installed filename (key: lowercase filename, value: source path)
	// Used to find source paths for service executables
	fileSourcePaths map[string]string

	// File components by installed filename (key: lowercase filename, value: component)
	// Used to attach service definitions to existing file components
	fileComponents map[string]*Component

	// Every file component, grouped by the source path it was built from. A source used
	// more than once needs its components' GUIDs qualified by where each one installs to;
	// see resolveDuplicateSourceGUIDs.
	fileComponentsBySource map[string][]placedComponent

	// Registry processor and components
	registryProcessor  *registry.Processor
	RegistryComponents []*registry.Component

	// Shortcut components by target folder
	DesktopShortcuts   []*ShortcutComponent
	StartMenuShortcuts []*ShortcutComponent

	// Custom actions
	CustomActions []*CustomAction
	nextActionID  int

	// Remove on uninstall items
	RemoveOnUninstallItems []*RemoveOnUninstallItem
	nextRemoveID           int
}

// RemoveOnUninstallItem represents an item to remove during uninstall.
type RemoveOnUninstallItem struct {
	ID        string
	Registry  string // e.g., "HKLM\Software\MyCompany\MyApp"
	Folder    string // e.g., "[APPDATADIR]MyCompany\MyApp"
	FeatureID string // feature this component belongs to
}

// CustomAction represents a WiX custom action.
type CustomAction struct {
	ID          string
	Command     string
	Directory   string
	When        string // before-install, after-install, before-uninstall, etc.
	FailOnError bool   // true → Return='check' (non-zero exit fails the install)
	Quiet       string // "no" (visible ExeCommand), "yes" (hidden via WixQuietExec), "auto" (hidden only when UILevel<=3)
}

// ShortcutComponent represents a WiX component containing a shortcut.
type ShortcutComponent struct {
	ID       string
	GUID     string
	Shortcut *Shortcut
}

// NewContext creates a new generation context.
func NewContext(setup *ir.Setup, vars variables.Dictionary, workDir string) *Context {
	return &Context{
		Setup:                  setup,
		Variables:              vars,
		WorkDir:                workDir,
		componentIDs:           make(map[string]bool),
		DirectoryTrees:         make(map[string]*Directory),
		ExcludedFolders:        make(map[string]bool),
		featureIDs:             make(map[string]string),
		featureNames:           make(map[string]string),
		FeatureComponents:      make(map[string][]string),
		targetFileSeen:         make(map[string]int),
		fileSourcePaths:        make(map[string]string),
		fileComponents:         make(map[string]*Component),
		fileComponentsBySource: make(map[string][]placedComponent),
		registryProcessor:      newRegistryProcessor(workDir, vars),
		RegistryComponents:     make([]*registry.Component, 0),
		DesktopShortcuts:       make([]*ShortcutComponent, 0),
		StartMenuShortcuts:     make([]*ShortcutComponent, 0),
		CustomActions:          make([]*CustomAction, 0),
		RemoveOnUninstallItems: make([]*RemoveOnUninstallItem, 0),
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
	FeatureIDs     map[string]bool // Features that use this directory (for permission component refs)
}

// Component represents a WiX component containing files or other resources.
type Component struct {
	ID           string
	GUID         string
	Files        []*File
	Environment  *Environment
	Service      *Service
	CreateFolder bool
}

// File represents a file to be installed.
type File struct {
	ID         string
	Name       string
	ShortName  string // 8.3 format, only set for duplicate targets to avoid collision
	SourcePath string
	KeyPath    bool
}

// Environment represents an environment variable.
type Environment struct {
	ID        string
	Name      string
	Value     string
	Part      string // "all" (replace) or "last" (append); default "all"
	Permanent bool   // if true, env var survives uninstall
}

// Service represents a Windows service.
type Service struct {
	ID                string
	Name              string
	DisplayName       string
	Description       string
	Start             string
	Type              string
	ErrorControl      string
	Restart           bool // restart on failure, as msis-2.x's restart="yes"
	FileName          string
	StartAfterInstall bool // true (default): start service on install
}

// Shortcut represents a shortcut (Desktop or StartMenu).
type Shortcut struct {
	ID          string
	Name        string
	Description string
	Target      string // File path to execute (e.g., "[INSTALLDIR]app.exe")
	Icon        string // Optional icon path
	WorkingDir  string // Working directory ID (e.g., "INSTALLDIR")
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

// productScopedID returns a path string prefixed with the product's UpgradeCode,
// ensuring different products generate unique component GUIDs for non-file components.
// Without this, components like permission CreateFolder and PATH environment entries
// would get identical GUIDs across all MSIS-built products, causing MSI "shared component"
// conflicts that prevent proper directory cleanup during uninstall.
func (c *Context) productScopedID(path string) string {
	upgradeCode := c.Variables.UpgradeCode()
	if upgradeCode != "" {
		return upgradeCode + "/" + path
	}
	return path
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

// generateShortName creates a valid 8.3 short name for duplicate target files.
// Format: XXXXX_N.EXT where XXXXX is max 5 chars from base name,
// N is the occurrence number, and EXT is max 3 chars from extension.
// Uses _N instead of ~N to avoid WIX1044 ambiguous short name warning.
func generateShortName(fileName string, occurrence int) string {
	// Split into base and extension
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)

	// Clean extension (remove leading dot, truncate to 3 chars, uppercase)
	ext = strings.TrimPrefix(ext, ".")
	if len(ext) > 3 {
		ext = ext[:3]
	}
	ext = strings.ToUpper(ext)

	// Determine max base length based on occurrence number digits
	// Total must be <= 8 chars: base + "_" + digits
	occStr := fmt.Sprintf("%d", occurrence)
	maxBaseLen := 8 - 1 - len(occStr) // 8 - underscore - digits
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}

	// Clean base name: keep only alphanumeric and underscore, truncate, uppercase
	var cleanBase strings.Builder
	for _, c := range strings.ToUpper(base) {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			cleanBase.WriteRune(c)
			if cleanBase.Len() >= maxBaseLen {
				break
			}
		}
	}
	baseStr := cleanBase.String()
	if baseStr == "" {
		baseStr = "FILE"[:maxBaseLen]
	}

	// Format: BASE_N.EXT (no tilde to avoid WIX1044)
	if ext != "" {
		return fmt.Sprintf("%s_%s.%s", baseStr, occStr, ext)
	}
	return fmt.Sprintf("%s_%s", baseStr, occStr)
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
		// Get the directory name from variables (e.g., INSTALLDIR -> "MyApp" or "Company\MyApp")
		// Also check INSTALL_FOLDER as an alias for INSTALLDIR
		rootName := c.Variables[rootKey]
		if rootName == "" && rootKey == "INSTALLDIR" {
			rootName = c.Variables["INSTALL_FOLDER"]
		}

		// For appdata-like roots, fall back to INSTALLDIR value when not explicitly set.
		// This matches C# msis-2.x behavior: users expect [APPDATADIR] to resolve to
		// C:\ProgramData\<AppName>, not C:\ProgramData directly.
		if rootName == "" {
			switch rootKey {
			case "APPDATADIR", "ROAMINGAPPDATADIR", "LOCALAPPDATADIR":
				rootName = c.Variables["INSTALLDIR"]
				if rootName == "" {
					rootName = c.Variables["INSTALL_FOLDER"]
				}
			}
		}

		// Handle nested paths like "NGBT\chimera" - need to create parent directories
		// and put the custom ID (INSTALLDIR) on the final directory
		if strings.Contains(rootName, "\\") {
			parts := strings.Split(rootName, "\\")
			// Create the first part as root (without custom ID)
			root = &Directory{
				ID:         c.NextDirectoryID(),
				Name:       parts[0],
				Children:   make(map[string]*Directory),
				FeatureIDs: make(map[string]bool),
			}
			c.DirectoryTrees[rootKey] = root

			// Create intermediate directories, putting custom ID on the last one
			current := root
			for i := 1; i < len(parts); i++ {
				isLast := i == len(parts)-1
				child := &Directory{
					ID:         c.NextDirectoryID(),
					Name:       parts[i],
					Parent:     current,
					Children:   make(map[string]*Directory),
					FeatureIDs: make(map[string]bool),
				}
				if isLast {
					child.CustomID = rootKey // Put INSTALLDIR on the final directory
				}
				current.Children[strings.ToLower(parts[i])] = child
				current = child
			}
		} else {
			root = &Directory{
				ID:         c.NextDirectoryID(),
				Name:       rootName, // Will be empty if variable not set, which is fine
				CustomID:   rootKey,
				Children:   make(map[string]*Directory),
				FeatureIDs: make(map[string]bool),
			}
			c.DirectoryTrees[rootKey] = root
		}
	}

	if subPath == "" {
		// For nested INSTALLDIR paths, find the directory with the custom ID
		return c.findDirectoryWithCustomID(root, rootKey)
	}

	// Navigate/create path from the CustomID directory (not the tree root).
	// For nested roots like "NGBT\chimera", the tree root is NGBT but the
	// CustomID (e.g., APPDATADIR) is on the child "chimera". Subpaths must
	// be created under the CustomID directory, not as siblings of it.
	current := c.findDirectoryWithCustomID(root, rootKey)
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
				FeatureIDs:     make(map[string]bool),
			}
			current.Children[key] = child
		}
		current = child
	}
	return current
}

// findDirectoryWithCustomID recursively finds a directory with the given custom ID.
// If root has the custom ID, returns root. Otherwise searches children.
func (c *Context) findDirectoryWithCustomID(dir *Directory, customID string) *Directory {
	if dir.CustomID == customID {
		return dir
	}
	// In a defined order (#73): today each level above the CustomID directory has one child, but
	// with siblings a map range would make the result random.
	for _, name := range slices.Sorted(maps.Keys(dir.Children)) {
		if found := c.findDirectoryWithCustomID(dir.Children[name], customID); found != nil {
			return found
		}
	}
	// Fallback to root if not found (shouldn't happen)
	return dir
}

// ParseTarget parses a target like "[INSTALLDIR]subfolder" into rootKey and subPath.
// Handles bracketed form: [INSTALLDIR]path -> rootKey=INSTALLDIR, subPath=path
// Handles bare root keys: INSTALLDIR, APPDATADIR -> rootKey=<name>, subPath=""
// Handles root keys with path: INSTALLDIR\subdir -> rootKey=INSTALLDIR, subPath=subdir
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

	// Normalize path separators first
	target = strings.ReplaceAll(target, "/", "\\")

	// Check for root keys (with optional path suffix)
	rootKeys := []string{"INSTALLDIR", "APPDATADIR", "COMMONFILESDIR", "WINDOWSDIR",
		"SYSTEMDIR", "ROAMINGAPPDATADIR", "LOCALAPPDATADIR"}

	for _, rk := range rootKeys {
		if strings.HasPrefix(strings.ToUpper(target), rk) {
			if len(target) == len(rk) {
				// Exact match: "INSTALLDIR"
				return rk, ""
			}
			if target[len(rk)] == '\\' {
				// Root key with path: "INSTALLDIR\subdir"
				return rk, target[len(rk)+1:]
			}
		}
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

	// Second pass: pre-assign feature IDs (ensures consistency between processing and generation)
	for i := range c.Setup.Features {
		c.assignFeatureIDs(&c.Setup.Features[i], "", i)
	}

	// Third pass: process features and items
	for i, feature := range c.Setup.Features {
		if err := c.processFeature(&feature, "", i); err != nil {
			return nil, err
		}
	}

	// Process items written directly under <setup> rather than inside a <feature>.
	//
	// They are given a real feature id whenever the package declares features of its
	// own, so every generator associates their components exactly as it does for a
	// feature's items — each already guards on `featureID != ""`, in eleven places.
	// Without an id those components were emitted and never referenced, and WiX failed
	// the build with "error WIX0267: Found orphaned Component" (issue #15).
	//
	// The empty id is kept when the package declares NO features, because WiX then
	// invents a default feature and adopts the loose components itself. That path works
	// today, so it is left exactly as it is rather than given a feature of our own —
	// which also avoids changing the feature identity of packages already in the field.
	topLevelFeatureID := ""
	if len(c.Setup.Features) > 0 {
		topLevelFeatureID = packageItemsFeatureID
	}
	for _, item := range c.Setup.Items {
		if err := c.processItem(item, topLevelFeatureID); err != nil {
			return nil, err
		}
	}

	// Handle ADD_TO_PATH variable - adds INSTALLDIR to system PATH
	if c.Variables.GetBool("ADD_TO_PATH") && len(c.Setup.Features) > 0 {
		// Get the first feature's ID to associate the PATH component
		firstFeatureID := c.featureIDs["0"]
		c.addPathEnvironment(firstFeatureID)
	}

	if err := c.checkServiceFileOwnership(); err != nil {
		return nil, err
	}

	// All file components exist by now, so a source used in more than one place can be
	// given per-target GUIDs before anything is rendered (issue #21).
	c.resolveDuplicateSourceGUIDs()

	// Generate launch conditions for requirements
	launchSearchXML, launchCondXML := c.generateLaunchConditions()

	// Generate remove-on-uninstall XML first, as it registers components with features
	removeOnUninstallXML := c.generateRemoveOnUninstallXML()

	// Build preserved IDs for registry components (needed by both preservation and registry XML)
	preservedIDs := c.registryProcessor.BuildAllPreservedIDs(c.RegistryComponents)

	// Generate output
	output := &GeneratedOutput{
		DirectoryXML:              c.generateDirectoryXMLForRoot("INSTALLDIR"),
		AppDataDirXML:             c.generateDirectoryXMLForRoot("APPDATADIR"),
		RoamingAppDataDirXML:      c.generateDirectoryXMLForRoot("ROAMINGAPPDATADIR"),
		LocalAppDataDirXML:        c.generateDirectoryXMLForRoot("LOCALAPPDATADIR"),
		CommonFilesDirXML:         c.generateDirectoryXMLForRoot("COMMONFILESDIR"),
		WindowsDirXML:             c.generateDirectoryXMLForRoot("WINDOWSDIR"),
		SystemDirXML:              c.generateDirectoryXMLForRoot("SYSTEMDIR"),
		FeatureXML:                c.generateAllFeatureXML(),
		RegistryXML:               c.generateAllRegistryXML(preservedIDs),
		DesktopXML:                c.generateShortcutsXML(c.DesktopShortcuts),
		StartMenuXML:              c.generateShortcutsXML(c.StartMenuShortcuts),
		CustomActionsXML:          c.generateCustomActionsXML(),
		InstallExecuteSequence:    c.generateInstallExecuteSequence(),
		RemoveOnUninstallXML:      removeOnUninstallXML,
		LaunchConditionSearchXML:  launchSearchXML,
		LaunchConditionsXML:       launchCondXML,
		PreservationPropertiesXML: c.registryProcessor.GeneratePreservationXML(c.RegistryComponents, preservedIDs),

		// The registry processor's diagnostics are gathered while parsing .reg files,
		// so they are collected here rather than reported from inside that package.
		Warnings: append(c.warnings, c.registryProcessor.Warnings()...),
	}

	return output, nil
}

// generateLaunchConditions creates WiX XML for runtime requirement checks.
func (c *Context) generateLaunchConditions() (searchXML, conditionsXML string) {
	if len(c.Setup.Requires) == 0 {
		return "", ""
	}

	// Determine architecture from PLATFORM variable
	arch := "x64" // Default
	platform := c.Variables.Platform()
	if strings.EqualFold(platform, "x86") {
		arch = "x86"
	} else if strings.EqualFold(platform, "arm64") {
		arch = "arm64"
	}

	// Generate launch conditions
	conditions := requirements.GenerateLaunchConditions(c.Setup.Requires, arch)
	return requirements.GenerateXML(conditions)
}

// GeneratedOutput holds the generated WiX XML fragments.
type GeneratedOutput struct {
	DirectoryXML              string // INSTALLDIR tree (under ProgramFilesFolder)
	AppDataDirXML             string // APPDATADIR tree (under CommonAppDataFolder - C:\ProgramData)
	RoamingAppDataDirXML      string // ROAMINGAPPDATADIR tree (under AppDataFolder - %APPDATA%)
	LocalAppDataDirXML        string // LOCALAPPDATADIR tree (under LocalAppDataFolder - %LOCALAPPDATA%)
	CommonFilesDirXML         string // COMMONFILESDIR tree (under CommonFilesFolder)
	WindowsDirXML             string // WINDOWSDIR tree (under WindowsFolder)
	SystemDirXML              string // SYSTEMDIR tree (under SystemFolder)
	FeatureXML                string
	RegistryXML               string
	DesktopXML                string
	StartMenuXML              string
	CustomActionsXML          string
	InstallExecuteSequence    string
	RemoveOnUninstallXML      string
	LaunchConditionSearchXML  string // Registry searches for launch conditions
	LaunchConditionsXML       string // Launch condition elements
	PreservationPropertiesXML string // Property+RegistrySearch elements for preserve="yes"

	// Warnings are build-time diagnostics about values msis had to alter, or that
	// Windows Installer will reinterpret. Printed by main.go; see Context.warn and
	// registry.Processor.Warnings.
	Warnings []string
}

// resolveOrWarn expands {{VAR}} references in an item value, reporting a failure
// instead of discarding it.
//
// These call sites used to be `if resolved, err := Resolve(v); err == nil`, which drops
// the error and keeps the original text. A form msis does not support — "{{VAR, DEFAULT}}"
// is the one seen in the wild — then reaches the built package verbatim, as an
// environment variable's value or a source path, with nothing said at build time.
//
// Not an error: keeping the original text is the established behaviour, and failing the
// build on it would break packages that already ship. The author is now told.
func (c *Context) resolveOrWarn(value, what string) string {
	resolved, err := c.Variables.Resolve(value)
	if err != nil {
		c.warn("%s could not be resolved and is used as written: %q (%s)", what, value, oneLine(err.Error()))
		return value
	}
	return resolved
}

// oneLine collapses whitespace so an embedded multi-line message stays on one line.
// The template engine's parse errors span three lines; printed as-is, the continuations
// lose the "Warning:" prefix and its colouring, and the output stops being one
// diagnostic per line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (c *Context) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
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

// assignFeatureIDs pre-assigns unique IDs to features using index-based paths.
// This ensures the same IDs are used during both item processing and XML generation.
func (c *Context) assignFeatureIDs(feature *ir.Feature, parentIndexPath string, index int) {
	// Build index path using position (not name) to avoid collisions
	indexPath := fmt.Sprintf("%d", index)
	if parentIndexPath != "" {
		indexPath = parentIndexPath + "/" + indexPath
	}

	// Generate and store unique ID for this feature
	featureID := c.NextFeatureID()
	c.featureIDs[indexPath] = featureID
	c.featureNames[featureID] = feature.Name

	// Process sub-features
	for i := range feature.SubFeatures {
		c.assignFeatureIDs(&feature.SubFeatures[i], indexPath, i)
	}
}

// isExcluded checks if a path should be excluded, matching against both absolute
// and relative forms (relative to basePath or WorkDir). Exclude entries are stored
// with forward slashes normalized to backslashes by collectExcludes, so inputs are
// normalized the same way here.
func (c *Context) isExcluded(path, basePath string) bool {
	normalize := func(p string) string {
		return strings.ToLower(strings.ReplaceAll(p, "/", "\\"))
	}
	// Check absolute path
	if c.ExcludedFolders[normalize(path)] {
		return true
	}
	// Check path relative to basePath
	if basePath != "" {
		relPath, err := filepath.Rel(basePath, path)
		if err == nil {
			if c.ExcludedFolders[normalize(relPath)] {
				return true
			}
		}
	}
	// Check path relative to WorkDir
	relPath, err := filepath.Rel(c.WorkDir, path)
	if err == nil {
		if c.ExcludedFolders[normalize(relPath)] {
			return true
		}
	}
	return false
}

func (c *Context) processFeature(feature *ir.Feature, parentIndexPath string, index int) error {
	// Build index path (matches assignFeatureIDs)
	indexPath := fmt.Sprintf("%d", index)
	if parentIndexPath != "" {
		indexPath = parentIndexPath + "/" + indexPath
	}

	// Get the pre-assigned feature ID
	featureID := c.featureIDs[indexPath]

	// Process items
	for _, item := range feature.Items {
		if err := c.processItem(item, featureID); err != nil {
			return err
		}
	}

	// Process sub-features
	for i := range feature.SubFeatures {
		if err := c.processFeature(&feature.SubFeatures[i], indexPath, i); err != nil {
			return err
		}
	}

	return nil
}

func (c *Context) processItem(item ir.Item, featureID string) error {
	switch it := item.(type) {
	case ir.Files:
		return c.processFiles(it, featureID)
	case ir.SetEnv:
		return c.processSetEnv(it, featureID)
	case ir.Service:
		return c.processService(it, featureID)
	case ir.Shortcut:
		return c.processShortcut(it, featureID)
	case ir.Execute:
		return c.processExecute(it, featureID)
	case ir.Exclude:
		// Already processed in first pass
		return nil
	case ir.Registry:
		return c.processRegistry(it, featureID)
	case ir.CreateFolder:
		return c.processCreateFolder(it, featureID)
	case ir.RemoveOnUninstall:
		return c.processRemoveOnUninstall(it, featureID)
	}
	return nil
}

func (c *Context) processFiles(files ir.Files, featureID string) error {
	rootKey, subPath := ParseTarget(files.Target)

	// Source path as specified in .msis (relative to .msis file directory)
	// Resolve any {{VAR}} references in the source path
	source := files.Source
	source = c.resolveOrWarn(source, "source path of <files>")

	// Resolve to absolute for existence check
	absSource := source
	if !filepath.IsAbs(source) {
		absSource = filepath.Join(c.WorkDir, source)
	}

	// A source that is not there used to be skipped silently: the directory was created, the
	// payload was not, and both msis and wix build reported success. A mistyped path therefore
	// shipped a package missing the file, and nothing said so (issue #24). msis-2.x reports
	// "ERROR, '%s' is not a valid directory" and then fails, so failing here is also what the
	// reference implementation does.
	info, err := os.Stat(absSource)
	if err != nil {
		// %s, not %q: these are Windows paths, and %q doubles every backslash, so the user
		// is shown a path they did not write and cannot copy back into their .msis.
		if strings.ContainsAny(source, "*?") {
			return fmt.Errorf(`<files source="%s">: wildcards are not supported - name the `+
				`directory itself and its contents are installed recursively`, files.Source)
		}
		if os.IsNotExist(err) {
			return fmt.Errorf(`<files source="%s">: no such file or directory (looked in %s)`,
				files.Source, absSource)
		}
		// Anything else - a permission problem, an I/O error, a path too long - is reported as
		// itself rather than flattened into "not found", which would send the user hunting for
		// a typo that is not there.
		return fmt.Errorf(`<files source="%s">: %w`, files.Source, err)
	}

	if info.IsDir() {
		// Directory: subPath is always a directory path
		dir := c.GetOrCreateDirectory(rootKey, subPath, files.DoNotOverwrite)
		// Enumerate directory recursively
		// Use relative source for WXS paths, absolute for file enumeration
		return c.addDirectoryContents(dir, source, absSource, featureID, files.DoNotOverwrite)
	} else {
		// Single file: check if subPath ends with a filename (rename operation)
		// If subPath has an extension, treat it as a file rename
		targetFileName := info.Name() // Default: use source filename
		dirPath := subPath

		if subPath != "" {
			// msis target paths are Windows-style; split on '\' regardless of host OS.
			lastSep := strings.LastIndexByte(subPath, '\\')
			lastComponent := subPath
			if lastSep >= 0 {
				lastComponent = subPath[lastSep+1:]
			}
			// Check if last component looks like a filename (has an extension)
			if ext := filepath.Ext(lastComponent); ext != "" {
				// It's a rename: extract directory path and target filename
				if lastSep >= 0 {
					dirPath = subPath[:lastSep]
				} else {
					dirPath = ""
				}
				targetFileName = lastComponent
			}
		}

		dir := c.GetOrCreateDirectory(rootKey, dirPath, files.DoNotOverwrite)
		return c.addFile(dir, source, targetFileName, featureID)
	}
}

// addDirectoryContents recursively adds files from a directory.
// relBasePath is the relative path for WXS output (e.g., "install")
// absCurrentPath is the absolute path for file enumeration
func (c *Context) addDirectoryContents(dir *Directory, relBasePath, absCurrentPath, featureID string, doNotOverwrite bool) error {
	// Check if excluded (check both absolute and relative paths)
	if c.isExcluded(absCurrentPath, relBasePath) {
		return nil
	}

	// A directory that cannot be enumerated used to be skipped in silence, at every level of
	// the walk, so one unreadable subdirectory dropped its whole subtree while the rest of the
	// package looked normal and the build reported success (issue #25). The source is present
	// here - #24's check already rejected the ones that are not - so a failure now is a
	// permission problem, an I/O error, or a directory that went away mid-build, and none of
	// those should produce a package quietly missing files.
	//
	// The excluded-folder check above is the deliberate skip; this one never was.
	entries, err := os.ReadDir(absCurrentPath)
	if err != nil {
		return fmt.Errorf("reading source directory %s: %w", absCurrentPath, err)
	}

	// Sort entries for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	// Calculate relative path from base for WXS output
	absBasePath := relBasePath
	if !filepath.IsAbs(relBasePath) {
		absBasePath = filepath.Join(c.WorkDir, relBasePath)
	}
	relPath, _ := filepath.Rel(absBasePath, absCurrentPath)

	for _, entry := range entries {
		absFullPath := filepath.Join(absCurrentPath, entry.Name())

		if c.isExcluded(absFullPath, relBasePath) {
			continue
		}

		// Compute relative path for WXS Source attribute
		var wxsSourcePath string
		if relPath == "." || relPath == "" {
			wxsSourcePath = filepath.Join(relBasePath, entry.Name())
		} else {
			wxsSourcePath = filepath.Join(relBasePath, relPath, entry.Name())
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
					FeatureIDs:     make(map[string]bool),
				}
				dir.Children[key] = subDir
			}
			// Mark directory with feature so permission components are associated
			if featureID != "" {
				c.markDirectoryFeature(subDir, featureID)
			}
			if err := c.addDirectoryContents(subDir, relBasePath, absFullPath, featureID, doNotOverwrite); err != nil {
				return err
			}
		} else {
			if err := c.addFile(dir, wxsSourcePath, entry.Name(), featureID); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Context) addFile(dir *Directory, sourcePath, fileName, featureID string) error {
	// Create component for file
	// Use source path in component ID to handle feature-based file overrides
	// (same target file from different sources in different features)
	compPath := sourcePath // Use source path for uniqueness
	compID := c.NextComponentID(compPath)
	fileID := c.NextFileID()

	// Generate explicit GUID from source path to ensure uniqueness
	// even when multiple features install different versions of the same target file
	guid := GenerateGUID(sourcePath)

	// Track target file for duplicate detection
	// Key is dirID:lowercaseFilename to identify the target location
	targetKey := dir.ID + ":" + strings.ToLower(fileName)
	c.targetFileSeen[targetKey]++
	occurrence := c.targetFileSeen[targetKey]

	// Track source path and component by filename for service executable lookup
	// First occurrence wins (in case of duplicates)
	fileKey := strings.ToLower(fileName)
	if _, exists := c.fileSourcePaths[fileKey]; !exists {
		c.fileSourcePaths[fileKey] = sourcePath
	}

	// Generate ShortName only for duplicates (2nd occurrence and beyond)
	var shortName string
	if occurrence > 1 {
		shortName = generateShortName(fileName, occurrence)
	}

	comp := &Component{
		ID:   compID,
		GUID: guid,
		Files: []*File{
			{
				ID:         fileID,
				Name:       fileName,
				ShortName:  shortName,
				SourcePath: sourcePath,
				KeyPath:    true,
			},
		},
	}

	c.addComponentToDirectory(dir, comp, featureID)

	// Remember where this source landed; resolveDuplicateSourceGUIDs re-keys the GUIDs of
	// any source that ends up installed to more than one place.
	c.fileComponentsBySource[sourcePath] = append(c.fileComponentsBySource[sourcePath],
		placedComponent{comp: comp, dir: dir})

	// Track component by filename so services can attach to it
	if _, exists := c.fileComponents[fileKey]; !exists {
		c.fileComponents[fileKey] = comp
	}

	// Track component for feature (keyed by unique feature ID)
	if featureID != "" {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}

	return nil
}

// placedComponent is a file component together with the directory it installs into, which
// is what distinguishes two components built from the same source file.
type placedComponent struct {
	comp *Component
	dir  *Directory
}

// installedAt names the destination that gives this component its identity: the directory
// plus the installed file name.
//
// The file name is part of it because <files target="[INSTALLDIR]other.txt"> renames on
// install, so one source can legitimately land twice in ONE directory under two names. Keying
// on the directory alone gave those two components the same GUID and left them failing with
// WIX0369 - the very defect this fixes, in a shape the first version missed.
//
// The whole thing is case-folded because directory lookup is case-insensitive and keeps
// whichever spelling it saw first: declaring [INSTALLDIR]Data\one before [INSTALLDIR]data	wo
// makes the shared parent "Data", and declaring them the other way round makes it "data".
// Hashing the raw name would make identity depend on the order the elements are written in,
// which is exactly what this design exists to avoid.
func (p placedComponent) installedAt() string {
	path := targetPathOf(p.dir)
	if len(p.comp.Files) > 0 {
		path += "\\" + p.comp.Files[0].Name
	}
	return strings.ToLower(path)
}

// resolveDuplicateSourceGUIDs gives a distinct GUID to each component built from a source
// file that the package installs to more than one place.
//
// addFile derives a component's GUID from its source path alone, so listing one source
// against two targets produced two components with the same GUID and `wix build` rejected
// the package outright (issue #21):
//
//	error WIX0369: Component/@Id='CID_..._1' ... has a @Guid value '{...}' that duplicates
//	another component in this package.
//
// Because that is a hard error and msis builds the whole wix command line itself - there is
// no way to suppress it - no package that builds today contains a repeated source path.
// Every such package therefore keeps exactly the GUIDs it has: this rewrites nothing unless
// a source appears more than once, which until now could not ship.
//
// Where it does apply, ALL of that source's components are re-keyed, not just the second
// one. Leaving the first on the old scheme would make the identities depend on the order the
// <files> elements happen to be written in, so reordering two lines would swap two GUIDs.
// Keying every one of them on source plus target makes a component's identity a function of
// what it installs and where, which is what it should have been.
func (c *Context) resolveDuplicateSourceGUIDs() {
	for _, source := range sortedKeysOf(c.fileComponentsBySource) {
		placed := c.fileComponentsBySource[source]
		if len(placed) < 2 {
			continue
		}
		for _, p := range placed {
			p.comp.GUID = GenerateGUID(source + "|" + p.installedAt())
		}
	}
}

// targetPathOf names the install location of a directory, as the root key the user wrote
// plus the subpath below it: "INSTALLDIR", "APPDATADIR\MyApp\data".
//
// The walk stops at the directory carrying the root's CustomID rather than continuing to the
// top of the tree. Above that point the names come from the INSTALLDIR variable - for a
// nested value like "NGBT\chimera" the tree root is "NGBT" and the CustomID sits on
// "chimera" - and including them would tie component identity to the install folder's name,
// so renaming it would move every duplicated component.
func targetPathOf(dir *Directory) string {
	var parts []string
	for d := dir; d != nil; d = d.Parent {
		if d.CustomID != "" {
			parts = append(parts, d.CustomID)
			break
		}
		if d.Parent == nil {
			parts = append(parts, d.ID) // no root key found; the id is at least stable
			break
		}
		parts = append(parts, d.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\\")
}

func sortedKeysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// addComponentToDirectory places a component in a directory and records which feature
// owns that directory.
//
// The two belong together. They used to be separate steps, and the second was only
// taken on the file paths, so a feature holding no <files> left INSTALLDIR owned by no
// feature — and the permission component generated for it got no ComponentRef, failing
// the build with "error WIX0267: Found orphaned Component" (issue #18). A package whose
// feature contained only a <set-env> could not be built at all.
//
// An empty featureID means the package declares no features of its own; WiX then
// invents a default feature and adopts the loose components, so there is nothing to
// mark. See Generate, which assigns a real id to top-level items whenever features exist.
func (c *Context) addComponentToDirectory(dir *Directory, comp *Component, featureID string) {
	dir.Components = append(dir.Components, comp)
	if featureID != "" {
		c.markDirectoryFeature(dir, featureID)
	}
}

// markDirectoryFeature marks a directory and all its ancestors as owned by a feature.
func (c *Context) markDirectoryFeature(dir *Directory, featureID string) {
	for d := dir; d != nil; d = d.Parent {
		if d.FeatureIDs == nil {
			d.FeatureIDs = make(map[string]bool)
		}
		d.FeatureIDs[featureID] = true
	}
}

// resolveEnvValue translates msis directory roots in environment variable values
// to their WiX directory property equivalents.
// e.g. "[APPDATADIR]NGBT\Logs" -> "[CommonAppDataFolder]NGBT\Logs"
//
// Every root in the value is translated, in a fixed order (#73): the first version ranged a map
// and returned after the first root it happened to find, so a value naming two roots had a
// random one translated and the other left as is in the WXS. No WiX property contains an msis
// root in brackets, so the replacements cannot interact and their order does not matter.
func resolveEnvValue(value string) string {
	for _, r := range [...]struct{ msisRoot, wixProp string }{
		{"INSTALLDIR", "INSTALLDIR"},
		{"APPDATADIR", "CommonAppDataFolder"},
		{"ROAMINGAPPDATADIR", "AppDataFolder"},
		{"LOCALAPPDATADIR", "LocalAppDataFolder"},
		{"COMMONFILESDIR", "CommonFiles64Folder"},
		{"WINDOWSDIR", "WindowsFolder"},
		{"SYSTEMDIR", "System64Folder"},
	} {
		value = strings.ReplaceAll(value, "["+r.msisRoot+"]", "["+r.wixProp+"]")
	}
	return value
}

func (c *Context) processSetEnv(env ir.SetEnv, featureID string) error {
	// Environment variables go in INSTALLDIR
	dir := c.GetOrCreateDirectory("INSTALLDIR", "", false)

	// Resolve {{VAR}} references in the value
	value := c.resolveOrWarn(env.Value, fmt.Sprintf("value of <set-env name=%q>", env.Name))
	value = resolveEnvValue(value)

	envID := c.NextEnvID()
	compID := c.NextComponentID(c.productScopedID("env_" + env.Name))

	comp := &Component{
		ID:   compID,
		GUID: GenerateGUID(compID), // Explicit GUID required for non-file components
		Environment: &Environment{
			ID:        envID,
			Name:      env.Name,
			Value:     value,
			Permanent: env.Permanent,
		},
	}

	c.addComponentToDirectory(dir, comp, featureID)

	if featureID != "" {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}

	return nil
}

func (c *Context) processCreateFolder(cf ir.CreateFolder, featureID string) error {
	rootKey, subPath := ParseTarget(cf.Target)

	// Create the full directory path in the tree
	dir := c.GetOrCreateDirectory(rootKey, subPath, false)

	// Add a component with CreateFolder to ensure WiX creates the directory
	compID := c.NextComponentID(c.productScopedID("create_folder"))

	comp := &Component{
		ID:           compID,
		GUID:         GenerateGUID(compID),
		CreateFolder: true,
	}

	c.addComponentToDirectory(dir, comp, featureID)

	if featureID != "" {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}

	return nil
}

func (c *Context) processService(svc ir.Service, featureID string) error {
	svcID := c.NextServiceID()

	start := "auto"
	if svc.Start != "" {
		start = svc.Start
	}

	startAfterInstall := strings.ToLower(svc.StartAfterInstall) != "no"

	// msis-2.x's defaults (SetupItem/Service.cs): the display name is the service name, the
	// description the display name (#78).
	serviceDef := &Service{
		ID:                svcID,
		Name:              svc.ServiceName,
		DisplayName:       cmp.Or(svc.ServiceDisplayName, svc.ServiceName),
		Start:             start,
		Type:              cmp.Or(svc.ServiceType, "ownProcess"),
		ErrorControl:      cmp.Or(svc.ErrorControl, "normal"),
		Restart:           svc.Restart,
		FileName:          svc.FileName,
		StartAfterInstall: startAfterInstall,
	}
	serviceDef.Description = cmp.Or(svc.Description, serviceDef.DisplayName)

	if relPath, anchored := serviceFileRelPath(svc.FileName); anchored {
		return c.processAnchoredService(svc, serviceDef, relPath, featureID)
	}

	fileKey := strings.ToLower(svc.FileName)

	// If the service executable is already installed by a component in the SAME feature
	// (or the package declares no features, so everything is in WiX's default one),
	// attach the ServiceInstall to that existing component.
	if existingComp, ok := c.fileComponents[fileKey]; ok && (featureID == "" || slices.Contains(c.FeatureComponents[featureID], existingComp.ID)) {
		if existingComp.Service != nil {
			return fmt.Errorf("service %q: component for %q already carries service %q", svc.ServiceName, svc.FileName, existingComp.Service.Name)
		}
		existingComp.Service = serviceDef
		return nil
	}

	// The file is in a different feature, or not declared at all: the service gets a
	// component with its own copy at the INSTALLDIR root. That is sound only where the
	// copy's target is not also another feature's file - checkServiceFileOwnership
	// refuses the package if it is (#77).
	dir := c.GetOrCreateDirectory("INSTALLDIR", "", false)
	compID := c.NextComponentID(c.productScopedID("svc_" + svc.ServiceName))
	fileID := c.NextFileID()

	sourcePath := svc.FileName
	if path, ok := c.fileSourcePaths[fileKey]; ok {
		sourcePath = path
	}

	comp := &Component{
		ID:   compID,
		GUID: GenerateGUID(compID),
		Files: []*File{
			{
				ID:         fileID,
				Name:       svc.FileName,
				SourcePath: sourcePath,
				KeyPath:    true,
			},
		},
		Service: serviceDef,
	}

	c.addComponentToDirectory(dir, comp, featureID)

	if featureID != "" {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}

	return nil
}

// serviceFileRelPath returns a service file-name as a path relative to
// INSTALLDIR, and whether it addresses an installed location ("server\app.exe",
// "[INSTALLDIR]app.exe") rather than a bare executable name.
func serviceFileRelPath(fileName string) (string, bool) {
	name := strings.ReplaceAll(fileName, "/", "\\")

	const installDirPrefix = "[installdir]"
	hadRootPrefix := strings.HasPrefix(strings.ToLower(name), installDirPrefix)
	if hadRootPrefix {
		name = name[len(installDirPrefix):]
	}

	// Other [ROOT] prefixes are rejected downstream with a clear error.
	if !hadRootPrefix && strings.HasPrefix(name, "[") {
		return name, true
	}

	if !hadRootPrefix && !strings.Contains(name, "\\") {
		return "", false
	}
	return name, true
}

// processAnchoredService attaches the ServiceInstall to the file installed at
// relPath (relative to INSTALLDIR), which must be declared by a preceding
// <files> element.
func (c *Context) processAnchoredService(svc ir.Service, serviceDef *Service, relPath string, featureID string) error {
	if strings.HasPrefix(relPath, "[") {
		return fmt.Errorf("service %q: only the [INSTALLDIR] root is supported in file-name, got %q", svc.ServiceName, svc.FileName)
	}
	subPath, fileName := "", relPath
	if idx := strings.LastIndex(relPath, "\\"); idx >= 0 {
		subPath, fileName = relPath[:idx], relPath[idx+1:]
	}

	// Navigate only: a missing directory means the file was not declared,
	// which must fail instead of minting an empty component.
	root, ok := c.DirectoryTrees["INSTALLDIR"]
	if !ok {
		return fmt.Errorf("service %q: file-name %q does not match any installed file (declare it with <files> before the <service> element)", svc.ServiceName, svc.FileName)
	}
	dir := c.findDirectoryWithCustomID(root, "INSTALLDIR")
	for _, part := range strings.Split(subPath, "\\") {
		if part == "" {
			continue
		}
		child, ok := dir.Children[strings.ToLower(part)]
		if !ok {
			return fmt.Errorf("service %q: file-name %q does not match any installed file (declare it with <files> before the <service> element)", svc.ServiceName, svc.FileName)
		}
		dir = child
	}

	var existingComp *Component
	var existingFile *File
	for _, comp := range dir.Components {
		for _, f := range comp.Files {
			if strings.EqualFold(f.Name, fileName) {
				existingComp, existingFile = comp, f
				break
			}
		}
		if existingComp != nil {
			break
		}
	}
	if existingComp == nil {
		return fmt.Errorf("service %q: file-name %q does not match any installed file (declare it with <files> before the <service> element)", svc.ServiceName, svc.FileName)
	}

	// Same feature (or no features at all): the service attaches to the file's component.
	if featureID == "" || slices.Contains(c.FeatureComponents[featureID], existingComp.ID) {
		if existingComp.Service != nil {
			return fmt.Errorf("service %q: component for %q already carries service %q", svc.ServiceName, svc.FileName, existingComp.Service.Name)
		}
		existingComp.Service = serviceDef
		return nil
	}

	// Another feature's file: a sibling component installing the same file, as msis 3.0.5 did.
	// That is the deprecated #77 layout; checkServiceFileOwnership warns about it, or refuses
	// it under Strict.
	targetKey := dir.ID + ":" + strings.ToLower(fileName)
	c.targetFileSeen[targetKey]++
	occurrence := c.targetFileSeen[targetKey]
	var shortName string
	if occurrence > 1 {
		shortName = generateShortName(fileName, occurrence)
	}

	compID := c.NextComponentID(c.productScopedID("svc_" + svc.ServiceName))
	comp := &Component{
		ID:   compID,
		GUID: GenerateGUID(compID),
		Files: []*File{
			{
				ID:         c.NextFileID(),
				Name:       fileName,
				ShortName:  shortName,
				SourcePath: existingFile.SourcePath,
				KeyPath:    true,
			},
		},
		Service: serviceDef,
	}
	c.addComponentToDirectory(dir, comp, featureID)
	c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	return nil
}

// checkServiceFileOwnership reports a service whose executable shares its install target
// with a component of another feature (#77, D22): a warning, or under Strict an error.
//
// A service is registered from the key file of the component carrying its ServiceInstall,
// so that component must own the executable - and one file can have only one owning
// component. Two components installing one target in different features install it twice,
// and removing either feature deletes the file the other still needs: on the test VM,
// removing an optional Service feature deleted the application, and removing the
// application left a registered service pointing at a deleted binary, each with msiexec
// reporting success. Scripts in the field build this layout, so it is deprecated rather than
// refused: msis 4 will refuse it, and Strict refuses it now. It runs once every item is
// processed, because a <service> can come before the <files> that installs the same target.
func (c *Context) checkServiceFileOwnership() error {
	for _, root := range sortedKeysOf(c.DirectoryTrees) {
		if err := c.checkServiceFilesIn(c.DirectoryTrees[root]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) checkServiceFilesIn(dir *Directory) error {
	for _, svc := range dir.Components {
		if svc.Service == nil {
			continue
		}
		if msg := c.serviceFileConflict(dir, svc); msg != "" {
			if c.Strict {
				return errors.New(msg)
			}
			c.warn("%s", msg)
		}
	}
	for _, name := range sortedKeysOf(dir.Children) {
		if err := c.checkServiceFilesIn(dir.Children[name]); err != nil {
			return err
		}
	}
	return nil
}

// featuresOf lists the features referencing a component, in feature-ID order.
func (c *Context) featuresOf(compID string) []string {
	var ids []string
	for _, fid := range sortedKeysOf(c.FeatureComponents) {
		if slices.Contains(c.FeatureComponents[fid], compID) {
			ids = append(ids, fid)
		}
	}
	return ids
}

// serviceFileConflict is #77's message for a service component whose executable another
// feature's component in the same directory also installs, or "" when there is none. It says
// how the script can be written instead.
//
// The suggested elements are complete .msis XML, pasteable as they stand: the values are
// XML-escaped (a source under R&D\ must read R&amp;D\), and the copy's target names the
// installed file, so a <files> that renamed the executable keeps that name.
func (c *Context) serviceFileConflict(dir *Directory, svc *Component) string {
	for _, other := range dir.Components {
		if other == svc {
			continue
		}
		for _, exe := range svc.Files {
			for _, f := range other.Files {
				if !strings.EqualFold(f.Name, exe.Name) {
					continue
				}
				// The same feature(s) install and remove both together; nothing is lost.
				svcFeatures, otherFeatures := c.featuresOf(svc.ID), c.featuresOf(other.ID)
				if !slices.Equal(svcFeatures, otherFeatures) {
					return c.serviceFileConflictMessage(svc.Service.Name, exe.Name, f.SourcePath, otherFeatures, svcFeatures)
				}
			}
		}
	}
	return ""
}

func (c *Context) serviceFileConflictMessage(service, file, source string, fileFeatures, serviceFeatures []string) string {
	names := func(ids []string) string {
		quoted := make([]string, len(ids))
		for i, id := range ids {
			name, ok := c.featureNames[id]
			if !ok {
				name = "items outside any feature"
			}
			quoted[i] = fmt.Sprintf("%q", name)
		}
		return strings.Join(quoted, " and ")
	}
	fileIn, serviceIn := names(fileFeatures), names(serviceFeatures)
	return fmt.Sprintf("service %q: %s is installed by feature %s, but the <service> is in feature %s, so the "+
		"executable is installed a second time for it. A file can belong to only one component: removing either "+
		"feature from an installed product deletes the file the other still needs (#77). This layout is deprecated: "+
		"msis 4 will refuse it, and /STRICT refuses it now. Either move the <service> into feature %s, "+
		"which registers the service whenever that feature is installed, or give feature %s a copy of its own at "+
		"a target of its own and name that copy in file-name, keeping the <service>'s other attributes:\n"+
		"    <files source=\"%s\" target=\"[INSTALLDIR]service\\%s\"/>\n"+
		"    <service file-name=\"[INSTALLDIR]service\\%s\"/>",
		service, file, fileIn, serviceIn, fileIn, serviceIn, escapeXMLAttr(source), escapeXMLAttr(file), escapeXMLAttr(file))
}

func (c *Context) processShortcut(sc ir.Shortcut, featureID string) error {
	// Validate target first to avoid dangling component references
	target := strings.ToUpper(sc.Target)
	if target != "DESKTOP" && target != "STARTMENU" {
		return fmt.Errorf("invalid shortcut target %q for shortcut %q: must be DESKTOP or STARTMENU", sc.Target, sc.Name)
	}

	// Generate IDs
	shortcutID := c.NextShortcutID()
	compID := c.NextComponentID(c.productScopedID("shortcut_" + sc.Name))
	guid := GenerateGUID(compID)

	// Determine working directory from the file path
	// e.g., "[INSTALLDIR]app.exe" -> WorkingDir = "INSTALLDIR"
	workingDir := "INSTALLDIR"
	if strings.HasPrefix(sc.File, "[") {
		idx := strings.Index(sc.File, "]")
		if idx > 0 {
			workingDir = sc.File[1:idx]
		}
	}

	shortcut := &Shortcut{
		ID:          shortcutID,
		Name:        sc.Name,
		Description: sc.Description,
		Target:      sc.File,
		Icon:        sc.Icon,
		WorkingDir:  workingDir,
	}

	shortcutComp := &ShortcutComponent{
		ID:       compID,
		GUID:     guid,
		Shortcut: shortcut,
	}

	// Add to appropriate list based on target
	if target == "DESKTOP" {
		c.DesktopShortcuts = append(c.DesktopShortcuts, shortcutComp)
	} else {
		c.StartMenuShortcuts = append(c.StartMenuShortcuts, shortcutComp)
	}

	// Track component for feature
	if featureID != "" {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}

	return nil
}

// newRegistryProcessor builds the .reg processor with the variable dictionary attached, so
// {{VAR}} references in REG_SZ values expand at build time as they did in msis-2.x (#46).
func newRegistryProcessor(workDir string, vars variables.Dictionary) *registry.Processor {
	p := registry.NewProcessor(workDir, vars.UpgradeCode())
	p.Variables = vars
	return p
}

func (c *Context) processRegistry(reg ir.Registry, featureID string) error {
	// Process the registry file using the registry processor
	components, err := c.registryProcessor.Process(reg)
	if err != nil {
		return err
	}

	// Add components to the list
	c.RegistryComponents = append(c.RegistryComponents, components...)

	// Track component IDs for feature association
	for _, comp := range components {
		if featureID != "" {
			c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], comp.ID)
		}
	}

	return nil
}

func (c *Context) processExecute(exec ir.Execute, featureID string) error {
	// Validate the when value
	if _, ok := customActionTimings[exec.When]; !ok {
		return fmt.Errorf("invalid execute when value %q: must be one of before-install, after-install, after-install-not-patch, before-upgrade, before-uninstall", exec.When)
	}

	// Validate quiet value and that it is only used with deferred timings.
	// WixQuietExec hides the console window (CREATE_NO_WINDOW); the before-install
	// timing runs immediate and is not supported here.
	quiet := exec.Quiet
	if quiet == "" {
		quiet = "no"
	}
	switch quiet {
	case "no", "yes", "auto":
		// valid
	default:
		return fmt.Errorf("invalid quiet value %q: must be one of no, yes, auto", quiet)
	}
	if quiet != "no" && exec.When == "before-install" {
		return fmt.Errorf("quiet=%q is not supported with when=before-install (only deferred timings)", quiet)
	}

	// Generate unique action ID
	actionID := fmt.Sprintf("CUSTOMACTION_%05d", c.nextActionID)
	c.nextActionID++

	// Default directory to INSTALLDIR if not specified
	directory := exec.Directory
	if directory == "" {
		directory = "INSTALLDIR"
	}

	ca := &CustomAction{
		ID:          actionID,
		Command:     exec.Cmd,
		Directory:   directory,
		When:        exec.When,
		FailOnError: exec.FailOnError,
		Quiet:       quiet,
	}

	c.CustomActions = append(c.CustomActions, ca)
	return nil
}

// shouldSetFilePermissions returns true if file permissions should be applied.
// Returns false if DISABLE_FILE_PERMISSIONS is set to true.
func (c *Context) shouldSetFilePermissions() bool {
	return !c.Variables.GetBool("DISABLE_FILE_PERMISSIONS")
}

// getPermissionAttributes returns the permission attributes based on RESTRICT_FILE_PERMISSIONS.
func (c *Context) getPermissionAttributes() string {
	if c.Variables.GetBool("RESTRICT_FILE_PERMISSIONS") {
		return "GenericRead='yes' Read='yes' GenericExecute='yes'"
	}
	return "GenericAll='yes'"
}

// generatePermissionComponent generates a CreateFolder component with permissions.
func (c *Context) generatePermissionComponent(dir *Directory, sb *strings.Builder, depth int) {
	indent := strings.Repeat("    ", depth)

	// Generate a unique component ID for this directory's permission
	dirID := dir.ID
	if dir.CustomID != "" {
		dirID = dir.CustomID
	}
	compID := c.NextComponentID(c.productScopedID("perm_" + dirID))
	guid := GenerateGUID(compID)
	permissions := c.getPermissionAttributes()

	sb.WriteString(fmt.Sprintf("%s<Component Id='%s' Guid='%s'>\n", indent, compID, guid))
	sb.WriteString(fmt.Sprintf("%s    <CreateFolder>\n", indent))
	sb.WriteString(fmt.Sprintf("%s        <util:PermissionEx User='Users' Domain='[MachineName]' %s/>\n", indent, permissions))
	sb.WriteString(fmt.Sprintf("%s    </CreateFolder>\n", indent))
	sb.WriteString(fmt.Sprintf("%s</Component>\n", indent))

	// Add permission component to all features that own this directory
	for featureID := range dir.FeatureIDs {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}
}

// addPathEnvironment adds INSTALLDIR to the system PATH environment variable.
func (c *Context) addPathEnvironment(featureID string) {
	dir := c.GetOrCreateDirectory("INSTALLDIR", "", false)

	compID := c.NextComponentID(c.productScopedID("add_to_path"))
	envID := c.NextEnvID()

	comp := &Component{
		ID:   compID,
		GUID: GenerateGUID(compID),
		Environment: &Environment{
			ID:    envID,
			Name:  "PATH",
			Value: "[INSTALLDIR]",
			Part:  "last",
		},
	}

	c.addComponentToDirectory(dir, comp, featureID)

	if featureID != "" {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}
}

// generateDirectoryXMLForRoot generates XML for a specific root key (INSTALLDIR, APPDATADIR, etc.)
func (c *Context) generateDirectoryXMLForRoot(rootKey string) string {
	tree, ok := c.DirectoryTrees[rootKey]
	if !ok {
		return ""
	}

	var sb strings.Builder
	c.generateDirectoryXML(tree, &sb, 2)
	return sb.String()
}

func (c *Context) generateDirectoryXML(dir *Directory, sb *strings.Builder, depth int) {
	indent := strings.Repeat("    ", depth)

	// Open directory tag
	if dir.CustomID != "" {
		// Root directory with custom ID (e.g., INSTALLDIR)
		if dir.Name != "" {
			sb.WriteString(fmt.Sprintf("%s<Directory Id='%s' Name='%s'>\n", indent, dir.CustomID, escapeWixPath(dir.Name)))
		} else {
			sb.WriteString(fmt.Sprintf("%s<Directory Id='%s'>\n", indent, dir.CustomID))
		}
	} else if dir.Name != "" {
		// Regular subdirectory
		sb.WriteString(fmt.Sprintf("%s<Directory Id='%s' Name='%s'>\n", indent, dir.ID, escapeWixPath(dir.Name)))
	}

	// Generate CreateFolder with permissions if enabled - only for directories that have a
	// NAME. An unnamed directory is the standard folder it sits in: a root whose variable is
	// unset (`<Directory Id='INSTALLDIR'>` with no Name) resolves to ProgramFiles64Folder
	// itself, an unnamed APPDATADIR to C:\ProgramData, an unnamed WINDOWSDIR to C:\Windows.
	// The condition used to add `|| dir.CustomID != ""`, which let exactly those through: with
	// INSTALLDIR unset msis tried to grant Users full control of C:\Program Files, Windows
	// refused (Error 25521), and the install rolled back (#55, observed in T8). msis-2.x
	// emitted the root and its permission component only when the root had a name
	// (WxsItem/Directory.cs, CreateComponent), so this is parity as well as safety.
	if dir.Name != "" && c.shouldSetFilePermissions() {
		c.generatePermissionComponent(dir, sb, depth+1)
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
		shortName := ""
		if file.ShortName != "" {
			shortName = fmt.Sprintf(" ShortName='%s'", escapeWixPath(file.ShortName))
		}
		sb.WriteString(fmt.Sprintf("%s    <File Id='%s' Name='%s'%s Source='%s'%s/>\n",
			indent, file.ID, escapeWixPath(file.Name), shortName, escapeWixPath(file.SourcePath), keyPath))
	}

	// Environment
	if comp.Environment != nil {
		env := comp.Environment
		part := env.Part
		if part == "" {
			part = "all"
		}
		permanent := "no"
		if env.Permanent {
			permanent = "yes"
		}
		sb.WriteString(fmt.Sprintf("%s    <Environment Id='%s' Name='%s' Value='%s' Permanent='%s' Part='%s' Action='set' System='yes'/>\n",
			indent, env.ID, env.Name, env.Value, permanent, part))
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

		name := escapeXMLAttr(svc.Name)
		fmt.Fprintf(sb, "%s    <ServiceInstall Id='%s' Name='%s' DisplayName='%s' Description='%s' Start='%s' Type='%s' ErrorControl='%s'>\n",
			indent, svc.ID, name, escapeXMLAttr(svc.DisplayName), escapeXMLAttr(svc.Description), startType,
			escapeXMLAttr(svc.Type), escapeXMLAttr(svc.ErrorControl))
		if svc.Restart {
			// msis-2.x's restart="yes", value for value (SetupItem/Service.cs, the RM4V case).
			fmt.Fprintf(sb, "%s        <util:ServiceConfig FirstFailureActionType='restart' SecondFailureActionType='restart' ThirdFailureActionType='restart' RestartServiceDelayInSeconds='30' ResetPeriodInDays='1'/>\n", indent)
		}
		fmt.Fprintf(sb, "%s    </ServiceInstall>\n", indent)
		if svc.StartAfterInstall {
			fmt.Fprintf(sb, "%s    <ServiceControl Id='%s_ctrl' Name='%s' Start='install' Stop='both' Remove='uninstall' Wait='yes'/>\n",
				indent, svc.ID, name)
		} else {
			fmt.Fprintf(sb, "%s    <ServiceControl Id='%s_ctrl' Name='%s' Stop='both' Remove='uninstall' Wait='yes'/>\n",
				indent, svc.ID, name)
		}
	}

	// CreateFolder for empty directories
	if comp.CreateFolder {
		sb.WriteString(fmt.Sprintf("%s    <CreateFolder/>\n", indent))
	}

	sb.WriteString(fmt.Sprintf("%s</Component>\n", indent))
}

// packageItemsFeatureID holds components from items written directly under <setup>
// rather than inside a <feature>. Fixed and distinctive so it cannot collide with a
// generated user feature id, and so output stays diffable.
const packageItemsFeatureID = "MSIS_PACKAGE_ITEMS"

// hasInstallDir reports whether the package will declare an INSTALLDIR directory: the
// tree exists only when some item placed a component under it (files, set-env, a
// service, ADD_TO_PATH, create-folder, remove-on-uninstall). Call it once processing is
// complete - Generate does, after every item and the remove-on-uninstall components.
func (c *Context) hasInstallDir() bool {
	_, ok := c.DirectoryTrees["INSTALLDIR"]
	return ok
}

func (c *Context) generateAllFeatureXML() string {
	var sb strings.Builder

	// The install-directory dialog lets the user choose where INSTALLDIR goes; with
	// nothing installed there, its SetTargetPath names a directory the package never
	// declares. Say so at build time rather than leave it to the install (#54).
	if c.Variables.GetBool("INSTALL_DIR_DIALOG") && !c.hasInstallDir() {
		c.warn("INSTALL_DIR_DIALOG is set, but nothing in this package installs under INSTALLDIR, " +
			"so there is no install folder to choose and the dialog points at a directory the " +
			"package does not declare; remove INSTALL_DIR_DIALOG")
	}

	for i := range c.Setup.Features {
		c.generateFeatureXML(&c.Setup.Features[i], &sb, 2, "", i)
	}

	c.generatePackageItemsFeatureXML(&sb)

	return sb.String()
}

// generatePackageItemsFeatureXML gives the components from top-level items a feature to
// belong to (issue #15).
//
// docs/msis.xsd permits <files>, <registry>, <set-env> and others directly under
// <setup>. Their components used to be emitted with no feature reference at all, and
// WiX failed the build with "error WIX0267: Found orphaned Component" as soon as the
// package also declared a feature of its own. Measured for files, registry, set-env and
// remove-on-uninstall alike.
//
// Only reached when the package declares features, because only then does the top-level
// processing assign packageItemsFeatureID — see Generate. A package with no features
// keeps relying on WiX's own default feature, unchanged.
//
// Hidden and non-optional: these items are package-level, so they should not appear in
// the feature tree as something to deselect, and they must not inherit the level or
// conditions of whichever feature happened to be declared first.
//
// That is a statement about the UI, not a guarantee. Display='hidden' and
// AllowAbsent='no' stop a user deselecting the feature in the dialog; they do not make
// installation unconditional, since a command line selecting features explicitly (for
// example ADDLOCAL naming only some) can still leave this one out.
func (c *Context) generatePackageItemsFeatureXML(sb *strings.Builder) {
	compIDs := c.FeatureComponents[packageItemsFeatureID]
	if len(compIDs) == 0 {
		return
	}

	sb.WriteString(fmt.Sprintf("        <Feature Id='%s' Title='%s' Level='1' Display='hidden' AllowAbsent='no'>\n",
		packageItemsFeatureID, escapeXMLAttr(c.Variables.ProductName())))
	for _, compID := range compIDs {
		sb.WriteString(fmt.Sprintf("            <ComponentRef Id='%s'/>\n", compID))
	}
	sb.WriteString("        </Feature>\n")
}

func (c *Context) generateAllRegistryXML(preservedIDs []map[string]int) string {
	if len(c.RegistryComponents) == 0 {
		return ""
	}

	// Apply registry permissions unless explicitly disabled
	// (msis-2.x always applies SDDL from registry entries)
	setPermissions := !c.Variables.GetBool("DISABLE_REGISTRY_PERMISSIONS")

	// Generate XML using the registry processor with preservation support
	return c.registryProcessor.GenerateXMLWithPreservedIDs(c.RegistryComponents, setPermissions, preservedIDs)
}

func (c *Context) generateFeatureXML(feature *ir.Feature, sb *strings.Builder, depth int, parentIndexPath string, index int) {
	indent := strings.Repeat("    ", depth)

	// Build index path (matches assignFeatureIDs and processFeature)
	indexPath := fmt.Sprintf("%d", index)
	if parentIndexPath != "" {
		indexPath = parentIndexPath + "/" + indexPath
	}

	// Get the pre-assigned feature ID (same one used for FeatureComponents)
	featureID := c.featureIDs[indexPath]

	// Level: 1 for enabled, 32767 for disabled (msis-2.x compatible)
	level := "1"
	if !feature.Enabled {
		level = "32767"
	}

	allowAbsent := "yes"
	if !feature.Allowed {
		allowAbsent = "no"
	}

	// Root feature gets ConfigurableDirectory so CustomizeDlg's Browse button
	// is enabled. Sub-features inherit INSTALLDIR and must not override.
	//
	// Only when an INSTALLDIR directory is actually emitted, though (#54). The directory
	// exists only if some item placed a component under it; registry components live
	// outside the directory tree, so a package whose features hold only <registry> items
	// had a ConfigurableDirectory naming a directory that was never declared, and WiX
	// refused the build: "WIX0094: The identifier 'Directory:INSTALLDIR' could not be
	// found". Such a package has no install folder for a user to choose, so there is
	// nothing to make configurable. msis-2.x emitted the attribute on every feature
	// unconditionally (Feature.cs), so it would have failed the same way.
	configurable := ""
	if parentIndexPath == "" && c.hasInstallDir() {
		configurable = " ConfigurableDirectory='INSTALLDIR'"
	}

	// The name is resolved like every other authored value (#75): msis-2.x translated it
	// (DescriptionReader.cs), and a <feature name="{{PRODUCT_NAME}}"> reached the MSI as the
	// literal reference. Escaped, since a title may hold a quote or an ampersand.
	title := escapeXMLAttr(c.resolveOrWarn(feature.Name, fmt.Sprintf("<feature name=%q>", feature.Name)))
	sb.WriteString(fmt.Sprintf("%s<Feature Id='%s' Title='%s' Level='%s' AllowAbsent='%s'%s>\n",
		indent, featureID, title, level, allowAbsent, configurable))

	// Component refs (keyed by unique feature ID)
	if compIDs, ok := c.FeatureComponents[featureID]; ok {
		for _, compID := range compIDs {
			sb.WriteString(fmt.Sprintf("%s    <ComponentRef Id='%s'/>\n", indent, compID))
		}
	}

	// Sub-features
	for i := range feature.SubFeatures {
		c.generateFeatureXML(&feature.SubFeatures[i], sb, depth+1, indexPath, i)
	}

	sb.WriteString(fmt.Sprintf("%s</Feature>\n", indent))
}

// generateShortcutsXML generates WiX XML for shortcut components.
func (c *Context) generateShortcutsXML(shortcuts []*ShortcutComponent) string {
	if len(shortcuts) == 0 {
		return ""
	}

	var sb strings.Builder
	productName := c.Variables["PRODUCT_NAME"]

	for _, sc := range shortcuts {
		sb.WriteString(fmt.Sprintf("            <Component Id='%s' Guid='%s'>\n", sc.ID, sc.GUID))

		// Generate Shortcut element
		shortcut := sc.Shortcut
		if shortcut.Icon != "" {
			// Shortcut with icon
			sb.WriteString(fmt.Sprintf("                <Shortcut Id='%s' Name='%s' Description='%s' Target='%s' WorkingDirectory='%s'>\n",
				shortcut.ID, escapeXMLAttr(shortcut.Name), escapeXMLAttr(shortcut.Description),
				escapeXMLAttr(shortcut.Target), shortcut.WorkingDir))
			sb.WriteString(fmt.Sprintf("                    <Icon Id='Icon_%s' SourceFile='%s'/>\n",
				shortcut.ID, escapeXMLAttr(shortcut.Icon)))
			sb.WriteString("                </Shortcut>\n")
		} else {
			// Shortcut without icon
			sb.WriteString(fmt.Sprintf("                <Shortcut Id='%s' Name='%s' Description='%s' Target='%s' WorkingDirectory='%s'/>\n",
				shortcut.ID, escapeXMLAttr(shortcut.Name), escapeXMLAttr(shortcut.Description),
				escapeXMLAttr(shortcut.Target), shortcut.WorkingDir))
		}

		// Registry value for KeyPath (shortcuts cannot be keypaths)
		// Use component ID as registry value name to avoid collisions when same shortcut name
		// is used for both Desktop and StartMenu
		sb.WriteString(fmt.Sprintf("                <RegistryValue Root='HKCU' Key='Software\\%s\\Shortcuts' Name='%s' Type='integer' Value='1' KeyPath='yes'/>\n",
			escapeXMLAttr(productName), sc.ID))

		sb.WriteString("            </Component>\n")
	}

	return sb.String()
}

// escapeXMLAttr escapes special characters for XML attribute values.
func escapeXMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// escapeWixPath escapes a filesystem-derived value (a File/@Name, File/@Source,
// or Directory/@Name) for a WiX attribute. On top of the XML escaping it doubles
// '$' to '$$', because the WiX binder treats '$' as the start of a variable
// reference and collapses '$$' back to a single '$'. Real-world payloads contain
// such names — e.g. projectM presets like "$$$ Royal - Mashup (110).milk".
func escapeWixPath(s string) string {
	return strings.ReplaceAll(escapeXMLAttr(s), "$", "$$")
}

// quietExecBinaryRef is the WiX Util custom-action binary used to run a command
// with no console window (CREATE_NO_WINDOW, via WixQuietExec). The project
// references the X86 build of the Util CA across all platforms (see the
// WixShellExec usage in templates/*/template.wxs).
const quietExecBinaryRef = "Wix4UtilCA_X86"

// quoteLeadingExecutable wraps the leading executable token of a command line in
// double quotes if it is not already quoted. WixQuietExec rejects a command line
// whose application name is not quoted ("Command string must begin with quoted
// application name", error 0x80070057), so the quiet path needs this; the plain
// ExeCommand (type 34) path does not and is left untouched. The executable token
// is everything up to the first whitespace (property refs like [INSTALLDIR] have
// no spaces in the unresolved string, so this stays correct after MSI formatting).
func quoteLeadingExecutable(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || strings.HasPrefix(cmd, "\"") {
		return cmd
	}
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return "\"" + cmd[:i] + "\"" + cmd[i:]
	}
	return "\"" + cmd + "\""
}

// emittedAction is one WiX CustomAction definition together with its
// InstallExecuteSequence scheduling entry, kept together so the two generators
// stay in sync.
type emittedAction struct {
	def string // <CustomAction .../> definition
	seq string // <Custom .../> sequence entry
}

// combineConditions ANDs two MSI condition expressions, ignoring empty ones.
func combineConditions(a, b string) string {
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return "(" + a + ") AND (" + b + ")"
	}
}

// expandCustomAction turns one CustomAction into the concrete WiX CustomAction
// definitions and sequence entries it requires, honoring the quiet setting:
//
//	"no"   → a plain ExeCommand action (visible console window) - unchanged behavior.
//	"yes"  → a WixQuietExec action (no console window, output captured to the MSI log).
//	"auto" → both: WixQuietExec when UILevel<=3 (/qn or /qb), visible when UILevel>=4.
//
// It is deterministic and side-effect free, so both generators can call it.
func (c *Context) expandCustomAction(ca *CustomAction) []emittedAction {
	timing, ok := customActionTimings[ca.When]
	if !ok {
		return nil
	}

	returnAttr := "ignore"
	if ca.FailOnError {
		returnAttr = "check"
	}
	cmd := escapeXMLAttr(ca.Command)
	// WixQuietExec requires the executable to be quoted; the visible ExeCommand path does not.
	quietCmd := escapeXMLAttr(quoteLeadingExecutable(ca.Command))
	immediate := ca.When == "before-install"

	// visibleDef builds a plain ExeCommand custom action (visible console window).
	visibleDef := func(id string) string {
		if immediate {
			return fmt.Sprintf("        <CustomAction Id='%s' Directory='%s' ExeCommand='%s' Execute='immediate' Return='%s'/>\n",
				id, ca.Directory, cmd, returnAttr)
		}
		return fmt.Sprintf("        <CustomAction Id='%s' Directory='%s' ExeCommand='%s' Execute='deferred' Return='%s' Impersonate='no'/>\n",
			id, ca.Directory, cmd, returnAttr)
	}

	// seqEntry builds an InstallExecuteSequence scheduling entry for an action.
	seqEntry := func(actionID, condition string) string {
		if condition != "" {
			return fmt.Sprintf("            <Custom Action='%s' %s='%s' Condition='%s'/>\n",
				actionID, timing.position, timing.reference, condition)
		}
		return fmt.Sprintf("            <Custom Action='%s' %s='%s'/>\n",
			actionID, timing.position, timing.reference)
	}

	// quietPair builds the WixQuietExec action that hides the console window.
	// A type-51 immediate action stores the command line in the property whose
	// name equals the deferred action's Id; the deferred WixQuietExec action then
	// receives it as CustomActionData and runs it with CREATE_NO_WINDOW.
	quietPair := func(execID, condition string) []emittedAction {
		setterID := execID + "_CMD"
		setterDef := fmt.Sprintf("        <CustomAction Id='%s' Property='%s' Value='%s' Execute='immediate'/>\n",
			setterID, execID, quietCmd)
		execDef := fmt.Sprintf("        <CustomAction Id='%s' DllEntry='WixQuietExec' BinaryRef='%s' Execute='deferred' Return='%s' Impersonate='no'/>\n",
			execID, quietExecBinaryRef, returnAttr)
		// The setter (immediate) must run before the deferred action is written to the script.
		var setterSeq string
		if condition != "" {
			setterSeq = fmt.Sprintf("            <Custom Action='%s' Before='%s' Condition='%s'/>\n", setterID, execID, condition)
		} else {
			setterSeq = fmt.Sprintf("            <Custom Action='%s' Before='%s'/>\n", setterID, execID)
		}
		return []emittedAction{
			{def: setterDef, seq: setterSeq},
			{def: execDef, seq: seqEntry(execID, condition)},
		}
	}

	switch ca.Quiet {
	case "yes":
		return quietPair(ca.ID, timing.condition)
	case "auto":
		quietCond := combineConditions(timing.condition, "UILevel &lt;= 3")
		visibleCond := combineConditions(timing.condition, "UILevel &gt;= 4")
		out := quietPair(ca.ID, quietCond)
		visID := ca.ID + "_VIS"
		out = append(out, emittedAction{def: visibleDef(visID), seq: seqEntry(visID, visibleCond)})
		return out
	default: // "no" (or empty)
		return []emittedAction{{def: visibleDef(ca.ID), seq: seqEntry(ca.ID, timing.condition)}}
	}
}

// generateCustomActionsXML generates WiX CustomAction elements.
func (c *Context) generateCustomActionsXML() string {
	if len(c.CustomActions) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, ca := range c.CustomActions {
		for _, e := range c.expandCustomAction(ca) {
			sb.WriteString(e.def)
		}
	}
	return sb.String()
}

// customActionTimings maps when values to Custom element templates.
var customActionTimings = map[string]struct {
	position  string // After or Before
	reference string // Reference action
	condition string // Optional condition
}{
	"after-install":           {"Before", "InstallFinalize", "(NOT REMOVE = \"ALL\")"},
	"after-install-not-patch": {"Before", "InstallFinalize", "NOT WIX_UPGRADE_DETECTED"},
	"before-install":          {"After", "CostFinalize", ""},
	"before-upgrade":          {"After", "CostFinalize", "WIX_UPGRADE_DETECTED"},
	"before-uninstall":        {"After", "InstallInitialize", "(REMOVE=\"ALL\")"},
}

// generateInstallExecuteSequence generates WiX InstallExecuteSequence Custom elements.
func (c *Context) generateInstallExecuteSequence() string {
	if len(c.CustomActions) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, ca := range c.CustomActions {
		for _, e := range c.expandCustomAction(ca) {
			sb.WriteString(e.seq)
		}
	}
	return sb.String()
}

// processRemoveOnUninstall handles a remove-on-uninstall item.
func (c *Context) processRemoveOnUninstall(item ir.RemoveOnUninstall, featureID string) error {
	id := fmt.Sprintf("RemoveOnUninstall_%04d", c.nextRemoveID)
	c.nextRemoveID++

	c.RemoveOnUninstallItems = append(c.RemoveOnUninstallItems, &RemoveOnUninstallItem{
		ID:        id,
		Registry:  item.Registry,
		Folder:    item.Folder,
		FeatureID: featureID,
	})
	return nil
}

// generateRemoveOnUninstallXML generates WiX XML for remove-on-uninstall items.
// For registry: generates RemoveRegistryKey element
// For folders: generates util:RemoveFolderEx element with SetProperty
func (c *Context) generateRemoveOnUninstallXML() string {
	if len(c.RemoveOnUninstallItems) == 0 {
		return ""
	}

	var sb strings.Builder

	for _, item := range c.RemoveOnUninstallItems {
		// One item can ask for both a registry key and a folder, and each is emitted as its
		// own component. Both used to be named C_<item id>, so such an item produced a
		// duplicate Component id and wix build rejected the package with WIX0091 (issue #23).
		//
		// The role suffix is added ONLY when both components are really emitted, so every
		// package that builds today keeps its component ids exactly as they are. These
		// components carry Guid='*', whose generated GUID WiX derives from the component's
		// keypath (and directory), not from the id alone - both are preserved here, since the
		// keypath registry values below are still named from the unchanged item id.
		//
		// "Really emitted" is narrower than "both attributes are set": a registry root msis
		// does not recognize is skipped silently, and such an item emits the folder component
		// alone and builds fine today.
		registryRoot, registryKey := "", ""
		if item.Registry != "" {
			registryRoot, registryKey = parseRegistryPath(item.Registry)
		}
		emitsRegistry := registryRoot != "" && registryKey != ""
		emitsFolder := item.Folder != ""

		registryCompID := fmt.Sprintf("C_%s", item.ID)
		folderCompID := registryCompID
		if emitsRegistry && emitsFolder {
			registryCompID = fmt.Sprintf("C_%s_reg", item.ID)
			folderCompID = fmt.Sprintf("C_%s_dir", item.ID)
		}

		if emitsRegistry {
			// RemoveRegistryKey needs to be in a Component
			compID := registryCompID
			sb.WriteString(fmt.Sprintf("        <Component Id='%s' Guid='*' Directory='INSTALLDIR'>\n", compID))
			// util:RemoveRegistryKey rather than the standard element: the standard one fires
			// whenever its component is removed, a major upgrade included (#76).
			sb.WriteString(fmt.Sprintf("            <util:RemoveRegistryKey Id='%s' Root='%s' Key='%s' On='uninstall' Condition='%s'/>\n",
				item.ID, registryRoot, registryKey, onRealUninstall))
			// Need a keypath - use a registry value
			sb.WriteString(fmt.Sprintf("            <RegistryValue Root='HKCU' Key='Software\\%s\\%s' Name='RemoveOnUninstall_%s' Type='integer' Value='1' KeyPath='yes'/>\n",
				c.Variables["MANUFACTURER"], c.Variables["PRODUCT_NAME"], item.ID))
			sb.WriteString("        </Component>\n")

			// Track component for feature
			if item.FeatureID != "" {
				c.FeatureComponents[item.FeatureID] = append(c.FeatureComponents[item.FeatureID], compID)
			}
		}

		if item.Folder != "" {
			// Generate util:RemoveFolderEx for folder removal
			// Need to set a property with the folder path, then use RemoveFolderEx
			// A RegistrySearch-backed property must be PUBLIC (all uppercase).
			propID := strings.ToUpper(fmt.Sprintf("REMOVE_FOLDER_%s", item.ID))
			compID := folderCompID
			searchID := fmt.Sprintf("RS_%s", item.ID)
			regKey := fmt.Sprintf("Software\\%s\\%s", c.Variables["MANUFACTURER"], c.Variables["PRODUCT_NAME"])
			regName := fmt.Sprintf("RemoveFolderPath_%s", item.ID)

			// The WiX Util RemoveFoldersEx custom action is auto-scheduled by the extension
			// BEFORE CostInitialize, so its folder Property must be populated that early.
			// A SetProperty from [INSTALLDIR] can't do that (INSTALLDIR only resolves during
			// costing) and re-scheduling the extension CA is rejected (duplicate WixAction).
			// So: store the resolved folder path in the registry at install time, and read it
			// back via RegistrySearch (runs in AppSearch, before the CA) into the property.
			sb.WriteString(fmt.Sprintf("        <Property Id='%s'>\n", propID))
			sb.WriteString(fmt.Sprintf("            <RegistrySearch Id='%s' Root='HKLM' Key='%s' Name='%s' Type='raw'/>\n",
				searchID, regKey, regName))
			sb.WriteString("        </Property>\n")

			// Component with RemoveFolderEx; the registry value (resolved folder path) is the keypath.
			sb.WriteString(fmt.Sprintf("        <Component Id='%s' Guid='*' Directory='INSTALLDIR'>\n", compID))
			sb.WriteString(fmt.Sprintf("            <util:RemoveFolderEx On='uninstall' Property='%s' Condition='%s'/>\n", propID, onRealUninstall))
			sb.WriteString(fmt.Sprintf("            <RegistryValue Root='HKLM' Key='%s' Name='%s' Type='string' Value='%s' KeyPath='yes'/>\n",
				regKey, regName, item.Folder))
			sb.WriteString("        </Component>\n")

			// Track component for feature
			if item.FeatureID != "" {
				c.FeatureComponents[item.FeatureID] = append(c.FeatureComponents[item.FeatureID], compID)
			}
		}
	}

	return sb.String()
}

// onRealUninstall keeps <remove-on-uninstall> to a real uninstall (#76, decisions D21). A major
// upgrade removes the previous version completely before installing the new one
// (RemoveExistingProducts after InstallValidate), which removes the cleanup components - and
// both cleanups fired on any removal of their component, so every update deleted the data they
// name: observed on a VM, 2026-09-25. In the previous version's session UPGRADINGPRODUCTCODE is
// set, and WiX's util custom actions evaluate the Condition there (MsiEvaluateCondition), so
// the cleanup stays off. msis's own destructive hook actions carry the same guard.
const onRealUninstall = "NOT UPGRADINGPRODUCTCODE"

// parseRegistryPath splits a registry path like "HKLM\Software\MyApp" into root and key.
func parseRegistryPath(path string) (root, key string) {
	// Normalize separators
	path = strings.ReplaceAll(path, "/", "\\")

	// Find first backslash
	idx := strings.Index(path, "\\")
	if idx == -1 {
		return "", ""
	}

	rootPart := strings.ToUpper(path[:idx])
	key = path[idx+1:]

	// Map common abbreviations to WiX root names
	switch rootPart {
	case "HKLM", "HKEY_LOCAL_MACHINE":
		root = "HKLM"
	case "HKCU", "HKEY_CURRENT_USER":
		root = "HKCU"
	case "HKCR", "HKEY_CLASSES_ROOT":
		root = "HKCR"
	case "HKU", "HKEY_USERS":
		root = "HKU"
	default:
		return "", ""
	}

	return root, key
}
