"""T5 + T7, build side - build the cleanup packages and stage them for a test VM.

Two tickets, one mechanism, so one probe. The core - install, seed, repair, uninstall - passed
on 2026-09-19; this also covers the four cases that kept #3 open (todo-testme.md T7):

  - a MAJOR UPGRADE 1.0.0 -> 1.0.1 of a package carrying the cleanup - does it fire mid-upgrade?
  - the target folder already EMPTY at uninstall;
  - the target folder that NEVER EXISTED (the application never ran);
  - the MINIMAL and SILENT-X86 templates, one core pass each.


  T5 / issue #15 - <remove-on-uninstall> written at TOP LEVEL. Until #15 that failed to
                   build, so the components never installed and never ran. Giving them a
                   feature is the moment the deletion became reachable.
  T7 / issue #3  - the same elements inside a <feature>, which is what #3 documents.

Both delete: a recursive folder removal and a registry key removal, of things the installer
does not own. So the probe checks what is removed AND what is left alone - the sentinels
matter as much as the deletions, because the whole value of this over
REMOVE_FOLDERS_ON_UNINSTALL is a narrower blast radius.

This half needs no elevation and installs nothing:

    uv run t5t7_cleanup_probe.py        # build both packages, check the wxs, stage vm-payload/

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

# [APPDATADIR] is NOT C:\ProgramData. The root falls back to the INSTALLDIR value, so
# APPDATADIR is C:\ProgramData\<INSTALLDIR> and "[APPDATADIR]Vendor\logs" resolves to
# C:\ProgramData\<INSTALLDIR>\Vendor\logs. Read out of the built MSI's Directory table:
#
#   APPDATADIR | CommonAppDataFolder | CleanupProbeTop
#
# Both tickets call this out as the trap: a package author expecting C:\ProgramData\Vendor\logs
# would be aiming a recursive delete at a directory that is not the one they meant. The probe
# therefore asserts the remembered path EQUALS the intended absolute path, rather than merely
# looking absolute.
PACKAGES = [
    {
        "key": "top",
        "title": "T5 - cleanup items at top level (issue #15)",
        "product": "Cleanup Probe TopLevel",
        "installdir": "CleanupProbeTop",
        "upgrade_code": "{4A7C1E92-5B3D-4F08-9C61-2E8B7D0A3F51}",
        "registry_target": r"Software\Vendor\CleanupProbeTop",
        "registry_sibling": r"Software\Vendor\CleanupProbeTopOther",
        # Top-level items are gathered into the synthetic feature #15 added.
        "expect_feature": "MSIS_PACKAGE_ITEMS",
        "top_level": True,
    },
    {
        "key": "feat",
        "title": "T7 - cleanup items inside a <feature> (issue #3)",
        "product": "Cleanup Probe Feature",
        "installdir": "CleanupProbeFeat",
        "upgrade_code": "{6C2D3F84-7E19-4A5B-8D30-1F9C4E2B6A73}",
        "registry_target": r"Software\Vendor\CleanupProbeFeat",
        "registry_sibling": r"Software\Vendor\CleanupProbeFeatOther",
        "expect_feature": "FEATURE_00000",
    },
    {
        "key": "feat-minimal",
        "title": "T7 - the minimal template",
        "product": "Cleanup Probe Minimal",
        "installdir": "CleanupProbeMin",
        "upgrade_code": "{2B8E4C61-9D3A-4F17-A5C2-7E0B1D6F3A94}",
        "registry_target": r"Software\Vendor\CleanupProbeMin",
        "registry_sibling": r"Software\Vendor\CleanupProbeMinOther",
        "expect_feature": "FEATURE_00000",
        "template": "minimal/template.wxs",
    },
    {
        "key": "feat-silentx86",
        "title": "T7 - the silent x86 template",
        "product": "Cleanup Probe Silent86",
        "installdir": "CleanupProbeSil86",
        "upgrade_code": "{7F1A3D95-2C6E-4B08-9E47-5A0C8B2D1F63}",
        "registry_target": r"Software\Vendor\CleanupProbeSil86",
        "registry_sibling": r"Software\Vendor\CleanupProbeSil86Other",
        "expect_feature": "FEATURE_00000",
        # x86: installs under Program Files (x86), and its HKLM keys live in the 32-bit view.
        "platform": "x86",
        "silent": True,
    },
    # The upgrade pair: one product, one UpgradeCode, two versions. Each installs a version.txt
    # holding its version, so the VM can tell which one is installed.
    {
        "key": "upg-1.0.0",
        "title": "T7 - major upgrade, the old version",
        "product": "Cleanup Probe Upgrade",
        "installdir": "CleanupProbeUpg",
        "upgrade_code": "{5E0C7A38-4B19-4D62-8F3E-1A9D6C2B7E40}",
        "registry_target": r"Software\Vendor\CleanupProbeUpg",
        "registry_sibling": r"Software\Vendor\CleanupProbeUpgOther",
        "expect_feature": "FEATURE_00000",
        "version": "1.0.0",
    },
    {
        "key": "upg-1.0.1",
        "title": "T7 - major upgrade, the new version",
        "product": "Cleanup Probe Upgrade",
        "installdir": "CleanupProbeUpg",
        "upgrade_code": "{5E0C7A38-4B19-4D62-8F3E-1A9D6C2B7E40}",
        "registry_target": r"Software\Vendor\CleanupProbeUpg",
        "registry_sibling": r"Software\Vendor\CleanupProbeUpgOther",
        "expect_feature": "FEATURE_00000",
        "version": "1.0.1",
    },
]

MANUFACTURER = "Probe Co"
README_FILE = "Payload for the T5/T7 cleanup probe. See todo-testme.md.\n"


def msis_source(pkg: dict) -> str:
    top_level = pkg.get("top_level", False)
    version = pkg.get("version", "1.0.0")
    cleanup = (
        '  <create-folder target="[APPDATADIR]Vendor\\logs"/>\n'
        '  <remove-on-uninstall folder="[APPDATADIR]Vendor\\logs"/>\n'
        f'  <remove-on-uninstall registry="HKLM\\{pkg["registry_target"]}"/>\n'
    )
    inside = "" if top_level else "  " + cleanup.replace("\n  ", "\n    ").rstrip() + "\n"
    silent = ' silent="yes"' if pkg.get("silent") else ""
    platform = f'  <set name="PLATFORM" value="{pkg["platform"]}"/>\n' if pkg.get("platform") else ""
    versioned = (f'    <files source="version-{version}.txt" target="[INSTALLDIR]version.txt"/>\n'
                 if "version" in pkg else "")
    return f"""<?xml version="1.0" encoding="utf-8"?>
