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

| Version | Type | Architectures | Auto-Download | Pinned installer |
|---------|------|---------------|---------------|------------------|
| `2022` | `vcredist` | x64, x86, arm64 | ✅ Yes | 14.44.35211.0 |
| `2019` | `vcredist` | x64, x86 | ✅ Yes | 14.29.30157.0 |
| `2017` | `vcredist` | x64, x86 | ❌ No (use `source`) | — |
| `2015` | `vcredist` | x64, x86 | ❌ No (use `source`) | — |

**Note:** VC++ 2015-2022 are binary compatible. If you need VC++ 2015 or 2017, using `version="2022"` is recommended as it provides the latest security fixes while maintaining compatibility and supports auto-download.

"Pinned installer" is the exact build this msis release downloads and verifies — see
[Integrity: pinned URL and digest](#integrity-pinned-url-and-digest). A newer build of the same
redistributable reaches your bundle through an msis update, or through `source=`.

```xml
<requires type="vcredist" version="2022"/>
```

For versions without auto-download, provide a local installer:
```xml
<requires type="vcredist" version="2015" source=".\redist\vc_redist.x64.exe"/>
```

### .NET Framework

| Version | Type | Notes | Auto-Download | Pinned installer |
|---------|------|-------|---------------|------------------|
| `4.8.1` | `netfx` | Latest, Windows 10 21H2+ | ✅ Yes | 4.8.09195.10 (offline) |
| `4.8` | `netfx` | Recommended for broad compatibility | ✅ Yes | 4.8.04115.00 (offline) |
| `4.7.2` | `netfx` | Windows 7 SP1+ | ✅ Yes | 4.7.03081.00 (offline) |
| `4.7.1` | `netfx` | | ❌ No (use `source`) | — |
| `4.7` | `netfx` | | ❌ No (use `source`) | — |
| `4.6.2` | `netfx` | Minimum for modern .NET apps | ❌ No (use `source`) | — |

All three are the **offline** installers, so a bundle carrying one installs without network
access. (Before the pins of #30 the 4.8.1 and 4.7.2 entries pointed at links that served the 1.4 MB *web*
installer under the offline installer's name; a bundle built with those needed the internet at
install time. A cache holding one of them fails verification and is replaced on the next build.)

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

MSIS downloads prerequisite installers from Microsoft and caches them locally:

**Cache Location:** `%LOCALAPPDATA%\msis\prerequisites\`

Benefits:
- First build downloads prerequisites (for versions with auto-download support)
- Subsequent builds reuse cached files
- Cache is shared across all projects
- No need to include large installers in source control

### Integrity: pinned URL and digest

Every installer msis can download is **pinned**: msis carries, for each one, a version-specific
Microsoft URL and the SHA-256 of the file that URL serves (the "Pinned installer" columns above
name the builds). msis verifies that digest

- **after every download**, before the file is given its name in the cache. A corrupted or
  substituted download never becomes a cached file;
- **on every reuse** of a cached file. The cache directory is writable by you and by anything
  running as you, so a file that verified last week is checked again today. A cached file that
  no longer matches is discarded and downloaded again, once.

If the fresh download does not match either, the build is **refused**, and the error names both
digests. The bytes arriving at the pinned URL are not the bytes this msis release pinned — a
damaged transfer, something on the network path answering in Microsoft's place, or Microsoft
having republished the file. msis cannot tell which; see
[the troubleshooting entry](#does-not-match-the-digest-msis-pins) for how to find out before
using `source=` or updating msis.

Each download goes to an exclusively created temporary file of its own, so two builds fetching
the same prerequisite at once verify and publish only their own bytes.

**Pinned rather than "latest", on purpose.** The `aka.ms` links Microsoft offers for the VC++
redistributable always serve the newest build, so a digest recorded against one of them fails as
soon as Microsoft ships an update. With a pin, every machine that builds with the same msis
release chains byte-identical prerequisites — which is what a reproducible bundle, and the SBOM
msis emits for it, need. The cost is that a newer redistributable reaches your bundles with an
msis release rather than automatically; `source=` is the escape hatch in between. The decision
and its alternatives are recorded as D5 in `docs/decisions.md`.

**Where the digests come from.** Each was recorded from a download over TLS from the pinned URL,
with the file's Authenticode signature checked (signer: Microsoft Corporation). For the Visual
Studio download URLs the path itself carries the file's SHA-256, and the pinned value is that
one. Microsoft publishes no separate digest list for these files, so this is the provenance: that
download, on the date recorded in the source, signed by that signer. A test in msis holds the
table to these rules.

### Re-pinning

A pin ages when Microsoft ships a newer build of the same redistributable at its mutable link.
msis carries that link beside every pin (the `Alias` field — `aka.ms/vs/17/release/...`, the .NET
`fwlink`s), and two `just` recipes turn it into a routine:

```bash
just repin-check   # resolve every alias, compare with the pin; no downloads
just repin         # for each moved pin: download, hash, check the signature, print the entry
```

`repin-check` exits 0 when every pin is current, 1 when an alias now serves a different file or
a pinned URL no longer answers, and 2 when a pin could not be checked at all — so it can run on a
schedule, and it should run before every release. `repin` gathers, for each moved pin, exactly
the evidence the original pins were taken with: the SHA-256 of the downloaded bytes, the
Authenticode status and signer (must be Valid, Microsoft Corporation), the file version, and for
the Visual Studio CDN the hash segment in the URL path — and prints the replacement entry in the
table's own form. Paste it over the old one in `internal/prereqcache/cache.go`, update the date
in the `DownloadURLs` comment and the "Pinned installer" columns above, and run the Verify
chain: `TestEveryDownloadIsPinned` holds the table to its rules. A file that fails any of the
checks is not offered as a pin; find out why before touching the table.

Neither recipe is part of `just check`: both need the network, and a build must not. Use the
recipes (or the built executable, `bootstrap\repin.exe`) when the exit status matters: they
build the tool and run it directly, because `go run` reports every non-zero status as 1 and
would lose the difference between "a pin has moved" and "a pin could not be checked". When the
pinned URL itself has stopped answering, the report says so — builds that need that
prerequisite fail today — and, if the alias has moved on, the replacement is verified all the
same.

### View Cached Prerequisites

```bash
msis /STATUS
```

Shows each cached file, marked `(SHA-256 verified)` or with the reason a build would not reuse
it — it does not match its pin, or no pinned download has that name. Nothing is deleted by
`/STATUS`; a build makes that decision when it needs the file.

### Custom/Offline Source

For offline builds or custom installers, specify a `source` attribute:

```xml
<requires type="vcredist" version="2022" source=".\redist\vc_redist.x64.exe"/>
```

When `source` is specified:
- No automatic download occurs
- The specified file is used directly
- You are responsible for providing the correct installer. msis does **not** verify it — it has
  no digest to check a file of yours against — so check the download yourself (Microsoft's
  installers carry an Authenticode signature; `Get-AuthenticodeSignature` in PowerShell shows it)

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

### "does not match the digest msis pins"

The bytes msis received are not the bytes it expected, and it has already discarded them —
nothing unverified is in the cache. Possible causes: the download was damaged (a connection
dropped mid-file); something on the path answers in Microsoft's place (a proxy serving an HTML
sign-in page, a captive portal, a filtering appliance rewriting the download); or Microsoft
republished the file at the pinned URL since this msis release recorded its digest.

1. Retry once; a damaged download rarely repeats.
2. If it repeats, the cause is **not yet known** — a persistent proxy response or a substitution
   repeats just as reliably as a republished file. Do not reach for `source=` with a copy from
   the same network path. Verify an installer independently first: check its Authenticode
   signature (`Get-AuthenticodeSignature` — signer Microsoft Corporation, status Valid) and its
   file version against the pinned build in the tables above. Only then either supply it with
   `source=`, or update msis to a release whose pin matches the file Microsoft now serves.

The error names the expected and the received digest and the installer build msis was pinned to,
so a report of it is complete as it stands.

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
