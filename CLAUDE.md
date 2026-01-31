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
│   └── wix/            # WiX CLI integration
│       ├── builder.go
│       └── builder_test.go
├── templates/          # WiX 6.x Handlebars templates (TODO)
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

## Key Dependencies

- `github.com/aymerick/raymond` - Handlebars template engine
- `github.com/gersonkurz/go-regis3` - Registry file parsing (Phase 4)

## Reference Implementation

The C# version is at `../msis-2.x/`. Key files:
- `msis-cmd/Program.cs` - CLI entry point
- `msi-simplified/BuildContext.cs` - Central orchestrator
- `msi-simplified/DescriptionReader.cs` - .msis XML parser
- `msi-simplified/Description.cs` - Parsed setup container
- `msi-simplified/SetupItem/` - XML element parsers
- `msi-simplified/WxsItem/` - WiX XML generators

## Decision Documents

- `docs/decisions/005-phase-3-plan-decisions.md` - XSD validation, templates, scope
- `docs/phase-3-plan.md` - Detailed milestone breakdown
- `docs/msis.xsd` - .msis schema (authoritative)

## WiX 6 Integration

- Namespace: `http://wixtoolset.org/schemas/v4/wxs`
- Root element: `<Package>` (not `<Product>`)
- CLI: `wix build -out file.msi -arch x64 -b bindpath`
- EULA: Requires acceptance via `-acceptEula wix6`
