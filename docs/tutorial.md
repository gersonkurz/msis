# msis Tutorial

This tutorial walks you through creating Windows installers with msis, from a simple single-file installer to complex multi-architecture bundles.

## Before You Start

### What You Need

1. **msis** - The installer generator ([download](https://github.com/gersonkurz/msis/releases) or build from source)
2. **WiX Toolset 6** - The underlying MSI compiler

Install WiX 6 via .NET:
```bash
dotnet tool install --global wix
```

Verify your setup:
```bash
msis /STATUS
```

This shows where msis finds WiX and its templates. If WiX isn't found, make sure `wix.exe` is in your PATH.

### How msis Works

msis doesn't create MSI files directly. Instead:

1. You write a `.msis` script describing what you want
2. msis generates WiX XML (`.wxs` file)
3. WiX compiles the XML into an MSI

The `/BUILD` flag does steps 2 and 3 automatically. Without it, you just get the `.wxs` file.

---

## Tutorial 1: Your First Installer

Let's create an installer for a simple command-line tool called `hello.exe`.

### Step 1: Create Your Script

Create a file called `hello.msis`:

```xml
<setup>
  <set name="PRODUCT_NAME" value="Hello World"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{12345678-1234-1234-1234-123456789ABC}"/>

  <feature name="Hello">
    <files source="hello.exe" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

### Step 2: Understand the Variables

Every installer needs these four variables:

| Variable | What It Does |
|----------|--------------|
| `PRODUCT_NAME` | Shown in Add/Remove Programs and the installer UI |
| `PRODUCT_VERSION` | Must be `X.Y.Z` format (e.g., `1.0.0`, `2.3.1`) |
| `MANUFACTURER` | Your company name, shown in Add/Remove Programs |
| `UPGRADE_CODE` | A GUID that identifies your product family (see below) |

#### What's an UPGRADE_CODE?

The UPGRADE_CODE is crucial. It's a GUID (globally unique identifier) that tells Windows "this is the same product" across versions.

**The rule**: Keep the same UPGRADE_CODE forever for a product. Change it, and Windows treats version 2.0 as a completely different application from version 1.0.

Generate a new GUID:
- PowerShell: `[guid]::NewGuid().ToString("B").ToUpper()`
- Online: https://www.guidgenerator.com/

Use the `{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}` format with braces.

### Step 3: Build the Installer

Place `hello.exe` in the same directory as your script, then:

```bash
msis /BUILD hello.msis
```

This creates `hello.msi`.

### Step 4: Test It

Run the MSI. Your file gets installed to `C:\Program Files\Hello World\hello.exe` (or `Program Files (x86)` for x86 builds).

---

## Tutorial 2: Adding More Files

Real applications have multiple files. Let's package an app with:
- `myapp.exe` - The main executable
- `myapp.dll` - A required library
- `config.json` - Default configuration
- `docs/` - A folder with documentation

### Using Wildcards

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyApp">
    <!-- Copy everything from dist/ to the install folder -->
    <files source="dist\*" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

The `*` wildcard copies the entire directory tree recursively.

### Organizing Files into Subfolders

```xml
<feature name="MyApp">
  <!-- Main files go to INSTALLDIR -->
  <files source="bin\myapp.exe" target="[INSTALLDIR]"/>
  <files source="bin\myapp.dll" target="[INSTALLDIR]"/>

  <!-- Config goes to a config subfolder -->
  <files source="config\*" target="[INSTALLDIR]config\"/>

  <!-- Docs go to a docs subfolder -->
  <files source="docs\*" target="[INSTALLDIR]docs\"/>
</feature>
```

### Protecting User Configuration

What if the user modifies `config.json`? By default, upgrades overwrite all files. To preserve user changes:

```xml
<files source="config.json" target="[INSTALLDIR]" do-not-overwrite="true"/>
```

This installs the file on first install but leaves it alone during upgrades.

---

## Tutorial 3: Desktop and Start Menu Shortcuts

Most GUI applications need shortcuts. Here's how to add them:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyApp">
    <files source="dist\*" target="[INSTALLDIR]"/>

    <!-- Desktop shortcut -->
    <shortcut name="MyApp"
              target="DESKTOP"
              file="[INSTALLDIR]MyApp.exe"/>

    <!-- Start Menu shortcut -->
    <shortcut name="MyApp"
              target="STARTMENU"
              file="[INSTALLDIR]MyApp.exe"
              description="Launch MyApp"/>
  </feature>
</setup>
```

### Shortcut Options

| Attribute | Required | Description |
|-----------|----------|-------------|
| `name` | Yes | The shortcut's display name |
| `target` | Yes | `DESKTOP` or `STARTMENU` |
| `file` | Yes | Path to the executable (use `[INSTALLDIR]` prefix) |
| `description` | No | Tooltip text |
| `icon` | No | Custom icon file (defaults to the exe's icon) |

### Custom Icons

```xml
<shortcut name="MyApp"
          target="DESKTOP"
          file="[INSTALLDIR]MyApp.exe"
          icon="[INSTALLDIR]app.ico"/>
```

---

## Tutorial 4: Registry Settings

Many applications need registry entries for file associations, settings, or integration with Windows.

### The Easy Way: Use a .reg File

If you already have a `.reg` file (exported from regedit or created by hand), msis can import it directly:

```xml
<feature name="MyApp">
  <files source="dist\*" target="[INSTALLDIR]"/>
  <registry file="settings.reg"/>
</feature>
```

Example `settings.reg`:
```
Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyCompany\MyApp]
"InstallPath"="C:\\Program Files\\MyApp"
"Version"="1.0.0"

[HKEY_LOCAL_MACHINE\SOFTWARE\MyCompany\MyApp\Settings]
"Theme"="dark"
"MaxConnections"=dword:00000010
```

### Using Variables in Registry Values

You can reference msis variables in your `.reg` file using `$$VAR$$` syntax:

```
[HKEY_LOCAL_MACHINE\SOFTWARE\MyCompany\MyApp]
"InstallPath"="$$INSTALLDIR$$"
"Version"="$$PRODUCT_VERSION$$"
```

These get expanded when the installer runs, so `InstallPath` correctly reflects where the user chose to install.

### Registry Value Types

msis supports all standard registry types:

| Type | Example |
|------|---------|
| String (REG_SZ) | `"Name"="Value"` |
| DWORD | `"Count"=dword:0000000a` |
| QWORD | `"BigNum"=qword:00000000000000ff` |
| Binary | `"Data"=hex:01,02,03,04` |
| Multi-string | `"List"=hex(7):4f,00,6e,00,65,00,00,00,54,00,77,00,6f,00,00,00,00,00` |
| Expandable string | `"Path"=hex(2):25,00,50,00,41,00,54,00,48,00,25,00,00,00` |

### Deleting Registry Keys

To remove a registry key during uninstall (not just leave it orphaned):

```
[-HKEY_LOCAL_MACHINE\SOFTWARE\MyCompany\MyApp]
```

The `-` prefix marks the key for deletion.

---

## Tutorial 5: Environment Variables

Need to add your application to the system PATH or set other environment variables?

### Adding to PATH

Set the `ADD_TO_PATH` variable:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyTool"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>
  <set name="ADD_TO_PATH" value="true"/>

  <feature name="MyTool">
    <files source="mytool.exe" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

After installation, users can run `mytool` from any command prompt.

### Custom Environment Variables

```xml
<feature name="MyApp">
  <files source="dist\*" target="[INSTALLDIR]"/>
  <set-env name="MYAPP_HOME" value="[INSTALLDIR]"/>
  <set-env name="MYAPP_DATA" value="C:\ProgramData\MyApp"/>
</feature>
```

---

## Tutorial 6: Windows Services

For background services that run without user interaction:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyService"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyService">
    <files source="MyService.exe" target="[INSTALLDIR]"/>

    <service file-name="[INSTALLDIR]MyService.exe"
             service-name="MyService"
             service-display-name="My Background Service"
             start="auto"
             description="Performs important background tasks"/>
  </feature>
</setup>
```

### Service Options

| Attribute | Values | Description |
|-----------|--------|-------------|
| `start` | `auto`, `demand`, `disabled` | When the service starts |
| `service-type` | `ownProcess`, `shareProcess` | Process model (usually `ownProcess`) |
| `error-control` | `ignore`, `normal`, `critical` | What happens if the service fails to start |

### Service Lifecycle

The installer automatically:
1. Stops the service before uninstall/upgrade
2. Installs/updates the service files
3. Starts the service after install (if `start="auto"`)

---

## Tutorial 7: Custom Actions (Running Scripts)

Sometimes you need to run commands during installation:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyApp">
    <files source="dist\*" target="[INSTALLDIR]"/>

    <!-- Run setup script after files are installed -->
    <execute cmd="[INSTALLDIR]setup.cmd" when="after-install"/>

    <!-- Run cleanup before uninstall -->
    <execute cmd="[INSTALLDIR]cleanup.cmd" when="before-uninstall"/>
  </feature>
</setup>
```

### Timing Options

| Value | When It Runs | Elevated? |
|-------|--------------|-----------|
| `before-install` | Before files are copied | No |
| `after-install` | After files are copied | Yes |
| `before-uninstall` | Before files are removed | Yes |
| `after-uninstall` | After files are removed | Yes |

**Note**: `after-install` and later run with elevated privileges (as SYSTEM), which is usually what you want for configuration tasks.

---

## Tutorial 8: Optional Features

Let users choose what to install:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <!-- Core files - always installed -->
  <feature name="Core" enabled="true">
    <files source="bin\*" target="[INSTALLDIR]"/>
  </feature>

  <!-- Documentation - optional, off by default -->
  <feature name="Documentation" enabled="false">
    <files source="docs\*" target="[INSTALLDIR]docs\"/>
  </feature>

  <!-- Examples - optional, on by default -->
  <feature name="Examples" enabled="true">
    <files source="examples\*" target="[INSTALLDIR]examples\"/>
  </feature>
</setup>
```

### Feature Attributes

| Attribute | Description |
|-----------|-------------|
| `enabled="true"` | Selected by default |
| `enabled="false"` | Not selected by default |
| `allowed="false"` | Hidden from user, always installed |

### Nested Features

Features can contain other features for hierarchical organization:

```xml
<feature name="Application">
  <feature name="Core">
    <files source="bin\*" target="[INSTALLDIR]"/>
  </feature>

  <feature name="Plugins">
    <feature name="PDF Export">
      <files source="plugins\pdf\*" target="[INSTALLDIR]plugins\pdf\"/>
    </feature>
    <feature name="Excel Export">
      <files source="plugins\excel\*" target="[INSTALLDIR]plugins\excel\"/>
    </feature>
  </feature>
</feature>
```

---

## Tutorial 9: Multi-Architecture Builds

Your app might need to run on different Windows architectures: 64-bit (x64), 32-bit (x86), or ARM64.

### Single-Architecture MSI

By default, msis builds for x64. To build for a specific architecture:

**For x86 (32-bit):**
```xml
<set name="PLATFORM" value="x86"/>
```

**For ARM64:**
```xml
<set name="PLATFORM" value="arm64"/>
```

### Creating Separate MSIs

The typical approach is to have separate `.msis` files:

**setup-x64.msis:**
```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>
  <set name="PLATFORM" value="x64"/>

  <feature name="MyApp">
    <files source="bin\x64\*" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

**setup-x86.msis:**
```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>
  <set name="PLATFORM" value="x86"/>

  <feature name="MyApp">
    <files source="bin\x86\*" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

Build each one:
```bash
msis /BUILD setup-x64.msis
msis /BUILD setup-x86.msis
```

---

## Tutorial 10: Universal Bundles

A **bundle** is a single `.exe` that contains multiple MSIs and automatically installs the right one for the user's system. For complete bundle reference, see [Bundle.md](Bundle.md).

### Creating a Bundle

First, build your individual MSIs:
```bash
msis /BUILD setup-x64.msis
msis /BUILD setup-x86.msis
msis /BUILD setup-arm64.msis
```

Then create a bundle script:

**setup-bundle.msis:**
```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>
  <set name="INSTALLDIR" value="MyApp"/>

  <bundle>
    <msi source_64bit="MyApp-1.0.0-x64.msi"
         source_32bit="MyApp-1.0.0-x86.msi"
         source_arm64="MyApp-1.0.0-arm64.msi"/>
  </bundle>
</setup>
```

Build the bundle:
```bash
msis /BUILD setup-bundle.msis
```

This creates `MyApp-1.0.0.exe`.

### How Architecture Detection Works

The bundle automatically detects the system architecture:
- **ARM64 Windows**: Installs the ARM64 MSI
- **64-bit Windows**: Installs the x64 MSI
- **32-bit Windows**: Installs the x86 MSI

If an MSI isn't provided for an architecture, users on that platform see an error.

### x64/ARM64 Only Bundle

Don't need 32-bit support? Just omit it:

```xml
<bundle>
  <msi source_64bit="MyApp-1.0.0-x64.msi"
       source_arm64="MyApp-1.0.0-arm64.msi"/>
</bundle>
```

---

## Tutorial 11: Prerequisites in Bundles

Bundles can install prerequisites (like Visual C++ Runtime) before your application:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>
  <set name="INSTALLDIR" value="MyApp"/>

  <bundle>
    <!-- Install VC++ 2022 Runtime first -->
    <prerequisite type="vcredist" version="2022"/>

    <!-- Then install our MSI -->
    <msi source_64bit="MyApp-1.0.0-x64.msi"
         source_32bit="MyApp-1.0.0-x86.msi"/>
  </bundle>
</setup>
```

### Built-in Prerequisites

| Type | Versions | Description |
|------|----------|-------------|
| `vcredist` | 2015, 2017, 2019, 2022 | Visual C++ Redistributable |
| `netfx` | 4.6.2, 4.7, 4.7.1, 4.7.2, 4.8, 4.8.1 | .NET Framework |

### Prerequisites Folder

The bundle expects prerequisite installers in a `prerequisites/` folder:
```
project/
  setup-bundle.msis
  MyApp-1.0.0-x64.msi
  MyApp-1.0.0-x86.msi
  prerequisites/
    vc_redist.x64.exe
    vc_redist.x86.exe
```

Override the location:
```xml
<set name="PREREQUISITES_FOLDER" value="deps\"/>
```

### Custom Prerequisites

For prerequisites not in the built-in list:

```xml
<bundle>
  <exe id="CustomRuntime"
       source="prerequisites\custom-runtime.exe"
       args="/quiet"
       detect="HKLM\SOFTWARE\CustomRuntime,Version,1.0"/>

  <msi source_64bit="MyApp-1.0.0-x64.msi"/>
</bundle>
```

The `detect` attribute specifies a registry key to check. If the key exists with the specified value, the prerequisite is skipped.

For more details on prerequisites, custom packages, and bundle variables, see [Bundle.md](Bundle.md).

---

## Tutorial 12: Putting It All Together

Here's a complete example for a real-world application:

```xml
<setup>
  <!-- Product identification -->
  <set name="PRODUCT_NAME" value="Acme Productivity Suite"/>
  <set name="PRODUCT_VERSION" value="2.5.0"/>
  <set name="MANUFACTURER" value="Acme Corporation"/>
  <set name="UPGRADE_CODE" value="{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}"/>

  <!-- Installation options -->
  <set name="INSTALLDIR" value="Acme\ProductivitySuite"/>
  <set name="ADD_TO_PATH" value="true"/>
  <set name="LICENSE_FILE" value="license.rtf"/>
  <set name="SETUP_ICON" value="app.ico"/>

  <!-- Main application -->
  <feature name="Application" enabled="true">
    <files source="bin\*" target="[INSTALLDIR]"/>

    <!-- Shortcuts -->
    <shortcut name="Acme Productivity Suite"
              target="DESKTOP"
              file="[INSTALLDIR]AcmeApp.exe"
              icon="[INSTALLDIR]app.ico"/>
    <shortcut name="Acme Productivity Suite"
              target="STARTMENU"
              file="[INSTALLDIR]AcmeApp.exe"
              description="Launch Acme Productivity Suite"/>

    <!-- Registry settings -->
    <registry file="settings.reg"/>

    <!-- Post-install configuration -->
    <execute cmd="[INSTALLDIR]configure.cmd" when="after-install"/>
  </feature>

  <!-- Background service -->
  <feature name="Background Sync Service" enabled="true">
    <files source="service\*" target="[INSTALLDIR]service\"/>

    <service file-name="[INSTALLDIR]service\AcmeSync.exe"
             service-name="AcmeSyncService"
             service-display-name="Acme Background Sync"
             start="auto"
             description="Synchronizes your data in the background"/>
  </feature>

  <!-- Optional documentation -->
  <feature name="Documentation" enabled="false">
    <files source="docs\*" target="[INSTALLDIR]docs\"/>

    <shortcut name="Acme Documentation"
              target="STARTMENU"
              file="[INSTALLDIR]docs\index.html"/>
  </feature>
</setup>
```

### Building

```bash
# Just generate WXS to inspect it
msis acme.msis

# Build the MSI
msis /BUILD acme.msis

# Keep the WXS file for debugging
msis /BUILD /RETAINWXS acme.msis
```

---

## Troubleshooting

### "wix CLI not found"

Install WiX 6:
```bash
dotnet tool install --global wix
```

### "Extension not found"

Install the required WiX extensions:
```bash
wix extension add WixToolset.UI.wixext
wix extension add WixToolset.Util.wixext
```

For bundles, also add:
```bash
wix extension add WixToolset.BootstrapperApplications.wixext
wix extension add WixToolset.Netfx.wixext
```

### "ICE validation error"

WiX runs validation checks (ICEs) on the generated MSI. Common issues:

- **ICE30**: Missing component GUID - this shouldn't happen with msis, file a bug
- **ICE38**: Shortcut outside of feature - make sure shortcuts are inside `<feature>` tags
- **ICE43**: Mismatch in component key path - often caused by duplicate file references

### Debugging

1. Generate WXS without building:
   ```bash
   msis setup.msis
   ```

2. Inspect the generated `.wxs` file

3. Build with the WXS retained:
   ```bash
   msis /BUILD /RETAINWXS setup.msis
   ```

4. Check msis configuration:
   ```bash
   msis /STATUS
   ```

---

## Next Steps

- [Schema Reference](msis.xsd) - Complete XML element and attribute reference
- [Bundle Guide](Bundle.md) - Advanced bundle options and prerequisites
- [Developer Overview](overview.md) - Architecture and internals for contributors
