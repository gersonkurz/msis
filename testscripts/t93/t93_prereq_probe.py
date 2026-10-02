"""#93 and #94, build side - prerequisites are detected (NETFRAMEWORK45) and run per-machine (D32).

Two builds of one explicit bundle (the regular template, run with /quiet), from msis at OLD
(before the fix) and from the working tree:

  NETFX  <prerequisite type="netfx" version="4.8.1" source=...>    a stand-in; detect: NETFRAMEWORK45 >= 533320
  VC     <prerequisite type="vcredist" version="2022" source=...>  a stand-in; detect: the templates' VC++ search
  EPM    <exe per-machine="yes" detect="0">                         always runs; must run elevated
  EPU    <exe detect="0">                                           always runs; must run unelevated
  and a small MSI

Each stand-in is `marker/`, which records that it ran and whether it was elevated. On the VM .NET
4.8.1 (Release 533509) and every VC++ runtime are installed, so with the fix NETFX and VC must be
detected Present and never run. Without it, NETFRAMEWORK45 is never set, so NETFX is detected
Absent and runs (#93). Every stand-in that runs runs elevated, marked per-machine or not: a
package without PerMachine takes the bundle's per-machine scope (#94 was not a bug).

    uv run t93_prereq_probe.py
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
OLD = "86efaa5"  # the last commit before #93/#94
TOP = "MsisProbe93"
CODES = {"old": "{2B7E4D19-6C3A-4F85-9D01-8E5A3C7B1F93}", "new": "{2B7E4D19-6C3A-4F85-9D01-8E5A3C7B1F94}"}
APP_CODE = "{7D1A3F58-9E2C-4B64-A0F7-3C8E6B2D4A93}"
STUBS = ["NETFX", "VC", "EPM", "EPU"]


def run(cmd: list[str], cwd: Path, env: dict | None = None) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace", env=env)
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def msis_at(rev: str | None, work: Path) -> Path:
    exe = work / f"msis-{rev or 'new'}.exe"
    if rev is None:
        if run(["go", "build", "-o", str(exe), "./cmd/msis"], REPO).returncode != 0:
            raise SystemExit("cannot build msis")
        return exe
    tree = Path(tempfile.mkdtemp(prefix="t93-"))
    shutil.rmtree(tree)
    if run(["git", "worktree", "add", "-q", "--detach", str(tree), rev], REPO).returncode != 0:
        raise SystemExit(f"cannot check out {rev}")
    try:
        if run(["go", "build", "-o", str(exe), "./cmd/msis"], tree).returncode != 0:
            raise SystemExit(f"cannot build msis {rev}")
    finally:
        run(["git", "worktree", "remove", "--force", str(tree)], REPO)
    return exe


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exes = {"old": msis_at(OLD, work), "new": msis_at(None, work)}
    env = dict(os.environ, GOOS="windows", GOARCH="amd64")

    app = work / "app"
    app.mkdir()
    (app / "app.txt").write_text("the probe's application\n", encoding="utf-8")
    (app / "app.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="MSIS Prereq Probe 93 App"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{APP_CODE}"/>
  <set name="INSTALLDIR" value="{TOP}\\App"/>
  <set name="BUILD_TARGET" value="app.msi"/>
  <feature name="Main"><files source="app.txt" target="[INSTALLDIR]"/></feature>
</setup>
""", encoding="utf-8", newline="\r\n")
    if run([str(exes["new"]), "/BUILD", f"/TEMPLATEFOLDER:{REPO / 'templates'}", "app.msis"], app).returncode != 0:
        raise SystemExit("the app MSI did not build")

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir(exist_ok=True)
    for which in ("old", "new"):
        folder = work / which
        folder.mkdir()
        shutil.copy2(app / "app.msi", folder / "app.msi")
        for stub in STUBS:
            # One build per package and per bundle, stamped: identical files share a CacheId.
            if run(["go", "build", "-ldflags", f"-X main.id={stub}-{which}", "-o", str(folder / f"{stub.lower()}.exe"),
                    "./testscripts/t93/marker"], REPO, env).returncode != 0:
                raise SystemExit("cannot build the marker")
        (folder / "bundle.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="MSIS Prereq Probe 93 {which}"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{CODES[which]}"/>
  <set name="LICENSE_URL" value="https://example.com/license"/>
  <set name="BUILD_TARGET" value="probe93-{which}.exe"/>
  <bundle>
    <prerequisite type="netfx" version="4.8.1" source="netfx.exe"/>
    <prerequisite type="vcredist" version="2022" source="vc.exe"/>
    <exe id="EPM" source="epm.exe" detect="0" per-machine="yes"/>
    <exe id="EPU" source="epu.exe" detect="0"/>
    <msi source="app.msi"/>
  </bundle>
</setup>
""", encoding="utf-8", newline="\r\n")
        proc = run([str(exes[which]), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", "bundle.msis"], folder)
        (HERE / f"build-{which}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
        if proc.returncode != 0:
            raise SystemExit(f"the {which} bundle did not build")
        wxs = (folder / f"probe93-{which}-bundle.wxs").read_text(encoding="utf-8")
        for banned in ("msi-simplica", "RemoveFolderEx", "REMOVE_FOLDERS"):
            if banned in wxs:
                raise SystemExit(f"{which}: the bundle WXS contains {banned}")
        has_search, machine = "Variable='NETFRAMEWORK45'" in wxs, wxs.count("PerMachine='yes'")
        want = (True, 1) if which == "new" else (False, 1)  # only EPM is marked; the search is #93
        if (has_search, machine) != want:
            logger.error("{}: NETFRAMEWORK45 search {}, {} per-machine packages; want {}", which, has_search, machine, want)
            return 1
        shutil.copy2(folder / f"probe93-{which}.exe", payload / f"probe93-{which}.exe")
        logger.info("built {}: search={}, per-machine packages={}", which, has_search, machine)

    manifest = {"top": TOP, "stubs": STUBS, "app_product": "MSIS Prereq Probe 93 App",
                "bundles": {w: {"exe": f"probe93-{w}.exe", "product": f"MSIS Prereq Probe 93 {w}"} for w in ("old", "new")},
                "guard_key": "Software\\msis\\Packages\\" + APP_CODE.strip("{}").upper()}
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t93_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {}", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
