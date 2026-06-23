# Installer Hooks & Destructive Uninstall Cleanup

This page explains MSIS's native **installer-hook** system: what it does, **why it was
restructured**, the variables that drive it, and how to extend it. If you just want the variable
reference in template context, see [Templates & Customization](templates.md#uninstall-behavior-and-installer-hooks).

## What it is

By default, uninstalling an MSIS package is a **standard Windows Installer uninstall**: it removes
only what the MSI installed — files, registry entries, shortcuts. Anything your application created
at **runtime** (databases, logs, user configuration) is left untouched.

`USE_INSTALLER_HOOKS=True` enables a **native custom-action DLL** (named by `DLL_ENTRY`, e.g.
`msi-simplica.dll`) that adds custom actions to the install/uninstall sequence. The reference DLL
shipped with MSIS implements two **destructive cleanup** actions; the rest of the surface is an
extension point (see [Hook ABI](#hook-abi-extension-contract)).

## Why this was restructured (motivation)

This whole area was reworked after a real incident: a customer's application stored its SQLite
database under `C:\ProgramData\<Product>\DATABASE\proakt.db`, created by the app at first run.
Uninstalling the product **deleted the database** — data the installer never created and the
customer expected to keep.

The cause was three compounding problems:

1. **A blunt, recursive delete.** With `USE_INSTALLER_HOOKS=True` and `REMOVE_FOLDERS_ON_UNINSTALL=True`,
   the native DLL recursively deletes the **entire `INSTALLDIR` and `APPDATADIR` trees** —
   everything, including runtime/customer files the MSI never installed. There was no way to exempt
   a file.
2. **Silent, easy to arm by accident.** `REMOVE_FOLDERS_ON_UNINSTALL` did nothing on its own (it
   needs `USE_INSTALLER_HOOKS`), the templates gated it inconsistently (the silent template wasn't
   guarded), and `REMOVE_REGISTRY_TREE="False"` was *truthy* in the template engine — so a
   "disabled" value still ran. Nothing warned you.
3. **A stale, external dependency.** MSIS referenced `msi-simplica.dll` but didn't build or ship it;
   behavior depended on whatever (possibly old) binary happened to sit in the template folder.

The fixes, in order: make the danger **explicit and warned**, make the destructive variables gate
**consistently**, add an **opt-out** for specific files, and **own the DLL** so it is built and
shipped reproducibly.

## Variables

All four are read at build time; the destructive ones only take effect with `USE_INSTALLER_HOOKS=True`.

### `USE_INSTALLER_HOOKS`
Enables the native hook DLL. When set, MSIS checks the **arch-native** DLL exists before the WiX
build and **fails with a clear message** if it is missing (checked across the same bind paths WiX
uses). x86, x64, and arm64 are all supported.

### `REMOVE_FOLDERS_ON_UNINSTALL`
> ⚠️ **Dangerous.** On a full uninstall, recursively deletes the entire `INSTALLDIR` **and**
> `APPDATADIR` trees, including runtime/customer files. MSIS warns whenever this is active.

Boolean. Requires `USE_INSTALLER_HOOKS=True` (warns "no effect" otherwise). Retained mainly for
legacy packages — prefer leaving it off so standard uninstall preserves user data.

### `REMOVE_REGISTRY_TREE`
> ⚠️ **Dangerous.** Recursively deletes the listed registry trees (`RegDeleteTree`) on full
> uninstall — can remove state not owned by the MSI.

A **comma-separated list of registry roots** (not a boolean), preserved for compatibility:

```xml
<set name="REMOVE_REGISTRY_TREE"
     value="HKEY_LOCAL_MACHINE\Software\Pergamon,HKEY_CLASSES_ROOT\WOSA/XFS_ROOT"/>
```

It is "active" when it holds a real value; `False`/`No`/`Off`/`0`/empty/missing (case-insensitive)
disable it. Requires `USE_INSTALLER_HOOKS=True`.

### `RETAIN_FILES_ON_UNINSTALL`
The opt-out for folder cleanup: a `;`-separated list of files that must **not** be deleted.

```xml
<set name="RETAIN_FILES_ON_UNINSTALL"
     value="[APPDATADIR]DATABASE\proakt.db;[APPDATADIR]CONFIG\local.ini"/>
```

`[PROPERTY]` tokens are resolved at uninstall time, matching is case-insensitive, and the parent
directories of a retained file are preserved automatically. Interpreted by the DLL — MSIS only
passes it through as an MSI property. It only protects files when built against an **updated hook
DLL** that honors it (older DLLs ignore it); MSIS warns if it is set while folder cleanup is inactive.

## Build-time safety

MSIS emits warnings when generating the package:

- `REMOVE_FOLDERS_ON_UNINSTALL=True` → always warns it recursively deletes `INSTALLDIR`/`APPDATADIR`.
- `REMOVE_REGISTRY_TREE` active → warns it recursively deletes the listed registry trees.
- Either set without `USE_INSTALLER_HOOKS=True`, or `RETAIN_FILES_ON_UNINSTALL` set while cleanup is
  inactive → a "no effect" warning.
- `USE_INSTALLER_HOOKS=True` with the hook DLL missing for the target platform → the build **fails**
  clearly (rather than a cryptic WiX bind error later).

## The native DLL

The DLL lives in [`native/msi-simplica/`](../native/msi-simplica) (migrated from msis-2.x and
modernized: VS 2026 toolset, x86/x64/arm64, WiX-native `wcautil`/`dutil` via pinned NuGet
`PackageReference` — no hardcoded paths). Build it from a **Visual Studio 2026 Developer shell**:

```
just build-hooks
```

That builds all three architectures and stages `msi-simplica.dll` into `templates/x86`,
`templates/x64`, and `templates/arm64`. `just release` / `just release-all` run it automatically,
and the MSIS installer ships the DLLs to `%LOCALAPPDATA%\MSIS\templates\<arch>\`. The DLLs are
**build artifacts** (git-ignored); a fresh clone must run `just build-hooks` before building a
hook-using package. (An arm64 build uses the x64 *template* but loads the **arm64** DLL.)

## Hook ABI (extension contract)

The hook DLL is a documented ABI, not a fixed binary. The templates define call sites for **eight**
entry points; a custom `DLL_ENTRY` DLL may implement any subset (unimplemented entry points are
ignored, because each call site is `Return="ignore"`):

| Entry point | When it runs |
|-------------|--------------|
| `BeforeInstall` / `AfterInstall` | fresh install |
| `BeforeUpgrade` / `AfterUpgrade` | major upgrade |
| `BeforeUninstall` / `AfterUninstall` | full uninstall |
| `RemoveAllFoldersOnUninstall` | folder cleanup (driven by `REMOVE_FOLDERS_ON_UNINSTALL`) |
| `RemoveRegistryTreeOnUninstall` | registry cleanup (driven by `REMOVE_REGISTRY_TREE`) |

The **reference** DLL (`msi-simplica.dll`) implements only the two cleanup actions (plus a
`ListAllKnownProperties` diagnostic). The six lifecycle hooks are the **extension surface**: ship
your own DLL via `DLL_ENTRY` to run custom logic at install/upgrade/uninstall, without forking MSIS.

## See also

- [Templates & Customization](templates.md#uninstall-behavior-and-installer-hooks) — variable reference in template context
- [Prerequisites](prerequisites.md) — VC++ runtime via `<requires type="vcredist">` (merge modules are obsolete)
