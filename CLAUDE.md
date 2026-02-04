# CLAUDE.md

This file provides guidance to Claude Code when working with msis-3.x.

## Project Overview

msis-3.x is a Go rewrite of MSI-Simplified, a Windows installer generator that transforms declarative `.msis` XML scripts into WiX Toolset XML (`.wxs`) files, which are then compiled into MSI packages.

**Key Flow**: `.msis` script → Parse → IR → Template → `.wxs` file → WiX 6 → MSI

## Build Commands

```bash
just build              # Build for current platform
just build-all          # Build Windows amd64 + arm64
just test               # Run tests
just check              # Run fmt, vet, test
```

Or directly:
```bash
go build ./cmd/msis
go test ./...
```

## Architecture

```
msis-3.x/
├── cmd/msis/           # CLI entry point
│   └── main.go
├── internal/
│   ├── ir/             # Intermediate Representation types
│   │   └── types.go
│   ├── parser/         # .msis XML parsing → IR
│   │   ├── parser.go
│   │   └── parser_test.go
│   ├── variables/      # Variable dictionary with Handlebars
│   │   ├── variables.go
│   │   └── variables_test.go
│   ├── generator/      # IR → WiX XML generation
│   │   ├── context.go
│   │   └── context_test.go
│   ├── bundle/         # Bundle (bootstrapper) generation
│   │   ├── generator.go
│   │   └── prerequisites.go
│   ├── template/       # Handlebars template rendering
│   │   └── renderer.go
│   ├── registry/       # .reg file processing
│   │   └── processor.go
│   └── wix/            # WiX CLI integration
│       ├── builder.go
│       └── builder_test.go
├── templates/          # WiX 6.x Handlebars templates
├── docs/               # Documentation
│   └── Bundle.md
├── go.mod
└── justfile
```

## Current Status

- **Milestone 3.0**: CLI skeleton ✅
- **Milestone 3.1**: Parser + IR ✅
- **Milestone 3.2**: Variable resolution ✅
- **Milestone 3.3**: WXS generation (ng1-bmo scope) ✅
  - Generator context with ID generation ✅
  - Directory tree building ✅
  - Target path parsing ✅
  - Feature processing framework ✅
  - File enumeration (recursive directory scanning) ✅
  - SetEnv XML generation (Environment elements) ✅
  - Service XML generation (ServiceInstall/ServiceControl) ✅
  - CLI integration (outputs Directory and Feature XML) ✅
  - Tests: 15 generator tests ✅
- **Milestone 3.4**: Template rendering ✅
  - Template renderer using raymond (Handlebars) ✅
  - Variable context building ✅
  - Templates ported from msis-2.x ✅
  - CLI writes complete .wxs file ✅
  - Tests: 9 template tests ✅
- **Milestone 3.5**: WiX CLI integration ✅
  - WiX builder with `wix build` invocation ✅
  - EULA acceptance handling ✅
  - Cleanup of .wixpdb and .wxs ✅
  - Tests: 9 wix tests ✅

- **Milestone 4.0**: Registry integration ✅
  - go-regis3 dependency added ✅
  - Registry processor (internal/registry) ✅
  - .reg file parsing via go-regis3 ✅
  - WiX registry XML generation ✅
  - SDDL permissions support ✅
  - Delete markers ([-HKEY...], value=-) ✅
  - Delete-only .reg files with dummy keypath ✅
  - Tests: 11 registry tests ✅

- **Milestone 4.1**: Shortcuts ✅
  - Shortcut component generation ✅
  - Desktop shortcuts (DesktopFolder) ✅
  - Start menu shortcuts (ProgramMenuFolder) ✅
  - Registry keypath for shortcuts (HKCU) ✅
  - Optional icon support ✅
  - Feature component association ✅
  - Invalid target validation ✅
  - Duplicate name collision prevention ✅
  - Tests: 5 shortcut tests ✅

- **Milestone 4.2**: Permissions + ADD_TO_PATH ✅
  - ADD_TO_PATH variable handling ✅
  - PATH environment appending with Part='last' ✅
  - DISABLE_FILE_PERMISSIONS support ✅
  - RESTRICT_FILE_PERMISSIONS support ✅
  - util:PermissionEx on directories with CreateFolder ✅
  - Permission components in feature refs ✅
  - Tests: 6 new tests ✅

- **Milestone 4.3**: Execute / Custom Actions ✅
  - CustomAction element generation ✅
  - InstallExecuteSequence Custom elements ✅
  - before-install (immediate execution) ✅
  - after-install, before-uninstall (deferred, elevated) ✅
  - before-upgrade, after-install-not-patch timings ✅
  - Directory attribute with INSTALLDIR default ✅
  - Invalid when value validation ✅
  - Tests: 5 execute tests ✅

- **Milestone 4.4**: Template Parity and Cleanup ✅

