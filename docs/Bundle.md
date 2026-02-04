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

| Type | Versions | Description |
|------|----------|-------------|
| `vcredist` | 2022, 2019, 2017, 2015 | Visual C++ Redistributable |
| `netfx` | 4.8.1, 4.8, 4.7.2, 4.7.1, 4.7, 4.6.2 | .NET Framework |

Example:
```xml
<prerequisite type="vcredist" version="2022"/>
<prerequisite type="netfx" version="4.8"/>
```

### Prerequisite Files

By default, msis looks for prerequisite installers in a `prerequisites` folder relative to your .msis file:

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
| `LOGO_PREFIX` | Prefix for default logo files | `NGBT` |
| `PREREQUISITES_FOLDER` | Path to prerequisite installers | `./prerequisites` |

## Output

Bundles produce an `.exe` file (not `.msi`). The output filename defaults to:
```
{PRODUCT_NAME}-{PRODUCT_VERSION}.exe
```

Override with `BUILD_TARGET`:
```xml
<set name="BUILD_TARGET" value="MyApp-Setup.exe"/>
```

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
- `WixToolset.BootstrapperApplications.wixext` - Bootstrapper Application Library (WiX 6)
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
