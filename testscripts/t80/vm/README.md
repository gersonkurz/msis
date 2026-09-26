# #80 — run this on the test VM

Everything needed is in this folder: `full.msi` and `minimal.msi`, built on the development
machine, and `manifest.json`. This VM needs no repo, no Go, no WiX and no msis, and plain
Python is enough (no packages).

## ⚠ Take a VM snapshot first

It installs and uninstalls two per-machine MSIs, and creates `C:\T80\`.

## Run it

```powershell
python t80_vm_probe.py --selftest     # no elevation, installs nothing
python t80_vm_probe.py                # ELEVATED, interactive: you click through two installers
```

For each package the script prints which folder to pick where, opens the normal install UI,
and afterwards asks whether each OK closed the dialog. Copy the whole output back.

## Background

Since WiX 6, BrowseDlg's OK button publishes nothing itself; WiX's stock dialog sets do. msis's
templates carry their own set and did not, so in every msis 3 package OK in the Browse dialog
did nothing and only Cancel closed it (#80). The fix adds the two events (set the folder, close).

## What it checks

| Package | Template | BrowseDlg opened from |
|---|---|---|
| `full.msi` | `x64`, with `INSTALL_DIR_DIALOG` | InstallDirDlg's Change, then CustomizeDlg's Browse |
| `minimal.msi` | `minimal` | InstallDirDlg's Change |

A package passes when every folder picked in BrowseDlg reached `WIXUI_INSTALLDIR`/`INSTALLDIR`
(read from the verbose log), the package installed to the folder picked last, `app.txt` is
there, msiexec returned 0, and you saw each OK close the dialog.