- **Milestone 5.0**: Bundle IR and Parser ✅
  - Extended ir.Bundle with Prerequisites, MSI, ExePackages ✅
  - Added ir.Prerequisite, ir.BundleMSI, ir.ExePackage types ✅
  - Parser supports nested <prerequisite>, <msi>, <exe> elements ✅
  - Backward compatible with legacy shorthand syntax ✅
  - Validation for required attributes ✅
  - Tests: 4 new bundle parser tests ✅

- **Milestone 5.1**: Prerequisite Registry ✅
  - internal/bundle package created ✅
  - PrerequisiteDef struct with DisplayName, Source, DetectCondition, InstallArgs ✅
  - VC++ Redistributable definitions (2015, 2017, 2019, 2022) ✅
  - .NET Framework definitions (4.6.2, 4.7, 4.7.1, 4.7.2, 4.8, 4.8.1) ✅
  - LookupPrerequisite() function ✅
  - ExpandArch() for {arch} placeholder substitution ✅
  - Tests: 4 prerequisite tests ✅

- **Milestone 5.2**: Bundle Generator ✅
  - Generator struct with Setup, Variables, PrerequisitesFolder ✅
  - GeneratedBundle with ChainXML output ✅
  - generatePrerequisitePackage() for well-known prerequisites ✅
  - generateExePackage() for custom exe packages ✅
  - generateMSIPackages() with platform conditions ✅
  - Legacy shorthand syntax support ✅
  - PREREQUISITES_FOLDER variable override ✅
  - sanitizeID() for valid WiX identifiers ✅
  - Tests: 11 bundle tests ✅

- **Milestone 5.3**: CLI Integration ✅
  - Bundle detection via setup.IsSetupBundle() ✅
  - processBundleFile() for bundle workflow ✅
  - template.RenderString() for standalone template rendering ✅
  - wix.NewBundleBuilder() with Bal/Netfx extensions ✅
  - bundle.wxs and bundle-silent.wxs templates ✅
  - CHAIN placeholder for generated chain XML ✅
  - APPDATADIR_FILES stub filled (CommonAppDataFolder) ✅
  - DESKTOP_FILES working (done in 4.1) ✅
  - STARTMENU_FILES working (done in 4.1) ✅
  - LOGO defaults with prefix (NGBT default, LOGO_PREFIX override) ✅
  - DLL_CUSTOM passed to template context ✅
  - Bootstrap templates present (bootstrap.wxs, bootstrap-silent.wxs) ✅
  - Mixed directory roots (INSTALLDIR + APPDATADIR) ✅
  - Removed unsupported roots (PROGRAMFILESDIR, COMMONFILESDIR, etc.) ✅
  - Tests: 2 new APPDATADIR tests ✅

- **Milestone 5.4**: Documentation ✅
  - docs/Bundle.md created ✅
  - Bundle syntax (legacy shorthand + nested elements) ✅
  - Prerequisite reference (vcredist, netfx) ✅
  - Variable reference ✅
  - Migration guide from C++ bundler ✅

## Supported Directory Roots

| Root Key | WiX Folder | Typical Path |
|----------|------------|--------------|
| `INSTALLDIR` | ProgramFiles(64)Folder | C:\Program Files |
| `APPDATADIR` | CommonAppDataFolder | C:\ProgramData (all users) |
| `ROAMINGAPPDATADIR` | AppDataFolder | %APPDATA% (per-user roaming) |
| `LOCALAPPDATADIR` | LocalAppDataFolder | %LOCALAPPDATA% (per-user local) |
| `COMMONFILESDIR` | CommonFiles(64)Folder | C:\Program Files\Common Files |
| `WINDOWSDIR` | WindowsFolder | C:\Windows |
| `SYSTEMDIR` | System(64)Folder | C:\Windows\System32 |

Paths not matching these roots are treated as INSTALLDIR subpaths.

## Key Dependencies

- `github.com/aymerick/raymond` - Handlebars template engine
- `github.com/gersonkurz/go-regis3` - Registry file parsing

## Reference Implementation

The C# version is at `../msis-2.x/`. Key files:
- `msis-cmd/Program.cs` - CLI entry point
- `msi-simplified/BuildContext.cs` - Central orchestrator
- `msi-simplified/DescriptionReader.cs` - .msis XML parser
- `msi-simplified/Description.cs` - Parsed setup container
- `msi-simplified/SetupItem/` - XML element parsers
- `msi-simplified/WxsItem/` - WiX XML generators

## Schema

- `docs/msis.xsd` - .msis XML schema (authoritative)
- `docs/Bundle.md` - Bundle (bootstrapper) documentation

## WiX 6 Integration

- Namespace: `http://wixtoolset.org/schemas/v4/wxs`
- Root element: `<Package>` (not `<Product>`)
- CLI: `wix build -out file.msi -arch x64 -b bindpath`
- EULA: Requires acceptance via `-acceptEula wix6`
