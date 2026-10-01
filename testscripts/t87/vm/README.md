# #87 — run this on the VM

Everything needed is in this folder: four MSIs built on the development machine, and
`manifest.json`. The VM needs no repo, Go, WiX or msis; plain Python is enough.

## ⚠ Take a VM snapshot first

The probe installs, upgrades, changes, and uninstalls one per-machine product of its own, "MSIS
Feature Probe 87", under `C:\Program Files\MsisProbe87\App`. It writes one runtime file there,
`data\user.dat`, and the packages record their version under
`HKLM\Software\msis\Packages\4B8D2E71-9C3A-4F65-A1D0-7E2B5C9F3A87` (decisions D27). Nothing else
is touched; the packages contain nothing that deletes recursively.

The probe refuses to start if its folder, its product or its registry key is already there.
Whatever happens, it ends by uninstalling the product, deleting the runtime file it wrote, and
removing the folders under `MsisProbe87` and the registry keys up to `Software\msis` that are then
empty. A failed cleanup fails the run.

## Run it

```powershell
python t87_vm_probe.py --selftest     # no elevation, installs nothing
python t87_vm_probe.py                # ELEVATED, unattended, a few minutes
python t87_vm_probe.py --cleanup      # ELEVATED: only if a run was interrupted
```

Copy the whole output back. The msiexec logs are in `logs\`.

## What it checks

The customer's choice, every time: v1 installed with **Main and Debug** only. Docs, on by
default, is deselected; Debug, off by default, is selected. Each step asks Windows Installer for
every feature's state and checks that each installed feature's file holds that package's
version.

| Step | Expected |
|---|---|
| 1.2 upgrade to v2id (NewOn, NewOnPart and NewTool inserted **with** ids) | Main, Debug installed; Docs absent; NewOn, NewOnPart (new, on by default) installed; NewTool (new, off) absent; runtime data intact |
| 1.3 `REMOVE=NewOn` | NewOn and its sub-feature absent; nothing else changed |
| 1.4 upgrade to v3 (every feature frozen to its shipped id, reordered, Docs removed) | Main, Debug installed; NewOn stays absent, as chosen in 1.3 |
| 1.5 uninstall | only the runtime data left |
| 2.2 v1 straight to v3 (a release skipped) | Main, Debug installed; NewOn, new to this customer, installed by default |
| 3.2 the control: NewTool inserted **without** an id | the #87 failure reproduces: NewTool installed, Debug not |
