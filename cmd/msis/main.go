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

	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/parser"
	"github.com/gersonkurz/msis/internal/variables"
)

// Version is set via ldflags at build time
var Version = "3.0.0-dev"

type cliArgs struct {
	build          bool
	retainWxs      bool
	template       string
	templateFolder string
	dryRun         bool
	files          []string
}

func main() {
	args := parseArgs()

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

	if setup.IsSetupBundle() {
		fmt.Println("  Type: Bundle (multi-MSI installer)")
	}

	// Milestone 3.2 - Variable resolution
	vars := variables.New()
	vars.LoadFromSetup(setup)
	if err := vars.ResolveAll(); err != nil {
		return fmt.Errorf("resolving variables: %w", err)
	}

	fmt.Printf("  Product: %s v%s (%s)\n",
		vars.ProductName(), vars.ProductVersion(), vars.Platform())

	// Milestone 3.3 - WXS generation
	workDir := filepath.Dir(filename)
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

	// TODO: Milestone 3.4 - Template rendering
	// TODO: Milestone 3.5 - WiX CLI integration

	if args.build {
		fmt.Printf("  [build] Would generate: %s\n", vars.BuildTarget())
		if !args.retainWxs {
			fmt.Println("  [build] Would delete .wxs after build")
		}
	}

	// For now, output the generated XML fragments to stdout (for debugging)
	if !args.build && output.DirectoryXML != "" {
		fmt.Println("\n<!-- Directory Structure -->")
		fmt.Print(output.DirectoryXML)
		fmt.Println("\n<!-- Features -->")
		fmt.Print(output.FeatureXML)
	}

	return nil
}

func parseArgs() *cliArgs {
	// Convert /FLAG syntax to --flag for flag package compatibility
	newArgs := make([]string, 0, len(os.Args))
	newArgs = append(newArgs, os.Args[0])

	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "/") && !strings.Contains(arg, "\\") && !strings.Contains(arg, ":") {
			// /FLAG -> --flag (but not paths like /c/foo or /flag:value)
			newArgs = append(newArgs, "--"+strings.ToLower(arg[1:]))
		} else if strings.HasPrefix(arg, "/") && strings.Contains(arg, ":") && !strings.HasPrefix(arg, "/c/") {
			// /FLAG:value -> --flag=value
			parts := strings.SplitN(arg, ":", 2)
			key := strings.ToLower(parts[0][1:])
			val := parts[1]
			newArgs = append(newArgs, "--"+key+"="+val)
		} else {
			newArgs = append(newArgs, arg)
		}
	}

	os.Args = newArgs

	args := &cliArgs{}

	flag.BoolVar(&args.build, "build", false, "run WiX build tools automatically")
	flag.BoolVar(&args.retainWxs, "retainwxs", false, "retain WXS file after build")
	flag.StringVar(&args.template, "template", "", "custom template to use")
	flag.StringVar(&args.templateFolder, "templatefolder", "", "template folder path")
	flag.BoolVar(&args.dryRun, "dry-run", false, "parse and validate only, no output")

	// Help flags
	var showHelp bool
	flag.BoolVar(&showHelp, "help", false, "show help")
	flag.BoolVar(&showHelp, "h", false, "show help")
	flag.BoolVar(&showHelp, "?", false, "show help")

	flag.Parse()

	if showHelp {
		printUsage()
		os.Exit(0)
	}

	args.files = flag.Args()
	return args
}

func printUsage() {
	fmt.Printf("MSIS - Version %s\n", Version)
	fmt.Printf("MSI-Simplified installer generator [%s/%s]\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Freeware written by Gerson Kurz (http://p-nand-q.com)\n")
	fmt.Println()
	fmt.Println("Usage: msis [OPTIONS] FILE [FILE...]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --build           Run WiX build tools automatically")
	fmt.Println("  --retainwxs       Retain WXS file after build")
	fmt.Println("  --template NAME   Custom template to use")
	fmt.Println("  --templatefolder  Template folder path")
	fmt.Println("  --dry-run         Parse and validate only, no output")
	fmt.Println("  --help, -h, /?    Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  msis setup.msis                    Generate .wxs file")
	fmt.Println("  msis setup.msis --build            Generate and build MSI")
	fmt.Println("  msis setup.msis --build --retainwxs  Build and keep .wxs")
	fmt.Println("  msis setup.msis --dry-run          Validate only")
}