<setup{silent}>
  <set name="PRODUCT_NAME" value="{pkg['product']}"/>
  <set name="PRODUCT_VERSION" value="{version}"/>
  <set name="MANUFACTURER" value="{MANUFACTURER}"/>
  <set name="UPGRADE_CODE" value="{pkg['upgrade_code']}"/>
  <set name="INSTALLDIR" value="{pkg['installdir']}"/>
{platform}{cleanup if top_level else ''}  <feature name="Main">
    <files source="readme.txt" target="[INSTALLDIR]"/>
{versioned}{inside}  </feature>
</setup>
"""


def run(cmd: list[str], cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess:
    logger.debug("$ {}", " ".join(str(c) for c in cmd))
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    for line in (proc.stdout or "").splitlines():
        logger.debug("  {}", line)
    for line in (proc.stderr or "").splitlines():
        logger.warning("  {}", line)
    if check and proc.returncode != 0:
        raise SystemExit(f"command failed ({proc.returncode}): {' '.join(str(c) for c in cmd)}")
    return proc


def build(skip_build: bool) -> Path:
    msis_exe = HERE / "msis.exe"
    if not skip_build:
        logger.info("building msis from {}", REPO)
        run(["go", "build", "-o", str(msis_exe), "./cmd/msis"], cwd=REPO)
    (HERE / "readme.txt").write_text(README_FILE, encoding="utf-8", newline="\r\n")
    for pkg in PACKAGES:
        if "version" in pkg:
            (HERE / f"version-{pkg['version']}.txt").write_text(pkg["version"], encoding="utf-8")
    return msis_exe


def build_package(msis_exe: Path, pkg: dict) -> bool:
    """Write the .msis, build it, and check the cleanup components are referenced."""
    logger.info("=== {} ===", pkg["title"])
    source = HERE / f"{pkg['key']}.msis"
    source.write_text(msis_source(pkg), encoding="utf-8", newline="\r\n")

    cmd = [str(msis_exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}"]
    if pkg.get("template"):
        cmd.append(f"/TEMPLATE:{REPO / 'templates' / pkg['template']}")
    run(cmd + [str(source)], cwd=HERE)

    wxs = (HERE / f"{pkg['key']}.wxs").read_text(encoding="utf-8", errors="replace")
    ok = True

    # Both cleanup components must be referenced by the expected feature. That is #15's
    # failure mode in its silent form: the components exist, nothing installs them.
    refs = feature_refs(wxs, pkg["expect_feature"])
    for component in ("C_RemoveOnUninstall_0000", "C_RemoveOnUninstall_0001"):
        if component in refs:
            logger.success("PASS {} referenced by <Feature Id='{}'>", component, pkg["expect_feature"])
        else:
            logger.error("FAIL {} is NOT referenced by <Feature Id='{}'> - it would never install",
                         component, pkg["expect_feature"])
            ok = False

    # The folder the uninstall will delete, as authored. The VM checks what it resolves to.
    remembered = next((l.strip() for l in wxs.splitlines()
                       if "RemoveFolderPath_" in l and "RegistryValue" in l), None)
    if remembered:
        logger.info("  remembered path: {}", remembered)
    else:
        logger.error("FAIL no RegistryValue recording the folder path - nothing to delete from")
        ok = False
    return ok


def feature_refs(wxs: str, feature_id: str) -> list[str]:
    """ComponentRef ids inside one <Feature>, which is where #15's defect showed."""
    refs: list[str] = []
    inside = False
    for line in wxs.splitlines():
        stripped = line.strip()
        if stripped.startswith("<Feature "):
            inside = f"Id='{feature_id}'" in stripped
            continue
        if stripped.startswith("</Feature>"):
            inside = False
            continue
        if inside and stripped.startswith("<ComponentRef "):
            start = stripped.index("Id='") + 4
            refs.append(stripped[start:stripped.index("'", start)])
    return refs


