"""T1 - what `preserve="yes"` actually does at install time (issue #5).

msis rewrote registry preservation so that a `.reg` default lives in an MSI property
with a `RegistrySearch` nested inside it. What was proven at the time: the XML we emit,
and that WiX compiles it. What was never proven: what Windows Installer does with it on
a real machine. This script does that, and nothing else.

Three things are genuinely unknown, and each has a row in the table below:

  1. A value that exists but is EMPTY. The search returns "" - does the empty live value
     survive, or does the .reg default overwrite it? The answer decides whether a user who
     blanked a setting gets to keep it blank.
  2. The elevated client -> server handoff. AppSearch runs client-side; the registry
     write happens server-side. Every preserved property carries Secure='yes' for
     exactly this reason, and a silent /qn install is that path.
  3. The unnamed (default) value of a key, which Microsoft's RegLocator docs qualify
     with "if it is not empty".

Run it twice - once silent, once with the installer UI - because Windows Installer
skips AppSearch in the execute sequence when the UI sequence already ran it, and both
paths have to give the same answer.

    uv run t1_preserve_probe.py          # silent (/qn)
    uv run t1_preserve_probe.py --ui     # full UI; click through the wizard

Must run ELEVATED: it installs a per-machine MSI and writes under HKLM.
"""

from __future__ import annotations

import argparse
import ctypes
import subprocess
import sys
import winreg
from dataclasses import dataclass
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]

# The probe key. HKLM, not HKCU: a per-machine install is what exercises unknown 2.
PROBE_KEY = r"SOFTWARE\MsisPreserveProbe"

# Always the 64-bit view. msis defaults to x64, so the MSI writes there; a 32-bit Python
# would otherwise read HKLM\SOFTWARE\WOW6432Node and report everything as missing.
VIEW = winreg.KEY_WOW64_64KEY

TYPE_NAMES = {
    winreg.REG_SZ: "REG_SZ",
    winreg.REG_EXPAND_SZ: "REG_EXPAND_SZ",
    winreg.REG_DWORD: "REG_DWORD",
    winreg.REG_QWORD: "REG_QWORD",
    winreg.REG_BINARY: "REG_BINARY",
    winreg.REG_MULTI_SZ: "REG_MULTI_SZ",
}


@dataclass(frozen=True)
class Expectation:
    """One row of T1's prediction table."""

    name: str  # "" is the key's unnamed (default) value
    seeded: object | None  # what the user "already had"; None = not seeded
    expected_value: object
    expected_type: int
    why: str

    @property
    def label(self) -> str:
        return self.name or "(unnamed)"


EXPECTATIONS = [
    Expectation(
        "Absent", None, "default-absent", winreg.REG_SZ,
        "not present before install, so the .reg default must win",
    ),
    Expectation(
        "AbsentDword", None, 42, winreg.REG_DWORD,
        "same, and it must land as a DWORD - '42' as REG_SZ would be a real defect",
    ),
    Expectation(
        "Existing", "live-existing", "live-existing", winreg.REG_SZ,
        "the user's value survives; this is what preserve= is for, and it is unknown 2",
    ),
    Expectation(
        "ExistingDword", 99, 99, winreg.REG_DWORD,
        "same for an integer, where the # prefix has to round-trip",
    ),
    # Observed on 2026-09-18, in both the silent and the full-UI pass: an existing but EMPTY
    # value is NOT preserved - the .reg default overwrites it. The search runs and reads the
    # empty value, but AppSearch makes no assignment from an empty result: the log shows no
    # PROPERTY CHANGE line for PS_RV_00002 while 00003 and 00004 each have one, so the property
    # still held its .reg default when the write happened.
    #
    # This records what the code does, not what it should do - msis-2.x behaves identically, so
    # it is long-standing rather than a regression. Tracked as issue #26. If #26 is fixed, flip
    # this back to "" in the same change, and this row starts failing until it is.
    Expectation(
        "EmptyExisting", "", "default-empty-existing", winreg.REG_SZ,
        "issue #26: an EMPTY live value is overwritten by the default; AppSearch assigns "
        "nothing from an empty result, so the property keeps its .reg default",
    ),
    Expectation(
        "", None, "default-unnamed", winreg.REG_SZ,
        "unknown 3: the key's unnamed value, which RegLocator documents differently",
    ),
]

REG_FILE = """Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\\SOFTWARE\\MsisPreserveProbe]
"Absent"="default-absent"
"Existing"="default-existing"
"EmptyExisting"="default-empty-existing"
"AbsentDword"=dword:0000002a
"ExistingDword"=dword:0000002a
@="default-unnamed"
"""

