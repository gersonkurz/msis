# msis

A Windows installer generator that transforms declarative `.msis` XML scripts into MSI packages via WiX Toolset 6.

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
    <files source="bin\*" target="[INSTALLDIR]"/>
    <shortcut name="MyApp" target="DESKTOP" file="[INSTALLDIR]MyApp.exe"/>
    <registry file="settings.reg"/>
  </feature>
</setup>
```

Instead of 500+ lines of WiX XML. The tool handles component GUIDs, directory trees, feature mapping, registry import, and [multi-architecture bundles](docs/Bundle.md).

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
| **[Schema](docs/msis.xsd)** | Complete XML element and attribute reference |
| **[Roadmap](docs/roadmap.md)** | Planned features and future direction |
| **[Developer Overview](docs/overview.md)** | Architecture, code structure, and internals |

## Command Line

```
msis [OPTIONS] FILE [FILE...]

Options:
  /BUILD                Generate WXS and build MSI using WiX
  /RETAINWXS            Keep the generated .wxs file after build
  /TEMPLATE:PATH        Use custom WiX template
  /TEMPLATEFOLDER:PATH  Base template folder
  /CUSTOMTEMPLATES:PATH Custom templates overlay
  /DRY-RUN              Parse and validate only, no output
  /STATUS               Show configuration (WiX location, templates)
  /?, /HELP             Show help
```

## Migration from msis-2.x

msis-3.x is largely compatible with msis-2.x scripts:

| Aspect | msis-2.x | msis-3.x |
|--------|----------|----------|
| WiX Version | WiX 3.x/4.x | WiX 6 or 7 (auto-detected) |
| Default Architecture | x86 | x64 |
| Bundle Engine | Custom C++ | WiX Burn |

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

Reconstructed from the Git log. No releases are tagged yet, so each version is identified by
its commit SHA. `3.0.3` is the current in-progress baseline (not yet released).

**3.0.3** — *current, unreleased*
- WiX 7 support alongside WiX 6, auto-detected at build time; the WiX 7 OSMF EULA is accepted automatically.
- `msis /SETUP-WIX` self-provisions the WiX toolchain and required extensions (replaced the earlier standalone setup scripts).
- `LAUNCH_TARGET` adds a "Launch" button to the bundle success page (bundle counterpart of the MSI's `START_EXE`).
- Fixes: preserved registry keys; `quiet` attribute on `<execute>`; vcredist detection via Burn variables; LOCALAPPDATADIR/INSTALLDIR path collision; `fail-on-error` on `<execute>`; options-dialog browse button; test/coverage tooling.

**3.0.2** — 2026-04-23 (`3ab349f`)
- Registry preservation on upgrade: keep existing values, remove only those msis created (UUID fix).
- Services: fixed sub-feature services (no duplicate `ComponentRef`); added `start-after-install`.
- Added the `create-folder` operation; permanent/non-permanent environment variables; variable replacement for Windows standard dirs (e.g. `APPDATADIR`).

**3.0.1** — 2026-03-13 (`0a1b6c9`)
- New `/SET:NAME=VALUE` command-line override for `.msis` variables.
- `INSTALL_DIR_DIALOG` support; stackable install dirs (e.g. `FOO\BAR`); writeability check before invoking `wix.exe`.
- Fixes: bundle generation, an omitted-Features bug, the `APPDATADIR` product-name path, and registry upgrade handling.

**3.0.0** — 2026-02-05 (`2c42e3d`)
- Initial Go rewrite of MSI-Simplified targeting WiX 6 (first commit 2026-01-28): `.msis` → IR → WiX XML → MSI.
- Bundle (bootstrapper) support and prerequisites (.NET / C++ runtime); removed obsolete merge modules (MSM).
- Visual Studio 2026 support; colored `/HELP` and `/STATUS`; `just build` updates an installed copy.

## License

MIT License - see LICENSE file.

## Author

Gerson Kurz / NG Branch Technology GmbH
