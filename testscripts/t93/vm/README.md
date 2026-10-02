# #93 / #94 — run this on the VM

Two bundles built on the development machine, `probe93-old.exe` (msis before the fix, the
control) and `probe93-new.exe`, and `manifest.json`. Plain Python is enough.

## ⚠ Take a VM snapshot first, and run it NOT elevated

Each bundle installs one small MSI under `C:\Program Files\MsisProbe93\App`, and runs stand-in
installers that only write a marker under `%LOCALAPPDATA%\MsisProbe93\markers`. Run the probe
from a **normal, non-elevated** shell: Burn must elevate the per-machine packages itself, so you
get a **UAC prompt for each install and each uninstall (four)**. Click Yes.

The .NET and VC++ prerequisites are stand-ins too: nothing real is installed. The probe refuses
to start if it is elevated, if .NET 4.8.1 or the VC++ x64 runtime is missing, or if its products
or markers are already there. It always ends by uninstalling and removing its markers.

```powershell
python t93_vm_probe.py --selftest
python t93_vm_probe.py                # NOT elevated; Yes on each UAC prompt
python t93_vm_probe.py --cleanup      # after an interrupted run
```

Copy the whole output back.

## What it checks

| Bundle | .NET stand-in | VC++ stand-in | per-machine exe | exe without the attribute |
|---|---|---|---|---|
| new | detected Present, not run (#93) | not run | ran elevated | ran elevated |
| old (control) | detected Absent, **ran** (elevated) | not run | ran elevated | ran elevated |

Every package runs elevated whether or not it is marked per-machine: a package without the
attribute takes the bundle's scope, which is per-machine (#94: not a bug, decisions D32).
