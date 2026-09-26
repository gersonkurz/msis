# #79 — run this on the test VM

Everything needed is in this folder: `same.msi` and `features.msi`, built on the development
machine, and `manifest.json` naming their feature ids and every file's content. This VM needs no
repo, no Go, no WiX and no msis, and plain Python is enough (no packages).

## ⚠ Take a VM snapshot first

It installs, changes features, repairs and uninstalls per-machine MSIs under
`C:\Program Files\TargetProbe79*`.

## Run it

```powershell
python t79_vm_probe.py --selftest     # no elevation, installs nothing
python t79_vm_probe.py                # ELEVATED, unattended
```

Copy the whole output back.

## Background

msis gives each `<files>` its own component, as msis-2.x did, so two `<files>` installing
different sources to one target make two components own one file (#79). #77 showed what that
does for a service's executable: removing either feature deleted the file the other still
needed. This checks the plain-file case, in the two shapes found:
- `same.msi`: one feature, core then NG copy of `CONFIG\CURRENCY.TXT`, the ProAKT shape;
- `features.msi`: `config.json` from Standard (on) and Variant (off by default), the issue's
  repro.

## What it checks

- PASS/FAIL: the file exists exactly while a feature owning it is installed, and where only one
  owner is installed it is that owner's copy.
- OBSERVED: which copy is on disk while both owners are installed.
