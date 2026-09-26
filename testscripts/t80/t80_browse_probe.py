"""#80, build side - BrowseDlg's OK button, in the full and the minimal template.

Since WiX 6, BrowseDlg's OK button publishes nothing itself; WiX's stock dialog sets do. msis's
templates carry their own set and did not, so OK did nothing and only Cancel closed the dialog.
The fix adds the two events; TestBrowseDlgOKSetsTheFolderAndCloses checks the compiled
ControlEvent rows. What a table cannot show is the dialog working, so this stages two packages
for a click-through on the VM:

  full     - templates/x64, INSTALL_DIR_DIALOG=True: BrowseDlg opens from InstallDirDlg's Change
             and from CustomizeDlg's Browse
  minimal  - templates/minimal: BrowseDlg opens from InstallDirDlg's Change

This half needs no elevation and installs nothing:

    uv run t80_browse_probe.py        # build msis + the packages, stage vm-payload/

Then copy `vm-payload/` to a snapshotted VM and run what is inside it.
"""

from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]

PACKAGES = {
    # name: (template folder, upgrade code, default INSTALLDIR folder name)
    "full": ("x64", "{3D8A6C21-7E4B-4F90-A1C5-9B2D4E6F8A07}", "BrowseProbe80Full"),
    "minimal": ("minimal", "{3D8A6C21-7E4B-4F90-A1C5-9B2D4E6F8A08}", "BrowseProbe80Minimal"),
}


def script(name: str, upgrade: str, installdir: str) -> str:
    return f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Browse Probe 80 {name}"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{upgrade}"/>
  <set name="INSTALLDIR" value="{installdir}"/>
  <set name="INSTALL_DIR_DIALOG" value="True"/>
  <feature name="Main"><files source="app.txt" target="[INSTALLDIR]"/></feature>
</setup>
"""


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--skip-build", action="store_true", help="reuse the existing msis.exe")
    args = parser.parse_args()
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")

    msis_exe = HERE / "msis.exe"
    if not args.skip_build:
        logger.info("building msis from {}", REPO)
        subprocess.run(["go", "build", "-o", str(msis_exe), "./cmd/msis"], cwd=REPO, check=True)

    (HERE / "app.txt").write_text("Browse probe payload for #80\n", encoding="utf-8")
    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    manifest = {}
    for name, (template, upgrade, installdir) in PACKAGES.items():
        (HERE / f"{name}.msis").write_text(script(name, upgrade, installdir), encoding="utf-8", newline="\r\n")
        cmd = [str(msis_exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}",
               f"/TEMPLATE:{REPO / 'templates' / template / 'template.wxs'}", f"{name}.msis"]
        proc = subprocess.run(cmd, cwd=HERE, capture_output=True, text=True, errors="replace")
        (HERE / f"{name}-build.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
        if proc.returncode != 0:
            logger.error("{} did not build:\n{}", name, proc.stdout + proc.stderr)
            return 1
        wxs = (HERE / f"{name}.wxs").read_text(encoding="utf-8")
        if 'Dialog="BrowseDlg" Control="OK" Event="SetTargetPath"' not in wxs:
            logger.error("{}: the WXS has no BrowseDlg OK SetTargetPath - built from stale templates?", name)
            return 1
        shutil.copy2(HERE / f"{name}.msi", payload / f"{name}.msi")
        manifest[name] = {"msi": f"{name}.msi", "template": template, "default_folder": installdir,
                          "browse_from_customize": template != "minimal"}
        logger.info("built {} ({})", name, template)
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t80_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {} - copy it to the VM", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
