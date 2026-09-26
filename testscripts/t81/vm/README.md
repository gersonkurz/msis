# #81 — run this on the test VM

Everything needed is in this folder: `v1.msi` and `v2.msi`, built on the development machine, and
`manifest.json` naming the install folder and every file's expected content. This VM needs no
repo, no Go, no WiX and no msis, and plain Python is enough (no packages).

## ⚠ Take a VM snapshot first

It installs, upgrades, repairs and uninstalls a per-machine MSI under `C:\Program Files\GuidProbe81`.

## Run it

```powershell
python t81_vm_probe.py --selftest     # no elevation, installs nothing
python t81_vm_probe.py                # ELEVATED, unattended
```

Copy the whole output back.

## Background

Up to 3.0.5 a file component's GUID hashed its source path as the build saw it; since #81
(decisions D23) it is the product plus where the file installs. So the first 3.0.6 build of a
product gives every file component a new GUID. D23 argues that is harmless, because the default
major upgrade removes the old version completely before installing the new one. `v1.msi` (1.0.0)
was built by msis before #81, `v2.msi` (1.0.1) by msis with D23, from a different folder; the
two share no component GUID.

## What it checks

| Step | Expected |
|---|---|
| 1 install v1 | v1's three files; one ARP entry, 1.0.0 |
| 2 upgrade to v2 | v2's four files at v2's content (one unchanged, one added); one ARP entry, 1.0.1 |
| 3 delete `conf\static.txt`, repair v2 | the file is back; still one ARP entry |
| 4 uninstall v2 | no files, no install folder, no ARP entry |

Every msiexec must return 0 (or 3010).
