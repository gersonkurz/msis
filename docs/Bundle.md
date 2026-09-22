# Bundle (Bootstrapper) Support

msis-3.x supports WiX Bundle (bootstrapper) generation for creating multi-MSI installers that can:
- Ship x86, x64, and ARM64 MSI packages in a single executable
- Install prerequisites (VC++ Redistributable, .NET Framework) before the main application
- Chain multiple installers in a defined sequence

## When to Use Bundles

Use bundles when you need to:
1. **Combine architectures**: Ship one installer that installs the correct MSI based on the target platform
2. **Include prerequisites**: Automatically install VC++ runtime or .NET Framework if not present
3. **Chain installers**: Install multiple packages in sequence

For single-platform, no-prerequisites scenarios, a regular MSI is simpler and preferred.

**Why bundles instead of MSI custom actions?**  
MSI is not designed to install other installers (MSI/EXE) via custom actions. Doing so is unreliable, breaks rollback, and is often blocked by enterprise policy. The supported, robust approach is a WiX Bundle (Burn): prerequisites first, then the MSI.

## Auto-Bundling with `<requires>`

**New in 3.x:** For simple prerequisite scenarios, use `<requires>` at the setup level instead of explicitly declaring a bundle:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <requires type="vcredist" version="2022"/>
  <feature name="MyApp">
    <files source="bin" target="INSTALLDIR"/>
  </feature>
</setup>
```

When you build with `/BUILD`, MSIS automatically:
1. Generates the MSI with launch conditions
2. Creates a bundle wrapper that installs prerequisites first

Use `/STANDALONE` to skip auto-bundling and generate only the MSI with launch conditions.

See [prerequisites.md](prerequisites.md) for complete documentation on the `<requires>` element.

## Basic Bundle Syntax

### Legacy Shorthand (Simple Multi-Arch)

The simplest bundle combines MSI packages for multiple architectures:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{GUID-HERE}"/>

  <bundle source_64bit="MyApp-x64.msi" source_32bit="MyApp-x86.msi" source_arm64="MyApp-arm64.msi"/>
</setup>
```

You can omit any architecture you don't need (e.g., omit `source_32bit` for 64-bit only).

### New Nested Syntax (Full Control)

For more control, use nested elements:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{GUID-HERE}"/>

  <bundle>
    <prerequisite type="vcredist" version="2022"/>
    <prerequisite type="netfx" version="4.8"/>
    <msi source_64bit="MyApp-x64.msi" source_32bit="MyApp-x86.msi" source_arm64="MyApp-arm64.msi"/>
  </bundle>
</setup>
```

## Prerequisites

### Well-Known Prerequisites

msis-3.x has built-in support for common prerequisites:

| Type | Versions | Auto-Download |
|------|----------|---------------|
| `vcredist` | 2022, 2019 | ✅ Yes |
| `vcredist` | 2017, 2015 | ❌ No (provide `source`) |
| `netfx` | 4.8.1, 4.8, 4.7.2 | ✅ Yes |
| `netfx` | 4.7.1, 4.7, 4.6.2 | ❌ No (provide `source`) |

Versions with auto-download are fetched from a pinned, version-specific Microsoft URL, verified against a pinned SHA-256 both when downloaded and every time the cached copy is reused, and cached locally — see [Integrity: pinned URL and digest](prerequisites.md#integrity-pinned-url-and-digest). Other versions require a `source` attribute pointing to the installer file, which msis does not verify.

Example:
```xml
<prerequisite type="vcredist" version="2022"/>
<prerequisite type="netfx" version="4.8"/>
```

For versions without auto-download:
```xml
<prerequisite type="vcredist" version="2015" source="prereqs/vc_redist.x64.exe"/>
```

### Automatic Prerequisite Downloads

**New in 3.x:** MSIS automatically downloads prerequisite installers from Microsoft and caches them in `%LOCALAPPDATA%\msis\prerequisites\`. This means you don't need to manually download or include prerequisite installers.

First build downloads prerequisites; subsequent builds use the cache. Each download is pinned to a version-specific URL and a SHA-256 digest carried in msis, checked after the download and again on every reuse of the cached file; a cached file that stopped matching is replaced, and a download that does not match refuses the build. `msis /STATUS` shows the cache with each file's verification result. Details, and the reasoning behind pinning rather than fetching "latest", are in the [prerequisites guide](prerequisites.md#integrity-pinned-url-and-digest).

### Manual Prerequisite Files (Legacy)

If you prefer to manage prerequisite files manually, place them in a `prerequisites` folder:

```
myapp/
├── MyApp.msis
├── prerequisites/
│   ├── vc_redist.x64.exe
│   ├── vc_redist.x86.exe
│   └── ndp48-x86-x64-allos-enu.exe
├── MyApp-x64.msi
└── MyApp-x86.msi
```

Override the folder with `PREREQUISITES_FOLDER`:
```xml
<set name="PREREQUISITES_FOLDER" value="C:\shared\prereqs"/>
```

### Custom Prerequisite Source

Override the default source path for a specific prerequisite:
```xml
<prerequisite type="vcredist" version="2022" source="C:\installers\vcredist.exe"/>
```

When a custom source is provided, only a single ExePackage is emitted (you handle architecture selection).

## MSI Packages

### Platform-Specific MSIs

```xml
<msi source_64bit="MyApp-x64.msi" source_32bit="MyApp-x86.msi" source_arm64="MyApp-arm64.msi"/>
```

The installer automatically selects the correct MSI based on the target platform. All attributes are optional - include only the architectures you support.

### Single MSI

For platform-neutral packages:
```xml
<msi source="MyApp.msi"/>
```

## Custom Executable Packages

Add custom executables to the install chain:

```xml
<bundle>
  <exe id="CustomSetup" source="custom-setup.exe"
       detect="EXISTS('HKLM\SOFTWARE\CustomApp')"
       args="/silent"/>
  <msi source_64bit="MyApp-x64.msi" source_32bit="MyApp-x86.msi"/>
