# Templates and Customization

msis uses Handlebars templates to generate WiX XML. This document covers template locations, structure, and customization options including logo branding.

## Template Locations

msis searches for templates in the following order:

1. **Custom templates** (command line): `/CUSTOMTEMPLATES:path`
2. **User templates**: `%LOCALAPPDATA%\msis\custom`
3. **Template folder** (command line): `/TEMPLATEFOLDER:path`
4. **Installed templates**: `%LOCALAPPDATA%\msis\templates`
5. **Portable templates**: `<executable-dir>\templates`
6. **Current directory**: `.\templates`

Files in earlier locations override later ones, so you can selectively replace individual templates without copying the entire folder.

## Template Structure

```
templates/
├── x64/                    # 64-bit MSI templates
│   └── template.wxs        # Full UI installer (a silent x64 build falls back to this)
├── x86/                    # 32-bit MSI templates
│   ├── template.wxs        # Full UI installer
│   └── template-silent.wxs # Silent/minimal installer
├── minimal/                # Minimal templates (no UI)
│   └── template.wxs
├── minimal-x86/            # Minimal 32-bit templates
│   └── template.wxs
├── bundle.wxs              # Bundle with UI
├── bundle-silent.wxs       # Silent bundle
├── wixlib/                 # Shared WiX libraries
└── custom/                 # User customizations (empty by default)
```

The recommended way to handle the VC++ runtime is `<requires type="vcredist" version="2022"/>` (see
[Prerequisites](prerequisites.md)) — VC++ merge modules are deprecated by Microsoft. The `minimal`
and x86 silent templates are MSM-free; the regular `x64`/`x86` templates still carry legacy
`<Merge>`/`<MergeRef>` blocks pending a separate, customer-affecting migration.

## Selecting Templates

By default, msis uses `x64/template.wxs` for 64-bit builds and `x86/template.wxs` for 32-bit builds.

Override with command line options:

```bash
# Use minimal template (no UI)
msis /BUILD /TEMPLATE:templates/minimal/template.wxs setup.msis

# Use custom template folder
msis /BUILD /TEMPLATEFOLDER:my-templates setup.msis

# Override with custom templates (highest priority)
msis /BUILD /CUSTOMTEMPLATES:my-overrides setup.msis
```

## Logo Customization

The installer UI displays logo images at various stages. msis supports customizing these via variables.

### Logo Variables

| Variable | Purpose | Default Size |
|----------|---------|--------------|
| `LOGO_BANNER` | Top banner on wizard pages | 493 x 58 pixels |
| `LOGO_DIALOG` | Side panel on welcome/finish pages | 493 x 312 pixels |
| `LOGO_BOOTSTRAP` | Bundle/bootstrapper UI logo | 75 x 75 pixels |
| `LOGO_PREFIX` | Prefix for auto-discovered logo files | (none) |

Both methods apply to the **MSI** (`LOGO_BANNER`, `LOGO_DIALOG`) and the **bundle**
(`LOGO_BOOTSTRAP`) — `LOGO_PREFIX` resolves logos for both.

### Where msis looks for logo files

For both methods, msis searches these directories **in order** (the same order WiX uses for its
build bind paths):

1. the directory of your `.msis` script,
2. the custom-templates overlay folder (`/CUSTOMTEMPLATES`),
3. the base template folder (`/TEMPLATEFOLDER`).

If a logo you asked for cannot be found in any of these, msis prints a **warning** at build time
(naming the variable, the file it looked for, and the paths searched) and lets WiX fall back to its
built-in default — it is no longer a silent no-op.

### Method 1: Explicit Logo Paths

Set the logo variables directly in your `.msis` file. The value is passed to WiX verbatim and
resolved against the search paths above, so a path relative to your `.msis` works:

```xml
<set name="LOGO_BANNER" value="branding\banner.bmp"/>
<set name="LOGO_DIALOG" value="branding\dialog.bmp"/>
<set name="LOGO_BOOTSTRAP" value="branding\logo.bmp"/>   <!-- bundle only -->
```

### Method 2: Logo Prefix (Convention-Based)

Set `LOGO_PREFIX` and msis will look for files following a naming convention:

```xml
<set name="LOGO_PREFIX" value="MyCompany"/>
```

