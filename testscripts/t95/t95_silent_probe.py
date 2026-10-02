"""#95, build side - silent bundles install and uninstall (D31).

Until 3.0.7 msis's silent bundle template gave WixStdBA Theme="none", and every silent bundle
failed at BA creation (0x80070490). This stages two silent bundles built from the working tree:

  explicit   <setup silent="yes"><bundle><msi source="app.msi"/></bundle>, chaining an x64 MSI
             built from the regular x64 template, which has a UI of its own
  auto       <setup silent="yes"> with <requires type="vcredist" version="2022"> - the auto-bundle
             wrapper, chaining the real, pinned VC++ 2022 x64 redistributable before its MSI

On the VM all VC++ runtimes are present, so the probe asserts the redistributable is detected
Present and never run. Each MSI installs one file whose content names its bundle.

This half needs no elevation and installs nothing (it may download the pinned redistributable
into msis's prerequisite cache):

    uv run t95_silent_probe.py
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
TOP = "MsisProbe95"
BUNDLES = {  # name -> (product, upgrade code of the MSI, upgrade code of the bundle)
    "explicit": ("MSIS Silent Probe 95 Explicit", "{1F6C3A82-7D4E-4B19-9E05-2A8D6C1B3F95}", "{9A2E5D17-4C8B-4F63-B1A0-6D3F8E2C5A95}"),
    "auto": ("MSIS Silent Probe 95 Auto", "{4D8B1E63-2A7C-4F95-8C16-5B9E3A0D7C95}", None),
}


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def msis(exe: Path, folder: Path, script: str, name: str) -> str:
    (folder / f"{name}.msis").write_text(script, encoding="utf-8", newline="\r\n")
    proc = run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", f"{name}.msis"], folder)
    (HERE / f"build-{name}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
    if proc.returncode != 0:
        raise SystemExit(f"{name} did not build")
    return proc.stdout


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exe = work / "msis.exe"
    if run(["go", "build", "-o", str(exe), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis")

    def setvars(product: str, code: str, folder: str, target: str) -> str:
        return (f'  <set name="PRODUCT_NAME" value="{product}"/>\n  <set name="PRODUCT_VERSION" value="1.0.0"/>\n'
                f'  <set name="MANUFACTURER" value="Probe Co"/>\n  <set name="UPGRADE_CODE" value="{code}"/>\n'
                f'  <set name="INSTALLDIR" value="{TOP}\\{folder}"/>\n  <set name="PLATFORM" value="x64"/>\n'
                f'  <set name="BUILD_TARGET" value="{target}"/>\n')

    # explicit: an x64 MSI from the regular template (with UI), wrapped in a silent <bundle>.
    product, code, bundle_code = BUNDLES["explicit"]
    folder = work / "explicit"
    folder.mkdir()
    (folder / "app.txt").write_text("installed by the explicit silent bundle\n", encoding="utf-8")
    msis(exe, folder, f"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<setup>\n{setvars(product, code, 'Explicit', 'app.msi')}"
         f'  <feature name="Main"><files source="app.txt" target="[INSTALLDIR]"/></feature>\n</setup>\n', "app")
    msis(exe, folder, f"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<setup silent=\"yes\">\n"
         f'  <set name="PRODUCT_NAME" value="{product} Bundle"/>\n  <set name="PRODUCT_VERSION" value="1.0.0"/>\n'
         f'  <set name="MANUFACTURER" value="Probe Co"/>\n  <set name="UPGRADE_CODE" value="{bundle_code}"/>\n'
         f'  <set name="BUILD_TARGET" value="explicit.exe"/>\n  <bundle>\n    <msi source="app.msi"/>\n  </bundle>\n</setup>\n', "explicit")

    # auto: <setup silent="yes"> + <requires vcredist 2022>, the auto-bundle path.
    product, code, _ = BUNDLES["auto"]
    folder = work / "auto"
    folder.mkdir()
    (folder / "app.txt").write_text("installed by the auto silent bundle\n", encoding="utf-8")
    msis(exe, folder, f"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<setup silent=\"yes\">\n{setvars(product, code, 'Auto', 'auto.msi')}"
         f'  <requires type="vcredist" version="2022"/>\n'
         f'  <feature name="Main"><files source="app.txt" target="[INSTALLDIR]"/></feature>\n</setup>\n', "auto")

    checks = {"explicit": work / "explicit" / "explicit-bundle.wxs", "auto": work / "auto" / "auto-bundle.wxs"}
    for name, wxs_path in checks.items():
        wxs = wxs_path.read_text(encoding="utf-8")
        if 'Theme="hyperlinkLicense"' not in wxs or "bal:DisplayInternalUICondition='1'" in wxs:
            logger.error("{}: the bundle WXS is not the fixed silent one", name)
            return 1
        for banned in ("msi-simplica", "RemoveFolderEx", "REMOVE_FOLDERS"):
            if banned in wxs:
                raise SystemExit(f"{name}: the bundle WXS contains {banned}")

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir(exist_ok=True)
    shutil.copy2(work / "explicit" / "explicit.exe", payload / "explicit.exe")
    shutil.copy2(work / "auto" / "auto.exe", payload / "auto.exe")
    manifest = {"top": TOP, "bundles": {
        "explicit": {"exe": "explicit.exe", "bundle_product": BUNDLES["explicit"][0] + " Bundle", "msi_product": BUNDLES["explicit"][0],
                     "file": f"PF:{TOP}\\Explicit\\app.txt", "content": "installed by the explicit silent bundle\n", "prerequisite": None},
        "auto": {"exe": "auto.exe", "bundle_product": BUNDLES["auto"][0], "msi_product": BUNDLES["auto"][0],
                 "file": f"PF:{TOP}\\Auto\\app.txt", "content": "installed by the auto silent bundle\n", "prerequisite": "Prereq_vcredist_2022"}},
        "guard_keys": ["Software\\msis\\Packages\\" + BUNDLES[n][1].strip("{}").upper() for n in BUNDLES]}
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t95_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {}", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
