// Package wix provides WiX CLI integration for building MSI packages.
package wix

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/variables"
)

// Builder handles WiX CLI invocation for MSI generation.
type Builder struct {
	WxsFile         string
	OutputFile      string
	Platform        string
	Language        string
	TemplateFolder  string
	CustomTemplates string
	SourceDir       string // Directory of the original .msis file (for resolving source paths)
	Variables       variables.Dictionary
	RetainWxs       bool
}

// NewBuilder creates a WiX builder from variables and paths.
// sourceDir is the directory of the original .msis file, used for resolving source paths.
func NewBuilder(vars variables.Dictionary, wxsFile, templateFolder, customTemplates, sourceDir string, retainWxs bool) *Builder {
	// Determine output file
	outputFile := vars.BuildTarget()
	if outputFile == "" {
		// Default to input filename with .msi extension
		outputFile = strings.TrimSuffix(wxsFile, filepath.Ext(wxsFile)) + ".msi"
	}

	return &Builder{
		WxsFile:         wxsFile,
		OutputFile:      outputFile,
		Platform:        vars.Platform(),
		Language:        vars["LANGUAGE"],
		TemplateFolder:  templateFolder,
		CustomTemplates: customTemplates,
		SourceDir:       sourceDir,
		Variables:       vars,
		RetainWxs:       retainWxs,
	}
}

// Build invokes WiX CLI to compile the WXS into an MSI.
func (b *Builder) Build() error {
	// Check if output file exists and can be overwritten
	if err := b.checkOutputWritable(); err != nil {
		return err
	}

	// Build MSI (EULA acceptance, if required, is passed on the build command)
	if err := b.runWixBuild(); err != nil {
		return fmt.Errorf("wix build: %w", err)
	}

	// Cleanup
	b.cleanup()

	return nil
}

// checkOutputWritable checks if the output file can be written to.
// If the file exists, tries to delete it to ensure it's not locked.
func (b *Builder) checkOutputWritable() error {
	outputPath := b.OutputFile
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(b.SourceDir, outputPath)
	}

	// Check if file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil // File doesn't exist, we're good
	}

	// File exists - try to delete it
	if err := os.Remove(outputPath); err != nil {
		return fmt.Errorf("cannot overwrite output file %s: file may be locked or in use by another process", outputPath)
	}

	return nil
}

// parseMajorVersion extracts the major version number from a WiX version
// string such as "6.0.2+b3f3403" or "7.0.0". Returns 0 if it can't be parsed.
func parseMajorVersion(version string) int {
	version = strings.TrimSpace(version)
	if dot := strings.IndexByte(version, '.'); dot > 0 {
		version = version[:dot]
	}
	n, err := strconv.Atoi(strings.TrimSpace(version))
	if err != nil {
		return 0
	}
	return n
}

// GetWixMajorVersion returns the installed WiX major version (e.g. 6 or 7),
// or 0 if it cannot be determined.
func GetWixMajorVersion() int {
	return parseMajorVersion(GetWixVersion())
}

// eulaAcceptArgs returns the arguments needed to accept the WiX EULA for the
// given major version, for one build invocation. WiX 7 enforces the OSMF EULA;
// `wix build` accepts it via `--acceptEula wix<major>` (the flag requires the
// EULA id as its value). This is defense-in-depth: `msis /SETUP-WIX` also
// records a persistent acceptance, but the flag keeps builds working in fresh
// environments (e.g. CI) with no acceptance file. WiX 6 and earlier have no EULA gate.
func eulaAcceptArgs(major int) []string {
	if major >= 7 {
		return []string{"--acceptEula", fmt.Sprintf("wix%d", major)}
	}
	return nil
}

