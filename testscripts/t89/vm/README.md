# #89 — run this on the VM

Everything needed is in this folder: six MSIs built on the development machine, and
`manifest.json`. The VM needs no repo, Go, WiX or msis; plain Python is enough.

## ⚠ Take a VM snapshot first

The probe installs, upgrades, repairs and uninstalls one per-machine product of its own, "MSIS
Downgrade Probe 89", under `C:\Program Files\MsisProbe89\App`. The guarded packages also write
one registry value, `HKLM\Software\msis\Packages\6C2E9B41-8D3F-4A75-B0E2-3F9A1C7D5E89\Version`.
Nothing else is touched. The packages contain no installer hooks and nothing that deletes
recursively; the build side checked that.

The probe refuses to start if its folder, its product or its registry key is already there.
Whatever happens, it ends by uninstalling the product and removing the folders under
`MsisProbe89`, and the registry keys up to `Software\msis`, that are then empty. It never deletes
a folder or key with content in it; it reports what is left instead.

## Run it

```powershell
python t89_vm_probe.py --selftest     # no elevation, installs nothing
python t89_vm_probe.py                # ELEVATED, unattended, a few minutes
python t89_vm_probe.py --cleanup      # ELEVATED: only if a run was interrupted
```

Copy the whole output back. The msiexec logs are in `logs\`.

## What it checks

Each package installs `core.dll`, whose file version is the package's version, two text files,
and a Child sub-feature's file.

| Step | Expected |
|---|---|
| A1–A4, without the guard | observed: does installing 1.2.3.80 over 1.2.3.90 lose `core.dll` (the report)? Does a repair bring it back? |
| B1 install 1.2.3.90 | installed; `00001.00002.00003.00090` recorded |
| B2 install 1.2.3.80 over it | refused (1603, with the message in the log); nothing changed |
| B3 install another 1.2.3.90 build | allowed; every file intact |
| B4 upgrade to 1.2.3.95 | works; 1.2.3.95 recorded |
| B5 install 1.2.3.90 over it | refused; nothing changed |
| B6 uninstall | no files; nothing recorded |
| B7 install 1.2.3.90 with only the Child sub-feature | the version is recorded all the same |
| B8 uninstall, then install 1.2.3.80 | the supported way back works |
| C1–C2, an install from before the guard | observed: 1.2.3.80 is not refused, because the old install recorded nothing |

Leg B decides PASS or FAIL. Legs A and C are observations.
