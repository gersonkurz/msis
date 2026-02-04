// Package wix provides WiX CLI integration for building MSI packages.
package wix

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	// Ensure EULA is accepted
	if err := b.ensureEulaAccepted(); err != nil {
		return fmt.Errorf("EULA check: %w", err)
	}

	// Build MSI
	if err := b.runWixBuild(); err != nil {
		return fmt.Errorf("wix build: %w", err)
	}

	// Cleanup
	b.cleanup()

	return nil
}

// ensureEulaAccepted checks if WiX EULA has been accepted and accepts it if needed.
func (b *Builder) ensureEulaAccepted() error {
	wixPath := GetWixPath()

	// Try running a simple wix command to see if EULA is already accepted
	cmd := exec.Command(wixPath, "--version")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return nil // EULA already accepted
	}

	// Check if error is EULA-related
	errOutput := stderr.String()
	if strings.Contains(errOutput, "EULA") || strings.Contains(errOutput, "eula") {
		// Accept EULA for WiX 6
		fmt.Println("  Accepting WiX 6 EULA...")
		acceptCmd := exec.Command(wixPath, "eula", "accept", "wix6")
		acceptCmd.Stdout = os.Stdout
		acceptCmd.Stderr = os.Stderr
		if err := acceptCmd.Run(); err != nil {
			return fmt.Errorf("accepting EULA: %w", err)
		}
		return nil
	}

	// Non-EULA error - return it
	if errOutput != "" {
		return fmt.Errorf("wix --version failed: %s", strings.TrimSpace(errOutput))
	}
	return fmt.Errorf("wix --version failed: %w", err)
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

	// Extensions
	args = append(args,
		"-ext", "WixToolset.UI.wixext",
		"-ext", "WixToolset.Util.wixext",
	)

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
	fmt.Printf("  Running: %s %s\n", wixPath, strings.Join(args, " "))

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
	// Ensure EULA is accepted (reuse MSI builder logic)
	msiBuilder := &Builder{} // Create temporary for EULA check
	if err := msiBuilder.ensureEulaAccepted(); err != nil {
		return fmt.Errorf("EULA check: %w", err)
	}

	// Build bundle
	if err := b.runWixBuild(); err != nil {
		return fmt.Errorf("wix build: %w", err)
	}

	// Cleanup
	b.cleanup()

	return nil
}

// runWixBuild executes wix build command for bundle.
func (b *BundleBuilder) runWixBuild() error {
	absWxsFile, _ := filepath.Abs(b.WxsFile)
	absOutputFile, _ := filepath.Abs(b.OutputFile)
	workDir := filepath.Dir(absWxsFile)

	wxsFilename := filepath.Base(absWxsFile)
	args := []string{"build", wxsFilename}

	// Bundle-specific extensions
	args = append(args,
		"-ext", "WixToolset.BootstrapperApplications.wixext", // Bootstrapper Application Library (renamed from Bal in WiX 6)
		"-ext", "WixToolset.Util.wixext",                     // Utility functions
		"-ext", "WixToolset.Netfx.wixext",                    // .NET Framework detection
	)

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
	fmt.Printf("  Running: %s %s\n", wixPath, strings.Join(args, " "))

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
