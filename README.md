# msis

A Windows installer generator that transforms declarative `.msis` XML scripts into MSI packages and bundles via WiX Toolset 6 or 7, and describes what it built in an SBOM.

## Why Does This Exist?

Writing WiX XML by hand is tedious. A simple installer requires hundreds of lines of boilerplate - GUIDs, component rules, directory structures, feature hierarchies. For most applications, you just want to say "put these files here, create this shortcut, set these registry keys."

`msis` lets you write this:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyApp">
    <files source="bin" target="[INSTALLDIR]"/>
    <shortcut name="MyApp" target="DESKTOP" file="[INSTALLDIR]MyApp.exe"/>
    <registry file="settings.reg"/>
  </feature>
</setup>
```

Instead of 500+ lines of WiX XML. The tool handles component GUIDs, directory trees, feature mapping, registry import, services, prerequisites and [multi-architecture bundles](docs/Bundle.md). Two builds of one script produce packages identical except for the documented fields ([decisions D20](docs/decisions.md)), and `/SBOM` describes them for the Cyber Resilience Act ([SBOM](#sbom)).

## Installation

### Prerequisites

**WiX Toolset 7** (or 6) with the extensions msis needs. msis detects the installed WiX
major version at build time and works with either — WiX 7 is recommended for new setups.

msis can provision WiX for you (msis itself is a self-contained binary — grab it from
[Get msis](#get-msis) first, then run this). It installs the correct WiX version, registers
all required extensions *pinned to the matching version*, and verifies the result:

```
msis /SETUP-WIX
```

> Why let msis do it? WiX extensions live in a single global store shared across WiX
> versions. Adding them without pinning a version (the common mistake) leaves mismatched
> copies that trigger `WIX6101 ... compatible with WiX vN?` warnings and "(damaged)" labels.
> `/SETUP-WIX` avoids that. Add `/WIX-VERSION:6.0.2` to stay on WiX 6.
>
> Copies from older WiX majors may remain in the cache and show as "(damaged)" in
> `wix extension list`; this is harmless — builds only load the version-matched
> extensions and an actual `/BUILD` never prints those warnings.

<details>
<summary>Manual install (equivalent)</summary>

```bash
dotnet tool install --global wix --version 7.0.0
wix extension add -g WixToolset.UI.wixext/7.0.0
wix extension add -g WixToolset.Util.wixext/7.0.0
wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.0   # bundles
wix extension add -g WixToolset.Netfx.wixext/7.0.0                      # bundles
```

Note the `-g` (global) flag and the `/7.0.0` version pin on every package — omitting either
is the usual cause of extension trouble. (Use `/6.0.2` throughout to stay on WiX 6.)
</details>

### Get msis

Download from the [releases page](https://github.com/gersonkurz/msis/releases), or build from source:

```bash
git clone https://github.com/gersonkurz/msis
cd msis/msis-3.x
go build -o msis.exe ./cmd/msis
```

Verify your setup:
```bash
msis /STATUS
```

## Quick Start

1. Create `setup.msis`:

```xml
<setup>
  <set name="PRODUCT_NAME" value="Hello World"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{12345678-1234-1234-1234-123456789ABC}"/>

  <feature name="Main">
    <files source="hello.exe" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

2. Build the MSI:
```bash
msis /BUILD setup.msis
```

That's it. Your installer is ready at `setup.msi`.

## Documentation

| Document | Description |
|----------|-------------|
| **[Tutorial](docs/tutorial.md)** | Step-by-step guides: files, shortcuts, registry, services, and more |
| **[Templates & Customization](docs/templates.md)** | Template locations, logo branding, custom templates |
| **[Bundle Guide](docs/Bundle.md)** | Multi-architecture installers and prerequisites |
| **[Installer Hooks](docs/installer-hooks.md)** | Native hooks, destructive uninstall cleanup, and `RETAIN_FILES_ON_UNINSTALL` |
| **[Settled Questions](docs/decisions.md)** | Things that look like defects and are not: what was decided, on what evidence |
| **[SBOM](docs/sbom.md)** | `/INSPECT`, `/SBOM`, `/ANALYZE` and `/SCAN`: what the document claims and what it does not, VEX, retention and BOM-Links |
| **[Schema](docs/msis.xsd)** | Complete XML element and attribute reference |
| **[Roadmap](docs/roadmap.md)** | Planned features and future direction |
| **[Developer Overview](docs/overview.md)** | Architecture, code structure, and internals |

