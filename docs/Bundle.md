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
<prerequisite type="vcredist" version="2022" source="C:\installers\vcredist.exe"
              sha256="cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b"/>
```

When a custom source is provided, only a single ExePackage is emitted (you handle architecture
selection). A download msis performs is pinned and verified by msis (D5); a file you supply is
verified only if you give its `sha256=` — then msis hashes the copy WiX will package and refuses
the build on a mismatch. Without it the file is chained unverified, with a build warning. See
[Custom/Offline Source](prerequisites.md#customoffline-source).

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
  <search variable="CustomAppInstalled" root="HKLM" key="SOFTWARE\CustomApp" bitness="64"/>
  <exe id="CustomSetup" source="custom-setup.exe"
       detect="CustomAppInstalled"
       args="/silent"/>
  <msi source_64bit="MyApp-x64.msi" source_32bit="MyApp-x86.msi"/>
</bundle>
```

Attributes:
- `id` - WiX package identifier (auto-generated from filename if omitted)
- `source` - Path to the executable
- `detect` - when the package counts as already installed (optional). It is a **Burn**
  condition over Burn variables, and msis copies it verbatim into the `ExePackage`'s
  `DetectCondition`. It is not an MSI condition: Burn has no `EXISTS()`, and nothing in `detect`
  reads the registry by itself. WiX does not check the condition either, so a wrong one builds
  without complaint and fails only on the target machine. Without `detect`, Burn cannot tell the
  package is already there, so it runs on every install of the bundle.
- `args` - Command-line arguments for silent install (optional)
- `per-machine` - `yes` marks the package per-machine, so Burn runs it elevated (optional). A
  package without it takes the bundle's scope. That is per-machine as long as the chain holds no
  per-user package, which covers every bundle of MSIs msis built, since those are per-machine.
  An explicit `<bundle>` that chains a per-user MSI from elsewhere becomes per-user, and its
  unmarked packages with it; there, give a machine-wide installer `per-machine="yes"` (#90, #94,
  decisions D30, D32).

### Detecting from the registry: `<search>`

