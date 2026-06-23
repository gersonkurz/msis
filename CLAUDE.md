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

## MSI vs Bundle UI variables (don't conflate)

A standalone MSI (Windows Installer UI) and a bundle (Burn / WixStandardBootstrapperApplication)
use different engines, so license and launch settings are **separate, non-interchangeable
variables** — a value set for one side has no effect on the other:

| Capability | MSI (`.msi`) | Bundle (`.exe`) |
|------------|--------------|-----------------|
| License | `LICENSE_FILE` — RTF file, accept dialog | `LICENSE_URL` — URL, hyperlink |
| Launch on finish | `START_EXE` — MSI Formatted path (`[INSTALLDIR]App.exe`) | `LAUNCH_TARGET` — Burn Formatted path (`[InstallFolder]\App.exe`) |

When advising users: for a standalone MSI use the MSI column; for a bundle (incl. auto-bundle
wrappers, which drive the visible UI) use the bundle column. All four flow to templates via the
variable dictionary. Full reference: `docs/templates.md` (MSI) and `docs/Bundle.md` (bundle).
(`START_EXE` and `LAUNCH_TARGET` are deliberately parallel: both are Formatted paths passed through
verbatim, not opaque ids. The one engine difference is the separator — MSI directory properties
already carry a trailing backslash (`[INSTALLDIR]App.exe`, no extra `\`), whereas Burn's
`[InstallFolder]` does not (`[InstallFolder]\App.exe`). `START_EXE` flows into `WixShellExecTarget`,
which `WixShellExec` runs through `MsiFormatRecord` at launch time. It previously required a WiX
File Id wrapped as `[#FileId]`, but msis emits opaque ids like `FILE_ID00007`, so that form was
unusable; the templates no longer wrap the value, so a power user can still pass `[#FileId]`
explicitly.)

## Installer hooks & uninstall folder removal (dangerous, gated)

`USE_INSTALLER_HOOKS=True` enables the native hook DLL (`DLL_ENTRY`, e.g. `msi-simplica.dll`) that
provides the `Before/After *` custom actions plus two cleanup actions. `REMOVE_FOLDERS_ON_UNINSTALL`
makes that DLL **recursively delete the entire `INSTALLDIR` and `APPDATADIR` trees** on full
uninstall — including runtime/customer files the MSI never installed (this is what deleted a
customer's SQLite DB). `REMOVE_REGISTRY_TREE` is the registry analogue: a **comma-separated list of
registry roots** (not a boolean) that the same DLL recursively `RegDeleteTree`s. Both only take
effect when `USE_INSTALLER_HOOKS=True`; CA definitions and scheduling are gated on both variables.
`RETAIN_FILES_ON_UNINSTALL` is a `;`-separated keep-list, passed through as an MSI property and
interpreted by the DLL (no-op until an updated DLL honors it).

- `variables.RegistryTreeActive()` decides registry "active" (false-like `False/No/Off/0/empty` →
  inactive); the renderer normalizes a false-like value to `""` so `{{#if REMOVE_REGISTRY_TREE}}`
  gates correctly (a bare `"False"` is otherwise Handlebars-truthy).
- Build-time warnings: `variables.CheckInstallerHookUsage()` (unit-tested) warns on active
  folder/registry removal, plus "no effect" when a setting lacks its prerequisite.
- `validateInstallerHooks()` in `main.go` fails the build if the arch-native hook DLL is missing
  (checked across the same bind paths WiX uses). x86/x64/arm64 are all supported.
- `variables.HookDllDir()` gives the arch-native DLL subfolder (x86/x64/**arm64**); the templates
  reference it via `{{HOOK_DLL_DIR}}/{{DLL_ENTRY}}` so an arm64 build (which uses the x64 *template*)
  loads the arm64 DLL.
- **The native DLL lives in `native/msi-simplica/`** (migrated from msis-2.x, modernized: v145,
  ARM64, WiX-native libs via pinned `PackageReference` — no hardcoded paths). `just build-hooks`
  (VS 2026 Developer shell) builds x86/x64/arm64 and stages them into `templates/<arch>/`. The DLLs
  are **build artifacts** (gitignored); `just release`/`release-all` build them and the installer
  ships them. `RETAIN_FILES_ON_UNINSTALL` parsing/skip logic lives in `CustomAction.cpp`.
- Templates: regular + the x86 silent template gate hooks consistently. `x64/template-silent.wxs`
  was **removed** (silent x64 falls back to the regular template) pending a clean reconstruction.
  VC++ merge modules are obsolete (`<requires type="vcredist">`); the regular templates still carry
  legacy `<Merge>` blocks (separate, customer-affecting migration).
- `Before*/After*` lifecycle hooks are an **extension contract** — the reference DLL implements only
  the cleanup actions; custom `DLL_ENTRY` DLLs may implement the lifecycle hooks (unimplemented are ignored).

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
