# T5 + T7 — run this on the test VM

Everything needed is in this folder: two MSIs built on the development machine, and
`manifest.json` saying what each one is expected to remove. This VM needs no repo, no Go, no
WiX and no msis.

## ⚠ Take a VM snapshot first

This installs per-machine MSIs, writes files under `C:\ProgramData` and keys under `HKLM`, and
then **deletes those directories and registry keys**. It cleans up after itself, but a snapshot
is the honest way to run something whose whole purpose is a recursive delete.

## Run it, elevated

```powershell
uv run t5t7_vm_probe.py
```

or, if uv is not on the VM — the script falls back to plain output when loguru is missing:

```powershell
python t5t7_vm_probe.py
```

Copy the whole output back.

Before that, and without elevation or installing anything:

```powershell
python t5t7_vm_probe.py --selftest
```

which checks that the verdict rejects every way the run can go wrong — including a deleted
sentinel. A probe for a destructive feature that cannot fail is worth nothing.

## What it does, per package

Two packages, the same cleanup elements in different places:

- **top.msi** — `<remove-on-uninstall>` at **top level** (T5, issue #15). Until #15 that
  package failed to build, so the deletion never ran at all.
- **feat.msi** — the same elements inside a `<feature>` (T7, issue #3).

For each:

1. **Install**, then check the path the installer remembered for the folder it will delete
   **equals** the intended absolute path. This is the trap both tickets call out: `[APPDATADIR]`
   already includes the product folder, so `[APPDATADIR]Vendor\logs` resolves to
   `C:\ProgramData\<INSTALLDIR>\Vendor\logs`, not `C:\ProgramData\Vendor\logs`. "It looks
   absolute" would pass while pointing somewhere else.
2. **Seed** what a running application would leave behind: a file in the target folder, a file
   in a **nested subfolder**, a value in the target registry key — plus the sentinels that must
   survive: a file in the target's **parent**, a file in a **sibling** directory, and a
   neighbouring registry key.
3. **Repair** (`msiexec /f`). Every seeded file must still be there. A repair must never be a
   data-loss event.
4. **Uninstall.** The target folder and everything beneath it must be gone, the target registry
   key must be gone, and **every sentinel must be untouched**.

## The sentinels are the point

The value of `<remove-on-uninstall>` over `REMOVE_FOLDERS_ON_UNINSTALL` is a narrower blast
radius. A run that removes the target but also takes the parent or a sibling with it is a
**FAIL**, not a detail — that is the failure mode that cost a customer their database.

If anything outside the named target is removed, the tickets say to stop and reopen (#15 for
top-level, #3 for the feature case) rather than adjust the documentation to match.

## Files

- `t5t7_vm_probe.py` — the probe; the only thing to run
- `manifest.json` — what each package is expected to remove, generated with the packages so
  the two cannot disagree about the intended path
- `top.msi`, `feat.msi` — the packages under test