MSIS_FILE = """<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Msis Preserve Probe"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{7C1B3A4D-1E2F-4A5B-8C6D-9E2F3A4B5C6E}"/>
  <set name="INSTALLDIR" value="MsisPreserveProbe"/>
  <feature name="Probe">
    <files source="readme.txt" target="[INSTALLDIR]"/>
    <registry file="rtprobe.reg" preserve="yes"/>
  </feature>
</setup>
"""
# INSTALLDIR is not optional here, and leaving it out is not a harmless omission: with no
# value, INSTALLDIR resolves to C:\\Program Files\\ ITSELF, msis emits its usual
# util:PermissionEx component for that directory, and the install dies during
# InstallFinalize with
#
#   Error 25521. Failed to set security descriptor on object C:\\Program Files\\
#   CustomAction Wix4ExecSecureObjects_X64 returned actual error code 1603
#
# even when elevated, because re-ACLing Program Files is refused. The failure looks like a
# permissions problem with the probe rather than a missing line in the .msis, which is why
# it is called out here.

# A registry-only package does not link: INSTALLDIR is never referenced, and WiX fails
# with WIX0094. This file exists only to give it something to install.
README_FILE = "Placeholder so INSTALLDIR resolves. See todo-testme.md T1.\n"


def is_elevated() -> bool:
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def write_fixtures() -> None:
    """Write the .reg, .msis and payload the probe installs."""
    for name, content in [
        ("rtprobe.reg", REG_FILE),
        ("rtprobe.msis", MSIS_FILE),
        ("readme.txt", README_FILE),
    ]:
        (HERE / name).write_text(content, encoding="utf-8", newline="\r\n")
        logger.info("wrote {}", name)


def build_msi(skip_build: bool) -> Path:
    """Build msis from the working tree, then build the probe MSI with it.

    Built from source on purpose: the point is to test the code in this repo, not
    whatever version happens to be installed. /TEMPLATEFOLDER points at the repo's
    templates for the same reason - msis otherwise prefers the installed copy under
    %LOCALAPPDATA%, which has silently invalidated probes before.
    """
    msis_exe = HERE / "msis.exe"
    if not skip_build:
        logger.info("building msis from {}", REPO)
        run(["go", "build", "-o", str(msis_exe), "./cmd/msis"], cwd=REPO)

    msi = HERE / "rtprobe.msi"
    msi.unlink(missing_ok=True)
    logger.info("building the probe MSI")
    run(
        [
            str(msis_exe),
            "/BUILD",
            "/RETAINWXS",
            f"/TEMPLATEFOLDER:{REPO / 'templates'}",
            str(HERE / "rtprobe.msis"),
        ],
        cwd=HERE,
    )
    if not msi.exists():
        raise SystemExit("the MSI was not produced; see the msis output above")
    logger.success("built {}", msi.name)
    return msi


def run(cmd: list[str], cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess:
    logger.debug("$ {}", " ".join(cmd))
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace")
    for line in (proc.stdout or "").splitlines():
        logger.debug("  {}", line)
    for line in (proc.stderr or "").splitlines():
        logger.warning("  {}", line)
    if check and proc.returncode != 0:
        raise SystemExit(f"command failed ({proc.returncode}): {' '.join(cmd)}")
    return proc


def seed_registry() -> None:
    """Put the key into the state a user who has been running the product would have.

    `Absent` and `AbsentDword` are deliberately NOT seeded: their .reg defaults must win.
    """
    delete_key()
    with winreg.CreateKeyEx(winreg.HKEY_LOCAL_MACHINE, PROBE_KEY, 0, winreg.KEY_WRITE | VIEW) as key:
        winreg.SetValueEx(key, "Existing", 0, winreg.REG_SZ, "live-existing")
        winreg.SetValueEx(key, "EmptyExisting", 0, winreg.REG_SZ, "")
        winreg.SetValueEx(key, "ExistingDword", 0, winreg.REG_DWORD, 99)
    logger.info("seeded Existing / EmptyExisting / ExistingDword")


def delete_key() -> None:
    try:
        winreg.DeleteKeyEx(winreg.HKEY_LOCAL_MACHINE, PROBE_KEY, VIEW, 0)
    except FileNotFoundError:
        pass


def read_key() -> dict[str, tuple[object, int]] | None:
    """Return {name: (value, type)}; None when the key does not exist."""
    try:
        key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, PROBE_KEY, 0, winreg.KEY_READ | VIEW)
    except FileNotFoundError:
        return None
    values: dict[str, tuple[object, int]] = {}
    with key:
        i = 0
        while True:
            try:
                name, value, kind = winreg.EnumValue(key, i)
            except OSError:
                break
            values[name] = (value, kind)
            i += 1
    return values


def dump(label: str) -> dict[str, tuple[object, int]] | None:
    values = read_key()
    logger.info("=== {} ===", label)
    if values is None:
        logger.info("  (key absent)")
        return None
    if not values:
        logger.info("  (key present, no values)")
    for name, (value, kind) in sorted(values.items()):
        logger.info(
            "  {:<16} {!r:<26} [{}]",
            name or "(unnamed)",
            value,
            TYPE_NAMES.get(kind, kind),
        )
    return values


