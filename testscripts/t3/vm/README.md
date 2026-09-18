# T3 — run this on the test VM

Everything needed is in this folder. The bundle was built on the development machine; this
VM needs no repo, no Go, no WiX and no msis.

## Before you start

The VM must be **64-bit Windows without the x86 VC++ 2015-2022 runtime**. The script checks
and refuses otherwise, because with the runtime already present the detect condition passes
either way and the run proves nothing.

Ideally the **x64** runtime *is* present — that is the exact case
[#8](https://github.com/gersonkurz/msis/issues/8) got wrong, where the old code called the
machine satisfied on the strength of the x64 runtime and never installed the x86 one. The
script says so if it is missing; the run is still valid, just weaker.

**Take a VM snapshot first.** The probe installs the redistributable permanently (see below).

## Run it, elevated

```powershell
uv run t3_vm_probe.py
```

or, if uv is not on the VM — the script falls back to plain output when loguru is missing:

```powershell
python t3_vm_probe.py
```

Copy the whole output back to the development machine.

## What it does

1. Records whether the x86 and x64 runtimes are present.
2. Installs the bundle. The x86 runtime must be installed, must appear in Add/Remove
   Programs, and the MSI must then install — `C:\Program Files (x86)\X86BundleProbe\readme.txt`
   exists. That last one is the user-visible symptom: before the fix the MSI's launch
   condition failed because it found no x86 runtime.
3. Uninstalls, then installs again. This time the prerequisite must be detected `Present` and
   planned `execute: None` — skipped, not reinstalled.
4. Uninstalls. The product must be gone.

Burn's own logs are written here as `install-1.log`, `install-2.log` and the uninstall logs;
the script pulls the `Detected package` / `Planned package` lines out of them.

## ⚠ One-shot, and it leaves the runtime behind

Bundles mark prerequisites `Permanent='yes'`, so uninstalling the probe does **not** remove
the Visual C++ redistributable. Once it is installed, the case under test is gone and the
script will refuse to run again.

To go again, **roll the VM back to the snapshot**. That is the only reset offered: removing a
machine-wide runtime is a destructive operation this probe has no way to test, so it does not
do it.

## Files

- `t3_vm_probe.py` — the probe; the only thing to run
- `X86 Bundle Probe-1.0.exe` — the bundle under test (the odd version in the name is
  [#27](https://github.com/gersonkurz/msis/issues/27), not a typo)

## Checking it without installing anything

```powershell
python t3_vm_probe.py --state      # does this machine qualify?
python t3_vm_probe.py --selftest   # does the verdict reject every failure mode?
```

Neither needs elevation. `--selftest` exists because an earlier version of this script
discarded installer return codes and would report PASS after a failed install.
