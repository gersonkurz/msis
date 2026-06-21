# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

msis-3.x is a Go rewrite of MSI-Simplified, a Windows installer generator. It transforms
declarative `.msis` XML scripts into WiX Toolset 6 XML (`.wxs`), then invokes the `wix` CLI to
compile MSI packages (or EXE bundles).

**Pipeline:** `.msis` script → Parse → IR → variable resolution → Generate WiX fragments →
Handlebars template render → `.wxs` file → `wix build` → `.msi` / `.exe`

The C# predecessor lives at `../msis-2.x/` and is the authoritative behavioral reference (see
[Reference Implementation](#reference-implementation)). The `.msis` file format is shared across
all versions.

## Build & Test

Requires Go and (for `/BUILD`) **WiX Toolset 6 or 7** (auto-detected). msis provisions WiX
itself: `msis /SETUP-WIX` (or `just setup-wix`) installs the pinned WiX version and its
extensions version-matched. The implementation and the canonical extension list live in
`internal/wix/setup.go` (`EnsureWix`, `AllExtensions`, `DefaultVersion`) — the build path in
`builder.go` reads the same lists, so they cannot drift.

```bash
just build              # Build current platform; auto-copies to C:\Program Files\MSIS if installed
just build-all          # Windows amd64 + 386 + arm64 into bootstrap/
just test               # gotestsum: writes msis-test.log + msis-junit.xml
just check              # fmt-check + vet + test  (run before committing)
just coverage           # coverage profile + Cobertura XML
just release-all        # Full dogfood: build x64/x86/arm64 MSIs + universal bundle from bootstrap/
```

Run a single test (use `gotestsum` indirection only when you need the reports):
```bash
go test ./internal/parser/ -run TestParseRequires
go test ./internal/generator/ -run TestX -v
```

Note: tests run with `-p=1` (serialized) because some exercise the real `wix` CLI and the
filesystem; don't assume package-level parallelism.

## Architecture

Each stage is its own package under `internal/`, wired together in `cmd/msis/main.go`:

| Package | Role |
|---------|------|
| `parser` | `.msis` XML → `ir.Setup`. Rejects unknown attributes; validates required fields. |
| `ir` | Intermediate Representation types. `Item` is an interface (`Files`, `Registry`, `Shortcut`, `Service`, `SetEnv`, `Execute`, ...) so features hold heterogeneous `[]Item`. |
| `variables` | Variable dictionary with Handlebars interpolation; `ResolveAll()` expands references. Exposes typed accessors (`ProductName()`, `Platform()`, `BuildTarget()`) and `CheckDeprecated()`. |
| `generator` | IR → WiX XML fragments. `Context` carries all generation state. |
| `requirements` | Standalone-MSI launch conditions (RegistrySearch) for `<requires>` runtimes. |
| `bundle` | Burn bootstrapper chain generation; prerequisite registry (VC++, .NET versions). |
| `prereqcache` | Downloads/caches prerequisite installers in `%LOCALAPPDATA%\msis\prerequisites\`. |
| `template` | Renders Handlebars (`raymond`) templates from `templates/`, with a custom-templates overlay. |
| `wix` | `wix build` invocation, EULA acceptance, version/extension detection, artifact cleanup. |
| `cli` | ANSI color helpers (respects `NO_COLOR` and `/NO-COLOR`). |

Key cross-cutting design points:

- **Deterministic IDs.** `generator.Context` uses monotonic counters (`nextFileID`, etc.) and
  uniqueness maps — never `Date.now()`/random — so output is stable and diffable. Preserve this
  when adding generators.
- **Directory roots.** Target paths are matched against known root keys (see table below); a
  `DirectoryTrees` map is built per root. Unmatched paths fall under `INSTALLDIR`.
- **Two build modes for `<requires>`.** Default: build the MSI, then auto-wrap it in a Burn
  bundle that chains the prerequisites (`processAutoBundle`). `/STANDALONE`: skip bundling, emit
  launch conditions instead. A file is treated as a bundle when `setup.IsSetupBundle()` is true.
- **Templates** live in `templates/` (e.g. `minimal/`, `minimal-x86/`, plus `bundle*.wxs`). A
  `/CUSTOMTEMPLATES` overlay folder takes precedence over the base `/TEMPLATEFOLDER`. Generated
  fragments are spliced into templates via placeholders (e.g. `CHAIN`).

## CLI Flags

The tool accepts Windows-style `/FLAG` and `/FLAG:VALUE`; `parseArgs` in `main.go` rewrites these
to `--flag` for Go's `flag` package (paths with `\` or `:` are left as files). `/SET:NAME=VALUE`
overrides variables. Key flags: `/BUILD`, `/RETAINWXS`, `/STANDALONE`, `/DRY-RUN`, `/STATUS`,
`/TEMPLATE`, `/TEMPLATEFOLDER`, `/CUSTOMTEMPLATES`. `/STATUS` is the diagnostic entry point (WiX
location/version, template search order, prerequisite cache).

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

Paths not matching these roots are treated as `INSTALLDIR` subpaths.

## WiX 6 / 7 Conventions

- Namespace: `http://wixtoolset.org/schemas/v4/wxs`; root element `<Package>` (not `<Product>`).
  The namespace and generated WXS are **identical** across WiX 6 and 7 — templates are version-agnostic.
- Build: `wix build -out file.msi -arch x64 -b bindpath`.
- **EULA**: WiX 6 has no EULA gate. WiX 7 enforces the OSMF EULA; msis detects the major version
  (`wix.GetWixMajorVersion()`) and adds `-acceptEula wix<major>` to the build for v7+ only.
  Helpers `parseMajorVersion`/`eulaAcceptArgs` in `internal/wix/builder.go` are unit-tested.
- Default architecture is **x64** (msis-2.x defaulted to x86); set `PLATFORM=x86`/`arm64` to change.

## Key Dependencies

- `github.com/aymerick/raymond` — Handlebars template engine
- `github.com/gersonkurz/go-regis3` — `.reg` file parsing

## Reference Implementation

The C# version at `../msis-2.x/` defines expected behavior. Most relevant files:
- `msis-cmd/Program.cs` — CLI entry point
- `msi-simplified/BuildContext.cs` — central orchestrator
- `msi-simplified/DescriptionReader.cs` — `.msis` parser
- `msi-simplified/SetupItem/` — per-element parsers; `msi-simplified/WxsItem/` — WiX generators

## Schema & Docs

- `docs/msis.xsd` — authoritative `.msis` schema (element/attribute reference)
- `docs/Bundle.md`, `docs/Prerequisites.md`, `docs/tutorial.md`, `docs/templates.md`,
  `docs/overview.md` — feature and architecture documentation
