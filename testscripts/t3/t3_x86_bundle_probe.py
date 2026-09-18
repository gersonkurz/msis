"""T3, build side - build the x86 auto-bundle and stage it for the test VM (issue #8).

#8 was shipped on the generated XML plus a structural argument, never on a real install,
because the bug only shows on a 64-bit machine that does NOT have the x86 VC++ runtime -
and a machine that already has it proves nothing, since the detect condition passes either
way.

The argument being confirmed on the VM: the bundle's `VcppRuntimeX86Installed` comes from a
util:RegistrySearch with Bitness="always32", which on 64-bit Windows reads
HKLM\\SOFTWARE\\WOW6432Node\\Microsoft\\VisualStudio\\14.0\\VC\\Runtimes\\x86. The 32-bit
MSI's own launch condition reads HKLM\\SOFTWARE\\...\\VC\\Runtimes\\x86 under WOW64
redirection - the same physical key. So bundle and MSI should agree about whether the
runtime is there. Before the fix the package carried InstallCondition='NOT VersionNT64', so
on 64-bit Windows the runtime was never installed and the MSI then refused to install with
its own launch condition. That is the user-visible symptom the VM run reproduces.

This half needs no elevation and installs nothing:

    uv run t3_x86_bundle_probe.py           # build, check the chain, stage vm-payload/

Then copy `vm-payload/` to the test VM and run what is inside it. See README.md.
"""

from __future__ import annotations

import argparse
import subprocess
import sys
import shutil
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]

MSIS_FILE = """<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="X86 Bundle Probe"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{BB1B3A4D-1E2F-4A5B-8C6D-9E2F3A4B5C66}"/>
  <set name="INSTALLDIR" value="X86BundleProbe"/>
  <set name="PLATFORM" value="x86"/>
  <requires type="vcredist" version="2022"/>
  <feature name="Probe">
    <files source="readme.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>
"""
# Two msis bugs meet here, and the probe steers between them rather than around them.
#
# BUILD_TARGET is deliberately NOT set: on a package that auto-bundles, the value is also
# handed to the MSI build, so wix is asked to produce an MSI at a path ending in .exe and
# fails with WIX0341 (issue #28).
#
# Without it, the bundle lands in the process's working directory under a name derived from
# PRODUCT_NAME, msis reports a different path that does not exist, and the patch version is
# eaten - 1.0.0 becomes 1.0 (issue #27). So the build runs with this directory as its working
# directory and the artifact is located afterwards rather than assumed.

README_FILE = "Payload for the T3 probe. See todo-testme.md T3.\n"


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


def write_fixtures() -> None:
    (HERE / "probe.msis").write_text(MSIS_FILE, encoding="utf-8", newline="\r\n")
    (HERE / "readme.txt").write_text(README_FILE, encoding="utf-8", newline="\r\n")
    logger.info("wrote probe.msis and readme.txt")


def build(skip_build: bool) -> Path:
    """Build msis from the working tree and then the auto-bundle."""
    msis_exe = HERE / "msis.exe"
    if not skip_build:
        logger.info("building msis from {}", REPO)
        run(["go", "build", "-o", str(msis_exe), "./cmd/msis"], cwd=REPO)

    for stale in HERE.glob("*.exe"):
        if stale.name != "msis.exe":
            stale.unlink()

    logger.info("building the auto-bundle (downloads vc_redist.x86.exe if not cached)")
    run([str(msis_exe), "/BUILD", "/RETAINWXS",
         f"/TEMPLATEFOLDER:{REPO / 'templates'}", str(HERE / "probe.msis")], cwd=HERE)

    # msis names the bundle from PRODUCT_NAME and reports a path it did not write to, so find
    # what was actually produced instead of trusting either (issue #27).
    produced = [p for p in HERE.glob("*.exe") if p.name != "msis.exe"]
    if len(produced) != 1:
        raise SystemExit(f"expected exactly one bundle in {HERE}, found {produced}")
    bundle = produced[0]
    logger.success("built {} ({:.1f} MB)", bundle.name, bundle.stat().st_size / 1e6)
    return bundle


def check_chain() -> bool:
    """Proof 1 - the generated chain, checkable without installing anything.

    The fix removed InstallCondition='NOT VersionNT64' from the x86 prerequisite. If it is
    still there, the build did not pick the fix up and there is no point installing.
    """
    wxs = (HERE / "probe-bundle.wxs").read_text(encoding="utf-8", errors="replace")
    line = next((l.strip() for l in wxs.splitlines()
                 if "Prereq_vcredist_2022_x86" in l and "ExePackage" in l), None)
    logger.info("=== the x86 prerequisite in the generated chain ===")
    if line is None:
        logger.error("no ExePackage for Prereq_vcredist_2022_x86 - the chain has no x86 runtime")
        return False
    logger.info("  {}", line)

    ok = True
    if "DetectCondition='VcppRuntimeX86Installed'" not in line:
        logger.error("FAIL the package does not detect on VcppRuntimeX86Installed")
        ok = False
    else:
        logger.success("PASS detects on VcppRuntimeX86Installed")
    if "InstallCondition" in line:
        logger.error("FAIL InstallCondition is still present - this build predates the #8 fix")
        ok = False
    else:
        logger.success("PASS no InstallCondition, so 64-bit Windows is not excluded")
    return ok


def stage(bundle: Path) -> Path:
    """Assemble the folder to copy to the VM: the bundle and the machine-side script.

    The redistributable is not shipped separately - it is inside the bundle, and the only
    reason to have a second copy was the machine-wide uninstall that --restore used to offer.
    That path was removed: it is destructive and there is no way to test it here, so the reset
    procedure is a VM snapshot.
    """
    payload = HERE / "vm-payload"
    payload.mkdir(exist_ok=True)

    # Drop bundles left by an earlier build so find_bundle() on the VM still sees exactly
    # one. Everything else is overwritten by copy2 rather than deleted first: a stray lock
    # on one file - a log left open, an antivirus scan - should not fail the whole staging.
    for old in payload.glob("*.exe"):
        if old.name != bundle.name and not old.name.startswith("vc_redist"):
            try:
                old.unlink()
            except OSError as err:
                logger.warning("could not remove stale {}: {}", old.name, err)

    shutil.copy2(bundle, payload / bundle.name)
    for name in ("t3_vm_probe.py", "README.md"):
        source = HERE / "vm" / name
        if source.exists():
            shutil.copy2(source, payload / name)

    stale = payload / "vc_redist.x86.exe"
    if stale.exists():
        try:
            stale.unlink()   # shipped only for the removed --restore path
        except OSError:
            pass

    total = sum(f.stat().st_size for f in payload.iterdir())
    logger.success("staged {} ({:.1f} MB): {}", payload.name, total / 1e6,
                   ", ".join(sorted(f.name for f in payload.iterdir())))
    return payload


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skip-build", action="store_true",
                        help="reuse the existing msis.exe instead of rebuilding it")
    args = parser.parse_args()

    logger.remove()
    logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
    logger.add(HERE / "t3-build.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")

    logger.info("T3 build side - nothing is installed here")
    write_fixtures()
    bundle = build(args.skip_build)

    if not check_chain():
        logger.error("the chain is wrong, so there is no point running this on the VM")
        return 1

    payload = stage(bundle)
    logger.success("copy {} to the test VM and run, ELEVATED:", payload)
    logger.info("    uv run t3_vm_probe.py        (or: python t3_vm_probe.py)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