## SBOM

msis writes a CycloneDX 1.6 SBOM for the installers it builds (`/BUILD /SBOM`, or `/SBOM` on an
existing `.msi` or bundle `.exe`). It reads the built artifact, not the script, so the document
describes what actually ships. Every file has its SHA-256 and SHA-512, and a bundle links to the
documents of the installers it chains. It aims at [BSI TR-03183-2](docs/sbom.md#bsi-tr-03183-2),
the most concrete SBOM specification behind the EU Cyber Resilience Act.
[docs/sbom.md](docs/sbom.md) says what the document claims, and just as importantly what it does not.
`/ANALYZE` adds the packages the payload declares itself: Python distributions, Maven jars and
.NET `.deps.json` entries, found by [syft](https://github.com/anchore/syft) and attached to the
files they are in. `/SCAN` then runs grype on the result.

**msis's own releases carry their SBOMs.** Each MSI and the universal bundle ship with a
`.cdx.json` beside them. Each MSI's document lists what is inside `msis.exe`: its Go modules and the
Go standard library, each with its licence. It also lists what is inside the installer-hook DLL:
the WiX libraries it links.

### Why msis's SBOMs are CC0-1.0

The software is MIT (see [License](#license)). Its SBOMs are offered under **CC0-1.0**, public
domain, set in `bootstrap/setup.msis` as `SBOM_DATA_LICENSE`.

An SBOM is not code. It is a set of facts about a release, and its only use is to be passed on:
- to the customer who installs msis;
- to their auditor;
- to vulnerability scanners and SBOM indexes that copy, merge and republish it.

Every one of those steps should be possible without anyone asking whether the document's licence
permits it. CC0 answers that question in advance. It is the same licence the SPDX specification
makes mandatory for every SPDX document, for the same reason.

**The SBOMs msis writes for your products are yours.** msis grants no licence on them unless your
`.msis` sets `SBOM_DATA_LICENSE`, just as it names no SBOM creator unless you set `SBOM_CREATOR`.
The document's creator decides, not the tool.

## Command Line

```
msis [OPTIONS] FILE [FILE...]

Building:
  /BUILD                 Generate the .wxs and build the MSI (and bundle) with WiX
  /SET:NAME=VALUE        Override or add a <set> variable
  /RETAINWXS             Keep the generated .wxs after the build
  /TEMPLATE:PATH         Use a custom WiX template
  /TEMPLATEFOLDER:PATH   Base template folder
  /CUSTOMTEMPLATES:PATH  Overlay folder for private assets (takes precedence)
  /STANDALONE            No auto-bundle: <requires> become launch conditions
  /STRICT                Refuse deprecated layouts instead of warning (msis 4 will)
  /DRY-RUN               Parse and validate only, no output

Reading and describing a built .msi or bundle .exe:
  /INSPECT               Report what is inside it
  /SBOM                  Write a CycloneDX SBOM beside it; with /BUILD, for what was built
  /ANALYZE               With /SBOM: add the packages syft finds declared in the payload
  /SCAN                  Run grype on the SBOM and apply the product's VEX
  /SCAN-DIR:DIR          With /SCAN: keep the reports in DIR

Setup and diagnostics:
  /SETUP-WIX             Install or repair the pinned WiX toolset and its extensions
  /WIX-VERSION:VER       With /SETUP-WIX: a specific WiX version
  /STATUS                Show configuration (WiX location and version, templates, cache)
  /NO-COLOR              Disable colored output
  /?, /HELP              Show help
```

`msis /?` prints the same with examples. [docs/sbom.md](docs/sbom.md) covers the second group in
full.

## Migration from msis-2.x

msis-3.x is largely compatible with msis-2.x scripts:

| Aspect | msis-2.x | msis-3.x |
|--------|----------|----------|
| WiX Version | WiX 3.x/4.x | WiX 6 or 7 (auto-detected) |
| Default Architecture | x86 | x64 |
| Bundle Engine | Custom C++ | WiX Burn |
| VC++ runtime | Merge modules (`INCLUDE_VCREDIST`) | `<requires type="vcredist">` + auto-bundling — no merge-module maintenance |
| Component GUIDs | Random per build | Deterministic: the product plus where each file installs ([D23](docs/decisions.md)) — the same across builds, build folders and releases |
| `DLL_CUSTOM` path | Bare filename under `<templates>/x86/` | Resolved through WiX's bind paths as written — name it `x64\Your.CA.dll` (or `x86\`) for a DLL staged beside the templates; see [templates.md](docs/templates.md#dll_custom--a-second-custom-action-dll-and-how-it-differs-from-dll_entry) |

**Migration steps:**
1. Install WiX + extensions: `msis /SETUP-WIX`
2. Validate: `msis /DRY-RUN setup.msis`
3. If you need x86: add `<set name="PLATFORM" value="x86"/>`
4. Rebuild: `msis /BUILD setup.msis`

Most scripts work unchanged. See the [Tutorial](docs/tutorial.md) for the full element reference.

## History

msis has had three generations, all sharing the same `.msis` script format:

- **msis-1.x** (C++) - Original implementation, internal use
- **msis-2.x** (C#) - Expanded features, production use since 2013
- **msis-3.x** (Go) - Current version, clean rewrite for WiX 6/7

### msis-3.x version history

Earlier versions were reconstructed from the Git log and tagged retroactively. The newest entry
below is the current release; `just set-version X.Y.Z` adds the next one.

**3.0.6** — 2026-09-26 (tag [`v3.0.6`](../../releases/tag/v3.0.6))

SBOMs, and a regression QA against real products. Every issue from #29 to #83 is closed, and the
fixes were checked against the 3.0.3 reference builds of ProAKT 3.6.0.x, Poste Italiane 4.2 and
NG1 2.4, with the risky cases run on a test VM (`todo-testme.md`).

**Upgrading products built with an earlier msis** — read this first:
- **`<remove-on-uninstall>` no longer runs when an upgrade removes the previous version**
  ([#76](../../issues/76), decisions D21). Up to 3.0.5 every major upgrade deleted the folder
  or registry key it names, the application's data included. An upgrade removes the old version
  with the **old** package's tables, though, so the first upgrade from a 3.0.5-or-earlier build
  still deletes it one last time (verified on the VM, T76). Back up that data before that first
  upgrade.
- **`preserve="yes"` survives a silent x86 package.** 3.0.3's x86 silent template wrote every
  preserved value as an empty string, on a fresh install and on an upgrade (T10). 3.0.6 keeps the
  values it finds. It cannot restore values already lost.
- **Component GUIDs are the product plus where each file installs** ([#81](../../issues/81),
  D23). They used to hash the absolute source path, so building from another folder changed
  every GUID. The first 3.0.6 build of a product changes them once; the major upgrade removes the
  old version completely first, so that is harmless (verified on the VM, T81). SBOM file refs
  change once too. The ProductCode no longer depends on the build folder either. A comparison of
  two packages (a regression check against a reference build) has to use the same msis on both
  sides, or every GUID differs.
- **`<service>` attributes reach the package** ([#78](../../issues/78)): `description`,
  `service-type`, `error-control` and `restart`, with msis-2.x's defaults. A service without
  `description` now gets its name as the description, which 3.0.3 left empty.
- **The Browse dialog's OK works** ([#80](../../issues/80)). Since WiX 6 the dialog publishes
  nothing itself; msis's own dialog sets now publish its events, so choosing a folder in
  Browse or Change takes effect. A custom template copied from 3.0.5 needs the two rows
  (docs/templates.md).

**SBOM**, for the EU Cyber Resilience Act, aimed at BSI TR-03183-2 ([docs/sbom.md](docs/sbom.md)):
- `/INSPECT` reports what is inside a built `.msi` or bundle `.exe`. `/SBOM` writes a CycloneDX
  1.6 document for it, from the artifact rather than the script: every file with SHA-256 and
  SHA-512, and a bundle linking to its installers' documents (BOM-Links, verified before they
  are made). `/BUILD /SBOM` adds what only the build knows: each payload's source, the toolchain,
  and where each prerequisite came from.
- The script can say what msis cannot read: `<sbom source= for=>` merges a component SBOM for a
  payload file, `<component>` declares a file's identity, and a contradiction stops the build.
- `<vex>` carries vulnerability statements that lapse when their conditions no longer hold.
  `/SCAN` runs grype on the document and applies them. `/ANALYZE` runs syft on the MSI's payload
  and adds the packages that declare themselves (Python distributions, Maven jars, .NET
  `.deps.json` entries); on ProAKT 3.6.0.73 it took grype from 0 to 154 findings (D25).
- msis's own release ships an SBOM beside each installer, listing what is inside `msis.exe` and
  the hook DLL, under CC0-1.0.

**New warnings, and `/STRICT`.** Two layouts that build today but lose files when a feature is
removed later are deprecated: they build as before, warn, `/STRICT` refuses them, and msis 4
will. One is a `<service>` in another feature than its executable ([#77](../../issues/77), D22);
the other is two features installing one file ([#79](../../issues/79), D24). Each warning says
how to rewrite it. Two `<files>` for one target in the same feature stay allowed and silent. The
installer-hook danger warnings now name what to use instead.

**Fixes:**
- Every prerequisite download is pinned to a URL and SHA-256 and verified on every cache reuse
  ([#30](../../issues/30)); `sha256=` verifies a supplied one too ([#50](../../issues/50)).
- Two builds of one script are identical except the documented fields, and the ProductCode is
  derived from the package's inputs ([#66](../../issues/66), D20); no output depends on map
  order ([#73](../../issues/73)).
- A registry-only package builds ([#54](../../issues/54)); no permission component on an
  unnamed root ([#55](../../issues/55)); the minimal templates offer the install folder only
  when there is one ([#56](../../issues/56)); a false-like `INSTALL_DIR_DIALOG` or
  `INCLUDE_VCREDIST` no longer takes the template branch ([#57](../../issues/57)).
- `{{VAR}}` expands in `.reg` string values again ([#46](../../issues/46)) and in
  `<feature name>` ([#75](../../issues/75)); the `BUILD_TARGET` directory is created
  ([#42](../../issues/42)); the overwrite check and the build agree on the output file
  ([#41](../../issues/41)).

**Releases and docs:** a release build refuses a dirty tree, re-checks the prerequisite pins
([#49](../../issues/49)), gates on SBOM coverage ([#63](../../issues/63)) and stops on a failed
packaging step ([#53](../../issues/53)). [docs/decisions.md](docs/decisions.md) records the
questions that were settled on evidence (D1–D25), so they are not rediscovered; GitHub issues
replace `TODO.md`.

**3.0.5** — 2026-09-19 (tag [`v3.0.5`](../../releases/tag/v3.0.5))

A correctness release: issues #5–#28 are closed, most of them cases where msis did the wrong thing
**without saying so** (one, #26, closed as a documented limit rather than a fix). 3.0.4 was bumped
in the justfile but never tagged or published, so everything here has accumulated since 3.0.3.

- **Silent failures are build failures now.** A `<files source=>` that does not exist was skipped
  and the package shipped without the payload ([#24](../../issues/24)); so was a source directory
  that could not be read ([#25](../../issues/25)). Templates whose placeholder sets had drifted
  discarded generated content — in the silent x86 template that destroyed a preserved registry value
  ([#19](../../issues/19)). `/TEMPLATE` was ignored for silent packages ([#20](../../issues/20)).
  Any top-level item was orphaned when the script also declared a `<feature>`
  ([#15](../../issues/15), WIX0267), and a `<feature>` with no `<files>` orphaned the INSTALLDIR
  permission component ([#18](../../issues/18)). Each one now either builds correctly or fails
  loudly.
- **`preserve="yes"` survives real registry data.** A REG_BINARY default used a per-nibble `#x0#x1`
  encoding and the install failed with Error 1406 ([#6](../../issues/6)); the `.reg` default was not
  XML-escaped, so an apostrophe or ampersand broke the build ([#9](../../issues/9)); a REG_SZ
  default beginning with `#` failed with Error 1406 ([#11](../../issues/11)); and a few hundred
  preserved values emitted one SetProperty custom action each, tripping WIX0179
  ([#5](../../issues/5)).
- **Three kinds of value are now excluded from preservation, deliberately.** An install probe showed
  that the `Type='raw'` RegistrySearch which reads the live value *damages* two of them
  ([#10](../../issues/10)): a REG_EXPAND_SZ came back already expanded and lost its type, baking one
  machine's paths into the registry, and a REG_QWORD came back as its 8 raw bytes reinterpreted as
  UTF-16 text — written back as REG_SZ, with the install exiting 0. Both are therefore written fresh
  from the `.reg` file instead, alongside the existing multi-string exclusion: **a live edit to an
  expandable string, a QWORD or a multi-string is overwritten on upgrade**, which is the lesser harm
  against silent corruption. QWORDs keep the documented 32-bit truncation (Windows Installer's
  Registry table has no QWORD encoding), and DWORDs, strings and binaries are still preserved. A
  fourth limit is unchanged: an existing **empty** value is not preserved either — the `.reg`
  default overwrites it ([#26](../../issues/26)). See the [Tutorial](docs/tutorial.md) for the full
  rules.
- **Auto-bundles install the runtime they promise.** A `PLATFORM=x86` auto-bundle gated the VC++
  runtime on `InstallCondition='NOT VersionNT64'`, skipping it on every 64-bit machine, while
  detecting the **x64** runtime — so a 64-bit PC carrying only the x64 runtime was reported as
  satisfied, and on a machine with neither the gate blocked the install anyway. The MSI's own
  `VCREDIST_X86_*` launch condition then refused the install, making the product uninstallable from
  the bundle ([#8](../../issues/8)). An auto-bundle wraps one MSI whose architecture is pinned by
  `PLATFORM`, so the runtime now follows the package rather than the OS; explicit multi-architecture
  bundles keep the OS-driven conditions. `PLATFORM=arm64` likewise detected the x64 runtime rather
  than the ARM64 one ([#12](../../issues/12)).
- **Output naming.** Without `BUILD_TARGET`, an auto-bundle `.exe` landed in the current working
  directory, lost its patch version to a mis-parsed extension, and was reported at a third path that
  did not exist ([#27](../../issues/27)). `BUILD_TARGET` is now a **name pattern** rather than a
  literal output path — its directory and stem are shared by the `.wxs`, the `.msi` and the bundle
  `.exe`, as in msis-2.x — so setting one no longer asks `wix build` to write an MSI to a `.exe`
  path and fail ([#28](../../issues/28)).
- **`<remove-on-uninstall>` actually removes the folder.** `RemoveFolderEx` needs its folder
  property populated before `CostInitialize`, which is too early for `[INSTALLDIR]` to have
  resolved; the resolved path is now stored in the registry at install time and read back by a
  `RegistrySearch` in `AppSearch`. The element can also name a folder **and** a registry key at once
  without emitting duplicate component ids ([#23](../../issues/23)). Verified on a snapshotted VM:
  the named tree and its nested contents go, and the parent directory, a sibling directory and a
  neighbouring registry key come through with their contents and values unchanged.
- **One source file can install to more than one destination** ([#21](../../issues/21)): component
  GUIDs now take the target directory into account, so a second target no longer fails with WIX0369.
  GUIDs for packages that already worked are unchanged.
- **Variables and paths.** Nested `<set>` references resolved in random map order, which
  occasionally produced an output file literally named `...{{PRODUCT_VERSION}}.msi`
  ([#7](../../issues/7)); a `{{VAR}}` preceded by a backslash was not substituted and the backslash
  was eaten, breaking Windows paths ([#13](../../issues/13)); `$` and friends can be escaped in
  `.msis` filenames.
- **Build-time warnings channel** for the generator and the registry writer ([#14](../../issues/14))
  — hazards those packages detect can now be reported instead of being decided silently.
- `<service>` accepts a path-qualified file name, anchoring `ServiceInstall` to the target
  directory; `START_EXE` sets `WixShellExecTarget` through an immediate custom action, so MSI
  Formatted paths such as `[INSTALLDIR]App.exe` resolve at runtime.
- **Docs and tooling.** `docs/msis.xsd` and the tutorial document `<create-folder>` and
  `<remove-on-uninstall>` ([#22](../../issues/22)); `docs/roadmap.md` describes the code that exists
  ([#16](../../issues/16)); `just set-version X.Y.Z` replaces hand-editing the version
  ([#17](../../issues/17)). `todo-testme.md` records which behaviour has been verified on a real VM
  and which is still owed.

**3.0.3** — 2026-06-23 (tag [`v3.0.3`](../../releases/tag/v3.0.3))
- WiX 7 support alongside WiX 6, auto-detected at build time; the WiX 7 OSMF EULA is accepted automatically.
- `msis /SETUP-WIX` self-provisions the WiX toolchain and required extensions (replaced the earlier standalone setup scripts).
- `LAUNCH_TARGET` adds a "Launch" button to the bundle success page (bundle counterpart of the MSI's `START_EXE`), with the ARM64 path resolved correctly.
- `START_EXE` (the MSI "Launch *Product*" checkbox) is now an MSI Formatted path like `[INSTALLDIR]App.exe`, matching `LAUNCH_TARGET`'s semantics. It previously needed a WiX File Id, which msis generates opaquely per run — making it effectively unusable.
- **Logo branding overhaul.** `LOGO_PREFIX` now resolves the **bundle** logo too (previously MSI-only); logo files are searched in the `.msis` directory → custom-templates → template folder, and the bundle build binds those same paths so an explicit source-relative `LOGO_BOOTSTRAP` resolves like it always did for the MSI; and a missing/mistyped logo now produces a **build-time warning** instead of silently falling back to the WiX default. Removed two dead `bootstrap*.wxs` templates.
- **Installer-hook safety overhaul.** The native hook DLL's recursive uninstall cleanup
  (`REMOVE_FOLDERS_ON_UNINSTALL`, `REMOVE_REGISTRY_TREE`) once deleted runtime/customer data
  (e.g. a customer's SQLite database). It is now explicit and warned at build time, gated
  consistently across templates, false-value-aware, and exempt-able per file via the new
  `RETAIN_FILES_ON_UNINSTALL`. The hook DLL (`msi-simplica.dll`) is now **built and shipped by
  this repo** (`native/msi-simplica/`, x86/x64/arm64) instead of being a stale external
  dependency, and its retain/cleanup core is **unit-tested** (`just test-hooks`, run automatically
  before the DLL build) — see [Installer Hooks](docs/installer-hooks.md).
- Fixes: preserved registry keys; `quiet` attribute on `<execute>`; vcredist detection via Burn variables; LOCALAPPDATADIR/INSTALLDIR path collision; `fail-on-error` on `<execute>`; options-dialog browse button; test/coverage tooling.

**3.0.2** — 2026-04-23 (tag [`v3.0.2`](../../releases/tag/v3.0.2))
- Registry preservation on upgrade: keep existing values, remove only those msis created (UUID fix).
- Services: fixed sub-feature services (no duplicate `ComponentRef`); added `start-after-install`.
- Added the `create-folder` operation; permanent/non-permanent environment variables; variable replacement for Windows standard dirs (e.g. `APPDATADIR`).

**3.0.1** — 2026-03-13 (tag [`v3.0.1`](../../releases/tag/v3.0.1))
- New `/SET:NAME=VALUE` command-line override for `.msis` variables.
- `INSTALL_DIR_DIALOG` support; stackable install dirs (e.g. `FOO\BAR`); writeability check before invoking `wix.exe`.
- Fixes: bundle generation, an omitted-Features bug, the `APPDATADIR` product-name path, and registry upgrade handling.

**3.0.0** — 2026-02-05 (tag [`v3.0.0`](../../releases/tag/v3.0.0))
- Initial Go rewrite of MSI-Simplified targeting WiX 6 (first commit 2026-01-28): `.msis` → IR → WiX XML → MSI.
- Bundle (bootstrapper) support and prerequisites (.NET / C++ runtime); removed obsolete merge modules (MSM).
- Visual Studio 2026 support; colored `/HELP` and `/STATUS`; `just build` updates an installed copy.

## License

MIT License - see LICENSE file.

## Author

Gerson Kurz / NG Branch Technology GmbH