// runWixBuild executes wix build command.
func (b *Builder) runWixBuild() error {
	// Convert paths to absolute for consistent resolution
	absWxsFile, _ := filepath.Abs(b.WxsFile)
	absOutputFile, _ := filepath.Abs(b.OutputFile)
	workDir := filepath.Dir(absWxsFile)

	// Build args - use just filename since we run from its directory
	wxsFilename := filepath.Base(absWxsFile)
	args := []string{"build", wxsFilename}

	// Architecture
	if b.Platform != "" {
		args = append(args, "-arch", strings.ToLower(b.Platform))
	}

	// Extensions (see setup.go for the canonical list)
	args = append(args, extArgs(msiExtensions)...)

	// EULA acceptance (WiX 7+ only; no-op on WiX 6)
	args = append(args, eulaAcceptArgs(GetWixMajorVersion())...)

	// Localization file
	locFile := b.getLocalizationFile()
	if locFile != "" {
		args = append(args, "-loc", locFile)
		args = append(args, "-culture", b.Language)
	}

	// Bind paths (for file resolution) - use absolute paths
	// Order: workDir, sourceDir (msis file location), custom templates, template folder
	args = append(args, "-b", workDir)

	// Source directory (where .msis file is) for resolving source paths
	if b.SourceDir != "" {
		absSourceDir, _ := filepath.Abs(b.SourceDir)
		if absSourceDir != workDir {
			args = append(args, "-b", absSourceDir)
		}
	}

	// Template folder bind paths (use absolute paths)
	// Custom templates first (takes precedence), then base folder
	if b.CustomTemplates != "" {
		absCustomTemplates, _ := filepath.Abs(b.CustomTemplates)
		args = append(args, "-b", absCustomTemplates)
	}
	if b.TemplateFolder != "" {
		absTemplateFolder, _ := filepath.Abs(b.TemplateFolder)
		args = append(args, "-b", absTemplateFolder)
	}

	// No PDB file (cleaner output)
	args = append(args, "-pdbtype", "none")

	// Output file - use absolute path
	args = append(args, "-o", absOutputFile)

	wixPath := GetWixPath()
	fmt.Printf("  Running: %s %s\n", cli.Filename(wixPath), strings.Join(args, " "))

	cmd := exec.Command(wixPath, args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// getLocalizationFile returns the absolute path to the WiX localization file.
func (b *Builder) getLocalizationFile() string {
	if b.Language == "" {
		return ""
	}

	// Template folder should already be absolute, but ensure it
	absTemplateFolder, _ := filepath.Abs(b.TemplateFolder)

	// Look in template folder's wixlib directory
	locFile := filepath.Join(absTemplateFolder, "wixlib", b.Language+".wxl")
	if _, err := os.Stat(locFile); err == nil {
		return locFile
	}

	// Try lowercase
	locFile = filepath.Join(absTemplateFolder, "wixlib", strings.ToLower(b.Language)+".wxl")
	if _, err := os.Stat(locFile); err == nil {
		return locFile
	}

	return ""
}

// cleanup removes temporary files unless retention is requested.
func (b *Builder) cleanup() {
	// Remove .wixpdb if it exists
	wixpdb := strings.TrimSuffix(b.OutputFile, filepath.Ext(b.OutputFile)) + ".wixpdb"
	if _, err := os.Stat(wixpdb); err == nil {
		os.Remove(wixpdb)
	}

	// Remove .wxs unless --retainwxs
	if !b.RetainWxs {
		if _, err := os.Stat(b.WxsFile); err == nil {
			os.Remove(b.WxsFile)
		}
	}
}

// GetWixPath returns the path to the WiX 6 CLI.
// Prefers dotnet tools installation over system PATH.
func GetWixPath() string {
	// Check dotnet tools location first (WiX 6)
	home, _ := os.UserHomeDir()
	dotnetWix := filepath.Join(home, ".dotnet", "tools", "wix.exe")
	if _, err := os.Stat(dotnetWix); err == nil {
		return dotnetWix
	}

	// Unix-style dotnet tools
	dotnetWix = filepath.Join(home, ".dotnet", "tools", "wix")
	if _, err := os.Stat(dotnetWix); err == nil {
		return dotnetWix
	}

	// Fall back to PATH
	return "wix"
}

// IsWixAvailable checks if wix CLI is available.
func IsWixAvailable() bool {
	wixPath := GetWixPath()
	if wixPath == "wix" {
		_, err := exec.LookPath("wix")
		return err == nil
	}
	_, err := os.Stat(wixPath)
	return err == nil
}

// GetWixVersion returns the WiX version string, or an error message if unavailable.
func GetWixVersion() string {
	wixPath := GetWixPath()
	cmd := exec.Command(wixPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "(unavailable)"
	}
	return strings.TrimSpace(string(output))
}

// GetInstalledExtensions returns a list of installed WiX extensions.
func GetInstalledExtensions() []string {
	wixPath := GetWixPath()
	cmd := exec.Command(wixPath, "extension", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var extensions []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			extensions = append(extensions, line)
		}
	}
	return extensions
}

// BundleBuilder handles WiX CLI invocation for Bundle (bootstrapper) generation.
type BundleBuilder struct {
	WxsFile         string
	OutputFile      string
	TemplateFolder  string
	CustomTemplates string
	Variables       variables.Dictionary
	RetainWxs       bool
}

// NewBundleBuilder creates a WiX bundle builder from variables and paths.
func NewBundleBuilder(vars variables.Dictionary, wxsFile, templateFolder, customTemplates string, retainWxs bool) *BundleBuilder {
	// Determine output file (bundles produce .exe)
	outputFile := vars.BuildTarget()
	if outputFile == "" {
		outputFile = vars.ProductName() + "-" + vars.ProductVersion()
	}
	outputFile = strings.TrimSuffix(outputFile, filepath.Ext(outputFile)) + ".exe"

	return &BundleBuilder{
		WxsFile:         wxsFile,
		OutputFile:      outputFile,
		TemplateFolder:  templateFolder,
		CustomTemplates: customTemplates,
		Variables:       vars,
		RetainWxs:       retainWxs,
	}
}

// Build invokes WiX CLI to compile the bundle WXS into an EXE.
func (b *BundleBuilder) Build() error {
	// Check if output file exists and can be overwritten
	if err := b.checkOutputWritable(); err != nil {
		return err
	}

	// Build bundle (EULA acceptance, if required, is passed on the build command)
	if err := b.runWixBuild(); err != nil {
		return fmt.Errorf("wix build: %w", err)
	}

	// Cleanup
	b.cleanup()

	return nil
}

// checkOutputWritable checks if the output file can be written to.
func (b *BundleBuilder) checkOutputWritable() error {
	outputPath := b.OutputFile
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(filepath.Dir(b.WxsFile), outputPath)
	}

	// Check if file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil // File doesn't exist, we're good
	}

	// File exists - try to delete it
	if err := os.Remove(outputPath); err != nil {
		return fmt.Errorf("cannot overwrite output file %s: file may be locked or in use by another process", outputPath)
	}

	return nil
}

