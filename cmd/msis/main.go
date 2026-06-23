// Copyright (c) 2013-2026, Gerson Kurz, NG Branch Technology GmbH
// MIT License

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gersonkurz/msis/internal/bundle"
	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/parser"
	"github.com/gersonkurz/msis/internal/prereqcache"
	"github.com/gersonkurz/msis/internal/template"
	"github.com/gersonkurz/msis/internal/variables"
	"github.com/gersonkurz/msis/internal/wix"
)

// Version and BuildTime are set via ldflags at build time
var Version = "3.0.3"
var BuildTime = ""

type cliArgs struct {
	build           bool
	retainWxs       bool
	template        string
	templateFolder  string
	customTemplates string
	dryRun          bool
	status          bool
	standalone      bool              // Skip auto-bundling, use launch conditions only
	noColor         bool              // Disable colored output
	setupWix        bool              // /SETUP-WIX: install/repair WiX toolset + extensions
	wixVersion      string            // /WIX-VERSION:VER override for /SETUP-WIX
	setOverrides    map[string]string // /SET:NAME=VALUE overrides
	files           []string
}

func main() {
	args := parseArgs()

	// Apply --no-color flag
	if args.noColor {
		cli.DisableColors()
	}

	if args.setupWix {
		if err := runSetupWix(args); err != nil {
			fmt.Fprintf(os.Stderr, "%s %v\n", cli.Error("Setup failed:"), err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if args.status {
		printStatus(args)
		os.Exit(0)
	}

	if len(args.files) == 0 {
		printUsage()
		os.Exit(10)
	}

	for _, filename := range args.files {
		if err := processFile(filename, args); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", cli.Error("Error processing"), cli.Filename(filename), err)
			os.Exit(1)
		}
	}
}

// setupHint points users at the self-provisioning command when a build fails in a way
// that a missing WiX tool or extension would explain.
const setupHint = "if WiX or its extensions are missing, run: msis /SETUP-WIX"

// validateInstallerHooks fails the build, with a clear message, when USE_INSTALLER_HOOKS=True but
// the native hook DLL for the target platform is unavailable. x86/x64/arm64 are all supported
// (msis ships an arch-native DLL for each); a missing DLL is a clear, early failure.
//
// bindDirs must be the same set of bind paths, in the same order, that the WiX build resolves the
// template's "<arch>/<DLL_ENTRY>" Binary against (work dir, .msis source dir, custom templates,
// template folder) so validation is never stricter than the build itself — legacy setups may carry
// the DLL beside the .msis or the generated WXS.
func validateInstallerHooks(vars variables.Dictionary, bindDirs []string) error {
	if !vars.GetBool("USE_INSTALLER_HOOKS") {
		return nil
	}
	dllEntry := vars["DLL_ENTRY"]
	if dllEntry == "" {
		return fmt.Errorf("USE_INSTALLER_HOOKS=True requires DLL_ENTRY to name the native hook DLL")
	}
	archFolder := vars.HookDllDir()
	var checked []string
	seen := map[string]bool{}
	for _, dir := range bindDirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		cand := filepath.Join(dir, archFolder, dllEntry)
		if _, err := os.Stat(cand); err == nil {
			return nil
		}
		checked = append(checked, cand)
	}
	return fmt.Errorf("USE_INSTALLER_HOOKS=True but the native hook DLL %q was not found for platform %s "+
		"(looked in: %s); build it with 'just build-hooks' or place it on a bind path",
		dllEntry, archFolder, strings.Join(checked, ", "))
}

// runSetupWix installs/repairs the WiX toolset and the extensions msis needs.
func runSetupWix(args *cliArgs) error {
	version := args.wixVersion
	if version == "" {
		version = wix.DefaultVersion
	}

	fmt.Printf("%s WiX %s\n", cli.Bold("Setting up"), version)

	progress := func(msg string) { fmt.Printf("  %s\n", cli.Info(msg)) }
	if err := wix.EnsureWix(version, progress); err != nil {
		return err
	}

	fmt.Printf("%s WiX %s ready with the extensions msis needs.\n", cli.Success("Done:"), version)

	// Warn if `wix` on PATH is a different install than the one msis uses.
	if pathWix, lookErr := exec.LookPath("wix"); lookErr == nil {
		if resolved := wix.GetWixPath(); resolved != "wix" && !strings.EqualFold(pathWix, resolved) {
			fmt.Printf("  %s\n", cli.Warning(fmt.Sprintf("note: 'wix' on PATH (%s) differs from the tool msis uses (%s); msis uses the latter.", pathWix, resolved)))
		}
	}
	return nil
}

func processFile(filename string, args *cliArgs) error {
	fmt.Printf("Processing %s...\n", cli.Filename(filename))

	// Milestone 3.1 - Parse .msis into IR
	setup, err := parser.Parse(filename)
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}

	fmt.Printf("  Parsed: %s sets, %s features, %s top-level items\n",
		cli.Number(fmt.Sprintf("%d", len(setup.Sets))),
		cli.Number(fmt.Sprintf("%d", len(setup.Features))),
		cli.Number(fmt.Sprintf("%d", len(setup.Items))))

	// Check if this is a bundle
	isBundle := setup.IsSetupBundle()
	if isBundle {
		fmt.Printf("  Type: %s\n", cli.Info("Bundle (bootstrapper)"))
	}

	// Milestone 3.2 - Variable resolution
	vars := variables.New()
	vars.LoadFromSetup(setup)

	// Apply /SET: command-line overrides
	for name, value := range args.setOverrides {
		vars.Set(name, value)
		fmt.Printf("  Override: %s=%s\n", cli.Info(name), cli.Filename(value))
	}

	if err := vars.ResolveAll(); err != nil {
		return fmt.Errorf("resolving variables: %w", err)
	}

	fmt.Printf("  Product: %s v%s (%s)\n",
		cli.Bold(vars.ProductName()), vars.ProductVersion(), vars.Platform())

	// Check for deprecated variables
	deprecatedWarnings := vars.CheckDeprecated()
	if len(deprecatedWarnings) > 0 {
		for _, warning := range deprecatedWarnings {
			fmt.Printf("  %s\n", cli.Warning("Warning: "+warning))
		}
	}

	// Warn about dangerous / ineffective installer-hook variable combinations.
	for _, warning := range vars.CheckInstallerHookUsage() {
		fmt.Printf("  %s\n", cli.Warning("Warning: "+warning))
	}

	workDir := filepath.Dir(filename)

	// Warn when VC++ runtime files appear to be bundled without <requires>.
	if len(setup.Requires) == 0 && len(deprecatedWarnings) == 0 {
		if hasVCRedistSources(setup) || hasVCRedistFolder(workDir) {
			fmt.Printf("  %s\n", cli.Warning("Warning: VC++ runtime files detected but no <requires> element. msis-3.x no longer bundles VC runtimes implicitly (msis-2.x did). Add <requires type=\"vcredist\" version=\"2022\"/> or provide prerequisites."))
		}
	}

	// Determine template folder
	templateFolder := args.templateFolder
	if templateFolder == "" {
		templateFolder = getDefaultTemplateFolder()
	}

	// Determine custom templates folder
	customTemplates := args.customTemplates
	if customTemplates == "" {
		customTemplates = getDefaultCustomTemplates()
	}

	// Branch based on bundle vs MSI
	if isBundle {
		return processBundleFile(setup, vars, workDir, templateFolder, customTemplates, args)
	}
	return processMSIFile(setup, vars, workDir, templateFolder, customTemplates, filename, args)
}