With this setting, msis looks for (in the search paths above):
- `MyCompany_WixUiBanner.bmp` (MSI banner)
- `MyCompany_WixUiDialog.bmp` (MSI dialog)
- `MyCompany_LogoBootstrap.bmp` (bundle)

An explicit `LOGO_BANNER`/`LOGO_DIALOG`/`LOGO_BOOTSTRAP` always wins over the prefix-derived name
for that slot.

### Logo Image Requirements

| Image | Format | Size | Notes |
|-------|--------|------|-------|
| Banner | BMP | 493 x 58 | Horizontal strip at top of wizard |
| Dialog | BMP | 493 x 312 | Left panel on welcome/complete pages |
| Bootstrap | BMP | 75 x 75 | Bundle UI icon |

All images must be Windows BMP format. PNG/JPG are not supported by WiX.

### Example: Custom Branding

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{GUID}"/>

  <!-- Custom branding -->
  <set name="LOGO_PREFIX" value="MyCompany"/>

  <feature name="Main">
    <files source="bin\*" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

With files:
```
project/
  setup.msis
  bin/
    myapp.exe
  templates/
    custom/
      MyCompany_WixUiBanner.bmp
      MyCompany_WixUiDialog.bmp
```

## Installer UI Options

The standard MSI templates support optional dialogs for license agreement and install directory selection.

### UI Variables

| Variable | Purpose | Value |
|----------|---------|-------|
| `LICENSE_FILE` | Show license agreement dialog | Path to RTF file |
| `INSTALL_DIR_DIALOG` | Show install directory selection dialog | `true` to enable |
| `START_EXE` | Offer "Launch *Product*" checkbox on the final dialog | MSI **Formatted path** to the installed exe (e.g. `[INSTALLDIR]App.exe`) |

### Dialog Flow

The installer dialog sequence depends on which options are enabled:

| LICENSE_FILE | INSTALL_DIR_DIALOG | Dialog Flow |
|--------------|-------------------|-------------|
| No | No | Welcome → Features → Ready → Install |
| No | Yes | Welcome → **Install Dir** → Features → Ready → Install |
| Yes | No | Welcome → **License** → Features → Ready → Install |
| Yes | Yes | Welcome → **License** → **Install Dir** → Features → Ready → Install |

### Example: Full UI with License and Directory Selection

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{GUID}"/>
  <set name="INSTALL_FOLDER" value="MyCompany\MyApp"/>

  <!-- Show license agreement (RTF format required) -->
  <set name="LICENSE_FILE" value="license.rtf"/>

  <!-- Allow user to change install location -->
  <set name="INSTALL_DIR_DIALOG" value="true"/>

  <feature name="Main">
    <files source="bin\*" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

### License File Requirements

- Must be RTF (Rich Text Format) - not TXT, DOC, or PDF
- Place in your project folder or a bind path location
- The dialog shows "I accept the terms" checkbox; user must accept to proceed

### Install Directory Dialog

When enabled, users can:
- See the default installation path
- Click "Change..." to browse for a different location
- The selected path is used for all `[INSTALLDIR]` targets

**Note**: The install directory dialog only affects `INSTALLDIR`. Files targeting other roots (like `APPDATADIR`) are not affected by this selection.

### Launch on finish (`START_EXE`)

Set `START_EXE` to add a "Launch *Product*" checkbox on the exit dialog that runs your application
after a successful install. The value is an MSI **Formatted path**, so it references the installed
file through a directory property:

```xml
<set name="START_EXE" value="[INSTALLDIR]MyApp.exe"/>
```

