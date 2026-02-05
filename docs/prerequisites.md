# Prerequisites in MSIS 3.x

MSIS 3.x introduces a declarative way to handle runtime prerequisites like VC++ Redistributables and .NET Framework. Instead of manually managing merge modules or copying DLLs, you simply declare what your application needs.

## Quick Start

Add a `<requires>` element to your `.msis` file:

```xml
<setup>
    <set name="PRODUCT_NAME" value="MyApp"/>
    <set name="PRODUCT_VERSION" value="1.0.0"/>

    <!-- Declare runtime requirements -->
    <requires type="vcredist" version="2022"/>

    <feature name="MyApp">
        <files source="bin" target="INSTALLDIR"/>
    </feature>
</setup>
```

When you build with `/BUILD`, MSIS will:
1. Generate an MSI with launch conditions (checks if runtime is installed)
2. Automatically create a bundle wrapper that installs the prerequisites before your MSI

## Supported Prerequisites

### VC++ Redistributable

| Version | Type | Architectures | Auto-Download |
|---------|------|---------------|---------------|
| `2022` | `vcredist` | x64, x86, arm64 | ✅ Yes |
| `2019` | `vcredist` | x64, x86 | ✅ Yes |
| `2017` | `vcredist` | x64, x86 | ❌ No (use `source`) |
| `2015` | `vcredist` | x64, x86 | ❌ No (use `source`) |

**Note:** VC++ 2015-2022 are binary compatible. If you need VC++ 2015 or 2017, using `version="2022"` is recommended as it provides the latest security fixes while maintaining compatibility and supports auto-download.

```xml
<requires type="vcredist" version="2022"/>
```

For versions without auto-download, provide a local installer:
```xml
<requires type="vcredist" version="2015" source=".\redist\vc_redist.x64.exe"/>
```

### .NET Framework

| Version | Type | Notes | Auto-Download |
|---------|------|-------|---------------|
| `4.8.1` | `netfx` | Latest, Windows 10 21H2+ | ✅ Yes |
| `4.8` | `netfx` | Recommended for broad compatibility | ✅ Yes |
| `4.7.2` | `netfx` | Windows 7 SP1+ | ✅ Yes |
| `4.7.1` | `netfx` | | ❌ No (use `source`) |
| `4.7` | `netfx` | | ❌ No (use `source`) |
| `4.6.2` | `netfx` | Minimum for modern .NET apps | ❌ No (use `source`) |

```xml
<requires type="netfx" version="4.8"/>
```

For versions without auto-download, provide a local installer:
```xml
<requires type="netfx" version="4.6.2" source=".\redist\ndp462-kb3151800-x86-x64-allos-enu.exe"/>
```

## Build Modes

### Default: Auto-Bundle

When `<requires>` is present, MSIS automatically generates a bundle (bootstrapper) that:
1. Checks if prerequisites are installed
2. Downloads and installs missing prerequisites
3. Installs your MSI

```bash
msis /BUILD setup.msis
# Output: setup.msi + setup.exe (bundle)
```

**Why a bundle (.exe)?**  
Windows Installer does not support installing other installers (MSI/EXE) from inside an MSI. Custom actions that launch installers are unreliable, can break rollback, and are often blocked by enterprise policy. The supported pattern is a bundle (Burn) that installs prerequisites first, then your MSI. If you must ship a single MSI, use `/STANDALONE` and accept that prerequisites must already be present.

### Standalone MSI

Use `/STANDALONE` to generate only the MSI with launch conditions (no bundling):

```bash
msis /BUILD /STANDALONE setup.msis
# Output: setup.msi only (with launch conditions)
```

The MSI will check for prerequisites at install time and show an error if they're missing. The user must install prerequisites manually.

## Prerequisite Caching

MSIS automatically downloads prerequisite installers from official Microsoft sources and caches them locally:

**Cache Location:** `%LOCALAPPDATA%\msis\prerequisites\`

Benefits:
- First build downloads prerequisites (for versions with auto-download support)
- Subsequent builds reuse cached files
- Cache is shared across all projects
- No need to include large installers in source control

**Integrity Note:** MSIS will display a warning when downloading prerequisites without SHA256 hash verification. This is informational—the files are downloaded from official Microsoft URLs but integrity cannot be cryptographically verified.

### View Cached Prerequisites

```bash
msis /STATUS
```

Shows cached prerequisites and their locations.

### Custom/Offline Source

For offline builds or custom installers, specify a `source` attribute:

```xml
<requires type="vcredist" version="2022" source=".\redist\vc_redist.x64.exe"/>
```

When `source` is specified:
- No automatic download occurs
- The specified file is used directly
- You are responsible for providing the correct installer

## Detection Logic

### VC++ Redistributable Detection

MSIS checks the registry:
```
HKLM\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\{x64|x86|arm64}
```

The `Installed` DWORD value indicates presence.

### .NET Framework Detection

MSIS checks the registry:
```
HKLM\SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full
```

The `Release` DWORD value indicates the installed version:

| Release Value | .NET Version |
|--------------|--------------|
| 533320+ | 4.8.1 |
| 528040+ | 4.8 |
| 461808+ | 4.7.2 |
| 461308+ | 4.7.1 |
| 460798+ | 4.7 |
| 394802+ | 4.6.2 |

## Multiple Prerequisites

You can declare multiple prerequisites:

```xml
<setup>
    <requires type="vcredist" version="2022"/>
    <requires type="netfx" version="4.8"/>

    <feature name="MyApp">
        <files source="bin" target="INSTALLDIR"/>
    </feature>
</setup>
```

Prerequisites are installed in the order declared.

## Troubleshooting

### "No download URL for..."

The specified prerequisite type/version combination is not recognized. Check the supported versions table above.

### Download Failures

If downloads fail:
1. Check your internet connection
2. Check if corporate firewall blocks Microsoft download URLs
3. Use the `source` attribute to provide a local installer

### Launch Condition Failed

If the MSI shows "This application requires..." error:
1. The prerequisite is not installed
2. Install the prerequisite manually, or
3. Use the bundle (.exe) instead of the MSI directly

### Cache Issues

To clear the prerequisite cache:
```bash
# Windows
rmdir /s /q "%LOCALAPPDATA%\msis\prerequisites"
```

## Migration from MSIS 2.x

### Merge Modules (Removed)

MSIS 3.x no longer supports merge modules. If you were using `INCLUDE_VCREDIST`:

**Before (2.x):**
```xml
<set name="INCLUDE_VCREDIST" value="True"/>
```

**After (3.x):**
```xml
<requires type="vcredist" version="2022"/>
```

### Manual DLL Copying

If you were copying VC++ DLLs manually:

**Before:**
```xml
<files source="redist\vc_dlls" target="INSTALLDIR"/>
```

**After:**
```xml
<requires type="vcredist" version="2022"/>
<!-- Remove the manual DLL copying -->
```

Benefits:
- Smaller MSI (no embedded DLLs)
- Proper system-wide installation
- Automatic updates via Windows Update
- No DLL conflicts

## Best Practices

1. **Use latest compatible version**: For VC++, prefer 2022 even if you built with 2019
2. **Test on clean systems**: Verify prerequisites install correctly on machines without development tools
3. **Consider offline scenarios**: Use `source` attribute for air-gapped environments
4. **Document requirements**: Even with auto-install, document prerequisites in your README