// processMSIFile generates a standard MSI package.
func processMSIFile(setup *ir.Setup, vars variables.Dictionary, workDir, templateFolder, customTemplates, filename string, args *cliArgs) error {
	// Determine if auto-bundling is needed
	needsAutoBundle := len(setup.Requires) > 0 && !args.standalone

	if needsAutoBundle {
		fmt.Printf("  Requirements: %s (will auto-bundle)\n", cli.Number(fmt.Sprintf("%d", len(setup.Requires))))
	} else if len(setup.Requires) > 0 {
		fmt.Printf("  Requirements: %s (standalone mode, using launch conditions)\n", cli.Number(fmt.Sprintf("%d", len(setup.Requires))))
	}

	// Milestone 3.3 - WXS generation
	ctx := generator.NewContext(setup, vars, workDir)
	output, err := ctx.Generate()
	if err != nil {
		return fmt.Errorf("generating WXS: %w", err)
	}

	// Count components generated
	componentCount := 0
	for _, compIDs := range ctx.FeatureComponents {
		componentCount += len(compIDs)
	}
	fmt.Printf("  Generated: %s directories, %s components\n",
		cli.Number(fmt.Sprintf("%d", len(ctx.DirectoryTrees))),
		cli.Number(fmt.Sprintf("%d", componentCount)))

	if args.dryRun {
		fmt.Printf("  %s\n", cli.Info("[dry-run] Parse and validate complete"))
		return nil
	}

	// Milestone 3.4 - Template rendering
	renderer := template.NewRenderer(vars, templateFolder, customTemplates, output)

	// The .msis directory is the first logo search root (matches WiX's bind-path order).
	if absWork, err := filepath.Abs(workDir); err == nil {
		renderer.SourceDir = absWork
	} else {
		renderer.SourceDir = workDir
	}

	// Support custom template override via --template flag
	if args.template != "" {
		renderer.SetCustomTemplate(args.template)
	}

	// Select silent or regular template based on setup.Silent
	var wxsContent string
	if setup.Silent {
		// Try silent template first
		wxsContent, err = renderer.RenderSilent()
		if err != nil {
			return fmt.Errorf("rendering silent template: %w", err)
		}
		if wxsContent == "" {
			// No silent template available, fall back to regular
			wxsContent, err = renderer.Render()
			if err != nil {
				return fmt.Errorf("rendering template: %w", err)
			}
		}
	} else {
		wxsContent, err = renderer.Render()
		if err != nil {
			return fmt.Errorf("rendering template: %w", err)
		}
	}

	// Surface any logo-resolution warnings (missing branding assets) rather than silently
	// falling back to the WiX defaults.
	for _, w := range renderer.LogoWarnings {
		fmt.Printf("  %s\n", cli.Warning("Warning: "+w))
	}

	// Determine output filename
	wxsFile := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".wxs"
	if vars.BuildTarget() != "" {
		wxsFile = strings.TrimSuffix(vars.BuildTarget(), filepath.Ext(vars.BuildTarget())) + ".wxs"
	}

	// Write WXS file
	if err := os.WriteFile(wxsFile, []byte(wxsContent), 0644); err != nil {
		return fmt.Errorf("writing WXS file: %w", err)
	}
	fmt.Printf("  Written: %s\n", cli.Filename(wxsFile))

	// Milestone 3.5 - WiX CLI integration
	if args.build {
		// Mirror the WiX build's bind-path resolution order (see wix.Builder.runWixBuild).
		if err := validateInstallerHooks(vars, []string{filepath.Dir(wxsFile), workDir, customTemplates, templateFolder}); err != nil {
			return err
		}
		if !wix.IsWixAvailable() {
			return fmt.Errorf("wix CLI not found; run: msis /SETUP-WIX")
		}

		builder := wix.NewBuilder(vars, wxsFile, templateFolder, customTemplates, workDir, args.retainWxs)
		if err := builder.Build(); err != nil {
			return fmt.Errorf("building MSI: %w\n  hint: %s", err, setupHint)
		}

		// Compute actual MSI output path (same logic as wix.NewBuilder)
		msiPath := vars.BuildTarget()
		if msiPath == "" {
			msiPath = strings.TrimSuffix(wxsFile, filepath.Ext(wxsFile)) + ".msi"
		}
		fmt.Printf("  %s %s\n", cli.Success("Built:"), cli.Filename(msiPath))

		// Milestone 6.2 - Auto-bundle if requirements present
		if needsAutoBundle {
			return processAutoBundle(setup, vars, workDir, templateFolder, customTemplates, msiPath, args)
		}
	}

	return nil
}

