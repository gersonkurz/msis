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
│   ├── parser/         # .msis XML parsing → IR
│   ├── variables/      # Variable dictionary with Handlebars
│   ├── generator/      # IR → WiX XML generation
│   ├── bundle/         # Bundle (bootstrapper) generation
│   ├── template/       # Handlebars template rendering
│   ├── registry/       # .reg file processing
│   └── wix/            # WiX CLI integration
├── templates/          # WiX 6.x Handlebars templates
├── bootstrap/          # Self-packaging scripts
│   ├── setup.msis      # x64 MSI
│   ├── setup-x86.msis  # x86 MSI
│   ├── setup-arm64.msis # ARM64 MSI
│   ├── setup-bundle.msis # Universal bundle
│   └── dist/           # Built installers (not in git)
├── docs/               # Documentation
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

- **Milestone 6.0**: IR and Parser for `<requires>` ✅
  - Added `ir.Requirement` type (Type, Version, Source) ✅
  - Added `Requires []Requirement` field to `ir.Setup` ✅
  - Parser handles `<requires>` elements at setup level ✅
  - Validation: type required, version or source required ✅
  - Type normalized to lowercase (Codex review feedback) ✅
  - Unknown attribute rejection ✅
  - Updated `msis.xsd` schema with RequiresType ✅
  - Tests: 5 new requires parser tests ✅

- **Milestone 6.1**: Launch Conditions for Standalone MSI ✅
  - Created `internal/requirements/` package ✅
  - VC++ 2015-2022 detection via registry (HKLM\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\{x64|x86|arm64}) ✅
  - .NET Framework 4.6.2-4.8.1 detection via Release DWORD value ✅
  - `GenerateLaunchConditions()` generates Property+RegistrySearch XML ✅
  - `GenerateXML()` produces Launch Condition elements ✅
  - Friendly error messages for missing runtimes ✅
  - Integrated into generator (`LaunchConditionSearchXML`, `LaunchConditionsXML`) ✅
  - Added to template context and all templates (x64, x86, minimal, minimal-x86) ✅
  - ARM64 platform support (Codex review feedback) ✅
  - Tests: 11 requirements tests + 4 generator integration tests ✅

- **Milestone 6.2**: Auto-Bundle Generation ✅
  - Added `/STANDALONE` flag to skip auto-bundling ✅
  - `AutoBundleGenerator` for creating bundle wrappers from requirements ✅
  - `RequirementsToPrerequisites()` converts ir.Requirement to ir.Prerequisite ✅
  - Two-phase build: First MSI, then bundle containing MSI ✅
  - `processAutoBundle()` handles bundle wrapper generation ✅
  - Automatic detection: auto-bundle when `<requires>` present and not standalone ✅
  - Tests: 3 new auto-bundle tests ✅

- **Milestone 6.3**: Prerequisite Source Management ✅
  - Created `internal/prereqcache/` package ✅
  - Global cache location: `%LOCALAPPDATA%\msis\prerequisites\` ✅
  - `PrerequisiteURL` struct with Type, Version, Arch, URL, FileName, SHA256 ✅
  - Download URLs for VC++ 2019/2022 (x64, x86, arm64) ✅
  - Download URLs for .NET Framework 4.7.2, 4.8, 4.8.1 ✅
  - `Cache.EnsurePrerequisite()` downloads if not cached ✅
  - `Cache.GetCachedPath()` returns cached file path ✅
  - `Cache.ListCached()` lists all cached files ✅
  - `downloadFile()` with temp file + rename for atomicity ✅
  - `verifyHash()` for SHA256 verification (optional) ✅
  - Bundle generator integration via `SetCache()` and `EnsurePrerequisites()` ✅
  - CLI integration with progress output during downloads ✅
  - `/STATUS` shows cached prerequisites ✅
  - HTTP timeout (5 minutes) for downloads ✅
  - Warning when downloading without SHA256 hash ✅
  - ARM64 prerequisite caching and bundle generation ✅
  - `NewCacheReadOnly()` for status queries without side effects ✅
  - Tests: 10 prereqcache tests + 4 bundle integration tests ✅

- **Milestone 6.4**: Cleanup and Migration ✅
  - Removed `templates/mergemodules/` directory (54 .msm files) ✅
  - Removed `INCLUDE_VCREDIST` from template context ✅
  - Added `DeprecatedVariables` list with migration hints ✅
  - `CheckDeprecated()` function returns warnings for deprecated variables ✅
  - CLI displays deprecation warnings after loading variables ✅
  - Deprecation warnings for: INCLUDE_VCREDIST, INCLUDE_VC100, INCLUDE_VC140, INCLUDE_MFC ✅
  - Clarified VC100/VC140 migration guidance (runtime change, not drop-in replacement) ✅
  - Tests: 1 new deprecation warning test ✅

- **Milestone 6.5**: CLI Output Enhancements (ANSI Colors) ✅
  - Created `internal/cli/colors.go` with ANSI color helpers ✅
  - Color functions: `Error()`, `Success()`, `Warning()`, `Info()`, `Bold()`, `Filename()`, `Number()` ✅
  - Terminal detection via `golang.org/x/term` ✅
  - Respects `NO_COLOR` environment variable (https://no-color.org/) ✅
  - Added `/NO-COLOR` flag to disable colors ✅
  - Applied colors to CLI output:
    - Red: Errors
    - Green: Success messages ("Built:")
    - Yellow: Warnings, deprecation notices
    - Cyan: Info, progress, filenames
    - Magenta: Numbers (counts)
    - Bold: Product name emphasis
  - Fixed EnableColors() to re-check VT support on Windows ✅
  - Tests: 4 color tests ✅

- **Milestone 6.6**: Documentation ✅
  - Created `docs/Prerequisites.md` with:
    - Supported runtimes reference (vcredist, netfx) ✅
    - Usage examples and quick start ✅
    - Build modes (auto-bundle vs standalone) ✅
    - Prerequisite caching explanation ✅
    - Custom/offline source instructions ✅
    - Detection logic details ✅
    - Migration guide from MSIS 2.x ✅
    - Troubleshooting section ✅
  - Updated `docs/Bundle.md`:
    - Added auto-bundling with `<requires>` section ✅
    - Added automatic prerequisite downloads section ✅
    - Cross-reference to Prerequisites.md ✅

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