def check(values: dict[str, tuple[object, int]] | None) -> bool:
    """Compare the post-install state against T1's prediction table."""
    logger.info("=== T1 prediction table ===")
    if values is None:
        logger.error("the key does not exist after install - nothing was written at all")
        return False

    all_ok = True
    for exp in EXPECTATIONS:
        if exp.name not in values:
            logger.error(
                "FAIL {:<16} missing entirely (expected {!r} {})",
                exp.label, exp.expected_value, TYPE_NAMES[exp.expected_type],
            )
            logger.error("       {}", exp.why)
            all_ok = False
            continue

        value, kind = values[exp.name]
        value_ok = value == exp.expected_value
        type_ok = kind == exp.expected_type
        if value_ok and type_ok:
            logger.success(
                "PASS {:<16} {!r} [{}]", exp.label, value, TYPE_NAMES.get(kind, kind)
            )
            continue

        all_ok = False
        logger.error(
            "FAIL {:<16} got {!r} [{}], expected {!r} [{}]",
            exp.label, value, TYPE_NAMES.get(kind, kind),
            exp.expected_value, TYPE_NAMES[exp.expected_type],
        )
        logger.error("       {}", exp.why)
    return all_ok


def report_appsearch(log_path: Path) -> None:
    """Show what AppSearch did to the preservation properties.

    These lines are the mechanism working or not working: each PS_RV_ property starts
    with the .reg default and AppSearch overwrites it when the live value is found.
    """
    logger.info("=== PROPERTY CHANGE lines for PS_RV_ in the MSI log ===")
    if not log_path.exists():
        logger.warning("  no log at {}", log_path)
        return

    text = None
    for encoding in ("utf-16", "utf-8", "latin-1"):
        try:
            text = log_path.read_text(encoding=encoding)
            break
        except (UnicodeError, LookupError):
            continue
    if text is None:
        logger.warning("  could not decode {}", log_path)
        return

    hits = [ln.strip() for ln in text.splitlines() if "PROPERTY CHANGE" in ln and "PS_RV_" in ln]
    if not hits:
        logger.warning("  none found - AppSearch may not have run, which is itself a finding")
    for line in hits:
        logger.info("  {}", line)


def uninstall(msi: Path) -> None:
    """Remove the probe product. 1605 means it was not installed, which is fine here -
    this also runs on the failure path so a cancelled wizard leaves nothing behind."""
    logger.info("uninstalling")
    proc = run(["msiexec", "/x", str(msi), "/qn"], check=False)
    if proc.returncode not in (0, 1605, 3010):
        logger.warning("uninstall returned {} - the product may still be installed", proc.returncode)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--ui", action="store_true",
        help="install with the full UI instead of /qn (the second required pass)",
    )
    parser.add_argument(
        "--skip-build", action="store_true",
        help="reuse the existing msis.exe instead of rebuilding it from the repo",
    )
    parser.add_argument(
        "--build-only", action="store_true",
        help="write the fixtures and build the MSI, then stop - needs no elevation",
    )
    args = parser.parse_args()

    logger.remove()
    logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
    logger.add(HERE / "t1-probe.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")

    if args.build_only:
        logger.info("T1 fixtures and MSI only - no registry is touched, nothing is installed")
        write_fixtures()
        build_msi(args.skip_build)
        logger.success("build complete; rerun elevated without --build-only to install")
        return 0

    if not is_elevated():
        logger.error("run this from an ELEVATED shell - it installs a per-machine MSI")
        return 2

    mode = "full UI" if args.ui else "silent (/qn)"
    logger.info("T1 preserve=\"yes\" runtime probe - {}", mode)

    write_fixtures()
    msi = build_msi(args.skip_build)

    seed_registry()
    dump("BEFORE install (seeded)")

    install_log = HERE / ("install-ui.log" if args.ui else "install-qn.log")
    cmd = ["msiexec", "/i", str(msi), "/l*v", str(install_log)]
    if not args.ui:
        cmd.append("/qn")
    else:
        logger.info("the installer window is about to open - click through it")

    # 3010 is "installed, reboot required", which is still a successful install.
    install = run(cmd, check=False)
    if install.returncode not in (0, 3010):
        logger.error(
            "install failed with {} - see {}. 1602 means you cancelled the wizard.",
            install.returncode, install_log,
        )
        uninstall(msi)
        return 1

    after = dump("AFTER install")
    ok = check(after)
    report_appsearch(install_log)

    uninstall(msi)
    remaining = dump("AFTER uninstall")
    if remaining is not None:
        logger.error("the key survived uninstall - T1 requires it to be gone")
        ok = False
    else:
        logger.success("the key is gone after uninstall")

    if ok:
        logger.success("T1 PASSED ({}) - every row matches the prediction", mode)
    else:
        logger.error(
            "T1 FAILED ({}) - do NOT reinstate the per-value SetProperty, that is the #5 "
            "bug. Open a new issue with this output and link it from #5.", mode
        )
    logger.info("full transcript: {}", HERE / "t1-probe.log")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
