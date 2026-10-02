# #95 — run this on the VM

Everything needed is in this folder: two silent bundles built on the development machine
(`explicit.exe`, `auto.exe`) and `manifest.json`. The VM needs no repo, Go, WiX or msis; plain
Python is enough.

## ⚠ Take a VM snapshot first

Each bundle installs one small MSI under `C:\Program Files\MsisProbe95\…`. `auto.exe` also chains
the real VC++ 2022 x64 redistributable. Every VC++ runtime is already registered on this VM, so
Burn must detect it as present and not run it; the probe fails if it runs, and checks the runtime
is still registered afterwards. Nothing else is touched; the packages contain nothing that deletes
recursively.

The probe refuses to start if its folder or products are already there. Whatever happens, it ends
by uninstalling its bundles and MSIs and removing the folders and keys that are then empty. A
failed cleanup fails the run.

## Run it

```powershell
python t95_vm_probe.py --selftest     # no elevation, installs nothing
python t95_vm_probe.py                # ELEVATED, unattended, a few minutes; /passive shows progress windows
python t95_vm_probe.py --cleanup      # ELEVATED: only if a run was interrupted
```

Copy the whole output back. Burn's and the MSIs' logs are in `logs\`.

## What it checks

For `explicit.exe` and `auto.exe`, each with `/quiet` and then `/passive`:

| Step | Expected |
|---|---|
| install | exit 0; the bundle and its MSI registered; the MSI's file installed; the MSI ran with no UI of its own (UILevel 2); for `auto.exe`, the VC++ redistributable detected Present and never executed |
| uninstall, through the bundle | exit 0; neither registered; the file gone |

Before the fix, these bundles did not start at all (0x80070490, T90's first run).

## Optional, by eye

Start `explicit.exe` with no switch (not elevated is fine): the standard bootstrapper window
should open with an Install button and no license checkbox. Close it without installing.
