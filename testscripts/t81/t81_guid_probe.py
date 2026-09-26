"""#81, build side - the one-time component GUID change, across a major upgrade.

Up to 3.0.5 a file component's GUID hashed its source path as the build saw it; since #81 (D23)
it is the product plus where the file installs. Every file component of a package built with
3.0.6 therefore has a different GUID from the same package built with 3.0.5. D23 argues that is
harmless: msis's templates use WiX's default MajorUpgrade, which removes the old version
completely before the new one installs, so no component is shared. This probe checks it on a
machine:

  v1  1.0.0, built by msis at OLD (the source-path scheme), sources named by absolute path
  v2  1.0.1, built by msis HEAD (the D23 scheme) from a DIFFERENT folder - NG1's CI shape

Both install the same destinations. Two files change content between the versions, one does not,
and v2 adds one. The build side refuses to stage unless every component GUID of v1 differs from
v2's, so the scenario really crosses the change.

This half needs no elevation and installs nothing:

    uv run t81_guid_probe.py        # build both msis versions + the packages, stage vm-payload/

Then copy `vm-payload/` to a snapshotted VM and run what is inside it.
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
OLD = "e785425"  # the last commit before #81: file GUIDs from the source path

INSTALLDIR = "GuidProbe81"
UPGRADE_CODE = "{5B7E3A92-1C4D-4E86-9F20-8D3A6C1E7B54}"
# destination below INSTALLDIR -> content per version (None: not in that version)
PAYLOAD = {
    r"app.exe": {"1.0.0": "app 1.0.0 - never executed\n", "1.0.1": "app 1.0.1 - never executed\n"},
    r"conf\settings.ini": {"1.0.0": "[main]\nlevel=1\n", "1.0.1": "[main]\nlevel=2\n"},
    r"conf\static.txt": {"1.0.0": "unchanged between versions\n", "1.0.1": "unchanged between versions\n"},
    r"conf\added.txt": {"1.0.0": None, "1.0.1": "new in 1.0.1\n"},
}


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def old_msis(work: Path) -> tuple[Path, Path]:
    """msis and its templates at OLD, built from that commit, so the probe is reproducible."""
    exe, templates = work / "msis-old.exe", work / "tpl-old"
    if not exe.exists():
        tree = Path(tempfile.mkdtemp(prefix="t81-"))
        shutil.rmtree(tree)
        if run(["git", "worktree", "add", "-q", "--detach", str(tree), OLD], REPO).returncode != 0:
            raise SystemExit(f"cannot check out {OLD}")
        try:
            if run(["go", "build", "-o", str(exe), "./cmd/msis"], tree).returncode != 0:
                raise SystemExit(f"cannot build msis {OLD}")
        finally:
            run(["git", "worktree", "remove", "--force", str(tree)], REPO)
    if not templates.exists():
        archive = work / "tpl-old.tar"
        with archive.open("wb") as out:
            subprocess.run(["git", "archive", OLD, "templates"], cwd=REPO, stdout=out, check=True)
        with tarfile.open(archive) as tar:
            tar.extractall(templates, filter="data")
        archive.unlink()
    return exe, templates / "templates"


def package(exe: Path, templates: Path, folder: Path, version: str) -> tuple[Path, dict[str, str]]:
    """Builds one version from its own folder, sources by absolute path; returns the MSI and
    the component GUIDs of the retained WXS by component id."""
    out = folder / "out"
    for rel, contents in PAYLOAD.items():
        if contents[version] is not None:
            (out / rel).parent.mkdir(parents=True, exist_ok=True)
            (out / rel).write_text(contents[version], encoding="utf-8")
    script = folder / "probe.msis"
    script.write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="GUID Probe 81"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{UPGRADE_CODE}"/>
  <set name="INSTALLDIR" value="{INSTALLDIR}"/>
  <set name="BUILD_TARGET" value="{folder / ('probe-' + version + '.msi')}"/>
  <feature name="Main">
    <files source="{out / 'app.exe'}" target="[INSTALLDIR]"/>
    <files source="{out / 'conf'}" target="[INSTALLDIR]conf"/>
  </feature>
</setup>
""", encoding="utf-8", newline="\r\n")
    cmd = [str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{templates}", str(script)]
    proc = run(cmd, folder)
    (HERE / f"build-{version}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
    if proc.returncode != 0:
        raise SystemExit(f"{version} did not build")
    wxs = (folder / f"probe-{version}.wxs").read_text(encoding="utf-8")
    # File components only: the non-file ones (permissions, ...) were product-scoped already and
    # keep their GUIDs, as D23 says.
    guids = dict(re.findall(r"<Component Id='(CID_[0-9a-f_]+)' Guid='([0-9a-f-]+)'[^>]*>\s*<File ", wxs))
    return folder / f"probe-{version}.msi", guids


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work / "ci", ignore_errors=True)
    shutil.rmtree(work / "local", ignore_errors=True)
    work.mkdir(exist_ok=True)
    exe_old, tpl_old = old_msis(work)
    exe_new = work / "msis-new.exe"
    if run(["go", "build", "-o", str(exe_new), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis HEAD")

    (work / "ci" / "ng1-1.0.0").mkdir(parents=True)
    (work / "local" / "Downloads").mkdir(parents=True)
    v1, old_guids = package(exe_old, tpl_old, work / "ci" / "ng1-1.0.0", "1.0.0")
    v2, new_guids = package(exe_new, REPO / "templates", work / "local" / "Downloads", "1.0.1")
    shared = set(old_guids.values()) & set(new_guids.values())
    logger.info("v1 {} file components, v2 {}; file-component GUIDs in common: {}", len(old_guids), len(new_guids), len(shared))
    if not old_guids or not new_guids or shared:
        logger.error("the two packages must share no component GUID, or the probe does not cross the change")
        return 1

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    shutil.copy2(v1, payload / "v1.msi")
    shutil.copy2(v2, payload / "v2.msi")
    manifest = {"installdir": INSTALLDIR, "product": "GUID Probe 81",
                "files": {rel: {v: c for v, c in contents.items()} for rel, contents in PAYLOAD.items()}}
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t81_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {} - copy it to the VM", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