</bundle>
```

Attributes:
- `id` - WiX package identifier (auto-generated from filename if omitted)
- `source` - Path to the executable
- `detect` - WiX condition to check if already installed (optional)
- `args` - Command-line arguments for silent install (optional)

## Bundle Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PRODUCT_NAME` | Display name in bootstrapper UI | Required |
| `PRODUCT_VERSION` | Version number | Required |
| `MANUFACTURER` | Company name | Required |
| `UPGRADE_CODE` | Bundle upgrade code (GUID) | Required |
| `LICENSE_URL` | URL to license agreement | Required for UI bundle |
| `LOGO_BOOTSTRAP` | Logo image for bootstrapper UI | `{LOGO_PREFIX}_LogoBootstrap.bmp` |
| `LOGO_PREFIX` | Prefix for default logo files | Empty (uses WiX defaults) |
| `PREREQUISITES_FOLDER` | Path to prerequisite installers | `./prerequisites` |
| `LAUNCH_TARGET` | Program to offer launching from the success page (adds a "Launch" button) | Empty (no button) |

**Logo customization example:**
```xml
<set name="LOGO_PREFIX" value="MyCompany"/>
<!-- Bundle uses MyCompany_LogoBootstrap.bmp; set LOGO_BOOTSTRAP directly to override. -->
```

`LOGO_PREFIX` resolves the bundle logo too (not just the MSI). msis looks for the file in your
`.msis` directory, the custom-templates folder, then the base template folder — and **warns** at
build time if it set a logo but the file is missing. See
[Logo Customization](templates.md#logo-customization) for the full resolution model shared with the MSI.

**Launch button example:**

Set `LAUNCH_TARGET` to add a "Launch" button on the bundle's success page (the
WixStandardBootstrapperApplication `LaunchTarget` variable). The value is a Burn *Formatted*
path, so it may reference bundle variables in `[...]` brackets — most usefully `[InstallFolder]`,
which msis already computes:

```xml
<set name="LAUNCH_TARGET" value="[InstallFolder]\MyApp.exe"/>
```

When `LAUNCH_TARGET` is unset, no button is shown. This is the bundle-level counterpart of the
MSI-level `START_EXE`. Silent bundles have no success page, so the setting has no effect there.

### Bundle vs MSI: license and launch settings

These bundle variables are **not** the same as their MSI counterparts — different engine,
different value format — and one does not substitute for the other:

