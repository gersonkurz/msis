# #85 and #87 — run this on the VM

Everything needed is in this folder: the six MSIs, built on the development machine, and
`manifest.json` with what each step expects. The VM needs no repo, Go, WiX or msis. Plain Python
is enough; no packages are needed.

## ⚠ Take a VM snapshot first

The probe installs, upgrades, repairs and uninstalls two per-machine MSIs of its own:
- "MSIS Id Probe 85" under `C:\Program Files\MsisProbe85\IdProbe` and
  `C:\ProgramData\MsisProbe85\IdProbe`
- "MSIS Feature Probe 87" under `C:\Program Files\MsisProbe85\FeatureProbe`

Nothing else is touched. The packages contain no installer hooks and nothing that deletes
recursively. The build side checked that, and that no folder permission lands on an unnamed
folder such as `C:\ProgramData` itself (#55). The permission components grant BUILTIN\Users
access to the probe's own folders only.

The probe refuses to start if either `MsisProbe85` folder exists or either product is installed.
Whatever happens, it ends by uninstalling both products, deleting the three runtime files it
wrote itself, and removing the folders under `MsisProbe85` that are then empty. It never deletes
a folder that still has content; it reports what is left instead.

## Run it

```powershell
python t85_vm_probe.py --selftest     # no elevation, installs nothing
python t85_vm_probe.py                # ELEVATED, unattended, a few minutes
python t85_vm_probe.py --cleanup      # ELEVATED: only if a run was interrupted
```

Copy the whole output back. The msiexec logs are in `logs\`, in case a step fails.

## What it checks

Leg 1, #85 (decisions D26): the first upgrade to path-derived ids.

| Step | Expected |
|---|---|
| L1.1 install 1.0.0 (old sequence-number ids) | its files and folders; one ARP entry. Then it records the Users ACL entries of every permissioned folder, and writes runtime data the MSI never installed |
| L1.2 upgrade to 1.0.1 (D26) | 1.0.1's files, the runtime data unchanged, folder ACLs as after L1.1 |
| L1.3 upgrade to 1.0.2 (D26, with added folders) | the same, plus the additions |
| L1.4 remove the Extra feature | `data\extra.txt` gone; the `data` folder, which Main shares, and its ACL kept |
| L1.5 delete `conf\settings.ini` and the empty `cache` folder, repair | both back; ACLs as after L1.1 |
| L1.6 uninstall | only the runtime data is left; the package's empty folders are gone; no ARP entry |

Leg 2, #87: does inserting a feature move the customer's feature choices?

| Step | Expected |
|---|---|
| control: install f1 with every feature, upgrade to f2append | Main and Debug installed, as chosen |
| insert: install f1 with every feature, upgrade to f2insert | printed as an observation: Main and Debug means #87 does not reproduce; anything else is #87 reproducing |

Leg 1 and the control decide PASS or FAIL. The insert is the answer to #87, and either result is
a valid outcome.
