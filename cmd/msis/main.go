// Copyright (c) 2013-2026, Gerson Kurz, NG Branch Technology GmbH
// MIT License

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gersonkurz/msis/internal/bundle"
	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/parser"
	"github.com/gersonkurz/msis/internal/template"
	"github.com/gersonkurz/msis/internal/variables"
	"github.com/gersonkurz/msis/internal/wix"
)

// Version is set via ldflags at build time
var Version = "3.0.0-dev"

type cliArgs struct {
	build           bool
	retainWxs       bool
	template        string
	templateFolder  string
	customTemplates string
	dryRun          bool
	status          bool
	files           []string
}

func main() {
	args := parseArgs()

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
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", filename, err)
			os.Exit(1)
		}
	}
}

func processFile(filename string, args *cliArgs) error {
	fmt.Printf("Processing %s...\n", filename)

	// Milestone 3.1 - Parse .msis into IR
	setup, err := parser.Parse(filename)
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}

	fmt.Printf("  Parsed: %d sets, %d features, %d top-level items\n",
		len(setup.Sets), len(setup.Features), len(setup.Items))

	// Check if this is a bundle
	isBundle := setup.IsSetupBundle()
	if isBundle {
		fmt.Println("  Type: Bundle (bootstrapper)")
	}

	// Milestone 3.2 - Variable resolution
	vars := variables.New()
	vars.LoadFromSetup(setup)
	if err := vars.ResolveAll(); err != nil {
		return fmt.Errorf("resolving variables: %w", err)
	}

	fmt.Printf("  Product: %s v%s (%s)\n",
		vars.ProductName(), vars.ProductVersion(), vars.Platform())

	workDir := filepath.Dir(filename)

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
	fmt.Printf("  Generated: %d directories, %d components\n",
		len(ctx.DirectoryTrees), componentCount)

	if args.dryRun {
		fmt.Println("  [dry-run] Parse and validate complete")
		return nil
	}

	// Milestone 3.4 - Template rendering
	renderer := template.NewRenderer(vars, templateFolder, customTemplates, output)

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

	// Determine output filename
	wxsFile := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".wxs"
	if vars.BuildTarget() != "" {
		wxsFile = strings.TrimSuffix(vars.BuildTarget(), filepath.Ext(vars.BuildTarget())) + ".wxs"
	}

	// Write WXS file
	if err := os.WriteFile(wxsFile, []byte(wxsContent), 0644); err != nil {
		return fmt.Errorf("writing WXS file: %w", err)
	}
	fmt.Printf("  Written: %s\n", wxsFile)

	// Milestone 3.5 - WiX CLI integration
	if args.build {
		if !wix.IsWixAvailable() {
			return fmt.Errorf("wix CLI not found in PATH; install WiX Toolset 6")
		}

		builder := wix.NewBuilder(vars, wxsFile, templateFolder, customTemplates, workDir, args.retainWxs)
		if err := builder.Build(); err != nil {
			return fmt.Errorf("building MSI: %w", err)
		}

		fmt.Printf("  Built: %s\n", vars.BuildTarget())
	}

	return nil
}

// processBundleFile generates a WiX Bundle (bootstrapper).
func processBundleFile(setup *ir.Setup, vars variables.Dictionary, workDir, templateFolder, customTemplates string, args *cliArgs) error {
	// Generate bundle chain
	gen := bundle.NewGenerator(setup, vars, workDir)
	bundleOutput, err := gen.Generate()
	if err != nil {
		return fmt.Errorf("generating bundle: %w", err)
	}

	prereqCount := len(setup.Bundle.Prerequisites)
	exeCount := len(setup.Bundle.ExePackages)
	fmt.Printf("  Generated: %d prerequisites, %d exe packages\n", prereqCount, exeCount)

	if args.dryRun {
		fmt.Println("  [dry-run] Parse and validate complete")
		return nil
	}

	// Render bundle template
	wxsContent, err := renderBundleTemplate(vars, templateFolder, customTemplates, bundleOutput, setup.Silent)
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
	fmt.Printf("  Written: %s\n", wxsFile)

	// Build bundle
	if args.build {
		if !wix.IsWixAvailable() {
			return fmt.Errorf("wix CLI not found in PATH; install WiX Toolset 6")
		}

		builder := wix.NewBundleBuilder(vars, wxsFile, templateFolder, customTemplates, args.retainWxs)
		if err := builder.Build(); err != nil {
			return fmt.Errorf("building bundle: %w", err)
		}

		fmt.Printf("  Built: %s.exe\n", baseName)
	}

	return nil
}

// renderBundleTemplate renders the bundle WXS template.
func renderBundleTemplate(vars variables.Dictionary, templateFolder, customTemplates string, bundleOutput *bundle.GeneratedBundle, silent bool) (string, error) {
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

	// Build context for template
	ctx := make(map[string]interface{})
	for k, v := range vars {
		ctx[k] = v
	}
	ctx["CHAIN"] = bundleOutput.ChainXML

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

	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "/") && !strings.Contains(arg, "\\") && !strings.Contains(arg, ":") {
			// /FLAG -> --flag (but not paths like /c/foo or /flag:value)
			converted := "--" + strings.ToLower(arg[1:])
			originalArgs[converted] = arg
			flags = append(flags, converted)
		} else if strings.HasPrefix(arg, "/") && strings.Contains(arg, ":") && !strings.HasPrefix(arg, "/c/") {
			// /FLAG:value -> --flag=value
			parts := strings.SplitN(arg, ":", 2)
			key := strings.ToLower(parts[0][1:])
			val := parts[1]
			converted := "--" + key + "=" + val
			originalArgs["--"+key] = "/" + strings.ToUpper(key)
			flags = append(flags, converted)
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
	return args
}

