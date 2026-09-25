# #77 — run this on the test VM

Everything needed is in this folder: `svc.msi`, built on the development machine, and
`manifest.json` naming its feature ids, the service and both executables' paths. This VM needs
no repo, no Go, no WiX and no msis, and plain Python is enough (no packages).

## ⚠ Take a VM snapshot first

It installs and uninstalls a per-machine MSI and registers a service (never started).

## Run it

```powershell
python t77_vm_probe.py --selftest     # no elevation, installs nothing
python t77_vm_probe.py                # ELEVATED
```

Copy the whole output back.

## Background

A `<service>` in a different feature from the `<files>` installing its executable makes msis
install that file twice, in two components. On this VM (2026-09-25) removing either feature
deleted the executable the other still needed, and msiexec reported success every time. That
layout is deprecated (#77, D22): msis still builds it with a warning, `/STRICT` refuses it, and
msis 4 will (the build side checks both). The warning proposes two ways to write it instead. This package is the one that keeps the
service optional: feature **Complete** installs `svcprobe.exe`; feature **Service** installs its
**own copy** under `service\` and registers that copy.

## What it checks

| Scenario | Steps |
|---|---|
| both, then remove Service | install both → remove Service → uninstall |
| both, then remove Complete | install both → remove Complete → uninstall |
| Complete, add Service, remove Service | default install → add Service → remove Service → uninstall |
| Service only | install Service only → uninstall |

After every step: the application's copy is present exactly while Complete is installed; the
service's copy is present, and the service registered with an ImagePath naming it, exactly while
Service is installed; both copies keep their contents. The msiexec verbose logs
(`s<scenario>-<step>.log`) stay beside the script.

Whenever the service is registered, its configuration is read back from its registry key and
compared with the manifest (#78). It checks:
- the display name, which is left out of the script, so msis-2.x's default (the service name)
  applies;
- the description;
- the service type (`shareProcess`), the error control (`critical`) and the start type;
- the failure actions `restart="yes"` sets: three restart actions of 30 s each (Windows
  repeats the last one for any later failure, so it restarts after every failure), and a
  failure count reset after a day without failures.

Each step prints the configuration it read.