// processAutoBundle generates a bundle wrapper for an MSI with prerequisites.
func processAutoBundle(setup *ir.Setup, vars variables.Dictionary, workDir, templateFolder, customTemplates, msiPath string, args *cliArgs) error {
	fmt.Printf("  %s\n", cli.Info("Generating auto-bundle wrapper..."))

	// Convert and validate requirements
	prereqs, err := bundle.RequirementsToPrerequisites(setup.Requires)
	if err != nil {
		return fmt.Errorf("invalid requirement: %w", err)
	}

	// Generate bundle chain
	gen := bundle.NewAutoBundleGenerator(vars, workDir, msiPath, prereqs)

	// Enable caching - download prerequisites if needed
	cache, err := prereqcache.NewCache()
	if err != nil {
		fmt.Printf("  %s: could not initialize prerequisite cache: %v\n", cli.Warning("Warning"), err)
		fmt.Printf("  %s\n", cli.Info("Prerequisites will be expected in local 'prerequisites' folder"))
	} else {
		gen.SetCache(cache)

		// Ensure all prerequisites are cached (download if needed)
		fmt.Printf("  %s\n", cli.Info("Checking prerequisites..."))
		progress := func(msg string) {
			fmt.Printf("    %s\n", cli.Info(msg))
		}
		if err := gen.EnsurePrerequisites(progress); err != nil {
			return fmt.Errorf("ensuring prerequisites: %w", err)
		}
	}

	bundleOutput, err := gen.Generate()
	if err != nil {
		return fmt.Errorf("generating auto-bundle: %w", err)
	}

	fmt.Printf("  Auto-bundle: %s prerequisites + MSI\n", cli.Number(fmt.Sprintf("%d", len(prereqs))))

	// Render bundle template
	wxsContent, err := renderBundleTemplate(vars, workDir, templateFolder, customTemplates, bundleOutput, setup.Silent)
	if err != nil {
		return fmt.Errorf("rendering bundle template: %w", err)
	}

	// Determine output filename (bundle produces .exe)
	baseName := strings.TrimSuffix(msiPath, filepath.Ext(msiPath))
	wxsFile := baseName + "-bundle.wxs"

	// Write WXS file
	if err := os.WriteFile(wxsFile, []byte(wxsContent), 0644); err != nil {
		return fmt.Errorf("writing bundle WXS file: %w", err)
	}
	fmt.Printf("  Written: %s\n", cli.Filename(wxsFile))

	// Build bundle
	bundleBuilder := wix.NewBundleBuilder(vars, wxsFile, templateFolder, customTemplates, workDir, args.retainWxs)
	if err := bundleBuilder.Build(); err != nil {
		return fmt.Errorf("building bundle: %w\n  hint: %s", err, setupHint)
	}

	fmt.Printf("  %s %s\n", cli.Success("Built:"), cli.Filename(baseName+".exe"))

	return nil
}