// discardWriter discards all writes (used to suppress flag package output)
type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

// getDefaultTemplateFolder returns the default template folder path.
// Search order:
// 1. %LOCALAPPDATA%\msis\templates (installed location)
// 2. Executable directory\templates (portable/dev)
// 3. Current directory\templates (fallback)
func getDefaultTemplateFolder() string {
	// 1. Check installed location: %LOCALAPPDATA%\msis\templates
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		installedPath := filepath.Join(localAppData, "msis", "templates")
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
		customPath := filepath.Join(localAppData, "msis", "custom")
		if _, err := os.Stat(customPath); err == nil {
			return customPath
		}
	}
	return ""
}

func printUsage() {
	fmt.Printf("MSIS - Version %s\n", Version)
	fmt.Printf("MSI-Simplified installer generator [%s/%s]\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Freeware written by Gerson Kurz (http://p-nand-q.com)\n")
	fmt.Println()
	fmt.Println("Usage: msis [OPTIONS] FILE [FILE...]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  /BUILD              Run WiX build tools automatically")
	fmt.Println("  /RETAINWXS          Retain WXS file after build")
	fmt.Println("  /TEMPLATE:NAME      Custom template to use")
	fmt.Println("  /TEMPLATEFOLDER:PATH   Base template folder (public defaults)")
	fmt.Println("  /CUSTOMTEMPLATES:PATH  Overlay folder for private assets (takes precedence)")
	fmt.Println("  /DRY-RUN            Parse and validate only, no output")
	fmt.Println("  /STATUS             Show configuration status")
	fmt.Println("  /?, /HELP           Show this help message")
	fmt.Println()
	fmt.Println("Template folder search order:")
	fmt.Println("  1. %LOCALAPPDATA%\\msis\\templates (installed)")
	fmt.Println("  2. <executable-dir>\\templates (portable)")
	fmt.Println("  3. .\\templates (current directory)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  msis setup.msis                          Generate .wxs file")
	fmt.Println("  msis /BUILD setup.msis                   Generate and build MSI")
	fmt.Println("  msis /BUILD /RETAINWXS setup.msis        Build and keep .wxs")
	fmt.Println("  msis /TEMPLATEFOLDER:templates /BUILD setup.msis")
	fmt.Println("  msis /DRY-RUN setup.msis                 Validate only")
}

func printStatus(args *cliArgs) {
	fmt.Printf("MSIS - Version %s\n", Version)
	fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()

	// WiX information
	fmt.Println("WiX Toolset:")
	wixPath := wix.GetWixPath()
	if wix.IsWixAvailable() {
		fmt.Printf("  Location: %s\n", wixPath)
		fmt.Printf("  Version:  %s\n", wix.GetWixVersion())

		// Show installed extensions
		extensions := wix.GetInstalledExtensions()
		if len(extensions) > 0 {
			fmt.Println("  Extensions:")
			for _, ext := range extensions {
				fmt.Printf("    - %s\n", ext)
			}
		}
	} else {
		fmt.Printf("  Location: (not found)\n")
		fmt.Println("  Install with: dotnet tool install --global wix")
	}
	fmt.Println()

	// Template folders
	fmt.Println("Template Folders:")

	// Determine effective template folder
	templateFolder := args.templateFolder
	if templateFolder == "" {
		templateFolder = getDefaultTemplateFolder()
	}
	fmt.Printf("  Base templates: %s", templateFolder)
	if _, err := os.Stat(templateFolder); err != nil {
		fmt.Print(" (not found)")
	}
	fmt.Println()

	// Custom templates
	customTemplates := args.customTemplates
	if customTemplates == "" {
		customTemplates = getDefaultCustomTemplates()
	}
	if customTemplates != "" {
		fmt.Printf("  Custom templates: %s", customTemplates)
		if _, err := os.Stat(customTemplates); err != nil {
			fmt.Print(" (not found)")
		}
		fmt.Println()
	} else {
		fmt.Println("  Custom templates: (none)")
	}
	fmt.Println()

	// Show search paths
	fmt.Println("Template Search Order:")
	fmt.Println("  1. %LOCALAPPDATA%\\msis\\templates (installed)")
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		installedPath := filepath.Join(localAppData, "msis", "templates")
		if _, err := os.Stat(installedPath); err == nil {
			fmt.Printf("     -> %s (found)\n", installedPath)
		} else {
			fmt.Printf("     -> %s (not found)\n", installedPath)
		}
	}

	fmt.Println("  2. <executable-dir>\\templates (portable)")
	if exePath, err := os.Executable(); err == nil {
		exeTemplates := filepath.Join(filepath.Dir(exePath), "templates")
		if _, err := os.Stat(exeTemplates); err == nil {
			fmt.Printf("     -> %s (found)\n", exeTemplates)
		} else {
			fmt.Printf("     -> %s (not found)\n", exeTemplates)
		}
	}

	fmt.Println("  3. .\\templates (current directory)")
	if _, err := os.Stat("templates"); err == nil {
		cwd, _ := os.Getwd()
		fmt.Printf("     -> %s (found)\n", filepath.Join(cwd, "templates"))
	} else {
		fmt.Println("     -> (not found)")
	}
}
