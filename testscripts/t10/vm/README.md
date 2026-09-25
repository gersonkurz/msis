# T10 — preserve="yes" in a silent package: run this on the test VM

Everything needed is in this folder: four small MSIs built on the development machine, and
`manifest.json` with the values they install. This VM needs no repo, no Go, no WiX and no msis;
plain Python is enough (no packages).

## ⚠ Take a VM snapshot first

It installs and uninstalls per-machine MSIs and writes and deletes `HKLM\SOFTWARE\MsisProbeT10`
(32-bit view, `WOW6432Node`).

## Run it

```powershell
python t10_vm_probe.py --selftest     # no elevation, installs nothing
python t10_vm_probe.py                # ELEVATED
```

Copy the whole output back.

## What it checks

`preserve="yes"` writes each registry value from a property whose default is the `.reg` value
and which a registry search replaces with the value already on the machine: a fresh install
writes the defaults, an upgrade keeps the site's values. msis 3.0.3's **silent** template drops
those properties (#19), and the ProAKT 3.6.0.73 Silent Setup built with it references 39 of them.

The packages carry the kinds of values ProAKT preserves: two REG_SZ (`UI=HEADLESS`,
`IPPort=8000`), two DWORDs (`Limit=2500000`, `Flag=1`), an empty REG_SZ default (`DeviceID`),
and one value that is not preserved (`Control`), as a control.

| Scenario | Steps | Judged |
|---|---|---|
| fresh 3.0.3 silent | install the 3.0.3 silent package | OBSERVED — what the field has |
| fresh 3.0.6 silent | install the 3.0.6 silent package | must write the defaults |
| upgrade 3.0.3 regular | install 3.0.3 1.0.0, set site values, upgrade with 3.0.3 regular 1.0.1 | must keep them (control) |
| upgrade 3.0.3 silent | same, upgrade with the 3.0.3 silent package | OBSERVED — what the field has |
| upgrade 3.0.6 silent | same, upgrade with the 3.0.6 silent package | must keep them |

Every value is compared by data **and** registry type; each scenario prints what it read.