func hasVCRedistSources(setup *ir.Setup) bool {
	if scanItemsForVCRedist(setup.Items) {
		return true
	}
	return scanFeaturesForVCRedist(setup.Features)
}

func scanFeaturesForVCRedist(features []ir.Feature) bool {
	for _, feature := range features {
		if scanItemsForVCRedist(feature.Items) {
			return true
		}
		if scanFeaturesForVCRedist(feature.SubFeatures) {
			return true
		}
	}
	return false
}

func scanItemsForVCRedist(items []ir.Item) bool {
	for _, item := range items {
		files, ok := item.(ir.Files)
		if !ok {
			continue
		}
		source := strings.ToLower(files.Source)
		if strings.Contains(source, "vc_redist") || strings.Contains(source, "vcredist") {
			return true
		}
	}
	return false
}

func hasVCRedistFolder(workDir string) bool {
	candidates := []string{
		filepath.Join(workDir, "Setup", "Tools", "vc_redist"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// processBundleFile generates a WiX Bundle (bootstrapper).
func processBundleFile(setup *ir.Setup, vars variables.Dictionary, workDir, templateFolder, customTemplates string, args *cliArgs) error {
	// Generate bundle chain
	gen := bundle.NewGenerator(setup, vars, workDir)

	// Enable caching if building (download prerequisites if needed)
	if args.build && len(setup.Bundle.Prerequisites) > 0 {
		cache, err := prereqcache.NewCache()
		if err != nil {
			fmt.Printf("  %s: could not initialize prerequisite cache: %v\n", cli.Warning("Warning"), err)
			fmt.Printf("  %s\n", cli.Info("Prerequisites will be expected in local 'prerequisites' folder"))
		} else {
			gen.SetCache(cache)

			// Ensure all prerequisites are cached (download if needed)
			fmt.Printf("  %s\n", cli.Info("Checking prerequisites..."))
			progress := func(msg string) {
				fmt.Printf("    %s\n", cli.Info(msg))
			}
			if err := gen.EnsurePrerequisites(progress); err != nil {
				return fmt.Errorf("ensuring prerequisites: %w", err)
			}
		}
	}

	bundleOutput, err := gen.Generate()
	if err != nil {
		return fmt.Errorf("generating bundle: %w", err)
	}

	prereqCount := len(setup.Bundle.Prerequisites)
	exeCount := len(setup.Bundle.ExePackages)
	fmt.Printf("  Generated: %s prerequisites, %s exe packages\n",
		cli.Number(fmt.Sprintf("%d", prereqCount)),
		cli.Number(fmt.Sprintf("%d", exeCount)))

	if args.dryRun {
		fmt.Printf("  %s\n", cli.Info("[dry-run] Parse and validate complete"))
		return nil
	}

	// Render bundle template
	wxsContent, err := renderBundleTemplate(vars, workDir, templateFolder, customTemplates, bundleOutput, setup.Silent)
	if err != nil {
		return fmt.Errorf("rendering bundle template: %w", err)
	}

	// Determine output filename (bundle produces .exe)
	baseName := vars.BuildTarget()
	if baseName == "" {
		baseName = vars.ProductName() + "-" + vars.ProductVersion()
	}
	baseName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
	wxsFile := baseName + "-bundle.wxs"

	// Write WXS file
	if err := os.WriteFile(wxsFile, []byte(wxsContent), 0644); err != nil {
		return fmt.Errorf("writing bundle WXS file: %w", err)
	}
	fmt.Printf("  Written: %s\n", cli.Filename(wxsFile))

	// Build bundle
	if args.build {
		if !wix.IsWixAvailable() {
			return fmt.Errorf("wix CLI not found; run: msis /SETUP-WIX")
		}

		builder := wix.NewBundleBuilder(vars, wxsFile, templateFolder, customTemplates, workDir, args.retainWxs)
		if err := builder.Build(); err != nil {
			return fmt.Errorf("building bundle: %w\n  hint: %s", err, setupHint)
		}

		fmt.Printf("  %s %s\n", cli.Success("Built:"), cli.Filename(baseName+".exe"))
	}

	return nil
}

// renderBundleTemplate renders the bundle WXS template. sourceDir is the .msis directory, used as
// the first logo search root so LOGO_PREFIX/LOGO_BOOTSTRAP resolve the same way they do for the MSI.
func renderBundleTemplate(vars variables.Dictionary, sourceDir, templateFolder, customTemplates string, bundleOutput *bundle.GeneratedBundle, silent bool) (string, error) {
	// Read bundle template
	templateName := "bundle.wxs"
	if silent {
		templateName = "bundle-silent.wxs"
	}

	templatePath := filepath.Join(templateFolder, templateName)
	if customTemplates != "" {
		customPath := filepath.Join(customTemplates, templateName)
		if _, err := os.Stat(customPath); err == nil {
			templatePath = customPath
		}
	}

	tmplContent, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading bundle template: %w", err)
	}

	// Build context via the shared helper so the bundle logo resolves from LOGO_PREFIX too, not
	// just an explicit LOGO_BOOTSTRAP. Logo search roots mirror WiX's bind-path order.
	absSource := sourceDir
	if abs, err := filepath.Abs(sourceDir); err == nil {
		absSource = abs
	}
	ctx, logoWarnings := template.BuildBundleContext(vars, bundleOutput.ChainXML, absSource, customTemplates, templateFolder)
	for _, w := range logoWarnings {
		fmt.Printf("  %s\n", cli.Warning("Warning: "+w))
	}

	// Render using raymond (same as MSI templates)
	return template.RenderString(string(tmplContent), ctx)
}

func parseArgs() *cliArgs {
	// Convert /FLAG syntax to --flag for flag package compatibility
	// Keep track of original args for error messages
	originalArgs := make(map[string]string)

	// Separate flags and files so flags can appear anywhere on command line
	// (Go's flag package stops parsing at first non-flag argument)
	var flags []string
	var files []string

	setOverrides := make(map[string]string)

	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "/") && !strings.Contains(arg, "\\") && !strings.Contains(arg, ":") {
			// /FLAG -> --flag (but not paths like /c/foo or /flag:value)
			converted := "--" + strings.ToLower(arg[1:])
			originalArgs[converted] = arg
			flags = append(flags, converted)
		} else if strings.HasPrefix(arg, "/") && strings.Contains(arg, ":") && !strings.HasPrefix(arg, "/c/") {
			// Check for /SET:NAME=VALUE before generic /FLAG:value handling
			upper := strings.ToUpper(arg)
			if strings.HasPrefix(upper, "/SET:") {
				rest := arg[5:] // preserve original case of NAME=VALUE
				eqIdx := strings.IndexByte(rest, '=')
				if eqIdx < 1 {
					fmt.Fprintf(os.Stderr, "Invalid /SET format: %s (use /SET:NAME=VALUE)\n\n", arg)
					printUsage()
					os.Exit(2)
				}
				setOverrides[rest[:eqIdx]] = rest[eqIdx+1:]
			} else {
				// /FLAG:value -> --flag=value
				parts := strings.SplitN(arg, ":", 2)
				key := strings.ToLower(parts[0][1:])
				val := parts[1]
				converted := "--" + key + "=" + val
				originalArgs["--"+key] = "/" + strings.ToUpper(key)
				flags = append(flags, converted)
			}
		} else if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			// Unix-style flags (pass through)
			flags = append(flags, arg)
		} else {
			// Not a flag - it's a file
			files = append(files, arg)
		}
	}

	// Combine: flags first, then files
	newArgs := append(flags, files...)

	args := &cliArgs{}

	// Create a custom flag set to control error handling
	fs := flag.NewFlagSet("msis", flag.ContinueOnError)

	// Suppress default error output - we'll handle it ourselves
	fs.SetOutput(&discardWriter{})

	fs.BoolVar(&args.build, "build", false, "")
	fs.BoolVar(&args.retainWxs, "retainwxs", false, "")
	fs.StringVar(&args.template, "template", "", "")
	fs.StringVar(&args.templateFolder, "templatefolder", "", "")
	fs.StringVar(&args.customTemplates, "customtemplates", "", "")
	fs.BoolVar(&args.dryRun, "dry-run", false, "")
	fs.BoolVar(&args.status, "status", false, "")
	fs.BoolVar(&args.standalone, "standalone", false, "")
	fs.BoolVar(&args.noColor, "no-color", false, "")
	fs.BoolVar(&args.setupWix, "setup-wix", false, "")
	fs.StringVar(&args.wixVersion, "wix-version", "", "")

	// Help flags
	var showHelp bool
	fs.BoolVar(&showHelp, "help", false, "")
	fs.BoolVar(&showHelp, "h", false, "")
	fs.BoolVar(&showHelp, "?", false, "")

	if err := fs.Parse(newArgs); err != nil {
		// Extract the unknown flag from the error message
		errStr := err.Error()
		if strings.Contains(errStr, "flag provided but not defined:") {
			// Parse out the flag name
			parts := strings.SplitN(errStr, ":", 2)
			if len(parts) == 2 {
				badFlag := strings.TrimSpace(parts[1])
				// Convert back to Windows-style for the error message
				if orig, ok := originalArgs[badFlag]; ok {
					fmt.Fprintf(os.Stderr, "Unknown option: %s\n\n", orig)
				} else {
					fmt.Fprintf(os.Stderr, "Unknown option: %s\n\n", badFlag)
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n\n", err)
		}
		printUsage()
		os.Exit(2)
	}

	if showHelp {
		printUsage()
		os.Exit(0)
	}

	args.files = fs.Args()
	args.setOverrides = setOverrides
	return args
}

// discardWriter discards all writes (used to suppress flag package output)
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

// getDefaultTemplateFolder returns the default template folder path.
// Search order:
// 1. %LOCALAPPDATA%\MSIS\templates (installed location)
// 2. Executable directory\templates (portable/dev)
// 3. Current directory\templates (fallback)
func getDefaultTemplateFolder() string {
	// 1. Check installed location: %LOCALAPPDATA%\MSIS\templates
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		installedPath := filepath.Join(localAppData, "MSIS", "templates")
		if _, err := os.Stat(installedPath); err == nil {
			return installedPath
		}
	}

	// 2. Check executable directory
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		exeTemplates := filepath.Join(exeDir, "templates")
		if _, err := os.Stat(exeTemplates); err == nil {
			return exeTemplates
		}
	}

	// 3. Fallback to relative path
	return "templates"
}