The value flows into `WixShellExecTarget`, which `WixShellExec` resolves with `MsiFormatRecord` at
launch time, so `[INSTALLDIR]` (and the other [directory roots](#supported-directory-roots)) expand
to their final installed paths. Note that MSI directory properties **already include a trailing
backslash**, so write `[INSTALLDIR]MyApp.exe` with **no** extra `\` (unlike the bundle's
`[InstallFolder]\App.exe`).

> Earlier versions required a WiX **File Id** wrapped as `[#FileId]`. Because msis generates opaque,
> per-run ids (`FILE_ID00007`), that form was effectively unusable — `START_EXE` is now a Formatted
> path, matching the bundle's `LAUNCH_TARGET`. The template no longer wraps the value, so you may
> still pass an explicit `[#FileId]` if you know the id.

This is the MSI-level counterpart of the bundle's [`LAUNCH_TARGET`](Bundle.md).

### MSI vs Bundle: license and launch settings

A standalone `.msi` and a bundle `.exe` use **different engines** for their UI — Windows
Installer for the MSI, Burn / WixStandardBootstrapperApplication for the bundle. The same
capability therefore has a **different variable, value format, and presentation** in each, and a
value set for one does **not** carry over to the other:

| Capability | Single MSI | Bundle (`.exe`) |
|------------|------------|-----------------|
| Show a license | `LICENSE_FILE` — path to an **RTF file**; shown in a license dialog with an accept-to-continue checkbox | `LICENSE_URL` — a **URL**; shown as a hyperlink on the welcome page |
| Offer to launch on finish | `START_EXE` — an MSI **Formatted path** (e.g. `[INSTALLDIR]App.exe`, no leading `\`); a "Launch *Product*" **checkbox** on the exit dialog | `LAUNCH_TARGET` — a Burn **Formatted path** (e.g. `[InstallFolder]\App.exe`); a "Launch" **button** on the success page |

Practical consequence: setting only `LICENSE_URL` shows the license in the **bundle** but not in
the individual MSIs — for those you must also set `LICENSE_FILE` (an RTF). Likewise `START_EXE`
(MSI) and `LAUNCH_TARGET` (bundle) are independent. When you ship MSIs wrapped in a bundle, the
bundle drives the visible UI, so set the bundle variables (`LICENSE_URL`, `LAUNCH_TARGET`) for what
the user sees, and the MSI variables only if the MSIs are also distributed standalone.

See [Bundle.md](Bundle.md) for the bundle-side variables.

## Uninstall behavior and installer hooks

> For the full picture — the rationale (a real data-loss incident), the native DLL, and how to
> write your own hooks — see **[Installer Hooks](installer-hooks.md)**. This section is the
> variable quick-reference.

By default, uninstalling an MSIS package is a **standard Windows Installer uninstall**: it removes
only what the MSI installed (files, registry, shortcuts). Files your application creates at runtime
— databases, logs, user config — are **left in place**. Three opt-in variables change this, and all
of them depend on the native installer-hook DLL.

### `USE_INSTALLER_HOOKS`

Enables the native hook DLL (named by `DLL_ENTRY`, e.g. `msi-simplica.dll`), which provides the
`Before/After Install/Upgrade/Uninstall` custom actions and the cleanup actions below. When
`USE_INSTALLER_HOOKS=True`, msis checks that the arch-native hook DLL exists **before** the WiX
build and **fails with a clear message** if it is missing. **x86, x64, and arm64 are all
supported** — msis ships an arch-native DLL for each (an arm64 build uses the x64 *template* but
loads the **arm64** DLL).

**The hook DLL is built from `native/msi-simplica/`** with `just build-hooks` (from a VS 2026
Developer shell), which stages `msi-simplica.dll` into `templates/x86,x64,arm64`. `just release` /
`just release-all` run this automatically, and the installer ships the DLLs to
`%LOCALAPPDATA%\MSIS\templates\<arch>\`. The `Before*/After*` entry points are an **extension
contract**: the reference DLL implements only the cleanup actions, but a custom `DLL_ENTRY` DLL may
implement any of the six lifecycle hooks (unimplemented ones are ignored).

### `REMOVE_FOLDERS_ON_UNINSTALL`

> ⚠️ **Dangerous.** On a full uninstall this **recursively deletes the entire `INSTALLDIR` and
> `APPDATADIR` trees**, including files the MSI never installed (runtime databases, logs, customer
> data). Only use it when you genuinely want the install *and* app-data folders wiped.

It requires `USE_INSTALLER_HOOKS=True` (the deletion is performed by the hook DLL). msis emits a
build-time warning whenever `REMOVE_FOLDERS_ON_UNINSTALL=True`, plus a "no effect" warning if hooks
are off. It is retained mainly for legacy packages; prefer leaving it off so standard uninstall
preserves user data.

### `REMOVE_REGISTRY_TREE`

> ⚠️ **Dangerous.** On a full uninstall this **recursively deletes the listed registry trees**
> (`RegDeleteTree`), which can remove state not owned by the MSI.

A **comma-separated list of registry roots** (not a boolean), preserved for compatibility:

```xml
<set name="REMOVE_REGISTRY_TREE"
     value="HKEY_LOCAL_MACHINE\Software\Pergamon,HKEY_CLASSES_ROOT\WOSA/XFS_ROOT"/>
```

It is "active" when it holds a real value; `False`/`No`/`Off`/`0`/empty/missing (case-insensitive)
disable it. Like folder removal it requires `USE_INSTALLER_HOOKS=True` (the deletion is done by the
hook DLL), and msis warns when it is active, plus a "no effect" warning if hooks are off.

### `RETAIN_FILES_ON_UNINSTALL`

A semicolon-separated list of files that folder cleanup must **not** delete. It is interpreted by
the hook DLL — msis only passes it through as an MSI property and warns if it is set while folder
cleanup is inactive. Paths may reference MSI properties such as `[APPDATADIR]`.

> **Note:** this only protects files when built against an **updated hook DLL** that honors
> `RETAIN_FILES_ON_UNINSTALL`. Older DLLs ignore it and still delete everything.

```xml
<set name="USE_INSTALLER_HOOKS" value="True"/>
<set name="REMOVE_FOLDERS_ON_UNINSTALL" value="True"/>
<set name="RETAIN_FILES_ON_UNINSTALL"
     value="[APPDATADIR]DATABASE\proakt.db;[APPDATADIR]CONFIG\local.ini"/>
```

## Custom Templates Folder

The `custom/` folder (in templates or `%LOCALAPPDATA%\msis\custom`) is for user overrides. It's searched first, so files here take precedence.

### Use Cases

1. **Custom logos**: Place logo BMPs here with your `LOGO_PREFIX`
2. **Modified templates**: Copy and modify any template file
3. **Additional resources**: License files, icons, etc.

### Setup for Custom Branding

1. Create the custom folder:
   ```
   %LOCALAPPDATA%\msis\custom\
   ```

2. Add your logo files:
   ```
   %LOCALAPPDATA%\msis\custom\MyCompany_WixUiBanner.bmp
   %LOCALAPPDATA%\msis\custom\MyCompany_WixUiDialog.bmp
   ```

3. Use in your `.msis`:
   ```xml
   <set name="LOGO_PREFIX" value="MyCompany"/>
   ```

## Template Variables

Templates use Handlebars syntax. Key variables available:

### Product Information
- `{{PRODUCT_NAME}}` - Product display name
- `{{PRODUCT_VERSION}}` - Version string
- `{{MANUFACTURER}}` - Company name
- `{{UPGRADE_CODE}}` - Upgrade GUID
- `{{PLATFORM}}` - Target platform (x64, x86, arm64)

### Generated Content
- `{{{FEATURES}}}` - Feature XML (triple braces = unescaped)
- `{{{INSTALLDIR_FILES}}}` - Directory/component XML for INSTALLDIR
- `{{{REGISTRY_ENTRIES}}}` - Registry XML
- `{{{CUSTOM_ACTIONS}}}` - CustomAction XML

### Logos (if set)
- `{{LOGO_BANNER}}` - Path to banner image
- `{{LOGO_DIALOG}}` - Path to dialog image
- `{{LOGO_BOOTSTRAP}}` - Path to bootstrap image

### UI Options (if set)
- `{{LICENSE_FILE}}` - Path to RTF license file (enables license dialog)
- `{{INSTALL_DIR_DIALOG}}` - Set to `true` to enable install directory dialog

## Creating Custom Templates

1. Copy an existing template as a starting point:
   ```bash
   cp templates/x64/template.wxs templates/custom/template.wxs
   ```

2. Modify the WiX XML as needed

3. Use your template:
   ```bash
   msis /BUILD /TEMPLATE:templates/custom/template.wxs setup.msis
   ```

### Template Tips

- Use `{{{variable}}}` (triple braces) for XML content to prevent escaping
- Use `{{variable}}` (double braces) for text values
- Test with `/RETAINWXS` to inspect generated output
- Check WiX 6 documentation for element syntax

## See Also

- [Tutorial](tutorial.md) - Step-by-step guides
- [Bundle Guide](Bundle.md) - Bundle-specific options
- [Schema Reference](msis.xsd) - Complete XML element reference
