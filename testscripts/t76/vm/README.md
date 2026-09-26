# #76 — run this on the test VM

Everything needed is in this folder: `v1.msi`, `v2.msi` and `v3.msi`, built on the development
machine, and `manifest.json`. This VM needs no repo, no Go, no WiX and no msis, and plain Python
is enough (no packages).

## ⚠ Take a VM snapshot first

It installs, upgrades and uninstalls a per-machine x86 MSI under
`C:\Program Files (x86)\UpgradeProbe76`.

## Run it

```powershell
python t76_vm_probe.py --selftest     # no elevation, installs nothing
python t76_vm_probe.py                # ELEVATED, unattended
```

Copy the whole output back.

## Background

`<remove-on-uninstall folder="[INSTALLDIR]"/>`, as the Poste Italiane 4.2 scripts use it,
deleted the folder on a major upgrade too, up to msis 3.0.5. 3.0.6 runs it on a real uninstall
only (#76, decisions D21). An upgrade removes the old version with the old package's own tables,
so the first upgrade from a 3.0.5-built package is predicted to delete the folder once more.

| Package | Version | Built by |
|---|---|---|
| `v1.msi` | 1.0.0 | msis 3.0.5 |
| `v2.msi` | 1.0.1 | msis 3.0.6 |
| `v3.msi` | 1.0.2 | msis 3.0.6 |

## What it checks

| Step | Expected |
|---|---|
| 1 install v1, seed `data\customer.db` | installed, one ARP entry |
| 2 upgrade v1 → v2 | OBSERVED: is the site data kept or gone? |
| 3 upgrade v2 → v3 | the site data is kept |
| 4 uninstall v3 | the folder is gone |
