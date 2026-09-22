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
	WxsFile string
	// OutputFile is ABSOLUTE, resolved once at construction against the process working
	// directory (see absPath). Everything that touches the output — the overwrite check, the
	// `-o` handed to wix, cleanup, the "Built:" line main prints — reads this one value, so
	// they cannot disagree about which file is meant (#41).
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
	// Determine output file. BUILD_TARGET names the release artifacts; the MSI is the ".msi"
	// one of them, whatever extension the target itself carries (see TargetPath).
	//
	// Without a target the name comes from the .wxs, which is already beside the source, and the
	// stem is taken WHOLE - it is the source's own name, not a target to normalize. Running it
	// through TargetPath would trim a second suffix, so "probe.exe.msis" would build "probe.msi"
	// and overwrite what "probe.msis" builds.
	outputFile := vars.BuildTarget()
	if outputFile == "" {
		outputFile = strings.TrimSuffix(wxsFile, filepath.Ext(wxsFile)) + ".msi"
	} else {
		outputFile = TargetPath(outputFile, "msi")
	}

	return &Builder{
		WxsFile:         wxsFile,
		OutputFile:      absPath(outputFile),
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
	if err := checkOutputWritable(b.OutputFile); err != nil {
		return err
	}

	// Build MSI (EULA acceptance, if required, is passed on the build command)
	if err := b.runWixBuild(); err != nil {
		return fmt.Errorf("wix build: %w", err)
	}

	b.cleanup()
	return nil
}

// absPath resolves a path against the process working directory, once, at construction.
//
// A relative BUILD_TARGET is deliberately cwd-relative (decisions D6): that is where the .wxs is
// written and where `wix build` puts `-o`, so it is where the artifact has always landed. What
// went wrong before #41 was that the pre-delete resolved the same relative value against a
// DIFFERENT base — the .msis directory for the MSI, the .wxs directory for the bundle — and so
// checked, and removed, a file the build was never going to write: for a bundle target
// `dist\setup.exe` with its .wxs at `dist\setup-bundle.wxs`, it deleted `dist\dist\setup.exe`.
// Resolving once and reading the one value everywhere is what rules that out.
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// checkOutputWritable removes an existing output so a locked file is found before the build
// rather than as a wix error at the end. outputPath is the ABSOLUTE path the build will write
// to — the builder's OutputFile — and nothing is re-resolved here.
func checkOutputWritable(outputPath string) error {
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil
	}
	if err := os.Remove(outputPath); err != nil {
		return fmt.Errorf("cannot overwrite output file %s: file may be locked or in use by another process", outputPath)
	}
	return nil
}

// removeArtifacts deletes the .wixpdb wix leaves beside the output, and the .wxs unless it is
// to be retained. Shared by both builders so their cleanup cannot drift.
func removeArtifacts(outputFile, wxsFile string, retainWxs bool) {
	wixpdb := strings.TrimSuffix(outputFile, filepath.Ext(outputFile)) + ".wixpdb"
	if _, err := os.Stat(wixpdb); err == nil {
		os.Remove(wixpdb)
	}
	if !retainWxs {
		if _, err := os.Stat(wxsFile); err == nil {
			os.Remove(wxsFile)
		}
	}
}

// outputArgs is the `-o` wix is given: the builder's OutputFile, verbatim. It exists as a
// function so a test can hold the argument next to the path the overwrite check used.
func outputArgs(outputFile string) []string {
	return []string{"-o", outputFile}
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

// bindPathArgs builds the ordered "-b <dir>" arguments WiX uses to resolve source files (logos,
// license, payloads). Order matters — WiX takes the first match — so it mirrors how paths are
// authored: the generated WXS dir, then the .msis source dir, then the custom-templates overlay,
// then the base template folder. Empty dirs are skipped; the source dir is omitted when it equals
// workDir (already added). All inputs are made absolute. Shared by the MSI and bundle builders so
// they cannot drift (a logo found beside the .msis must be bindable by both).
func bindPathArgs(workDir, sourceDir, customTemplates, templateFolder string) []string {
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return p
	}
	workDir = abs(workDir)

	var args []string
	add := func(dir string) {
		if dir != "" {
			args = append(args, "-b", dir)
		}
	}
	add(workDir)
	if sd := abs(sourceDir); sd != "" && sd != workDir {
		add(sd)
	}
	add(abs(customTemplates))
	add(abs(templateFolder))
	return args
}

// runWixBuild executes wix build command.
func (b *Builder) runWixBuild() error {
	workDir, args := b.buildArgs()
	return runWix(workDir, args)
}

// buildArgs assembles the `wix build` invocation: the directory to run it from (the .wxs's,
// so the .wxs is named bare) and the arguments. Separate from running it so a test can check
// what wix is told without wix being present.
func (b *Builder) buildArgs() (workDir string, args []string) {
	absWxsFile := absPath(b.WxsFile)
	workDir = filepath.Dir(absWxsFile)

	// Build args - use just filename since we run from its directory
	wxsFilename := filepath.Base(absWxsFile)
	args = []string{"build", wxsFilename}

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

	// Bind paths (for file resolution): workDir, .msis source dir, custom templates, template folder.
	args = append(args, bindPathArgs(workDir, b.SourceDir, b.CustomTemplates, b.TemplateFolder)...)

	// No PDB file (cleaner output)
	args = append(args, "-pdbtype", "none")

	// Output file: the one absolute path the overwrite check already used
	args = append(args, outputArgs(b.OutputFile)...)
	return workDir, args
}