// getDefaultCustomTemplates returns the default custom templates folder path.
func getDefaultCustomTemplates() string {
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		customPath := filepath.Join(localAppData, "MSIS", "custom")
		if _, err := os.Stat(customPath); err == nil {
			return customPath
		}
	}
	return ""
}

func printUsage() {
	fmt.Printf("MSIS - Version %s\n", cli.Bold(Version))
	fmt.Printf("MSI-Simplified installer generator [%s/%s]\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Freeware written by Gerson Kurz (%s)\n", cli.Info("http://p-nand-q.com"))
	fmt.Println()
	fmt.Printf("Usage: %s [OPTIONS] FILE [FILE...]\n", cli.Bold("msis"))
	fmt.Println()
	fmt.Println(cli.Bold("Options:"))
	fmt.Printf("  %s              Run WiX build tools automatically\n", cli.Info("/BUILD"))
	fmt.Printf("  %s     Override or add a <set> variable\n", cli.Info("/SET:NAME=VALUE"))
	fmt.Printf("  %s          Retain WXS file after build\n", cli.Info("/RETAINWXS"))
	fmt.Printf("  %s      Custom template to use\n", cli.Info("/TEMPLATE:NAME"))
	fmt.Printf("  %s   Base template folder (public defaults)\n", cli.Info("/TEMPLATEFOLDER:PATH"))
	fmt.Printf("  %s  Overlay folder for private assets (takes precedence)\n", cli.Info("/CUSTOMTEMPLATES:PATH"))
	fmt.Printf("  %s            Parse and validate only, no output\n", cli.Info("/DRY-RUN"))
	fmt.Printf("  %s         Skip auto-bundling, use launch conditions only\n", cli.Info("/STANDALONE"))
	fmt.Printf("  %s           Disable colored output\n", cli.Info("/NO-COLOR"))
	fmt.Printf("  %s          Install/repair the WiX toolset + extensions\n", cli.Info("/SETUP-WIX"))
	fmt.Printf("  %s With /SETUP-WIX: install a specific WiX version\n", cli.Info("/WIX-VERSION:VER"))
	fmt.Printf("  %s             Show configuration status\n", cli.Info("/STATUS"))
	fmt.Printf("  %s           Show this help message\n", cli.Info("/?, /HELP"))
	fmt.Println()
	fmt.Println(cli.Bold("Template folder search order:"))
	fmt.Println("  1. %LOCALAPPDATA%\\MSIS\\templates (installed)")
	fmt.Println("  2. <executable-dir>\\templates (portable)")
	fmt.Println("  3. .\\templates (current directory)")
	fmt.Println()
	fmt.Println(cli.Bold("Examples:"))
	fmt.Printf("  %s                          Generate .wxs file\n", cli.Filename("msis setup.msis"))
	fmt.Printf("  %s                   Generate and build MSI\n", cli.Filename("msis /BUILD setup.msis"))
	fmt.Printf("  %s        Build and keep .wxs\n", cli.Filename("msis /BUILD /RETAINWXS setup.msis"))
	fmt.Printf("  %s       Build MSI only (no auto-bundle)\n", cli.Filename("msis /BUILD /STANDALONE setup.msis"))
	fmt.Printf("  %s\n", cli.Filename("msis /SET:PRODUCT_VERSION=2.0.0 /BUILD setup.msis"))
	fmt.Printf("  %s                 Validate only\n", cli.Filename("msis /DRY-RUN setup.msis"))
	fmt.Printf("  %s                      Install WiX + extensions\n", cli.Filename("msis /SETUP-WIX"))
}

