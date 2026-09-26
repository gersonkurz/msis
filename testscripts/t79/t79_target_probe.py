"""#79, build side - two <files> installing different sources to ONE target.

msis gives each <files> its own component (so did msis-2.x), so two sources for one target are
two components owning one file. The issue predicts, from what #77 showed for services: removing
one owning feature deletes the file the other still installs, and which copy is on disk is
decided by Windows Installer's overwrite rules, not by the script. Nobody has checked. This
stages two packages for the VM:

  same      one feature installs core\\CONFIG and then ng\\CONFIG to [INSTALLDIR]CONFIG; the two
            CURRENCY.TXT differ (ProAKT 3.6.0.73's setup-ngbt.msis: Files_Core, then Files_NG)
  features  Standard (enabled) installs a\\config.json, Variant (enabled="false") b\\config.json,
            both to [INSTALLDIR] - the issue's repro

This half needs no elevation and installs nothing:

    uv run t79_target_probe.py        # build msis + the packages, stage vm-payload/

Then copy `vm-payload/` to a snapshotted VM and run what is inside it.
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]

HEAD = """<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Target Probe 79 {name}"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{code}"/>
  <set name="INSTALLDIR" value="TargetProbe79{name}"/>
"""

PACKAGES = {
    "same": {
        "code": "{7A3C9E15-2B4D-4F68-8E01-5C7B3D9A1F26}",
        "files": {r"core\CONFIG\CURRENCY.TXT": "core copy\n", r"core\CONFIG\other.txt": "core only\n",
                  r"ng\CONFIG\CURRENCY.TXT": "ng copy - the intended override\n"},
        "body": """  <feature name="Main">
    <files source="core\\CONFIG" target="[INSTALLDIR]CONFIG"/>
    <files source="ng\\CONFIG" target="[INSTALLDIR]CONFIG"/>
  </feature>
""",
        "features": {"Main": "FEATURE_00000"},
    },
    "features": {
        "code": "{7A3C9E15-2B4D-4F68-8E01-5C7B3D9A1F27}",
        "files": {r"a\config.json": '{"variant": "standard"}\n', r"b\config.json": '{"variant": "variant"}\n'},
        "body": """  <feature name="Standard">
    <files source="a\\config.json" target="[INSTALLDIR]"/>
  </feature>
  <feature name="Variant" enabled="false">
    <files source="b\\config.json" target="[INSTALLDIR]"/>
  </feature>
""",
        "features": {"Standard": "FEATURE_00000", "Variant": "FEATURE_00001"},
    },
}


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exe = work / "msis.exe"
    subprocess.run(["go", "build", "-o", str(exe), "./cmd/msis"], cwd=REPO, check=True)

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    manifest = {}
    for name, p in PACKAGES.items():
        folder = work / name
        for rel, content in p["files"].items():
            (folder / rel).parent.mkdir(parents=True, exist_ok=True)
            (folder / rel).write_text(content, encoding="utf-8")
        (folder / f"{name}.msis").write_text(HEAD.format(name=name, code=p["code"]) + p["body"] + "</setup>\n",
                                            encoding="utf-8", newline="\r\n")
        proc = subprocess.run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", f"{name}.msis"],
                              cwd=folder, capture_output=True, text=True, errors="replace")
        (HERE / f"build-{name}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
        if proc.returncode != 0:
            logger.error("{} did not build:\n{}", name, proc.stdout + proc.stderr)
            return 1
        wxs = (folder / f"{name}.wxs").read_text(encoding="utf-8")
        for title, fid in p["features"].items():
            if f"<Feature Id='{fid}' Title='{title}'" not in wxs:
                logger.error("{}: feature {} is not {} in the WXS", name, title, fid)
                return 1
        # The shape under test: one target, two components.
        owners = len(re.findall(r"Name='(?:CURRENCY\.TXT|config\.json)'", wxs))
        if owners != 2:
            logger.error("{}: expected two components for the shared target, found {}", name, owners)
            return 1
        shutil.copy2(folder / f"{name}.msi", payload / f"{name}.msi")
        manifest[name] = {"msi": f"{name}.msi", "product": f"Target Probe 79 {name}",
                          "installdir": f"TargetProbe79{name}", "features": p["features"], "files": p["files"]}
        logger.info("built {}", name)
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t79_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {} - copy it to the VM", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
