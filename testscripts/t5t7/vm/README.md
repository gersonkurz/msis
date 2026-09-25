# T5 + T7 — run this on the test VM

Everything needed is in this folder: six MSIs built on the development machine, and
`manifest.json` saying what each of the seven scenarios expects. This VM needs no repo, no Go, no
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

## What it runs

Seven scenarios, in this order. The first two passed on 2026-09-19; they run again because the
packages are rebuilt with the current msis. The rest are the four cases that kept #3 open.

| Scenario | Package(s) | What it checks |
|---|---|---|
| core | `top.msi` | the cleanup at **top level** (T5, issue #15) |
| core | `feat.msi` | the cleanup inside a `<feature>` (T7, issue #3) |
| core | `feat-minimal.msi` | the same, built with the **minimal** template |
| core | `feat-silentx86.msi` | the same, built with the **silent x86** template; its registry keys are checked in the 32-bit view |
| upgrade | `upg-1.0.0.msi` → `upg-1.0.1.msi` | a **major upgrade** of a package carrying the cleanup |
| empty | `feat.msi` | the target folder **already empty** at uninstall |
| never | `feat.msi` | the target folder **never existed** (the application never ran) |

**A core scenario:**

1. **Install**, then check the path the installer remembered for the folder it will delete
   **equals** the intended absolute path. This is the trap both tickets call out: `[APPDATADIR]`
   already includes the product folder, so `[APPDATADIR]Vendor\logs` resolves to
   `C:\ProgramData\<INSTALLDIR>\Vendor\logs`, not `C:\ProgramData\Vendor\logs`. "It looks
   absolute" would pass while pointing somewhere else.
2. **Seed** what a running application would leave behind: a file in the target folder, a file
   in a **nested subfolder**, a value in the target registry key. Also seed the sentinels that
   must survive: a file in the target's **parent**, a file in a **sibling** directory, and a
   neighbouring registry key.
3. **Repair** (`msiexec /f`). Every seeded file must still be there. A repair must never be a
   data-loss event.
4. **Uninstall.** The target folder and everything beneath it must be gone, the target registry
   key must be gone, and **every sentinel must be untouched**.

**The upgrade:**
1. Install 1.0.0 and seed.
2. Install 1.0.1 over it, a major upgrade. Check that 1.0.1 is what is installed, that the
   remembered path still holds, and that every sentinel is untouched.
3. The application's files and registry value **must survive the upgrade**. This is a
   PASS/FAIL. The first run (2026-09-25) recorded that the upgrade deleted them, which is #76;
   since the fix, an upgrade that deletes them fails the probe.
4. Re-seed, then uninstall 1.0.1: the target must go, and the sentinels must stay.

**Empty and never:**
1. Install, and seed only the sentinels.
2. Empty the target folder, or remove it.
3. Uninstall. The uninstall must succeed and every sentinel must survive. What becomes of the
   target folder is recorded.

Everything recorded rather than judged is printed as `OBSERVED` and listed again at the end.
Those lines go into `todo-testme.md` T7.

## The sentinels are the point

The value of `<remove-on-uninstall>` over `REMOVE_FOLDERS_ON_UNINSTALL` is a narrower blast
radius. A run that removes the target but also takes the parent or a sibling with it is a
**FAIL**, not a detail — that is the failure mode that cost a customer their database.

If anything outside the named target is removed, the tickets say to stop and reopen (#15 for
top-level, #3 for the feature case) rather than adjust the documentation to match.

## Files

- `t5t7_vm_probe.py` — the probe; the only thing to run
- `manifest.json` — what each scenario expects, generated with the packages so the two cannot
  disagree about the intended path
- `top.msi`, `feat.msi`, `feat-minimal.msi`, `feat-silentx86.msi`, `upg-1.0.0.msi`,
  `upg-1.0.1.msi` — the packages under test