| Capability | Bundle variable (here) | MSI variable (standalone `.msi`) |
|------------|------------------------|----------------------------------|
| License | `LICENSE_URL` — a **URL**, shown as a hyperlink | `LICENSE_FILE` — an **RTF file**, shown in an accept-to-continue dialog |
| Launch on finish | `LAUNCH_TARGET` — a Burn Formatted path (`[InstallFolder]\App.exe`) | `START_EXE` — an MSI Formatted path (`[INSTALLDIR]App.exe`, no leading `\`) |

So a bundle that only sets `LICENSE_URL` shows the license in the bundle UI but **not** in the
individual MSIs; set `LICENSE_FILE` on the MSI side too if those are distributed on their own.
When MSIs ship inside a bundle, the bundle drives the visible UI, so the bundle variables are
usually the ones that matter. See [templates.md](templates.md#installer-ui-options) for the MSI side.

## Output

Bundles produce an `.exe` file (not `.msi`). **The default output goes beside the source**, named
after it — msis never writes into the directory you happened to launch it from:

| Source | Default output |
|--------|----------------|
| `C:\src\setup.msis` containing `<bundle>` | `C:\src\setup.exe` |
| `C:\src\app.msis` with `<requires>` (auto-bundle) | `C:\src\app.msi` **and** `C:\src\app.exe` |

Override with `BUILD_TARGET`:
```xml
<set name="BUILD_TARGET" value="MyApp-Setup.exe"/>
```

`BUILD_TARGET` is a **name pattern, not a literal filename**. Its directory and stem are shared
by every artifact of the build, and each one supplies its own extension — so one value names the
`.wxs`, the `.msi` and the bundle `.exe` together, exactly as msis-2.x did:

| `BUILD_TARGET` | `.wxs` | `.msi` | bundle `.exe` |
|----------------|--------|--------|---------------|
| `dist\App-1.0.0.exe` | `dist\App-1.0.0.wxs` | `dist\App-1.0.0.msi` | `dist\App-1.0.0.exe` |
| `dist\App-1.0.0.msi` | `dist\App-1.0.0.wxs` | `dist\App-1.0.0.msi` | `dist\App-1.0.0.exe` |
| `dist\App-1.0.0` | `dist\App-1.0.0.wxs` | `dist\App-1.0.0.msi` | `dist\App-1.0.0.exe` |

Either extension therefore works on a package that auto-bundles, and so does none. Passing the
value straight to `wix build` used to make it infer the output type from the extension and fail
([issue #28](https://github.com/gersonkurz/msis/issues/28)).

Two more things to know:

- A **relative** value is resolved against the **current working directory**, not against the
  `.msis`. `just release-all` relies on this: `bootstrap/setup-bundle.msis` sets
  `dist\msis-<version>-setup.exe` and the recipe runs from `bootstrap/`, so the artifacts land in
  `bootstrap/dist/`. The directory has to exist; msis does not create it.
- Only a real `.exe` or `.msi` suffix is replaced. A value with **no** extension keeps every
  version segment: `MyApp-1.0.0` yields `MyApp-1.0.0.msi` and `MyApp-1.0.0.exe`. (msis-2.x cut
  at the last `.` here, which turned that into `MyApp-1.0`.)

A `.exe` `BUILD_TARGET` on a package that does **not** bundle — no `<requires>`, or
`/STANDALONE` — names an artifact msis will not produce. It builds the MSI, names it `.msi`,
and says so in a warning rather than renaming the target silently.

The bundle's intermediate WiX source is written as `<output base>-bundle.wxs` next to the
output — `setup-bundle.wxs`, `App-1.0.0-bundle.wxs` — and deleted after a successful build
unless you pass `/RETAINWXS`.

Earlier versions defaulted to `{PRODUCT_NAME}-{PRODUCT_VERSION}.exe` as a *relative* path, which
landed in the working directory and lost its patch version to a mis-parsed extension
([issue #27](https://github.com/gersonkurz/msis/issues/27)). A `BUILD_TARGET` still lands where
it always did — the fix never changed how a relative value is resolved — so setups that set one
ending in `.exe` or `.msi`, this repo's own release among them, produce the same filenames as
before. A `BUILD_TARGET` with *no* extension is the one case that changes: it now keeps its full
version, so `MyApp-1.0.0` yields `MyApp-1.0.0.exe` where it used to yield `MyApp-1.0.exe`.

**A relative `BUILD_TARGET` resolves against the directory msis is run from**, not against the
`.msis` file's directory: `dist\setup.exe` means `<current directory>\dist\setup.exe`, and the
`.wxs`, `.msi` and `.exe` all go there. (msis-2.x changed into the `.msis` directory before
building, so there a relative target landed beside the script; msis 3 does not change directory.
Settled as D6 in `docs/decisions.md`.) Before #41 the check that removes a stale output before
the build resolved the same relative value against a different base and could delete a file the
build was never going to write; every consumer of the output path now reads one absolute value.
Run msis from the directory the target is meant to be relative to, or give an absolute target.

## Silent vs UI Bundles

### UI Bundle (Default)

Uses WiX Standard Bootstrapper Application with `hyperlinkLicense` theme:
- Displays license agreement link
- Shows installation progress
- Requires `LICENSE_URL` variable

### Silent Bundle

When `silent="true"` on the setup element:
```xml
<setup silent="true">
  <bundle>...</bundle>
</setup>
```

Uses `none` theme - no UI, suitable for automated deployments.

## Install Chain Order

Packages are installed in this order:
1. Prerequisites (in declaration order)
2. Custom exe packages (in declaration order)
3. MSI package(s)

## Architecture Detection

The bundle uses WiX Burn conditions to select the correct packages:

| Architecture | Condition | Description |
|--------------|-----------|-------------|
| ARM64 | `NativeMachine = 43620` | ARM64 Windows (0xAA64) |
| x64 | `VersionNT64 AND NOT NativeMachine = 43620` | 64-bit Windows (excludes ARM64) |
| x86 | `NOT VersionNT64` | 32-bit Windows |

`NativeMachine` is a built-in Burn variable containing the `IMAGE_FILE_MACHINE_*` value for the native OS architecture.

## Example: Complete Bundle

```xml
<setup>
  <!-- Product information -->
  <set name="PRODUCT_NAME" value="My Application"/>
  <set name="PRODUCT_VERSION" value="2.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{12345678-1234-1234-1234-123456789ABC}"/>
  <set name="LICENSE_URL" value="https://mycompany.com/license"/>

  <!-- Bundle configuration -->
  <bundle>
    <!-- Install VC++ runtime if needed -->
    <prerequisite type="vcredist" version="2022"/>

    <!-- Install .NET Framework 4.8 if needed -->
    <prerequisite type="netfx" version="4.8"/>

    <!-- Custom prerequisite -->
    <exe id="DatabaseSetup" source="db-setup.exe"
         detect="EXISTS('HKLM\SOFTWARE\MyCompany\Database')"
         args="/quiet"/>

    <!-- Main application MSIs -->
    <msi source_64bit="MyApp-x64.msi" source_32bit="MyApp-x86.msi" source_arm64="MyApp-arm64.msi"/>
  </bundle>
</setup>
```

## Migration from C++ Bundler

If you previously used a custom C++ bundler (build.cmd) to combine MSIs:

1. Create a new .msis file with `<bundle>` element
2. Reference your existing MSI files:
   ```xml
   <bundle source_64bit="output/MyApp-x64.msi" source_32bit="output/MyApp-x86.msi"/>
   ```
3. Add any prerequisites your application needs
4. Build with `msis bundle.msis`

The WiX bundle provides:
- Proper uninstall tracking (single entry in Add/Remove Programs)
- Prerequisite detection (skips if already installed)
- Consistent UI across all packages
- Repair support

## WiX Extensions

Bundle builds automatically include these WiX extensions:
- `WixToolset.BootstrapperApplications.wixext` - Bootstrapper Application Library (WiX 6/7; renamed from `Bal` in WiX 6)
- `WixToolset.Util.wixext` - Utility functions
- `WixToolset.Netfx.wixext` - .NET Framework detection

## Troubleshooting

### "Unknown prerequisite" Error

The prerequisite type or version is not in the built-in registry. Use a custom `source` path:
```xml
<prerequisite type="vcredist" version="2022" source="path/to/vc_redist.exe"/>
```

### "Bundle has no MSI source" Error

Ensure your bundle has either:
- Legacy shorthand: `<bundle source_64bit="..." source_32bit="..."/>`
- Or nested MSI element: `<msi source="..." />` or `<msi source_64bit="..." source_32bit="..."/>`

### Prerequisites Not Found

Check that prerequisite files exist in `PREREQUISITES_FOLDER` (default: `./prerequisites`).

For VC++ Redistributable, expected filenames are:
- `vc_redist.x64.exe`
- `vc_redist.x86.exe`

For .NET Framework 4.8:
- `ndp48-x86-x64-allos-enu.exe`

## See Also

- [Tutorial](tutorial.md) - Step-by-step guides including bundle basics
- [Schema Reference](msis.xsd) - Complete XML element reference
- [Developer Overview](overview.md) - Architecture and internals