// runWixBuild executes wix build command for bundle.
func (b *BundleBuilder) runWixBuild() error {
	absWxsFile, _ := filepath.Abs(b.WxsFile)
	absOutputFile, _ := filepath.Abs(b.OutputFile)
	workDir := filepath.Dir(absWxsFile)

	wxsFilename := filepath.Base(absWxsFile)
	args := []string{"build", wxsFilename}

	// Bundle-specific extensions (see setup.go for the canonical list)
	args = append(args, extArgs(bundleExtensions)...)

	// EULA acceptance (WiX 7+ only; no-op on WiX 6)
	args = append(args, eulaAcceptArgs(GetWixMajorVersion())...)

	// Bind paths
	args = append(args, "-b", workDir)
	if b.CustomTemplates != "" {
		absCustomTemplates, _ := filepath.Abs(b.CustomTemplates)
		args = append(args, "-b", absCustomTemplates)
	}
	if b.TemplateFolder != "" {
		absTemplateFolder, _ := filepath.Abs(b.TemplateFolder)
		args = append(args, "-b", absTemplateFolder)
	}

	// No PDB file
	args = append(args, "-pdbtype", "none")

	// Output file
	args = append(args, "-o", absOutputFile)

	wixPath := GetWixPath()
	fmt.Printf("  Running: %s %s\n", cli.Filename(wixPath), strings.Join(args, " "))

	cmd := exec.Command(wixPath, args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// cleanup removes temporary files unless retention is requested.
func (b *BundleBuilder) cleanup() {
	// Remove .wixpdb if it exists
	wixpdb := strings.TrimSuffix(b.OutputFile, filepath.Ext(b.OutputFile)) + ".wixpdb"
	if _, err := os.Stat(wixpdb); err == nil {
		os.Remove(wixpdb)
	}

	// Remove .wxs unless --retainwxs
	if !b.RetainWxs {
		if _, err := os.Stat(b.WxsFile); err == nil {
			os.Remove(b.WxsFile)
		}
	}
}
