"""T10, build side - what a silent package does with preserve="yes" registry values.

The 3.0.3 regression QA found that 3.0.3's silent x86 template carries no preservation block:
the ProAKT 3.6.0.73 Silent Setup MSI writes its 39 preserved registry values as [PS_RV_n] with
no such property and no AppSearch (#19 fixed the template since). This probe settles what that
does on a machine, and that 3.0.6's silent package keeps the values, with a synthetic package
- no customer binaries, no post-install programs that might rewrite the same values.

Packages, one product (one UpgradeCode), x86 like ProAKT:

  base-303-1.0.0   msis v3.0.3, its templates, the regular template - the installed site
  reg-303-1.0.1    msis v3.0.3, regular template - the control upgrade (T1's mechanism)
  silent-303-1.0.1 msis v3.0.3, silent template - what the field has
  silent-306-1.0.1 msis HEAD, the repo's templates, silent template - the fix

This half needs no elevation and installs nothing (it builds msis v3.0.3 from its tag):

    uv run t10_preserve_probe.py

Then copy `vm-payload/` to a snapshotted VM and run what is inside it.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
TAG = "v3.0.3"
KEY = r"SOFTWARE\MsisProbeT10"

# The preserved values: the kinds ProAKT's proakt_preserved.reg has. Type is what the
# registry must hold: 1 REG_SZ, 4 REG_DWORD.
PRESERVED = [
    {"name": "UI", "type": 1, "default": "HEADLESS", "site": "TOUCH", "reg": '"UI"="HEADLESS"'},
    {"name": "IPPort", "type": 1, "default": "8000", "site": "9100", "reg": '"IPPort"="8000"'},
    {"name": "Limit", "type": 4, "default": 2500000, "site": 1234567, "reg": '"Limit"=dword:002625a0'},
    {"name": "Flag", "type": 4, "default": 1, "site": 0, "reg": '"Flag"=dword:00000001'},
    {"name": "DeviceID", "type": 1, "default": "", "site": "site-device", "reg": '"DeviceID"=""'},
]
CONTROL = {"name": "Control", "type": 1, "default": "always", "reg": '"Control"="always"'}


def reg_file(lines: list[str]) -> bytes:
    text = "Windows Registry Editor Version 5.00\r\n\r\n[HKEY_LOCAL_MACHINE\\" + KEY + "]\r\n" + \
        "".join(l + "\r\n" for l in lines)
    return "﻿".encode("utf-16-le") + text.encode("utf-16-le")


def script(version: str, silent: bool) -> str:
    return f"""<?xml version="1.0" encoding="utf-8"?>
<setup{' silent="true"' if silent else ''}>
  <set name="PRODUCT_NAME" value="Preserve Probe T10"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{{8B4E2A17-6C3D-4F95-A1E0-3D7C9B5F2E46}}"/>
  <set name="INSTALLDIR" value="PreserveProbeT10"/>
  <set name="PLATFORM" value="x86"/>
  <feature name="Main">
    <files source="readme.txt" target="[INSTALLDIR]"/>
    <registry preserve="yes" file="preserved.reg"/>
    <registry preserve="no" file="control.reg"/>
  </feature>
</setup>
"""


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    logger.debug("$ {}", " ".join(str(c) for c in cmd))
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    for line in (proc.stdout + proc.stderr).splitlines():
        logger.debug("  {}", line)
    return proc


def old_msis(work: Path) -> tuple[Path, Path]:
    """msis and its templates at TAG, built from the tag, so the probe is reproducible."""
    exe, templates = work / "msis303.exe", work / "tpl303"
    if not exe.exists():
        tree = Path(tempfile.mkdtemp(prefix="t10-"))
        shutil.rmtree(tree)
        if run(["git", "worktree", "add", "-q", str(tree), TAG], REPO).returncode != 0:
            raise SystemExit(f"cannot check out {TAG}")
        try:
            if run(["go", "build", "-o", str(exe), "./cmd/msis"], tree).returncode != 0:
                raise SystemExit(f"cannot build msis {TAG}")
        finally:
            run(["git", "worktree", "remove", "--force", str(tree)], REPO)
    if not templates.exists():
        archive = work / "tpl303.tar"
        with archive.open("wb") as out:
            subprocess.run(["git", "archive", TAG, "templates"], cwd=REPO, stdout=out, check=True)
        with tarfile.open(archive) as tar:
            tar.extractall(templates, filter="data")
        archive.unlink()
    return exe, templates / "templates"


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
    logger.add(HERE / "t10-build.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")

    work = HERE / "work"
    work.mkdir(exist_ok=True)
    logger.info("msis {} from its tag, msis HEAD from {}", TAG, REPO)
    exe303, tpl303 = old_msis(work)
    exe306 = work / "msis306.exe"
    if run(["go", "build", "-o", str(exe306), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis HEAD")
    tpl306 = REPO / "templates"

    (work / "readme.txt").write_text("T10 preserve probe payload\n", encoding="utf-8")
    (work / "preserved.reg").write_bytes(reg_file([p["reg"] for p in PRESERVED]))
    (work / "control.reg").write_bytes(reg_file([CONTROL["reg"]]))

    packages = {
        "base-303-1.0.0": (exe303, tpl303, "1.0.0", False),
        "reg-303-1.0.1": (exe303, tpl303, "1.0.1", False),
        "silent-303-1.0.1": (exe303, tpl303, "1.0.1", True),
        "silent-306-1.0.1": (exe306, tpl306, "1.0.1", True),
    }
    ok = True
    for name, (exe, tpl, version, silent) in packages.items():
        (work / f"{name}.msis").write_text(script(version, silent), encoding="utf-8", newline="\r\n")
        # BUILD_TARGET defaults to the product name; the /SET keeps the four apart.
        proc = run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{tpl}",
                    f"/SET:BUILD_TARGET={name}.msi", f"{name}.msis"], work)
        if proc.returncode != 0 or not (work / f"{name}.msi").exists():
            logger.error("FAIL {} did not build (exit {}); see t10-build.log", name, proc.returncode)
            ok = False
            continue
        wxs = (work / f"{name}.wxs").read_text(encoding="utf-8-sig", errors="replace")
        referenced = wxs.count("[PS_RV_")
        defined = wxs.count("Property Id='PS_RV_") + wxs.count('Property Id="PS_RV_')
        logger.info("{}: {} registry values reference [PS_RV_n], {} PS_RV properties defined",
                    name, referenced, defined)
        if name == "silent-303-1.0.1" and defined:
            logger.warning("  the QA finding did not reproduce: this 3.0.3 silent package defines them")
        if name != "silent-303-1.0.1" and defined < len(PRESERVED):
            logger.error("FAIL {} defines only {} of {} preserved properties", name, defined, len(PRESERVED))
            ok = False
    if not ok:
        return 1

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    for name in packages:
        shutil.copy2(work / f"{name}.msi", payload / f"{name}.msi")
    for name in ("t10_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    (payload / "manifest.json").write_text(json.dumps(
        {"key": KEY, "view": 32, "preserved": PRESERVED, "control": CONTROL}, indent=2), encoding="utf-8")
    logger.success("staged {}: {}", payload, ", ".join(sorted(f.name for f in payload.iterdir())))
    logger.info("copy it to a SNAPSHOTTED VM and run, ELEVATED:  python t10_vm_probe.py")
    return 0


if __name__ == "__main__":
    sys.exit(main())