// runWix runs `wix` with the given arguments from workDir, streaming its output.
func runWix(workDir string, args []string) error {
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
	removeArtifacts(b.OutputFile, b.WxsFile, b.RetainWxs)
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
	WxsFile string
	// OutputFile is ABSOLUTE, resolved once at construction against the process working
	// directory — see Builder.OutputFile and absPath (#41).
	OutputFile      string
	TemplateFolder  string
	CustomTemplates string
	SourceDir       string // .msis directory; a bundle bind path so source-relative logos/payloads resolve
	Variables       variables.Dictionary
	RetainWxs       bool
}

// TrimPackageSuffix removes a trailing ".exe" or ".msi" (case-insensitively) and nothing else.
//
// Not TrimSuffix(name, filepath.Ext(name)): for "MyApp-1.0.0", filepath.Ext returns ".0" - the
// last dot-segment of a VERSION, not an extension - so trimming it produced "MyApp-1.0" and the
// patch version vanished from the filename (issue #27). ".msi" is listed because BUILD_TARGET
// names the MSI on a package that also auto-bundles, and the bundle beside it has always been
// "MyApp-1.0.0.msi" -> "MyApp-1.0.0.exe".
func TrimPackageSuffix(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe", ".msi":
		return name[:len(name)-len(".exe")]
	}
	return name
}

// TargetPath names one build artifact from a build target: the target's directory and stem, plus
// ".<ext>". BUILD_TARGET is a NAME PATTERN, not a literal filename - msis-2.x derives the .wxs,
// .msi, .exe and .wixpdb from the single value (BuildContext.SetupFileName), and msis does the
// same. `wix build` infers its output type from the extension, so handing it a target meant for
// a different artifact made it try to build a Bundle from a <Package> and fail (issue #28).
//
// Unlike msis-2.x, which cut at the last '.', only a real ".exe" or ".msi" is replaced: cutting
// at the last dot turned "MyApp-1.0.0" into "MyApp-1.0" (issue #27).
func TargetPath(target, ext string) string {
	return TrimPackageSuffix(target) + "." + ext
}

// NewBundleBuilder creates a WiX bundle builder from variables and paths. sourceDir is the .msis
// directory, added to the bind paths so an explicit source-relative LOGO_BOOTSTRAP (or other
// payload) resolves the same way it does for the MSI build.
func NewBundleBuilder(vars variables.Dictionary, wxsFile, templateFolder, customTemplates, sourceDir string, retainWxs bool) *BundleBuilder {
	// Determine output file (bundles produce .exe).
	//
	// The default is derived from the .wxs path, as NewBuilder does for the MSI, so the
	// bundle lands beside the source. It used to be PRODUCT_NAME + "-" + PRODUCT_VERSION,
	// a RELATIVE name, which filepath.Abs then resolved against the process's working
	// directory - so `msis /BUILD some\far\away\setup.msis` wrote a 14 MB executable into
	// whatever directory the user happened to be in (issue #27).
	outputFile := vars.BuildTarget()
	if outputFile == "" {
		outputFile = strings.TrimSuffix(wxsFile, filepath.Ext(wxsFile))
		// The .wxs for an auto-bundle is "<name>-bundle.wxs"; the artifact users expect is
		// "<name>.exe", not "<name>-bundle.exe". As in NewBuilder, the stem that remains is
		// the source's own name and is kept whole rather than normalized again.
		outputFile = strings.TrimSuffix(outputFile, "-bundle") + ".exe"
	} else {
		outputFile = TargetPath(outputFile, "exe")
	}

	return &BundleBuilder{
		WxsFile:         wxsFile,
		OutputFile:      absPath(outputFile),
		TemplateFolder:  templateFolder,
		CustomTemplates: customTemplates,
		SourceDir:       sourceDir,
		Variables:       vars,
		RetainWxs:       retainWxs,
	}
}

// Build invokes WiX CLI to compile the bundle WXS into an EXE.
func (b *BundleBuilder) Build() error {
	if err := checkOutputWritable(b.OutputFile); err != nil {
		return err
	}

	// Build bundle (EULA acceptance, if required, is passed on the build command)
	if err := b.runWixBuild(); err != nil {
		return fmt.Errorf("wix build: %w", err)
	}

	b.cleanup()
	return nil
}

// runWixBuild executes wix build command for bundle.
func (b *BundleBuilder) runWixBuild() error {
	workDir, args := b.buildArgs()
	return runWix(workDir, args)
}

// buildArgs assembles the bundle's `wix build` invocation; see Builder.buildArgs.
func (b *BundleBuilder) buildArgs() (workDir string, args []string) {
	absWxsFile := absPath(b.WxsFile)
	workDir = filepath.Dir(absWxsFile)

	wxsFilename := filepath.Base(absWxsFile)
	args = []string{"build", wxsFilename}

	// Bundle-specific extensions (see setup.go for the canonical list)
	args = append(args, extArgs(bundleExtensions)...)

	// EULA acceptance (WiX 7+ only; no-op on WiX 6)
	args = append(args, eulaAcceptArgs(GetWixMajorVersion())...)

	// Bind paths: workDir, .msis source dir, custom templates, template folder (same order as the MSI build).
	args = append(args, bindPathArgs(workDir, b.SourceDir, b.CustomTemplates, b.TemplateFolder)...)

	// No PDB file
	args = append(args, "-pdbtype", "none")

	// Output file: the one absolute path the overwrite check already used
	args = append(args, outputArgs(b.OutputFile)...)
	return workDir, args
}

// cleanup removes temporary files unless retention is requested.
func (b *BundleBuilder) cleanup() {
	removeArtifacts(b.OutputFile, b.WxsFile, b.RetainWxs)
}
