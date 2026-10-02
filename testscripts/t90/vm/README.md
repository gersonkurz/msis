# #90 — run this on the VM

Everything needed is in this folder: `probe90.exe`, a bundle built on the development machine
from the regular template and run with `/quiet` (the silent template's bundles do not start, #95), and `manifest.json`. The VM needs no repo, Go, WiX or msis; plain Python is enough.

## ⚠ Take a VM snapshot first

The bundle installs a tiny MSI, "MSIS Search Probe 90 App", under
`C:\Program Files\MsisProbe90\App`, and runs six stand-in installers. Each only writes a marker
file under `C:\ProgramData\MsisProbe90\markers`. The probe writes registry values under its own
keys only: `HKLM\SOFTWARE\MsisProbe90` (both views) and `HKCU\Software\MsisProbe90`. The
WebView2 key is only read. Nothing else is touched, and the packages contain nothing that
deletes recursively.

The probe refuses to start if its folders, keys or products are already there. Whatever happens,
it ends by uninstalling the bundle and its MSI, deleting its marker files and the values it
wrote, and removing the folders and keys that are then empty. A failed cleanup fails the run.

## Run it

```powershell
python t90_vm_probe.py --selftest     # no elevation, installs nothing
python t90_vm_probe.py                # ELEVATED, unattended, a few minutes
python t90_vm_probe.py --cleanup      # ELEVATED: only if a run was interrupted
```

Copy the whole output back. Burn's logs are in `logs\`.

## What it checks

For each round and each `<exe>`: it ran (marker present) exactly when Burn's log says it detected
the package Absent, and exactly when the search's documented semantics (docs/Bundle.md) predict.

| Round | State | Expected |
|---|---|---|
| R1 | nothing | every synthetic exe runs |
| R2 | `flag` in HKLM's 32-bit view only | E32 skipped; E64 and EKEY (64-bit view) run |
| R3 | `flag` in the 64-bit view only | E64, EKEY skipped; E32 runs |
| R4 | `ver = 1.2.3`, no `flag` | EVAL skipped; E32 runs (key without the value) |
| R5 | `ver = 0.0.0.0` | EVAL runs |
| R6 | `ver` empty | EVAL runs |
| R7 | `flag` = DWORD 0 | E32 skipped (exists counts a value holding 0) |
| R8 | the HKCU key exists | ECU skipped |

In every round EWV (the WebView2 recipe, `per-machine="yes"`) is skipped exactly when the
machine-wide WebView2 runtime is registered, which the probe reads directly and prints.

Not covered: running as another account or as SYSTEM, and WiX 7. That `per-machine="yes"`
reaches the bundle is checked at build time; with the probe already elevated, a run cannot tell
whether Burn elevated for it.
