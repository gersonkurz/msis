"""#89, build side - installing an older build that differs only in the 4th version field.

Windows Installer compares only three version fields, and the templates allow same-version
upgrades, so 1.2.3.80 over 1.2.3.90 is taken as an upgrade: costing skips the older, lower-
versioned DLL because the installed one is newer, RemoveExistingProducts then deletes the
installed one, and msiexec reports success with the DLL missing. The fix (D27) records each
package's full version under its UpgradeCode and refuses to install over a later one.

  old-90, old-80        msis at OLD (no guard)  - reproduce the report, then try a repair
  new-90, new-80, new-95, new-90b (same version as new-90, other content)  - msis with D27
  items-90, items-80    msis with D27, the payload directly under <setup> (package-items feature)

Each package installs a versioned DLL (FileVersion = the package version, built with the
dotnet SDK), an unversioned text file that never changes and one that does, and an Extra
feature with a Child sub-feature.

This half needs no elevation and installs nothing:

    uv run t89_downgrade_probe.py   # build both msis versions, the DLLs and the packages

Then run what is in `vm-payload/` (see vm/README.md).
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
OLD = "5db5bae"  # the last commit before #89: no downgrade guard

TOP = "MsisProbe89"
PRODUCT, CODE = "MSIS Downgrade Probe 89", "{6C2E9B41-8D3F-4A75-B0E2-3F9A1C7D5E89}"
PACKAGES = {  # name -> (msis, version, changing text, layout)
    "old-90": ("old", "1.2.3.90", "a", "features"),
    "old-80": ("old", "1.2.3.80", "a", "features"),
    "new-90": ("new", "1.2.3.90", "a", "features"),
    "new-90b": ("new", "1.2.3.90", "b", "features"),
    "new-80": ("new", "1.2.3.80", "a", "features"),
    "new-95": ("new", "1.2.3.95", "a", "features"),
    # The payload directly under <setup>, so it installs through the generated package-items
    # feature, and the one authored feature off by default: all a default install installs is
    # that generated feature (review of #89).
    "items-90": ("new", "1.2.3.90", "a", "items"),
    "items-80": ("new", "1.2.3.80", "a", "items"),
}
LAYOUTS = {
    "features": """  <feature name="Main">
    <files source="src" target="[INSTALLDIR]"/>
  </feature>
  <feature name="Extra">
    <feature name="Child">
      <files source="child.txt" target="[INSTALLDIR]extra"/>
    </feature>
  </feature>
""",
    "items": """  <files source="src" target="[INSTALLDIR]"/>
  <feature name="Optional" enabled="false">
    <files source="child.txt" target="[INSTALLDIR]extra"/>
  </feature>
""",
}


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def msis_at(rev: str | None, work: Path) -> Path:
    """msis built at rev (None: the working tree)."""
    exe = work / f"msis-{rev or 'new'}.exe"
    if rev is None:
        if run(["go", "build", "-o", str(exe), "./cmd/msis"], REPO).returncode != 0:
            raise SystemExit("cannot build msis from the working tree")
        return exe
    tree = Path(tempfile.mkdtemp(prefix="t89-"))
    shutil.rmtree(tree)
    if run(["git", "worktree", "add", "-q", "--detach", str(tree), rev], REPO).returncode != 0:
        raise SystemExit(f"cannot check out {rev}")
    try:
        if run(["go", "build", "-o", str(exe), "./cmd/msis"], tree).returncode != 0:
            raise SystemExit(f"cannot build msis {rev}")
    finally:
        run(["git", "worktree", "remove", "--force", str(tree)], REPO)
    return exe


def versioned_dll(work: Path, version: str) -> Path:
    """A class library whose PE version resource says FileVersion = version."""
    proj = work / f"dll-{version}"
    proj.mkdir()
    (proj / "core.csproj").write_text(f"""<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
    <AssemblyName>core</AssemblyName>
    <Version>{version}</Version>
    <FileVersion>{version}</FileVersion>
    <AssemblyVersion>{version}</AssemblyVersion>
  </PropertyGroup>
</Project>
""", encoding="utf-8")
    (proj / "Core.cs").write_text("namespace Probe89; public static class Core { }\n", encoding="utf-8")
    if run(["dotnet", "build", "-c", "Release", "-o", str(proj / "out"), "-nologo"], proj).returncode != 0:
        raise SystemExit(f"cannot build core.dll {version}")
    return proj / "out" / "core.dll"


def file_version(dll: Path) -> str:
    out = subprocess.run(["powershell", "-NoProfile", "-Command", f"(Get-Item -LiteralPath '{dll}').VersionInfo.FileVersion"],
                         capture_output=True, text=True).stdout.strip()
    return out


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exes = {"old": msis_at(OLD, work), "new": msis_at(None, work)}
    dlls = {v: versioned_dll(work, v) for v in sorted({p[1] for p in PACKAGES.values()})}
    for v, dll in dlls.items():
        if file_version(dll) != v:
            logger.error("core.dll {} carries FileVersion {!r}", v, file_version(dll))
            return 1

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir()
    features = {}
    for name, (which, version, text, layout) in PACKAGES.items():
        folder = work / name
        (folder / "src").mkdir(parents=True)
        shutil.copy2(dlls[version], folder / "src" / "core.dll")
        (folder / "src" / "static.txt").write_text("never changes\n", encoding="utf-8")
        (folder / "src" / "changing.txt").write_text(f"content {text}\n", encoding="utf-8")
        (folder / "child.txt").write_text("the child feature's file\n", encoding="utf-8")
        (folder / f"{name}.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{PRODUCT}"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{CODE}"/>
  <set name="INSTALLDIR" value="{TOP}\\App"/>
  <set name="BUILD_TARGET" value="{name}.msi"/>
{LAYOUTS[layout]}</setup>
""", encoding="utf-8", newline="\r\n")
        proc = run([str(exes[which]), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", f"{name}.msis"], folder)
        (HERE / f"build-{name}.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
        if proc.returncode != 0:
            raise SystemExit(f"{name} did not build")
        wxs = (folder / f"{name}.wxs").read_text(encoding="utf-8")
        # The probe runs on the owner's main VM: nothing that deletes recursively.
        for banned in ("msi-simplica", "RemoveFolderEx", "REMOVE_FOLDERS", "RemoveRegistryKey"):
            if banned in wxs:
                raise SystemExit(f"{name}: the WXS contains {banned}")
        guarded = "MSIS_INSTALLED_VERSION" in wxs
        if guarded != (which == "new"):
            raise SystemExit(f"{name}: built by msis {which}, but the guard is {'present' if guarded else 'absent'}")
        root = ET.fromstring(wxs)
        features[name] = {f.get("Title"): f.get("Id") for f in root.iter() if f.tag.endswith("}Feature")}
        shutil.copy2(folder / f"{name}.msi", payload / f"{name}.msi")
        logger.info("built {} ({} msis, {}){}", name, which, version, ", guarded" if guarded else "")

    manifest = {"top": TOP, "product": PRODUCT, "upgrade_code": CODE, "dir": f"PF:{TOP}\\App",
                "registry_key": "Software\\msis\\Packages\\" + CODE.strip("{}").upper(),
                "packages": {n: {"version": v, "changing": f"content {t}\n", "features": features[n]}
                             for n, (_, v, t, _) in PACKAGES.items()}}
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t89_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {}", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
