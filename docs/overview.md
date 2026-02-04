# msis-3.x Code Documentation

This document provides a technical overview of the msis-3.x codebase, covering architecture, data flow, and implementation details.

## Table of Contents

1. [What msis Does](#what-msis-does)
2. [Architecture Overview](#architecture-overview)
3. [Package Structure](#package-structure)
4. [Data Flow Pipeline](#data-flow-pipeline)
5. [The Parser](#the-parser)
6. [Variable Resolution](#variable-resolution)
7. [WXS Generation](#wxs-generation)
8. [Template System](#template-system)
9. [Bundle Generation](#bundle-generation)
10. [WiX CLI Integration](#wix-cli-integration)

---

## What msis Does

msis transforms declarative `.msis` XML scripts into Windows Installer packages (.msi). The core pipeline is:

```
.msis file → Parse → IR → Generate → Template → .wxs file → WiX build → .msi
```

The tool abstracts away WiX complexity:
- **Auto-generates GUIDs** for components (deterministic, based on file paths)
- **Builds directory trees** from file glob patterns
- **Maps files to components** following WiX's one-file-per-component rule
- **Converts .reg files** to WiX registry XML
- **Produces bundles** that combine multiple architecture MSIs

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         cmd/msis/main.go                        │
│                        (CLI entry point)                        │
└─────────────────────────────────────────────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────┐
        ▼                       ▼                       ▼
┌───────────────┐     ┌─────────────────┐     ┌─────────────────┐
│    parser     │     │    variables    │     │    generator    │
│  .msis → IR   │     │  {{var}} resolve│     │   IR → WXS XML  │
└───────────────┘     └─────────────────┘     └─────────────────┘
        │                                             │
        ▼                                             ▼
┌───────────────┐                           ┌─────────────────┐
│      ir       │                           │    template     │
│ (data types)  │                           │   Handlebars    │
└───────────────┘                           └─────────────────┘
        │                                             │
        │         ┌─────────────────┐                 │
        └────────▶│    registry     │◀────────────────┘
                  │  .reg → WiX XML │
                  └─────────────────┘
                           │
                           ▼
                  ┌─────────────────┐
                  │       wix       │
                  │  wix.exe invoke │
                  └─────────────────┘
```

For bundles, an additional path:

```
┌───────────────┐     ┌─────────────────┐
│    bundle     │────▶│  bundle.wxs     │
│  Chain XML    │     │    template     │
└───────────────┘     └─────────────────┘
```

---

## Package Structure

```
msis-3.x/
├── cmd/msis/
│   └── main.go              # CLI entry point, argument parsing
│
├── internal/
│   ├── ir/
│   │   └── types.go         # Intermediate Representation types
│   │
│   ├── parser/
│   │   ├── parser.go        # XML parsing → IR conversion
│   │   └── parser_test.go
│   │
│   ├── variables/
│   │   ├── variables.go     # Dictionary with Handlebars resolution
│   │   └── variables_test.go
│   │
│   ├── generator/
│   │   ├── context.go       # IR → WXS XML generation
│   │   └── context_test.go
│   │
│   ├── bundle/
│   │   ├── generator.go     # Bundle chain XML generation
│   │   └── prerequisites.go # VC++/NetFx prerequisite definitions
│   │
│   ├── template/
│   │   └── renderer.go      # Handlebars template rendering
│   │
│   ├── registry/
│   │   └── processor.go     # .reg file → WiX XML conversion
│   │
│   └── wix/
│       ├── builder.go       # WiX CLI invocation
│       └── builder_test.go
│
├── templates/               # WiX Handlebars templates
│   ├── x64/template.wxs
│   ├── x86/template.wxs
│   ├── minimal/template.wxs
│   ├── bundle.wxs
│   └── wixlib/*.wxl         # Localization files
│
└── docs/
    ├── Bundle.md            # Bundle documentation
    ├── msis.xsd             # XML schema
    └── overview.md          # This file
```

### Package Responsibilities

| Package | Responsibility |
|---------|----------------|
| `ir` | Data types representing parsed .msis content |
| `parser` | XML unmarshaling with validation |
| `variables` | Variable dictionary with Handlebars expansion |
| `generator` | Converts IR to WXS XML fragments |
| `bundle` | Generates WiX Burn chain XML |
| `template` | Renders final .wxs using Handlebars |
| `registry` | Converts .reg files to WiX registry XML |
| `wix` | Invokes WiX CLI tools |

---

## Data Flow Pipeline

### Phase 1: Parse

`parser.Parse(filename)` reads the `.msis` XML file and produces an `ir.Setup` struct:

```go
type Setup struct {
    Silent   bool
    Sets     []Set        // Variable definitions
    Features []Feature    // Feature hierarchy
    Items    []Item       // Top-level items (outside features)
    Bundle   *Bundle      // Optional bundle config
}
```

The parser:
- Uses Go's `encoding/xml` with custom `UnmarshalXML` methods
- Validates required attributes
- Preserves document order for items
- Reports unknown elements/attributes as errors

### Phase 2: Variable Resolution

`variables.New()` creates a dictionary, then `LoadFromSetup()` and `ResolveAll()`:

```go
vars := variables.New()
vars.LoadFromSetup(setup)
vars.ResolveAll()

// Access resolved values
name := vars.ProductName()     // "My App"
version := vars.ProductVersion() // "1.0.0"
```

Variable resolution uses [raymond](https://github.com/aymerick/raymond) (Handlebars for Go):
- `{{VAR}}` expands to variable value
- Nested references resolve iteratively
- Missing variables remain as literals (for WiX properties like `[INSTALLDIR]`)

### Phase 3: WXS Generation

`generator.NewContext()` walks the IR and produces WXS XML fragments:

```go
ctx := generator.NewContext(setup, vars, workDir)
output, err := ctx.Generate()

// output contains:
// - INSTALLDIR_FILES: Directory/Component XML for install folder
// - FEATURES: Feature/ComponentRef XML
// - REGISTRY_ENTRIES: Registry XML
// - CUSTOM_ACTIONS: CustomAction XML
// etc.
```

Key responsibilities:
- **Directory tree building**: Scans source folders, creates WiX Directory elements
- **Component generation**: One component per file (WiX best practice)
- **ID generation**: Deterministic GUIDs based on file paths
- **Shortcut handling**: Creates shortcut components with registry keypaths
- **Registry processing**: Delegates to `registry` package for .reg files

### Phase 4: Template Rendering

`template.NewRenderer()` combines variables and generated XML with a Handlebars template:

```go
renderer := template.NewRenderer(vars, templateFolder, customTemplates, output)
wxsContent, err := renderer.Render()
```

The template receives a context with:
- All variables from the dictionary
- Generated XML fragments (`{{{INSTALLDIR_FILES}}}`, `{{{FEATURES}}}`, etc.)
- Conditional helpers (`{{#if LICENSE_FILE}}...{{/if}}`)

### Phase 5: WiX Build

`wix.NewBuilder()` invokes the WiX CLI:

```go
builder := wix.NewBuilder(vars, wxsFile, templateFolder, customTemplates, workDir, retainWxs)
builder.Build()
```

Build steps:
1. Check/accept WiX EULA
2. Run `wix build` with appropriate extensions and bind paths
3. Clean up temporary files (unless `--retainwxs`)

---

## The Parser

### XML Structure

The parser handles these elements:

| Element | Parent | Purpose |
|---------|--------|---------|
| `<setup>` | root | Container, optional `silent` attribute |
| `<set>` | setup | Variable definition |
| `<feature>` | setup, feature | Feature grouping |
| `<files>` | setup, feature | File copy specification |
| `<registry>` | setup, feature | Registry file import |
| `<shortcut>` | setup, feature | Shortcut creation |
| `<service>` | setup, feature | Service installation |
| `<set-env>` | setup, feature | Environment variable |
| `<execute>` | setup, feature | Custom action |
| `<exclude>` | setup, feature | Folder exclusion |
| `<bundle>` | setup | Bundle configuration |

### Validation Strategy

Each element type has a custom `UnmarshalXML` method that:
1. Tracks which attributes are present
2. Validates required attributes exist
3. Reports unknown attributes as errors
4. Returns typed errors with element context

Example from `parser.go`:

```go
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
        default:
            return fmt.Errorf("unknown attribute '%s' on <files>", attr.Name.Local)
        }
    }
    if !hasSource {
        return fmt.Errorf("<files> requires 'source' attribute")
    }
    // ...
}
```

---

## Variable Resolution

### The Dictionary

`variables.Dictionary` is a `map[string]string` with methods:

```go
type Dictionary map[string]string

func (d Dictionary) ProductName() string
func (d Dictionary) ProductVersion() string
func (d Dictionary) Platform() string
func (d Dictionary) BuildTarget() string
func (d Dictionary) Resolve(s string) (string, error)
func (d Dictionary) ResolveAll() error
```

### Resolution Algorithm

`ResolveAll()` iterates until no changes occur:

```go
func (d Dictionary) ResolveAll() error {
    for {
        changed := false
        for k, v := range d {
            resolved, _ := d.Resolve(v)
            if resolved != v {
                d[k] = resolved
                changed = true
            }
        }
        if !changed {
            break
        }
    }
    return nil
}
```

This handles transitive references:
```xml
<set name="APP" value="MyApp"/>
<set name="VERSION" value="1.0"/>
<set name="FILENAME" value="{{APP}}-{{VERSION}}.exe"/>
<!-- After resolution: FILENAME = "MyApp-1.0.exe" -->
```

### Special Variables

Some variables have computed defaults:

| Variable | Default Logic |
|----------|---------------|
| `PLATFORM` | `x64` |
| `LCID` | `1033` (English) |
| `CODEPAGE` | `1252` (Western) |
| `BUILD_TARGET` | `{name}-{version}-{platform}.msi` |
| `INSTALLDIR` | `PRODUCT_NAME` |

---

## WXS Generation

### Generator Context

`generator.Context` accumulates state during generation:

```go
type Context struct {
    Setup     *ir.Setup
    Variables variables.Dictionary
    WorkDir   string

    // Generated content
    DirectoryTrees    map[string]string   // Root → Directory XML
    FeatureComponents map[string][]string // Feature → ComponentRef IDs
    RegistryXML       string
    CustomActionsXML  string
    // ...
}
```

### Directory Tree Building

For each `<files>` element:

1. Parse target path (`[INSTALLDIR]subdir` → root=INSTALLDIR, subpath=subdir)
2. Scan source files (glob pattern or single file)
3. Build directory structure mirroring source layout
4. Generate one `<Component>` per file
5. Track component IDs for feature association

### Component ID Generation

Component IDs are deterministic to enable upgrades:

```go
func (c *Context) generateComponentID(filePath string) string {
    // Hash the relative path for reproducibility
    h := sha256.Sum256([]byte(filePath))
    return fmt.Sprintf("CID_%s", hex.EncodeToString(h[:8]))
}
```

### File Exclusion

`<exclude folder="...">` elements are collected before scanning:

```go
type Context struct {
    ExcludedFolders map[string]bool
}

func (c *Context) shouldExclude(path string) bool {
    for excluded := range c.ExcludedFolders {
        if strings.HasPrefix(path, excluded) {
            return true
        }
    }
    return false
}
```

---

## Template System

### Handlebars Integration

Templates use [raymond](https://github.com/aymerick/raymond), a Go Handlebars implementation.

Template variables:
- `{{PRODUCT_NAME}}` - escaped HTML
- `{{{FEATURES}}}` - raw (unescaped) for XML fragments
- `{{#if VAR}}...{{/if}}` - conditional sections

### Template Selection

Templates are selected based on platform and features:

| Condition | Template |
|-----------|----------|
| Platform = x64 | `templates/x64/template.wxs` |
| Platform = x86 | `templates/x86/template.wxs` |
| Custom specified | User-provided path |
| Silent = true | `-silent.wxs` variant if exists |

### Custom Template Overlay

Two-folder strategy:
1. **Base templates** (`/TEMPLATEFOLDER`): Default templates
2. **Custom templates** (`/CUSTOMTEMPLATES`): User overrides

Custom templates take precedence - if `custom/template.wxs` exists, it's used instead of the base.

---

## Bundle Generation

### Bundle vs MSI

| Aspect | MSI | Bundle |
|--------|-----|--------|
| Output | `.msi` | `.exe` |
| Architecture | Single | Multiple (x86/x64/ARM64) |
| Prerequisites | No | Yes (VC++, .NET) |
| WiX element | `<Package>` | `<Bundle>` |

### Bundle Chain Generation

`bundle.Generator` produces the `<Chain>` content:

```go
type GeneratedBundle struct {
    ChainXML string  // <ExePackage> and <MsiPackage> elements
}
```

Chain order:
1. Prerequisites (VC++, .NET Framework)
2. Custom exe packages
3. MSI packages (with architecture conditions)

### Architecture Detection

Bundle MSI selection uses WiX Burn conditions:

| Architecture | Condition |
|--------------|-----------|
| ARM64 | `NativeMachine = 43620` |
| x64 | `VersionNT64 AND NOT NativeMachine = 43620` |
| x86 | `NOT VersionNT64` |

`NativeMachine` is a built-in Burn variable containing the system's native architecture.

### Install Folder Handling

Bundles use `SetVariable` to resolve the install path at runtime:

```xml
<Variable Name="InstallFolder" Type="string" Value="" bal:Overridable="yes"/>
<SetVariable Id="SetInstallFolder" Variable="InstallFolder"
    Value="[ProgramFiles6432Folder]AppName"/>
```

`ProgramFiles6432Folder` is a Burn built-in that returns the native Program Files path regardless of OS architecture.

---

## WiX CLI Integration

### WiX 6 Differences

msis-3.x targets WiX 6, which differs from earlier versions:

| Aspect | WiX 3.x/4.x | WiX 6 |
|--------|-------------|-------|
| Installation | MSI | .NET tool |
| Root element | `<Product>` | `<Package>` |
| Namespace | `wix` | `http://wixtoolset.org/schemas/v4/wxs` |
| Bundle ext | `WixToolset.Bal.wixext` | `WixToolset.BootstrapperApplications.wixext` |

### Builder Implementation

`wix.Builder` handles:

1. **Path resolution**: Finds `wix.exe` in `~/.dotnet/tools/`
2. **EULA acceptance**: Runs `wix eula accept wix6` if needed
3. **Extension loading**: Adds `-ext` flags for UI, Util extensions
4. **Bind paths**: Adds `-b` flags for file resolution
5. **Cleanup**: Removes `.wixpdb` and optionally `.wxs`

### Status Command

`/STATUS` reports configuration for troubleshooting:

```go
func GetWixPath() string      // Path to wix.exe
func GetWixVersion() string   // Version string
func GetInstalledExtensions() []string  // Installed extensions
```

---

## Testing

### Test Organization

```
internal/
├── parser/parser_test.go      # XML parsing tests
├── variables/variables_test.go # Variable resolution tests
├── generator/context_test.go  # WXS generation tests
├── bundle/generator_test.go   # Bundle generation tests
├── registry/processor_test.go # Registry conversion tests
└── wix/builder_test.go        # WiX invocation tests
```

### Running Tests

```bash
# All tests
go test ./...

# Verbose
go test -v ./...

# Specific package
go test ./internal/parser
```

### Test Patterns

Tests use table-driven patterns:

```go
func TestVariableResolution(t *testing.T) {
    tests := []struct {
        name     string
        input    map[string]string
        expected map[string]string
    }{
        {
            name:     "simple reference",
            input:    map[string]string{"A": "hello", "B": "{{A}} world"},
            expected: map[string]string{"A": "hello", "B": "hello world"},
        },
        // ...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

---

## Dependencies

### External Dependencies

| Dependency | Purpose |
|------------|---------|
| `github.com/aymerick/raymond` | Handlebars template engine |
| `github.com/gersonkurz/go-regis3` | Registry file parsing |

### Why These Dependencies?

**raymond**: Handlebars is the established template syntax from msis-2.x. Raymond is a well-maintained Go implementation that matches the JavaScript/C# behavior.

**go-regis3**: Sister project that provides complete .reg file parsing with WiX XML output. Handles all registry value types, deletions, and edge cases.

---

## Summary

msis-3.x architecture follows these principles:

1. **Pipeline design**: Clear phases (parse → resolve → generate → template → build)
2. **Separation of concerns**: Each package has one responsibility
3. **Compatibility**: Same .msis format as 2.x, same templates work
4. **Determinism**: Same input produces same output (reproducible builds)
5. **Minimal dependencies**: Only two external packages
6. **Testability**: Each phase is independently testable