func printStatus(args *cliArgs) {
	if BuildTime != "" {
		fmt.Printf("MSIS - Version %s (built %s)\n", cli.Bold(Version), cli.Info(BuildTime))
	} else {
		fmt.Printf("MSIS - Version %s\n", cli.Bold(Version))
	}
	fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	// Prerequisite cache information
	fmt.Println(cli.Bold("Prerequisite Cache:"))
	cacheDir := prereqcache.GetDefaultCacheDir()
	fmt.Printf("  Location: %s\n", cli.Filename(cacheDir))
	if cache := prereqcache.NewCacheReadOnly(); cache != nil {
		if cached, err := cache.ListCached(); err == nil && len(cached) > 0 {
			fmt.Printf("  Cached files: %s\n", cli.Number(fmt.Sprintf("%d", len(cached))))
			for _, f := range cached {
				fmt.Printf("    - %s\n", cli.Filename(f))
			}
		} else {
			fmt.Printf("  Cached files: %s\n", cli.Info("(none)"))
		}
	} else {
		fmt.Printf("  Cached files: %s\n", cli.Info("(cache directory not created yet)"))
	}
	fmt.Println()

	// WiX information
	fmt.Println(cli.Bold("WiX Toolset:"))
	wixPath := wix.GetWixPath()
	if wix.IsWixAvailable() {
		fmt.Printf("  Location: %s\n", cli.Filename(wixPath))
		fmt.Printf("  Version:  %s\n", cli.Success(wix.GetWixVersion()))

		// Show EULA handling for the detected major version
		if major := wix.GetWixMajorVersion(); major >= 7 {
			fmt.Printf("  EULA:     %s\n", cli.Info("OSMF (builds pass --acceptEula; persistent acceptance via msis /SETUP-WIX)"))
		} else if major > 0 {
			fmt.Printf("  EULA:     %s\n", cli.Info("none (WiX 6 has no EULA gate)"))
		}

		// Show installed extensions
		extensions := wix.GetInstalledExtensions()
		if len(extensions) > 0 {
			fmt.Println("  Extensions:")
			for _, ext := range extensions {
				fmt.Printf("    - %s\n", ext)
			}
		}
	} else {
		fmt.Printf("  Location: %s\n", cli.Warning("(not found)"))
		fmt.Printf("  Install with: %s\n", cli.Info("msis /SETUP-WIX"))
	}
	fmt.Println()

	// Template folders
	fmt.Println(cli.Bold("Template Folders:"))

	// Determine effective template folder
	templateFolder := args.templateFolder
	if templateFolder == "" {
		templateFolder = getDefaultTemplateFolder()
	}
	if _, err := os.Stat(templateFolder); err != nil {
		fmt.Printf("  Base templates: %s %s\n", cli.Filename(templateFolder), cli.Warning("(not found)"))
	} else {
		fmt.Printf("  Base templates: %s\n", cli.Filename(templateFolder))
	}

	// Custom templates
	customTemplates := args.customTemplates
	if customTemplates == "" {
		customTemplates = getDefaultCustomTemplates()
	}
	if customTemplates != "" {
		if _, err := os.Stat(customTemplates); err != nil {
			fmt.Printf("  Custom templates: %s %s\n", cli.Filename(customTemplates), cli.Warning("(not found)"))
		} else {
			fmt.Printf("  Custom templates: %s\n", cli.Filename(customTemplates))
		}
	} else {
		fmt.Printf("  Custom templates: %s\n", cli.Info("(none)"))
	}
	fmt.Println()

	// Show search paths
	fmt.Println(cli.Bold("Template Search Order:"))
	fmt.Println("  1. %LOCALAPPDATA%\\MSIS\\templates (installed)")
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		installedPath := filepath.Join(localAppData, "MSIS", "templates")
		if _, err := os.Stat(installedPath); err == nil {
			fmt.Printf("     -> %s %s\n", cli.Filename(installedPath), cli.Success("(found)"))
		} else {
			fmt.Printf("     -> %s %s\n", cli.Filename(installedPath), cli.Warning("(not found)"))
		}
	}

	fmt.Println("  2. <executable-dir>\\templates (portable)")
	if exePath, err := os.Executable(); err == nil {
		exeTemplates := filepath.Join(filepath.Dir(exePath), "templates")
		if _, err := os.Stat(exeTemplates); err == nil {
			fmt.Printf("     -> %s %s\n", cli.Filename(exeTemplates), cli.Success("(found)"))
		} else {
			fmt.Printf("     -> %s %s\n", cli.Filename(exeTemplates), cli.Warning("(not found)"))
		}
	}

	fmt.Println("  3. .\\templates (current directory)")
	if _, err := os.Stat("templates"); err == nil {
		cwd, _ := os.Getwd()
		fmt.Printf("     -> %s %s\n", cli.Filename(filepath.Join(cwd, "templates")), cli.Success("(found)"))
	} else {
		fmt.Printf("     -> %s\n", cli.Warning("(not found)"))
	}
}
