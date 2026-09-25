# msis Tutorial

This tutorial walks you through creating Windows installers with msis, from a simple single-file installer to complex multi-architecture bundles.

## Before You Start

### What You Need

1. **msis** - The installer generator ([download](https://github.com/gersonkurz/msis/releases) or build from source)
2. **WiX Toolset 7** (or 6) - The underlying MSI compiler. msis detects which major
   version is installed and adapts; WiX 7 is recommended for new setups.

Once you have msis, let it provision WiX and the extensions it needs (installs and
verifies everything, pinned to the right version):
```
msis /SETUP-WIX
```
See [Troubleshooting](#troubleshooting) for `/WIX-VERSION`, harmless extension warnings, and the manual equivalent.

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

### Copying a Whole Folder

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <feature name="MyApp">
    <!-- Copy everything from dist/ to the install folder -->
    <files source="dist" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

Name the directory and its contents are installed **recursively**, subfolders included.

> **There is no wildcard syntax.** Earlier revisions of this tutorial showed
> `source="dist\*"`. That never worked: the path was not found, and the element was skipped
> without a word, so the package built successfully containing nothing. msis now rejects it
> with an error pointing at the form above. A `source` that does not exist is an error too,
> for the same reason — a mistyped path used to ship a package quietly missing the file.

### Organizing Files into Subfolders

```xml
<feature name="MyApp">
  <!-- Main files go to INSTALLDIR -->
  <files source="bin\myapp.exe" target="[INSTALLDIR]"/>
  <files source="bin\myapp.dll" target="[INSTALLDIR]"/>

  <!-- Config goes to a config subfolder -->
  <files source="config" target="[INSTALLDIR]config\"/>

  <!-- Docs go to a docs subfolder -->
  <files source="docs" target="[INSTALLDIR]docs\"/>
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
    <files source="dist" target="[INSTALLDIR]"/>

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
  <files source="dist" target="[INSTALLDIR]"/>
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

### Using Variables and Properties in Registry Values

A string value in your `.reg` file can be filled in at two different times.

**At build time, with msis variables.** `{{VAR}}` in a string value is replaced with the
variable's value when msis runs, exactly as in a `.msis` attribute:

```
[HKEY_LOCAL_MACHINE\SOFTWARE\MyCompany\MyApp]
"Version"="{{PRODUCT_VERSION}}"
"Vendor"="{{MANUFACTURER}}"
```

Only string values are expanded — not value names, not key paths, not DWORDs or binary or
multi-string values. A reference to a variable that is not defined, or text the template
engine cannot parse, is written **as authored** and reported as a build warning, so a typo
cannot silently turn a value into an empty string. That includes a variable used in a
condition: `{{#if FOO}}…{{/if}}` with FOO undefined is reported rather than treated as false,
so define an optional variable as empty. (msis-2.x expanded `{{VAR}}` here too, but rendered an
unknown name as nothing.) The `$$VAR$$` form an earlier version of this page described was
never honoured by any version and is plain text.

**At install time, with Windows Installer properties.** `[PROPERTY]` is resolved on the
user's machine, which is the only place a value like the install folder is known:

```
[HKEY_LOCAL_MACHINE\SOFTWARE\MyCompany\MyApp]
"InstallPath"="[INSTALLDIR]"
"Executable"="[INSTALLDIR]{{PRODUCT_NAME}}.exe"
```

`[INSTALLDIR]` becomes the folder the user chose, trailing backslash included — so there is
no `\` between it and the file name. Any Windows Installer property works the same way, and
the two mechanisms combine: in the second line msis fills in the product name at build time
and Windows Installer fills in the folder at install time. What brackets mean in a string
value, and when they need escaping, is the next section.

### Brackets: literal or formatted?

The column Windows Installer writes a string value from is a *Formatted* field. That gives
brackets a meaning, and which meaning depends on how the value reaches the column.

**A value written from your `.reg` file** — the default — is placed in that field exactly as
you wrote it. msis escapes it for XML and changes nothing else. Windows Installer then:

- substitutes `[NAME]` with the property `NAME`, or with nothing if there is no such property:
  `a[Foo]b` installs as `ab`;
- reads `[~]` as the REG_MULTI_SZ separator: `a[~]b` installs as a **multi-string** of `a` and
  `b`. The value changes type, and the install reports success;
- resolves `[\c]` to the character `c`. To install a literal `[`, write `[\[]` — which in a
  `.reg` file is spelled `"[\\[]"`, because `.reg` strings use `\\` for one backslash.
  `"Pattern"="a[\\[]b"` installs as `a[b`. (Copying the MSI form `[\[]` into a `.reg` file
  does not work: the parser reads `\[` as an escaped `[`, and `a[[]b` is what msis emits.)

All three were measured on a real install: the first two while fixing #11, the escape in
`todo-testme.md` T8 (#48). A leading `#` is a type
marker in the same column; WiX escapes it for a value written this way, and msis doubles it
for a preserved one, so `"Colour"="#FF0000"` installs as written either way (#11).

**A preserved value** — `preserve="yes"`, and a type preservation covers — takes another
route. It is written through a property, and Windows Installer inserts the property's content
without a second formatting pass. Its brackets survive as written: `a[Foo]b` installs as
`a[Foo]b` (measured, #11). An escape survives the same way, so do **not** write `[\[]` in a
value that will be preserved: `"a[\\[]b"` in a preserved `.reg` file installs as `a[\[]b`,
escape and all (measured, `todo-testme.md` T8).

**A value that starts with `[`** is the intentional property reference from the previous
section. It is never preserved and always formatted.

msis warns at build time about a non-preserved string containing an unescaped `[...]` or a
`[~]`, naming the value and the remedy. It does not rewrite the value: `[` legitimately means
two different things, and only you know which. That decision is recorded as D4 in
`docs/decisions.md`.

### Registry Value Types

msis reads all standard registry types. All of them install as-is except QWORD, which
Windows Installer cannot represent — see the note below the table:

| Type | Example |
|------|---------|
| String (REG_SZ) | `"Name"="Value"` |
| DWORD | `"Count"=dword:0000000a` |
| QWORD | `"BigNum"=qword:00000000000000ff` |
| Binary | `"Data"=hex:01,02,03,04` |
| Multi-string | `"List"=hex(7):4f,00,6e,00,65,00,00,00,54,00,77,00,6f,00,00,00,00,00` |
| Expandable string | `"Path"=hex(2):25,00,50,00,41,00,54,00,48,00,25,00,00,00` |

#### QWORD values are truncated to 32 bits

Two separate things are going on here — one imposed on msis, one chosen by it.

**The limitation (Windows Installer's).** MSI's Registry table can express REG_SZ,
REG_EXPAND_SZ, REG_BINARY, REG_MULTI_SZ and REG_DWORD — and nothing wider. There is no
QWORD encoding to emit. No installer built on Windows Installer can write a REG_QWORD
through the standard registry tables.

**The policy (msis's).** Faced with that, msis writes a `QWORD` from your `.reg` file
as a **REG_DWORD holding the low 32 bits**, dropping the upper 32 silently.
`0x0123456789ABCDEF` installs as `0x89ABCDEF`, typed REG_DWORD. msis could instead
refuse to build such a package; truncation is deliberately chosen over that, so
existing packages carrying a QWORD keep working. It is a trade, and the cost is that
a value you wrote is not the value that lands.

Previously neither happened cleanly: the full 64-bit number was emitted into a field
MSI defines no meaning for.

**QWORDs are also never preserved.** `preserve="yes"` reads the existing value with a
`Type='raw'` registry search, and that search returns a REG_QWORD's raw bytes
reinterpreted as text — seeding `0xFEDCBA9876543210` yields the string `㈐癔몘ﻜ`, which
is precisely those eight bytes read as UTF-16. Writing that back would replace the
user's number with mojibake. So a QWORD is always written fresh from your `.reg` file
as the truncated REG_DWORD above, overwriting whatever was there.

If your application genuinely needs a 64-bit registry value, do not rely on the
installer to place it — write it from the application on first run, or from a custom
action.

#### Expandable strings are not preserved

`preserve="yes"` (see below) does **not** apply to expandable strings. They are always
written from your `.reg` file, even if the user changed them.

The reason is that preservation reads the existing value with a `Type='raw'` registry
search, and that search *expands* a REG_EXPAND_SZ before handing it back — a live
`%TEMP%` arrives as `C:\Users\alice\AppData\Local\Temp`, with its type marker gone.
Writing that back would both downgrade the value to a plain REG_SZ and freeze one
machine's paths into the registry. Skipping preservation keeps the value correct:
a proper, unexpanded REG_EXPAND_SZ.

Multi-strings are skipped for preservation too, for a similar encoding reason.

### Preserving User-Modified Values

By default every value in your `.reg` file is written on install, overwriting whatever the
user had. `preserve="yes"` changes that: an existing value is left alone, and the value from
your `.reg` file is used only where there is nothing there yet.

```xml
<feature name="MyApp">
  <files source="dist" target="[INSTALLDIR]"/>
  <registry file="settings.reg" preserve="yes"/>
</feature>
```

So a setting the user changed survives the next upgrade, while a setting you added in this
release gets its default.

It works by reading the existing value during installation into a property that starts out
holding your `.reg` default. Nothing is written back verbatim from the old package: the value
that lands is either the live one or your default.

#### An existing but EMPTY value is not preserved

If the value exists and is an **empty string**, your `.reg` default is written instead — the
one case where `preserve="yes"` does not keep what the user had.

```
before install:   "Proxy" = ""                  (the user deliberately blanked it)
.reg file says:   "Proxy" = "proxy.corp.local"
after install:    "Proxy" = "proxy.corp.local"  (the blank is gone)
```

The cause is in Windows Installer rather than in msis. The search does run, and it does read
the empty value — but AppSearch makes no assignment from an empty result, so the property is
left holding the `.reg` default it was initialised with, and that default is what gets written.
In the MSI log the search appears with no `PROPERTY CHANGE` line after it, while the values that
were preserved each have one. "Found, but empty" and "not found" are therefore indistinguishable
by the time the result is a property value. msis-2.x behaves identically — this is as old as
the feature.

If a blank needs to mean something in your application, do not let the installer own that
value: leave it out of the `.reg` file and have the application write its own default on first
run, so an empty value stays empty.

#### The other exceptions

Three value types are never preserved, each for a reason covered above: [QWORDs](#qword-values-are-truncated-to-32-bits),
[expandable strings](#expandable-strings-are-not-preserved), and multi-strings. They are always
written from your `.reg` file.

A value whose `.reg` content starts with `[` is also written as-is, because it is an MSI
Formatted expression such as `[INSTALLDIR]` that has to be resolved at install time. What
that means for brackets in preserved and non-preserved values is under
[Brackets: literal or formatted?](#brackets-literal-or-formatted) above.

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
  <files source="dist" target="[INSTALLDIR]"/>
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
    <files source="dist" target="[INSTALLDIR]"/>

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

### Failing the install on non-zero exit

By default a custom action's exit code is ignored. To make the installer roll back when the command exits non-zero (e.g. a pre-flight validator), add `fail-on-error="true"`:

```xml
<execute cmd="[INSTALLDIR]myapp.exe validate --config &quot;[INSTALLDIR]myapp.json&quot;"
         when="after-install"
         fail-on-error="true"/>
```

A non-zero exit then surfaces as an MSI error and triggers automatic rollback of the install.

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
    <files source="bin" target="[INSTALLDIR]"/>
  </feature>

  <!-- Documentation - optional, off by default -->
  <feature name="Documentation" enabled="false">
    <files source="docs" target="[INSTALLDIR]docs\"/>
  </feature>

  <!-- Examples - optional, on by default -->
  <feature name="Examples" enabled="true">
    <files source="examples" target="[INSTALLDIR]examples\"/>
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
    <files source="bin" target="[INSTALLDIR]"/>
  </feature>

  <feature name="Plugins">
    <feature name="PDF Export">
      <files source="plugins\pdf" target="[INSTALLDIR]plugins\pdf\"/>
    </feature>
    <feature name="Excel Export">
      <files source="plugins\excel" target="[INSTALLDIR]plugins\excel\"/>
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
    <files source="bin\x64" target="[INSTALLDIR]"/>
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
    <files source="bin\x86" target="[INSTALLDIR]"/>
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

## Tutorial 12: Data Folders and Uninstall Cleanup

Most applications write files after installation — logs, a database, a cache. The installer
does not ship those files, so Windows Installer does not know about them: it will not create
the folder for you, and it will not remove it when the product is uninstalled.

Two elements cover that gap.

### Creating an empty folder

```xml
<feature name="Main">
  <files source="bin" target="[INSTALLDIR]"/>
  <create-folder target="[APPDATADIR]MyCompany\MyApp\logs"/>
</feature>
```

`<create-folder>` makes the directory at install time so your application can write to it
immediately. `target` is a root key plus a subpath, exactly like `<files target=...>`; a path
matching no known root is treated as a subpath of `INSTALLDIR`.

On uninstall the folder is removed only if it is **empty**. That is usually not the case —
which is what the next element is for.

### Removing a folder or registry key on uninstall

```xml
<feature name="Main">
  <files source="bin" target="[INSTALLDIR]"/>
  <remove-on-uninstall folder="[APPDATADIR]MyCompany\MyApp"/>
  <remove-on-uninstall registry="HKLM\Software\MyCompany\MyApp"/>
</feature>
```

`folder` deletes the directory **and everything under it**. `registry` deletes the key and all
of its subkeys and values.

You can also set both on one element; each is applied independently, exactly as if you had
written two elements:

```xml
<remove-on-uninstall folder="[APPDATADIR]MyCompany\MyApp" registry="HKLM\Software\MyCompany\MyApp"/>
```

> ⚠️ **This deletes files your installer never installed.** That is the point of it — but it
> means a `folder` pointed one level too high takes the user's data with it. A
> `[APPDATADIR]MyCompany` that other products of yours also use, or an `[INSTALLDIR]` the
> customer chose as an existing directory, will be removed wholesale, including any database
> or configuration living there. Point it at a directory your package owns, and nothing above
> it.
>
> **Know what a root already contains.** `[APPDATADIR]` is not `C:\ProgramData`. It is
> `C:\ProgramData\<name>`, where the name is the `APPDATADIR` variable if the script sets one,
> else `INSTALLDIR` (else `INSTALL_FOLDER`), as in msis-2.x. With `INSTALLDIR` set to `MyApp`,
> `[APPDATADIR]Vendor\logs` is `C:\ProgramData\MyApp\Vendor\logs`, not
> `C:\ProgramData\Vendor\logs`. The same holds for `[ROAMINGAPPDATADIR]` and
> `[LOCALAPPDATADIR]`, and `[INSTALLDIR]` is `C:\Program Files\<INSTALLDIR>`. Only if none of
> these variables is set does `[APPDATADIR]` mean `C:\ProgramData` itself, and a `folder`
> under it then names a directory any product may share. Check the resolved path before you
> ship a recursive delete: the MSI's Directory table records the folder each root names.

Recognized registry roots are `HKLM`, `HKCU`, `HKCR` and `HKU`, or their long forms
(`HKEY_LOCAL_MACHINE` and so on). **A root msis does not recognize is skipped silently** — the
package builds and the key is simply never removed — so check the spelling of that first
segment. The folder path is resolved during installation and recorded, so uninstall removes the
directory the product actually used even if `INSTALLDIR` was customized.

**It runs on a real uninstall only, not on an update.** A major upgrade removes the previous
version before it installs the new one. So without a guard, every update would delete what
`<remove-on-uninstall>` names, and on a VM it did, until 3.0.6 (#76). msis now emits both
cleanups with `Condition="NOT UPGRADINGPRODUCTCODE"`, so they stay off while an upgrade removes
the old version. One caveat: Windows Installer removes the old version with **its own** cached
package, so the protection holds for upgrades *from* a version built with msis 3.0.6 or later.
Updating a product whose installed version was built with an earlier msis still runs that
version's cleanup, once.

This is not the same as the installer-hook cleanup (`REMOVE_FOLDERS_ON_UNINSTALL`), which is a
blanket removal of the whole `INSTALLDIR`/`APPDATADIR` trees performed by the native hook DLL.
`<remove-on-uninstall>` is the targeted version: it names exactly what goes, it needs no hook
DLL, and it is what you want unless you have a specific reason to reach for the other.

---

## Tutorial 13: Supplying a Component SBOM

With `/SBOM`, msis writes a CycloneDX document beside each installer it builds, describing
everything it can see: every payload file, its bytes, its SHA-256 and where it installs.

What it cannot see is what is *inside* those files. `app.exe` is a hash and a name; the libraries
statically linked into it, the modules a Go binary carries, the packages a .NET application
depends on — none of that is visible from the outside. That is also exactly where a product's
top-level dependencies live, which is what the Cyber Resilience Act asks a manufacturer to be
able to produce.

Your build system knows. Most toolchains can emit a CycloneDX document for what they built —
`cyclonedx-gomod`, `cyclonedx-dotnet`, `syft` and others. `<sbom>` composes that document into
the installer's own:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="My Company"/>
  <set name="UPGRADE_CODE" value="{YOUR-GUID-HERE}"/>

  <sbom source="app.cdx.json" for="[INSTALLDIR]app.exe"/>

  <feature name="Main">
    <files source="bin" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

```bash
msis /BUILD /SBOM setup.msis
```

- **`source`** is the CycloneDX JSON document, relative to the `.msis` file. It is read by msis
  and never packaged.
- **`for`** is the install target of the file it describes. It must match **exactly one**
  installed file. A target matching several is an error rather than a guess — msis can package
  two different files to one destination, and attaching someone's dependency graph to whichever
  came first would be a coin toss presented as a fact.

### What msis does with it

The supplied document's components are emitted **verbatim**, with every field their author
wrote, including ones msis does not model — licences above all. Their identity is untouched: a
`purl` that came in comes out unchanged, and msis never invents one that was not there.

One thing is rewritten: the `bom-ref`. Two documents are becoming one, and a `bom-ref` is
document-local addressing rather than identity, so imported refs move into a namespace of their
own and every relationship that pointed at them is rewritten to match.

One thing is checked. If the supplied document carries a SHA-256 for its own subject, msis
compares it with the bytes in the package. A mismatch **fails the build** — the document is
about a different build of that file, and publishing it would attach a dependency graph to
content it was never about.

### What msis does NOT do

Receiving a document establishes nothing about it. msis does not verify that a supplied document
is complete, that it is accurate, or that it lists what is really inside the binary — and the
merged document says so, in a `msis:supplied.document` property, so a reader can tell your
supplier's claims from msis's own observations. Every imported component also carries
`msis:supplied.from`, naming the document it came from.

Coverage statements are preserved rather than improved:

| What your document declares about its own contents | What the installer's document says about that file |
|---|---|
| `complete` | `complete` |
| `incomplete` | `unknown` |
| `unknown` | `unknown` |
| nothing at all | `unknown` |

Only a document that actually asserts completeness lets the file be marked complete. Anything
else leaves it `unknown` — the marking it already had. Not `incomplete`: CycloneDX defines that
as *additional relationships exist*, which is a claim that something is missing, and knowing one
thing that is inside a file establishes nothing about whether anything else is. A component your document says nothing about comes out marked
`unknown` rather than silently complete — a reader has to be able to tell "depends on nothing"
from "nobody looked".

### Where it goes

`<sbom>` belongs in the `.msis` that packages the file. A bundle installs no files of its own —
it chains installers — so a bundle script has no install target to name; msis says so rather
than failing later. The bundle's document links to the MSI's, and the MSI's carries the merge.

### When you only know the facts: `<component>`

Often there is no CycloneDX document to supply, but you know what a file is: the vendor DLL you
ship is libfoo 2.3.1, MIT, from Foo Inc. Say so directly:

```xml
<component for="[INSTALLDIR]libfoo.dll"
           name="libfoo" version="2.3.1"
           creator="security@foo.example"
           license="MIT"
           purl="pkg:nuget/Foo@2.3.1"/>
```

`for` names exactly one installed file, as with `<sbom>`. Everything else is optional, but at
least one fact is required:

| attribute | |
|---|---|
| `name` | the component's name as its creator gives it; defaults to the file's name |
| `version` | the creator's version. If the package records a version resource for the file, the two must agree (trailing `.0` groups aside), or the build stops: a script that is wrong about the file is not published |
| `creator` | an email address, or a URL when there is none (BSI TR-03183-2's "component creator") |
| `license` | an SPDX licence expression: `MIT`, `Apache-2.0 OR MIT`, `GPL-3.0-only WITH Classpath-exception-2.0` |
| `purl`, `cpe` | identifiers a vulnerability database can match. **Give one only if you are sure:** a wrong one produces false matches and hides real ones |
| `source` | where the file's source code is published, as an absolute URL: the version in its repository if you can name it (`https://github.com/foo/libfoo/tree/v2.3.1`), else the repository itself (BSI TR-03183-2's "source code URI") |

**A whole folder.** A target ending in a separator declares the same facts for every file
installed under it, recursively unless `recursive="no"`:

```xml
<component for="[LOCALAPPDATADIR]templates\wixlib\" creator="https://wixtoolset.org" license="MS-RL"/>
```

Each file still gets its own entry (and D13's version check), `name` is refused (every file
keeps its own), and a folder that covers no file is an error. A file may be described once
only: covered by a folder declaration and also named by its own `<component>` or `<sbom>`, it
stops the build. Declare the folder's other files individually instead.

The facts go onto the file's **own** component in the SBOM, beside what msis read from the
bytes, not onto a separate one. `msis:declared.by` names the `<component>` they came from, and
`msis:declared.fields` lists which fields it set, so a reader can always tell a declared value
from an observed one. The licence becomes BSI's pair: the original licence, marked `declared`,
and the distribution licence, marked `concluded`. A compound expression such as
`Apache-2.0 OR MIT` can only be one CycloneDX expression, so it is given as the distribution
licence. A file described twice (two `<component>`s, or a `<component>` and an `<sbom>`) is refused.

`<sbom>` and `<component>` answer different questions. `<sbom>` describes what is **inside** a
file, the libraries it links, as components of their own. `<component>` describes **the file
itself**.

---

## Tutorial 14: Recording What Is Not Exploitable

A scanner matches a CVE against a library you ship. Most of the time the answer is "yes, that
library, no, we never call the affected code". Unless that answer is written down in a form a
machine can read, it is worked out again at the next audit, and the one after that.

That is what a VEX document is — Vulnerability Exploitability eXchange. You keep one in your
repository beside the `.msis`, and name it:

```xml
<setup>
  <set name="PRODUCT_NAME" value="MyApp"/>
  <set name="PRODUCT_VERSION" value="4.1"/>
  ...
  <vex source="app.vex.json"/>

  <feature name="Main">
    <files source="bin" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

```bash
msis /BUILD /SBOM setup.msis
```

msis writes the evaluated result beside the SBOM, as `MyApp.msi.vex.cdx.json`.

### What a statement looks like

```json
{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "version": 1,
  "vulnerabilities": [
    {
      "id": "CVE-2024-1234",
      "analysis": {
        "state": "not_affected",
        "justification": "code_not_reachable",
        "detail": "zlib is linked for the compressor only; the affected inflate path is never called"
      },
      "affects": [{ "ref": "msis/<upgrade-code>/file/%5binstalldir%5dapp.exe" }],
      "properties": [
        { "name": "msis:vex.assessedProductVersion", "value": "4.1" }
      ]
    }
  ]
}
```

The `ref` is a `bom-ref` from your SBOM — build once with `/SBOM` and copy it out of the
document. Those refs are deliberately stable across releases, so you write one once.

### The property that matters

`msis:vex.assessedProductVersion` records **which release you assessed**. It is required, and
the build fails without it.

That is not bureaucracy. Consider what happens when it is missing: version 4.1 ships zlib and
never calls the vulnerable path, so you record `not_affected`. In 4.2 the library is *byte for
byte identical* — but the application has started calling that path. Any tool that carried the
assessment forward on the strength of the unchanged hash would state `not_affected` about a
vulnerability that is now real, and the alert that should have been raised would be subtracted
by the very record meant to make triage honest.

So msis checks the release being built against the release you assessed:

| what your statement records | building 4.1 | building 4.2 |
|---|---|---|
| `assessedProductVersion: 4.1` | applies | **needs review** |
| `assessedProductVersion: 4.1`, `appliesToProductVersions: 4.1, 4.2` | applies | applies |
| `assessedProductVersion: 4.1`, `appliesToProductVersions: *` | applies | applies |

Carrying an assessment across releases is possible — it is just your decision rather than a
guess msis made for you. `msis:vex.appliesToProductVersions` takes an exact list or `*`; there
is no range syntax, because comparing versions in someone else's scheme is precisely the kind of
inference that would silently widen an assessment.

You may also record `msis:vex.assessedComponentDigest`. If the component's bytes change, the
statement needs review whatever its version scope says.

### What msis does with a statement that no longer applies

It keeps it — dropping an assessment loses the work and the audit trail — and marks it:

- `msis:vex.applicability` becomes `needs-review`, with `msis:vex.reviewReason` saying which
  condition lapsed;
- if the statement was **suppressing** a finding (`not_affected`, `false_positive`, `resolved`),
  its `analysis.state` becomes `in_triage`, and the original is preserved in
  `msis:vex.previousState`. A conclusion whose premises no longer hold must stop reading as a
  conclusion.
- if the statement was **warning** (`exploitable`), it is left exactly as it is. Silencing a
  warning because its scope lapsed would be the same mistake in the other direction.

The build prints how many statements apply and how many need review, and warns when any do.

### Asking the question later

`tools/sbom-index` indexes VEX sidecars alongside the SBOMs — they are CycloneDX documents, so
the same corpus scan finds them:

```bash
just sbom-index                    # build the index over your corpus
just sbom-query affected-unassessed "-arg name=zlib1.dll -arg version= -arg cve=CVE-2024-1234"
```

That is "which of our releases ship this component, minus the ones we have already assessed as
not exploitable". Only an assessment that *still applies* subtracts; one needing review is
reported, which is the whole point.

```bash
just sbom-query assessments "-arg cve=CVE-2024-1234"
```

lists what was claimed, for which release, and whether it still holds.

### What msis does not do

msis assesses nothing, and cannot: whether a vulnerable path is reachable is a question about
your source. It checks the conditions you recorded, and it makes sure an assessment cannot
outlive the reason it was true. Everything else in the document is your claim, not msis's.

---

## Tutorial 15: Putting It All Together

Here's a complete example for a real-world application:

```xml
<setup>
  <!-- Product identification -->
  <set name="PRODUCT_NAME" value="Acme Productivity Suite"/>
  <set name="PRODUCT_VERSION" value="2.5.0"/>
  <set name="MANUFACTURER" value="Acme Corporation"/>
  <set name="UPGRADE_CODE" value="{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}"/>

  <!-- Installation options -->
  <set name="INSTALL_FOLDER" value="Acme\ProductivitySuite"/>
  <set name="ADD_TO_PATH" value="true"/>
  <set name="LICENSE_FILE" value="license.rtf"/>
  <set name="INSTALL_DIR_DIALOG" value="true"/>
  <set name="SETUP_ICON" value="app.ico"/>

  <!-- Main application -->
  <feature name="Application" enabled="true">
    <files source="bin" target="[INSTALLDIR]"/>

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
    <files source="service" target="[INSTALLDIR]service\"/>

    <service file-name="[INSTALLDIR]service\AcmeSync.exe"
             service-name="AcmeSyncService"
             service-display-name="Acme Background Sync"
             start="auto"
             description="Synchronizes your data in the background"/>
  </feature>

  <!-- Optional documentation -->
  <feature name="Documentation" enabled="false">
    <files source="docs" target="[INSTALLDIR]docs\"/>

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

### "wix CLI not found" or "Extension not found"

Both usually mean WiX or its extensions aren't installed at a matching version. Let msis
provision them — it installs the correct WiX version and all required extensions, pinned to
that version, and verifies the result:

```
msis /SETUP-WIX
```

To install a specific WiX version (e.g. stay on WiX 6), add `/WIX-VERSION:6.0.2`.

If `wix extension list` prints `WIX6101 ... compatible with WiX vN?` warnings or shows
extensions as "(damaged)", those are leftover copies from a different WiX major in the shared
cache. They are **harmless** — builds only load the version-matched extensions and stay quiet
(an actual `msis /BUILD` never prints these). The WiX CLI cannot remove other-major copies, so
msis leaves them alone.

<details>
<summary>Manual install (equivalent)</summary>

```bash
dotnet tool install --global wix --version 7.0.0
wix extension add -g WixToolset.UI.wixext/7.0.0
wix extension add -g WixToolset.Util.wixext/7.0.0
wix extension add -g WixToolset.BootstrapperApplications.wixext/7.0.0   # bundles
wix extension add -g WixToolset.Netfx.wixext/7.0.0                      # bundles
```

The `-g` (global) flag and `/7.0.0` version pin matter on every line: WiX shares one global
extension store across versions, so an unpinned add leaves mismatched copies behind. (Use
`/6.0.2` throughout to stay on WiX 6 — msis supports both.)
</details>

Confirm what msis actually resolves (it uses the dotnet-tool `wix`, not whatever is on PATH):
```bash
msis /STATUS
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
- [Templates & Customization](templates.md) - Logo branding, custom templates
- [Bundle Guide](Bundle.md) - Advanced bundle options and prerequisites
- [SBOM](sbom.md) - `/INSPECT` and `/SBOM`, retention, BOM-Links, and what the document does not cover
- [Roadmap](roadmap.md) - Planned features including custom UI properties
- [Developer Overview](overview.md) - Architecture and internals for contributors
