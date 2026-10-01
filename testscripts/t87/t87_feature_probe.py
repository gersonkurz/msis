"""#87, build side - feature choices across upgrades, with and without explicit feature ids (D28).

MajorUpgrade's MigrateFeatures hands each installed feature's state to the feature with the same
id in the new package. Positional ids (FEATURE_00000, ...) move when a feature is inserted, so the
customer's choices go to the wrong features (T85 reproduced it). D28 lets a feature carry an
explicit id, and positional numbers count only the features without one.

  v1     1.0.0  Main (on), Docs (on), Debug (off)                         - no ids
  v2id   1.0.1  NewOn (id, on) > NewOnPart (id, on), Main, Docs,
                NewTool (id, off), Debug                                  - inserted WITH ids
  v2pos  1.0.1  Main, Docs, NewTool (off, no id), Debug                   - the control: inserted without
  v3     1.0.2  Debug (id FEATURE_00002), Main (id FEATURE_00000), NewOn > NewOnPart, NewTool
                - every feature frozen to its shipped id, then reordered, and Docs removed

Every feature installs one file whose content names the feature and the package version, so a
file left behind by an older version cannot pass for the new one.

This half needs no elevation and installs nothing:

    uv run t87_feature_probe.py

Then run what is in `vm-payload/` (see vm/README.md).
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
TOP = "MsisProbe87"
PRODUCT, CODE = "MSIS Feature Probe 87", "{4B8D2E71-9C3A-4F65-A1D0-7E2B5C9F3A87}"


def f(title: str, on: bool = True, id: str = "", children: list | None = None) -> dict:
    return {"title": title, "on": on, "id": id, "children": children or []}


PACKAGES = {
    "v1": ("1.0.0", [f("Main"), f("Docs"), f("Debug", on=False)]),
    "v2id": ("1.0.1", [f("NewOn", id="NewOn", children=[f("NewOnPart", id="NewOnPart")]), f("Main"), f("Docs"),
                       f("NewTool", on=False, id="NewTool"), f("Debug", on=False)]),
    "v2pos": ("1.0.1", [f("Main"), f("Docs"), f("NewTool", on=False), f("Debug", on=False)]),
    "v3": ("1.0.2", [f("Debug", on=False, id="FEATURE_00002"), f("Main", id="FEATURE_00000"),
                     f("NewOn", id="NewOn", children=[f("NewOnPart", id="NewOnPart")]), f("NewTool", on=False, id="NewTool")]),
}

# The ids the scheme must produce: the shipped ones unchanged where ids were given, and the
# control's shift where they were not.
EXPECTED_IDS = {
    "v1": {"Main": "FEATURE_00000", "Docs": "FEATURE_00001", "Debug": "FEATURE_00002"},
    "v2id": {"Main": "FEATURE_00000", "Docs": "FEATURE_00001", "Debug": "FEATURE_00002",
             "NewOn": "NewOn", "NewOnPart": "NewOnPart", "NewTool": "NewTool"},
    "v2pos": {"Main": "FEATURE_00000", "Docs": "FEATURE_00001", "NewTool": "FEATURE_00002", "Debug": "FEATURE_00003"},
    "v3": {"Main": "FEATURE_00000", "Debug": "FEATURE_00002", "NewOn": "NewOn", "NewOnPart": "NewOnPart", "NewTool": "NewTool"},
}


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def features_xml(features: list[dict], folder: Path, version: str, indent: str = "  ") -> str:
    out = ""
    for feat in features:
        name = feat["title"].lower() + ".txt"
        (folder / name).write_text(f"{feat['title']} {version}\n", encoding="utf-8")
        attrs = f' id="{feat["id"]}"' if feat["id"] else ""
        attrs += "" if feat["on"] else ' enabled="false"'
        out += f'{indent}<feature name="{feat["title"]}"{attrs}>\n'
        out += f'{indent}  <files source="{name}" target="[INSTALLDIR]"/>\n'
        out += features_xml(feat["children"], folder, version, indent + "  ")
        out += f"{indent}</feature>\n"
    return out


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exe = work / "msis.exe"
    if run(["go", "build", "-o", str(exe), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis from the working tree")

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    manifest = {"top": TOP, "product": PRODUCT, "dir": f"PF:{TOP}\\App",
                "registry_key": "Software\\msis\\Packages\\" + CODE.strip("{}").upper(),
                "runtime": [f"PF:{TOP}\\App\\data\\user.dat"], "packages": {}}
    for name, (version, features) in PACKAGES.items():
        folder = work / name
        folder.mkdir()
        body = features_xml(features, folder, version)
        (folder / f"{name}.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{PRODUCT}"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{CODE}"/>
  <set name="INSTALLDIR" value="{TOP}\\App"/>
  <set name="BUILD_TARGET" value="{name}.msi"/>
{body}</setup>
""", encoding="utf-8", newline="\r\n")
        proc = run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", f"{name}.msis"], folder)
        (HERE / f"build-{name}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
        if proc.returncode != 0:
            raise SystemExit(f"{name} did not build")
        wxs = (folder / f"{name}.wxs").read_text(encoding="utf-8")
        for banned in ("msi-simplica", "RemoveFolderEx", "REMOVE_FOLDERS", "RemoveRegistryKey"):
            if banned in wxs:
                raise SystemExit(f"{name}: the WXS contains {banned}")
        root = ET.fromstring(wxs)
        ids = {el.get("Title"): el.get("Id") for el in root.iter() if el.tag.endswith("}Feature")}
        ids = {t: i for t, i in ids.items() if t in EXPECTED_IDS[name]}
        if ids != EXPECTED_IDS[name]:
            logger.error("{}: feature ids {} , want {}", name, ids, EXPECTED_IDS[name])
            return 1
        shutil.copy2(folder / f"{name}.msi", payload / f"{name}.msi")
        manifest["packages"][name] = {"version": version, "ids": ids}
        logger.info("built {} ({}): {}", name, version, ids)
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t87_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {}", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
