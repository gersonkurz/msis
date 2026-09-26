"""#76, build side - the first upgrade from a package built before the fix.

<remove-on-uninstall folder="[INSTALLDIR]"/> (the Poste Italiane 4.2 scripts use exactly this)
deleted the folder on a major upgrade as well as on an uninstall, up to msis 3.0.5. 3.0.6 runs
it on a real uninstall only (D21). But an upgrade removes the OLD version with the old package's
own tables, so the first upgrade from a 3.0.5-built package to a 3.0.6-built one is predicted
to delete the folder one last time. This probe checks the prediction and the fix:

  v1  1.0.0, built by msis v3.0.5 (the old condition)
  v2  1.0.1, built by msis HEAD   (D21)
  v3  1.0.2, built by msis HEAD

Same product, x86, one feature with a payload file and the element, as in the PI scripts.

This half needs no elevation and installs nothing:

    uv run t76_upgrade_probe.py        # build both msis versions + the packages, stage vm-payload/

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

HERE = Path(__file__).resolve().parents[0]
REPO = HERE.parents[1]
OLD = "v3.0.5"

PRODUCT = "Upgrade Probe 76"
INSTALLDIR = "UpgradeProbe76"
UPGRADE_CODE = "{2D6B8F43-9A1C-4E57-B803-6C4E2A9D7F15}"
VERSIONS = {"v1": ("1.0.0", "old"), "v2": ("1.0.1", "new"), "v3": ("1.0.2", "new")}


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def old_msis(work: Path) -> tuple[Path, Path]:
    """msis and its templates at OLD, built from the tag, so the probe is reproducible."""
    exe, templates = work / "msis-old.exe", work / "tpl-old"
    if not exe.exists():
        tree = Path(tempfile.mkdtemp(prefix="t76-"))
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


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    work.mkdir(exist_ok=True)
    exe_old, tpl_old = old_msis(work)
    exe_new = work / "msis-new.exe"
    if run(["go", "build", "-o", str(exe_new), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis HEAD")

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    for key, (version, which) in VERSIONS.items():
        folder = work / key
        shutil.rmtree(folder, ignore_errors=True)
        folder.mkdir()
        (folder / "app.txt").write_text(f"app {version}\n", encoding="utf-8")
        (folder / "probe.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{PRODUCT}"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{UPGRADE_CODE}"/>
  <set name="INSTALLDIR" value="{INSTALLDIR}"/>
  <set name="PLATFORM" value="x86"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
    <remove-on-uninstall folder="[INSTALLDIR]"/>
  </feature>
</setup>
""", encoding="utf-8", newline="\r\n")
        exe, tpl = (exe_old, tpl_old) if which == "old" else (exe_new, REPO / "templates")
        proc = run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{tpl}", "probe.msis"], folder)
        (HERE / f"build-{key}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
        if proc.returncode != 0:
            return 1
        wxs = next(folder.glob("*.wxs")).read_text(encoding="utf-8")
        # The condition under test, read out of what each msis generated.
        fixed = "UPGRADINGPRODUCTCODE" in wxs
        if fixed != (which == "new"):
            logger.error("{}: the removal is {}conditioned on a real uninstall; expected the {} behaviour",
                         key, "" if fixed else "NOT ", which)
            return 1
        msi = next(folder.glob("*.msi"))
        shutil.copy2(msi, payload / f"{key}.msi")
        logger.info("built {} ({}, msis {})", key, version, OLD if which == "old" else "HEAD")
    (payload / "manifest.json").write_text(json.dumps(
        {"product": PRODUCT, "installdir": INSTALLDIR,
         "versions": {k: v for k, (v, _) in VERSIONS.items()}}, indent=2), encoding="utf-8")
    for name in ("t76_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {} - copy it to the VM", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
