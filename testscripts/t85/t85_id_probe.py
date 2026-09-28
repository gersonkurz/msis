"""#85 and #87, build side - ids across major upgrades.

Leg 1, #85 (decisions D26). Up to 3.0.6 Directory and File ids were sequence numbers
(DIR_ID00003, FILE_ID00007), and a folder-permission component's id and GUID followed its
directory's. Since #85 they are derived from where they install. The first build with D26 gives
every directory and file a new id, and every permission component below a root and every
<create-folder> component a new GUID. D26 argues that is harmless under the templates' default
MajorUpgrade, as D23 did for file components; T81 checked that for FILE components only. This
leg covers the rest, with the shapes where a mistake would cost customer data:

  v1  1.0.0, msis at OLD (sequence numbers)
  v2  1.0.1, msis with D26
  v3  1.0.2, msis with D26, plus a __pycache__ folder, an empty folder and a file sorted first,
      and another <create-folder> - the additions that renumbered everything before D26

A nested INSTALLDIR (MsisProbe85\\IdProbe), so APPDATADIR falls back to it and both trees have a
directory above their root; permission components on every named folder (the default); two
<create-folder>; and APPDATADIR\\data shared by two features, so its permission component
belongs to both. The VM side seeds runtime data the MSI never installed and checks it survives.

Leg 2, #87. Feature ids are positions (FEATURE_00000, ...), and MajorUpgrade's MigrateFeatures
carries each installed feature's state to the feature with the SAME id in the new package. So
inserting a feature is expected to hand a customer's choices to the wrong features:

  f1        1.0.0  Main, Debug (off by default)
  f2append  1.0.1  Main, Debug, NewTool (off)   - control: no id moves
  f2insert  1.0.1  Main, NewTool (off), Debug   - Debug's id becomes NewTool's

This half needs no elevation and installs nothing:

    uv run t85_id_probe.py        # build both msis versions + the packages, stage vm-payload/

Then copy `vm-payload/` to the VM and run what is inside it (see vm/README.md).
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
OLD = "3822972"  # the last commit before #85: 3.0.6 plus #84, sequence-number ids

TOP = "MsisProbe85"  # the one folder, under Program Files and under ProgramData, the probe owns
IDS_PRODUCT, IDS_CODE = "MSIS Id Probe 85", "{8E5C1A47-3B2D-4F90-A6E1-5D7C2B9F0A85}"
FEAT_PRODUCT, FEAT_CODE = "MSIS Feature Probe 87", "{3D9A6F12-7C4B-4E58-B1A0-9E2F6D8C4B87}"
PF, PD = f"PF:{TOP}\\IdProbe", f"PD:{TOP}\\IdProbe"  # INSTALLDIR and APPDATADIR, VM-side keys

# Leg 1 payload: staged source path -> {version: content}; None = not in that version.
IDS_FILES = {
    r"I\app.exe": {"1.0.0": "app 1.0.0 - never executed\n", "1.0.1": "app 1.0.1\n", "1.0.2": "app 1.0.2\n"},
    r"I\conf\settings.ini": {"1.0.0": "level=1\n", "1.0.1": "level=2\n", "1.0.2": "level=2\n"},
    r"I\lib\x.dll": {v: "x - not a real dll\n" for v in ("1.0.0", "1.0.1", "1.0.2")},
    r"I\lib\sub\y.dll": {v: "y - not a real dll\n" for v in ("1.0.0", "1.0.1", "1.0.2")},
    r"I\aaa.txt": {"1.0.0": None, "1.0.1": None, "1.0.2": "sorted first\n"},
    r"I\__pycache__\m.pyc": {"1.0.0": None, "1.0.1": None, "1.0.2": "bytecode\n"},
    r"data\seed.db": {v: "seed shipped by the package\n" for v in ("1.0.0", "1.0.1", "1.0.2")},
    r"extra\extra.txt": {v: "the Extra feature's file\n" for v in ("1.0.0", "1.0.1", "1.0.2")},
}
# Where each staged source lands, as a VM-side key.
IDS_TARGET = {"I\\": PF + "\\", "data\\": PD + "\\data\\", "extra\\": PD + "\\data\\"}
IDS_EMPTY = {"1.0.2": [r"I\_empty"]}  # staged empty folders, walked like any other
IDS_CREATE = {"1.0.0": ["logs", "cache"], "1.0.1": ["logs", "cache"], "1.0.2": ["logs", "cache", "archive"]}

FEATURES = {
    "f1": ("1.0.0", [("Main", True, "main.txt"), ("Debug", False, "debug.txt")]),
    "f2append": ("1.0.1", [("Main", True, "main.txt"), ("Debug", False, "debug.txt"), ("NewTool", False, "newtool.txt")]),
    "f2insert": ("1.0.1", [("Main", True, "main.txt"), ("NewTool", False, "newtool.txt"), ("Debug", False, "debug.txt")]),
}


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def old_msis(work: Path) -> tuple[Path, Path]:
    """msis and its templates at OLD, built from that commit, so the probe is reproducible."""
    exe, templates = work / "msis-old.exe", work / "tpl-old"
    tree = Path(tempfile.mkdtemp(prefix="t85-"))
    shutil.rmtree(tree)
    if run(["git", "worktree", "add", "-q", "--detach", str(tree), OLD], REPO).returncode != 0:
        raise SystemExit(f"cannot check out {OLD}")
    try:
        if run(["go", "build", "-o", str(exe), "./cmd/msis"], tree).returncode != 0:
            raise SystemExit(f"cannot build msis {OLD}")
    finally:
        run(["git", "worktree", "remove", "--force", str(tree)], REPO)
    archive = work / "tpl-old.tar"
    with archive.open("wb") as out:
        subprocess.run(["git", "archive", OLD, "templates"], cwd=REPO, stdout=out, check=True)
    with tarfile.open(archive) as tar:
        tar.extractall(templates, filter="data")
    archive.unlink()
    return exe, templates / "templates"


def build(exe: Path, templates: Path, folder: Path, name: str, script: str) -> tuple[Path, ET.Element]:
    """Builds folder/name.msis; returns the MSI and the parsed retained WXS."""
    (folder / f"{name}.msis").write_text(script, encoding="utf-8", newline="\r\n")
    proc = run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{templates}", f"{name}.msis"], folder)
    (HERE / f"build-{name}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
    if proc.returncode != 0:
        raise SystemExit(f"{name} did not build")
    wxs = (folder / f"{name}.wxs").read_text(encoding="utf-8")
    # The probe runs on the owner's main VM: nothing that deletes recursively may be in it.
    for banned in ("msi-simplica", "RemoveFolderEx", "REMOVE_FOLDERS", "RemoveRegistryKey"):
        if banned in wxs:
            raise SystemExit(f"{name}: the WXS contains {banned}; the probe must not remove anything recursively")
    return folder / f"{name}.msi", ET.fromstring(wxs)


def local(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def identities(root: ET.Element) -> dict[str, str]:
    """Every Directory, File and component of the WXS named by its Name path (the root's
    directories by their Id) -> its Id, plus Guid for components. Also refuses a permission
    component on an unnamed directory: that would grant Users full control of a bare system
    folder such as C:\\ProgramData (#55)."""
    out: dict[str, str] = {}

    def walk(el: ET.Element, path: str) -> None:
        for child in el:
            kind = local(child.tag)
            if kind == "Directory" or kind == "StandardDirectory":
                name = child.get("Name") or child.get("Id")
                here = f"{path}\\{name}".lower()
                if kind == "Directory":
                    out[f"dir {here}"] = child.get("Id")
                walk(child, here)
            elif kind == "Component":
                files = [f for f in child if local(f.tag) == "File"]
                perm = any(local(g.tag) == "PermissionEx" for g in child.iter())
                if perm and local(el.tag) == "Directory" and not el.get("Name"):
                    raise SystemExit(f"a permission component sits in the unnamed directory {path}")
                for f in files:
                    out[f"file {path}\\{f.get('Name').lower()}"] = f.get("Id")
                what = "file " + files[0].get("Name").lower() if files else ("permission" if perm else "create-folder")
                out[f"component {path} {what}"] = f"{child.get('Id')} {child.get('Guid')}"
            else:
                walk(child, path)  # Package, Fragment, ...: not part of the path

    walk(root, "")
    return out


def feature_ids(root: ET.Element) -> dict[str, str]:
    return {f.get("Title"): f.get("Id") for f in root.iter() if local(f.tag) == "Feature"}


def ids_package(exe: Path, templates: Path, work: Path, version: str) -> tuple[Path, ET.Element]:
    folder = work / f"ids-{version}"
    for rel, contents in IDS_FILES.items():
        if contents[version] is not None:
            (folder / rel).parent.mkdir(parents=True, exist_ok=True)
            (folder / rel).write_text(contents[version], encoding="utf-8")
    for rel in IDS_EMPTY.get(version, []):
        (folder / rel).mkdir(parents=True)
    creates = "".join(f'    <create-folder target="[APPDATADIR]{c}"/>\n' for c in IDS_CREATE[version])
    script = f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{IDS_PRODUCT}"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{IDS_CODE}"/>
  <set name="INSTALLDIR" value="{TOP}\\IdProbe"/>
  <set name="BUILD_TARGET" value="ids-{version}.msi"/>
  <feature name="Main">
    <files source="I" target="[INSTALLDIR]"/>
    <files source="data" target="[APPDATADIR]data"/>
{creates}  </feature>
  <feature name="Extra">
    <files source="extra\\extra.txt" target="[APPDATADIR]data"/>
  </feature>
</setup>
"""
    return build(exe, templates, folder, f"ids-{version}", script)


def ids_expected(version: str) -> dict:
    files = {}
    for rel, contents in IDS_FILES.items():
        if contents[version] is None:
            continue
        prefix = next(p for p in IDS_TARGET if rel.startswith(p))
        files[IDS_TARGET[prefix] + rel.removeprefix(prefix)] = contents[version]
    dirs = [f"{PD}\\{c}" for c in IDS_CREATE[version]]
    dirs += [PF + "\\" + rel.removeprefix("I\\") for rel in IDS_EMPTY.get(version, [])]
    return {"files": files, "dirs": dirs}


def feat_package(exe: Path, work: Path, name: str) -> tuple[Path, dict[str, str]]:
    version, feats = FEATURES[name]
    folder = work / name
    folder.mkdir(parents=True)
    body = ""
    for title, enabled, file in feats:
        (folder / file).write_text(f"{title}'s file\n", encoding="utf-8")
        body += f'  <feature name="{title}"{"" if enabled else " enabled=\"false\""}>\n'
        body += f'    <files source="{file}" target="[INSTALLDIR]"/>\n  </feature>\n'
    script = f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{FEAT_PRODUCT}"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{FEAT_CODE}"/>
  <set name="INSTALLDIR" value="{TOP}\\FeatureProbe"/>
  <set name="BUILD_TARGET" value="{name}.msi"/>
{body}</setup>
"""
    msi, wxs = build(exe, REPO / "templates", folder, name, script)
    identities(wxs)  # the unnamed-directory permission check
    return msi, feature_ids(wxs)


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exe_old, tpl_old = old_msis(work)
    exe_new = work / "msis-new.exe"
    if run(["go", "build", "-o", str(exe_new), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis from the working tree")

    # Leg 1.
    msis = {}
    ids = {}
    for version, (exe, tpl) in {"1.0.0": (exe_old, tpl_old), "1.0.1": (exe_new, REPO / "templates"),
                                "1.0.2": (exe_new, REPO / "templates")}.items():
        msis[version], wxs = ids_package(exe, tpl, work, version)
        ids[version] = identities(wxs)
        extra = feature_ids(wxs).get("Extra")
    v1, v2, v3 = ids["1.0.0"], ids["1.0.1"], ids["1.0.2"]

    # The scenario must really cross the change: v1 numbered, v2 derived, and the permission
    # components below the roots with new GUIDs.
    numbered = [k for k, v in v1.items() if k.startswith(("dir ", "file ")) and re.fullmatch(r"(DIR|FILE)_ID\d{5}", v)]
    derived = [k for k, v in v2.items() if k.startswith(("dir ", "file ")) and re.fullmatch(r"(DIR|FILE)_[0-9a-f]{16}(_\d+)?", v)]
    moved = [k for k in v1 if k.endswith(" permission") and k in v2 and v1[k] != v2[k]]
    logger.info("v1: {} numbered ids; v2: {} derived ids; permission components with a new GUID: {}",
                len(numbered), len(derived), len(moved))
    if not numbered or not derived or not moved:
        logger.error("v1 and v2 do not cross the id change")
        return 1
    # And D26 must hold between two D26 releases: v3's additions renumber nothing of v2's.
    changed = {k: (v, v3.get(k)) for k, v in v2.items() if v3.get(k) != v}
    if changed:
        logger.error("v2 -> v3 changed {} identities, D26 says none: {}", len(changed), changed)
        return 1
    logger.info("v2 -> v3: all {} identities of v2 unchanged, {} added", len(v2), len(v3) - len(v2))

    # Leg 2.
    feat = {name: feat_package(exe_new, work, name) for name in FEATURES}
    f1, fa, fi = (feat[n][1] for n in ("f1", "f2append", "f2insert"))
    if not (f1["Debug"] == fa["Debug"] == fi["NewTool"] != fi["Debug"]):
        logger.error("the feature ids are not the shape leg 2 tests: {}", {n: f[1] for n, f in feat.items()})
        return 1
    logger.info("f1 Debug={}; f2insert NewTool={}, Debug={}", f1["Debug"], fi["NewTool"], fi["Debug"])

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    for version, msi in msis.items():
        shutil.copy2(msi, payload / f"ids-{version}.msi")
    for name, (msi, _) in feat.items():
        shutil.copy2(msi, payload / f"{name}.msi")
    manifest = {
        "top": TOP,
        "ids": {"product": IDS_PRODUCT, "extra_feature": extra,
                "versions": {v: ids_expected(v) for v in msis},
                # What the probe writes itself after installing v1: data the MSI never installed.
                "runtime": {f"{PF}\\conf\\user.ini": "the customer's settings\n",
                            f"{PD}\\logs\\runtime.log": "written by the running application\n",
                            f"{PD}\\data\\customer.db": "the customer's database\n"},
                # Every folder a permission component grants on, in every version.
                "acl_dirs": [f"PF:{TOP}", PF, f"{PF}\\conf", f"{PF}\\lib", f"{PF}\\lib\\sub",
                             f"PD:{TOP}", PD, f"{PD}\\data", f"{PD}\\logs", f"{PD}\\cache"]},
        "features": {"product": FEAT_PRODUCT, "dir": f"PF:{TOP}\\FeatureProbe",
                     "ids": {n: f[1] for n, f in feat.items()},
                     "files": {t: file for _, feats in FEATURES.values() for t, _, file in feats}},
    }
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t85_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {} - copy it to the VM", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