A Burn condition can only test variables, so registry state is first read into one. A `<search>`
inside `<bundle>` does that: it becomes a `util:RegistrySearch` that sets a Burn variable
before Burn decides what to install (#90, decisions D30).

| Attribute | |
|---|---|
| `variable` | The Burn variable it sets (required). A letter or underscore, then letters, digits and underscores. Not a Burn built-in (`VersionNT64`, `NativeMachine`, ...), not a name starting with `Wix`, not one the bundle templates use (`VcppRuntimeX64Installed`, `InstallFolder`, `NETFRAMEWORK45`, ...), and unique. |
| `root` | `HKLM` or `HKCU` (required) |
| `key` | The registry key (required), without `WOW6432Node`: `bitness` chooses the view |
| `value` | The value name (optional) |
| `result` | `exists` (default) or `value` |
| `bitness` | `32` or `64` (required): which registry view is read |

What each combination sets:

| `result` | `value` | The variable is |
|---|---|---|
| `exists` | omitted | 1 if the key exists, else 0 |
| `exists` | given | 1 if that value exists, whatever it holds (a DWORD 0 counts as existing), else 0 |
| `value` | given | the value's data; left **unset** if there is none |
| `value` | omitted | the key's default (unnamed) value; unset if there is none |

- **`bitness` is required**, because the wrong view is how a prerequisite gets installed on
  every run. On 64-bit Windows a 32-bit installer writes under `WOW6432Node`, which
  `bitness="32"` reads; `bitness="64"` reads what 64-bit installers write.
- **A `REG_SZ` is a string.** To compare it as a version, compare it with a version literal:
  `MyVersion >= v2.1` (the `v` makes Burn compare versions); `MyVersion >= "2.1"` compares
  strings.
- **Searches are independent.** Burn runs them before it detects packages, but their order in
  the script is no guarantee, so one search cannot use another's result.
- **A search no `<exe>` refers to** (in `detect` or `args`) gets a warning, since a misspelt
  name would leave the package undetected. A custom bundle template may use the variable
  legitimately; the warning says only what this script shows.
- The searches go into the bundle templates' `{{{SEARCHES}}}` placeholder. A custom bundle template
  without it fails the build when the script has a search.
- A condition over Burn's built-in variables (`VersionNT64`, `NativeMachine`, ...) needs no
  search. The built-in VC++ detection works like a search: the shipped templates declare
  `VcppRuntimeX64Installed` and the others, and the `vcredist` prerequisite tests them.

### Example: the WebView2 Runtime, machine-wide

Microsoft's rule: the runtime is installed if `pv` under its EdgeUpdate client key exists, is
not empty and is above `0.0.0.0`. On 64-bit Windows the machine-wide key sits in the 32-bit
view ([Microsoft](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution#detect-if-a-webview2-runtime-is-already-installed)).

```xml
<bundle>
  <search variable="WebView2Machine" root="HKLM" bitness="32" value="pv" result="value"
          key="SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"/>
  <exe id="WebView2" source="MicrosoftEdgeWebview2Setup.exe" args="/silent /install"
       per-machine="yes" detect="WebView2Machine &gt; v0.0.0.0"/>
  <msi source="MyApp.msi"/>
</bundle>
```

This detects only a machine-wide runtime, and the bundle installs one, elevated, for every user.
`per-machine="yes"` only says so explicitly. Microsoft also registers a per-user runtime under
`HKCU\Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`. Accepting
that one too (`OR WebView2User > v0.0.0.0`, from a second search) is right only when the runtime
is needed just for the user running setup: it is the HKCU of whoever runs the bundle, so a
per-user runtime found there would skip the machine-wide install other users need.

msis has no built-in `webview2` prerequisite. Microsoft publishes the WebView2 installers as
evergreen links, which msis's prerequisite pinning (decisions D5) does not accept, so you supply
the installer yourself. A supplied `<exe>` is not checked against a digest the way
`<prerequisite source= sha256=>` is.

## Bundle Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PRODUCT_NAME` | Display name in bootstrapper UI | Required |
| `PRODUCT_VERSION` | Version number | Required |
| `MANUFACTURER` | Company name | Required |
| `MANUFACTURER_URL` | The product creator's web address. Written as the bundle's `AboutUrl` (Programs and Features) and read back by `/SBOM` as the product creator's URL. Burn has no email field, so `MANUFACTURER_EMAIL` reaches only the MSI (#64) | Empty |
| `UPGRADE_CODE` | Bundle upgrade code (GUID) | Required |
| `LICENSE_URL` | URL to license agreement. Without it the bundle shows no license link and no accept checkbox, and Install is available at once | Empty |
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
  `.msis` (msis-2.x changed into the `.msis` directory before building, so there a relative target
  landed beside the script; msis 3 does not change directory — settled as D6 in
  `docs/decisions.md`). `just release-all` relies on this: `bootstrap/setup-bundle.msis` sets
  `dist\msis-<version>-setup.exe` and the recipe runs from `bootstrap/`, so the artifacts land in
  `bootstrap/dist/`. Every consumer of the output path reads one absolute value, so the check
  that removes a stale output before the build cannot delete a different file than the one the
  build writes (#41). Run msis from the directory the target is meant to be relative to, or give
  an absolute target.
- The target's **directory is created if it does not exist**, as msis-2.x did (#42, D7):
  `dist\App-1.0.0.msi` on a fresh checkout creates `dist\`. The `.wxs` is the first artifact
  written and shares its directory with the `.msi` and `.exe`, so creating it creates the output
  directory. A path that cannot be created — a file already has its name, say — fails naming
  the directory.
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
Where a relative target lands, and that its directory is created, is under *Two more things to
know* above.

## Silent vs UI Bundles

### UI Bundle (Default)

Uses WiX Standard Bootstrapper Application with `hyperlinkLicense` theme:
- Displays the license agreement link when `LICENSE_URL` is set
- Shows installation progress
- Shows the MSI's own UI (its dialogs, or reduced progress under `/passive`)

### Silent Bundle

When `silent="true"` on the setup element:
```xml
<setup silent="true">
  <bundle>...</bundle>
</setup>
```

Meant for unattended deployment (#95, decisions D31):
- the same standard bootstrapper and theme, with the Options button hidden and a license link
  only if `LICENSE_URL` is set;
- the chained MSI shows **no UI of its own**: msis leaves out `bal:DisplayInternalUICondition`
  on every MSI package, in an explicit `<bundle>` and in the auto-bundle alike.

It is silent when you start it so: the standard bootstrapper has no switch that forces quiet mode
from inside the bundle.

```
setup.exe /quiet /norestart      no UI at all
setup.exe /passive /norestart    progress only, no questions
```

Started with no switch, it shows the standard bootstrapper window. The install folder is the
MSI's default; the bundle does not forward `INSTALLDIR` from its command line to the MSI.

**Bundles built by msis 3.0.6 or earlier from the shipped silent template do not start at all**
(0x80070490). That template gave the bootstrapper `Theme="none"`, which ships no theme. Rebuild
them. A custom bundle template that still has `Theme="none"` on
`WixStandardBootstrapperApplication` now fails the build with a message.

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

    <!-- Custom prerequisite, skipped where its registry key exists -->
    <search variable="DatabaseInstalled" root="HKLM" key="SOFTWARE\MyCompany\Database" bitness="64"/>
    <exe id="DatabaseSetup" source="db-setup.exe"
         detect="DatabaseInstalled"
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
