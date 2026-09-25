"""#77, build side - the service-in-another-feature layout, and the one msis now proposes instead.

A <service> in a different feature from the <files> installing its executable made msis install
that file a second time, in a component of the service's feature: two components owning one
file. On the test VM (2026-09-25) removing either feature deleted the executable the other
still needed, with msiexec reporting success. msis now refuses that layout and names two ways
to write it instead; this probe checks both halves:

  1. the #77 layout (chimera's: Complete installs the exe, optional Service registers it) must
     FAIL to build, with the #77 error;
  2. the layout the error proposes - the Service feature installs its own copy at a target of
     its own and registers that - is built and staged for the VM, which changes features and
     checks both copies and the service registration after every step.

The proposed layout's <service> also sets every attribute #78 made msis honour: description,
service-type, error-control and restart, with the display name left to msis-2.x's default. The
VM reads the installed service's configuration back from the registry.

This half needs no elevation and installs nothing:

    uv run t77_service_probe.py        # build msis + the packages, stage vm-payload/

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

PRODUCT = "Service Probe 77"
MANUFACTURER = "Probe Co"
INSTALLDIR = "ServiceProbe77"
EXE = "svcprobe.exe"
SERVICE = "msisSvcProbe77"
# ponytail: the payload is a text file named .exe. The service is registered but never started
# (start-after-install="no"), and the SCM does not look at the binary until a start - what is
# under test is which component owns which file, not what the file does.
EXE_TEXT = "svcprobe payload for #77 - never executed\n"

HEAD = f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{PRODUCT}"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="{MANUFACTURER}"/>
  <set name="UPGRADE_CODE" value="{{3D9A5C71-8E24-4B6F-A013-7C5E2D9B4F86}}"/>
  <set name="INSTALLDIR" value="{INSTALLDIR}"/>
  <feature name="Complete">
    <files source="{EXE}" target="[INSTALLDIR]"/>
  </feature>
"""
SERVICE_ATTRS = (f'service-name="{SERVICE}" service-display-name="{SERVICE}" start="demand" '
                 'start-after-install="no"')

# The #77 layout: the service names the Complete feature's file.
REJECTED = HEAD + f"""  <feature name="Service" enabled="false">
    <service file-name="{EXE}" {SERVICE_ATTRS}/>
  </feature>
</setup>
"""

# What the #77 error proposes: the Service feature's own copy, at a target of its own. Its
# <service> also carries every attribute #78 fixed - no display name, so msis-2.x's default
# applies - and the VM checks what the installed service's configuration says about each.
DESCRIPTION = "Probe for #77 & #78"
PROPOSED = HEAD + f"""  <feature name="Service" enabled="false">
    <files source="{EXE}" target="[INSTALLDIR]service\\"/>
    <service file-name="[INSTALLDIR]service\\{EXE}" service-name="{SERVICE}" start="demand"
             start-after-install="no" description="Probe for #77 &amp; #78" service-type="shareProcess"
             error-control="critical" restart="yes"/>
  </feature>
</setup>
"""


def msis(msis_exe: Path, name: str) -> subprocess.CompletedProcess:
    cmd = [str(msis_exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", f"{name}.msis"]
    logger.debug("$ {}", " ".join(cmd))
    proc = subprocess.run(cmd, cwd=HERE, capture_output=True, text=True, errors="replace")
    for line in (proc.stdout + proc.stderr).splitlines():
        logger.debug("  {}", line)
    return proc


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skip-build", action="store_true", help="reuse the existing msis.exe")
    args = parser.parse_args()

    logger.remove()
    logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
    logger.add(HERE / "t77-build.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")

    msis_exe = HERE / "msis.exe"
    if not args.skip_build:
        logger.info("building msis from {}", REPO)
        subprocess.run(["go", "build", "-o", str(msis_exe), "./cmd/msis"], cwd=REPO, check=True)

    payload_exe = HERE / EXE
    # Only when it differs: a scanner tends to hold a fresh .exe open, and rewriting it fails.
    if not payload_exe.exists() or payload_exe.read_text(encoding="utf-8") != EXE_TEXT:
        payload_exe.write_text(EXE_TEXT, encoding="utf-8")
    for name, text in (("rejected", REJECTED), ("svc", PROPOSED)):
        (HERE / f"{name}.msis").write_text(text, encoding="utf-8", newline="\r\n")

    rejected = msis(msis_exe, "rejected")
    if rejected.returncode == 0 or "#77" not in rejected.stdout + rejected.stderr:
        logger.error("FAIL the #77 layout built (exit {}) - this msis still installs the exe twice",
                     rejected.returncode)
        return 1
    logger.success("PASS the #77 layout is refused with the #77 error")

    if msis(msis_exe, "svc").returncode != 0:
        logger.error("FAIL the layout the #77 error proposes does not build; see t77-build.log")
        return 1
    logger.success("PASS the proposed layout builds")

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    shutil.copy2(HERE / "svc.msi", payload / "svc.msi")
    for name in ("t77_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    root = f"C:\\Program Files\\{INSTALLDIR}"
    manifest = {
        "msi": "svc.msi",
        "complete": "FEATURE_00000",
        "service": "FEATURE_00001",
        "service_name": SERVICE,
        "app_exe": f"{root}\\{EXE}",
        "service_exe": f"{root}\\service\\{EXE}",
        "exe_text": EXE_TEXT,
        # What the installed service's configuration must say (#78): the registry values under
        # HKLM\SYSTEM\CurrentControlSet\Services\<name>, and restart="yes" as msis-2.x set it -
        # three restart actions, 30 s each (Windows repeats the last for any later failure), the
        # failure count reset after a day without failures.
        "service_config": {
            "DisplayName": SERVICE,
            "Description": DESCRIPTION,
            "Type": 0x20,  # SERVICE_WIN32_SHARE_PROCESS
            "ErrorControl": 3,  # SERVICE_ERROR_CRITICAL
            "Start": 3,  # SERVICE_DEMAND_START
            "FailureActions": {"reset_seconds": 86400, "actions": [[1, 30000]] * 3},  # SC_ACTION_RESTART
        },
    }
    wxs = (HERE / "svc.wxs").read_text(encoding="utf-8", errors="replace")
    for title, fid in (("Complete", manifest["complete"]), ("Service", manifest["service"])):
        if f"<Feature Id='{fid}' Title='{title}'" not in wxs:
            logger.error("FAIL feature {} is not {} in svc.wxs", title, fid)
            return 1
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    logger.success("staged {}: {}", payload, ", ".join(sorted(f.name for f in payload.iterdir())))
    logger.info("copy it to a SNAPSHOTTED VM and run, ELEVATED:  python t77_vm_probe.py")
    return 0


if __name__ == "__main__":
    sys.exit(main())