def paths(pkg: dict) -> dict:
    """Where a package puts things on the VM, from the same values that wrote its .msis.

    Passed as data rather than restated on the VM side: the intended absolute path is the one
    thing both tickets insist must be asserted by equality, so it must not be possible for the
    two halves to disagree about it.
    """
    appdata = f"C:\\ProgramData\\{pkg['installdir']}"
    target = f"{appdata}\\Vendor\\logs"
    x86 = pkg.get("platform") == "x86"
    program_files = "C:\\Program Files (x86)" if x86 else "C:\\Program Files"
    return {
        "product": pkg["product"],
        # A 32-bit package's HKLM keys live in the 32-bit view (WOW6432Node).
        "view": 32 if x86 else 64,
        "remembered_key": f"Software\\{MANUFACTURER}\\{pkg['product']}",
        "remembered_name": "RemoveFolderPath_RemoveOnUninstall_0000",
        "target_dir": target,
        "nested_file": f"{target}\\deep\\nested.txt",
        "target_file": f"{target}\\runtime.log",
        "parent_sentinel": f"{appdata}\\Vendor\\keep-me.txt",
        "sibling_sentinel": f"{appdata}\\Vendor\\other\\keep-me.txt",
        "installed_file": f"{program_files}\\{pkg['installdir']}\\readme.txt",
        "version_file": f"{program_files}\\{pkg['installdir']}\\version.txt",
        "registry_target": pkg["registry_target"],
        "registry_sibling": pkg["registry_sibling"],
    }


def manifest() -> list[dict]:
    """The scenarios the VM runs, in order."""
    by_key = {p["key"]: p for p in PACKAGES}

    def core(key: str) -> dict:
        pkg = by_key[key]
        return {"kind": "core", "key": key, "title": pkg["title"], "msi": f"{key}.msi", **paths(pkg)}

    feat = by_key["feat"]
    old, new = by_key["upg-1.0.0"], by_key["upg-1.0.1"]
    return [
        core("top"),
        core("feat"),
        core("feat-minimal"),
        core("feat-silentx86"),
        {"kind": "upgrade", "key": "upgrade", "title": "T7 - a MAJOR UPGRADE 1.0.0 -> 1.0.1",
         "msi_old": "upg-1.0.0.msi", "msi_new": "upg-1.0.1.msi",
         "version_old": old["version"], "version_new": new["version"], **paths(new)},
        {"kind": "empty", "key": "feat-empty",
         "title": "T7 - the target folder already EMPTY at uninstall", "msi": "feat.msi", **paths(feat)},
        {"kind": "never", "key": "feat-never",
         "title": "T7 - the target folder NEVER EXISTED at uninstall", "msi": "feat.msi", **paths(feat)},
    ]


def stage() -> Path:
    payload = HERE / "vm-payload"
    payload.mkdir(exist_ok=True)
    for pkg in PACKAGES:
        shutil.copy2(HERE / f"{pkg['key']}.msi", payload / f"{pkg['key']}.msi")
    for name in ("t5t7_vm_probe.py", "README.md"):
        source = HERE / "vm" / name
        if source.exists():
            shutil.copy2(source, payload / name)
    (payload / "manifest.json").write_text(json.dumps(manifest(), indent=2), encoding="utf-8")

    total = sum(f.stat().st_size for f in payload.iterdir())
    logger.success("staged {} ({:.1f} MB): {}", payload.name, total / 1e6,
                   ", ".join(sorted(f.name for f in payload.iterdir())))
    return payload


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skip-build", action="store_true",
                        help="reuse the existing msis.exe instead of rebuilding it")
    parser.add_argument("--stage-only", action="store_true",
                        help="re-stage vm-payload from the existing packages; the VM script "
                             "changes far more often than the MSIs, and rebuilding them trips "
                             "over a scanner holding the output file")
    args = parser.parse_args()

    logger.remove()
    logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
    logger.add(HERE / "t5t7-build.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")

    logger.info("T5 + T7 build side - nothing is installed here")

    if args.stage_only:
        missing = [p["key"] for p in PACKAGES if not (HERE / f"{p['key']}.msi").exists()]
        if missing:
            logger.error("no package built yet for {} - run without --stage-only first", missing)
            return 1
        stage()
        return 0

    msis_exe = build(args.skip_build)

    ok = True
    for pkg in PACKAGES:
        if not build_package(msis_exe, pkg):
            ok = False
    if not ok:
        logger.error("a package is wrong; there is no point taking this to a VM")
        return 1

    payload = stage()
    logger.success("copy {} to a SNAPSHOTTED VM and run, ELEVATED:", payload)
    logger.info("    uv run t5t7_vm_probe.py      (or: python t5t7_vm_probe.py)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
