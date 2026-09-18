# /// script
# requires-python = ">=3.11"
# dependencies = ["loguru"]
# ///
"""T3, machine side - run this on the test VM (issue #8).

Everything it needs is in this folder: the bundle was built elsewhere and copied here, so
there is no repo, no Go, no WiX and no msis on this machine. This script installs the
bundle, checks what happened, installs it a second time to confirm the prerequisite is
skipped, uninstalls, and prints a verdict.

What is being confirmed: on 64-bit Windows WITHOUT the x86 VC++ runtime, an x86 package
that declares <requires type="vcredist"> must install that runtime and then install its
MSI. Before the fix for #8 the prerequisite carried InstallCondition='NOT VersionNT64', so
on 64-bit Windows it was skipped and the MSI then refused to install, because its own
launch condition found no x86 runtime.

    uv run t3_vm_probe.py --state      # what this machine has; installs nothing
    uv run t3_vm_probe.py --selftest   # checks the verdict logic itself; installs nothing
    uv run t3_vm_probe.py              # ELEVATED: the real thing

THIS INSTALLS SOFTWARE and is one-shot: it installs the Microsoft Visual C++ 2015-2022
Redistributable (x86) machine-wide, and bundles mark prerequisites Permanent, so it stays
after the probe's own product is removed. Once it is present the case under test is gone.
Reset by rolling the VM back to a snapshot taken before the run - take one first.
"""

from __future__ import annotations

import argparse
import ctypes
import os
import subprocess
import sys
import winreg
from dataclasses import dataclass, field, replace
from pathlib import Path

try:
    from loguru import logger
except ImportError:  # a bare VM without network need not be a blocker
    class _Logger:
        """Minimal stand-in printing the same shape as loguru, so output is comparable."""

        def _emit(self, level: str, message: str, *args: object) -> None:
            print(f"{level:<8} " + (message.format(*args) if args else message))

        def info(self, m: str, *a: object) -> None: self._emit("INFO", m, *a)
        def debug(self, m: str, *a: object) -> None: self._emit("DEBUG", m, *a)
        def warning(self, m: str, *a: object) -> None: self._emit("WARNING", m, *a)
        def error(self, m: str, *a: object) -> None: self._emit("ERROR", m, *a)
        def success(self, m: str, *a: object) -> None: self._emit("SUCCESS", m, *a)
        def remove(self) -> None: pass
        def add(self, *a: object, **k: object) -> None: pass

    logger = _Logger()  # type: ignore[assignment]

HERE = Path(__file__).resolve().parent

RUNTIMES = r"SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes"
INSTALLED_FILE = Path(r"C:\Program Files (x86)\X86BundleProbe\readme.txt")

# Each runtime registers in the registry view of its own bitness, and reading the wrong one
# is not a near miss - it reports "absent" on a machine that has it:
#
#   x86   64-bit view                absent
#   x86   32-bit view (WOW6432Node)  Installed=1 Version=v14.51.36247.00
#
# The x86 redistributable is a 32-bit installer, so it lands under WOW6432Node. That is also
# exactly where the bundle looks: its util:RegistrySearch carries Bitness="always32". An
# earlier version of this script read x86 through the 64-bit view, reported the runtime as
# absent, and let a run proceed that could not prove anything - the case under test was
# already gone.
VIEWS = {"x86": winreg.KEY_WOW64_32KEY, "x64": winreg.KEY_WOW64_64KEY}

# msiexec/Burn: 0 is success, 3010 is success pending a reboot, 1605 is "was not installed"
# which only makes sense for an uninstall.
INSTALL_OK = (0, 3010)
UNINSTALL_OK = (0, 1605, 3010)


def is_elevated() -> bool:
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def windows_is_64bit() -> bool:
    """Is the OS 64-bit, regardless of this interpreter's bitness?

    sys.maxsize describes Python, not Windows: a 32-bit Python on 64-bit Windows would look
    32-bit and the probe would refuse a machine that is exactly right. Windows sets
    PROCESSOR_ARCHITEW6432 in a 32-bit process on 64-bit Windows, and PROCESSOR_ARCHITECTURE
    otherwise.
    """
    arch = os.environ.get("PROCESSOR_ARCHITEW6432") or os.environ.get("PROCESSOR_ARCHITECTURE", "")
    return arch.upper() in {"AMD64", "ARM64", "IA64"}


def runtime_installed(arch: str) -> bool:
    """Is the VC++ runtime for this architecture registered?

    Read through the view matching the architecture - see VIEWS above. For x86 that is the
    32-bit view, the same physical key the bundle reads with Bitness="always32".
    """
    try:
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, RUNTIMES + "\\" + arch, 0,
                            winreg.KEY_READ | VIEWS[arch]) as key:
            value, _ = winreg.QueryValueEx(key, "Installed")
            return bool(value)
    except FileNotFoundError:
        return False


def redistributable_in_arp() -> list[str]:
    """Display names of installed x86 Visual C++ runtime entries, from Add/Remove Programs.

    Matched loosely and case-insensitively on purpose: the entries are named
    "Microsoft Visual C++ v14 Redistributable (x86)" and "Microsoft Visual C++ 2022 X86
    Minimum Runtime", among others. An earlier version required "2015" in the name and
    matched "x86" case-sensitively, so it found nothing on a machine that had the runtime
    and reported a failure that was its own.
    """
    found: list[str] = []
    for view in (winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
        try:
            uninstall = winreg.OpenKey(
                winreg.HKEY_LOCAL_MACHINE,
                r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall",
                0, winreg.KEY_READ | view)
        except FileNotFoundError:
            continue
        with uninstall:
            i = 0
            while True:
                try:
                    name = winreg.EnumKey(uninstall, i)
                except OSError:
                    break
                i += 1
                try:
                    with winreg.OpenKey(uninstall, name) as sub:
                        display, _ = winreg.QueryValueEx(sub, "DisplayName")
                except (FileNotFoundError, OSError):
                    continue
                low = display.lower()
                if "visual c++" in low and ("(x86)" in low or "x86 " in low):
                    found.append(display)
    return sorted(set(found))


@dataclass
class Facts:
    """Everything observed during a run. The verdict is a pure function of this, so the
    decision can be exercised without a VM - see --selftest."""

    install1_rc: int = 0
    install1_detected_absent: bool = True
    install1_planned_install: bool = True
    runtime_after_1: bool = True
    arp_after_1: list[str] = field(default_factory=lambda: ["Microsoft Visual C++ v14 Redistributable (x86)"])
    product_after_1: bool = True

    uninstall1_rc: int = 0
    product_after_uninstall1: bool = False

    install2_rc: int = 0
    install2_detected_present: bool = True
    install2_planned_none: bool = True
    product_after_2: bool = True

    uninstall2_rc: int = 0
    product_after_uninstall2: bool = False


def verdict(f: Facts) -> tuple[bool, list[tuple[str, str]]]:
    """Decide PASS/FAIL from the observations. Pure, so --selftest can prove that each
    failure mode actually fails - an earlier version discarded installer return codes and
    would report PASS after a 1603."""
    out: list[tuple[str, str]] = []
    ok = True

    def check(cond: bool, good: str, bad: str) -> None:
        nonlocal ok
        if cond:
            out.append(("PASS", good))
        else:
            out.append(("FAIL", bad))
            ok = False

    check(f.install1_rc in INSTALL_OK, "install 1 succeeded",
          f"install 1 returned {f.install1_rc} - 1602 is a cancelled wizard, 1603 a failure")
    check(f.install1_detected_absent, "the prerequisite was detected as absent",
          "the prerequisite was NOT detected as absent - this machine already had the "
          "runtime, so the run proves nothing")
    check(f.install1_planned_install, "it was planned for installation",
          "it was not planned for installation - on a 64-bit machine this is exactly the "
          "#8 symptom")
    check(f.runtime_after_1, "the x86 runtime is registered after install 1",
          "the x86 runtime is still absent - the bundle did not install it")
    check(bool(f.arp_after_1), "it appears in Add/Remove Programs",
          "no x86 Visual C++ entry in Add/Remove Programs")
    check(f.product_after_1, "the MSI installed",
          "the MSI did not install - before the #8 fix this is what failed, because its "
          "launch condition found no x86 runtime")

    check(f.uninstall1_rc in UNINSTALL_OK, "uninstall 1 succeeded",
          f"uninstall 1 returned {f.uninstall1_rc}")
    check(not f.product_after_uninstall1, "the product was removed",
          "the product survived the first uninstall")

    check(f.install2_rc in INSTALL_OK, "install 2 succeeded",
          f"install 2 returned {f.install2_rc}")
    check(f.install2_detected_present, "the prerequisite was detected as already present",
          "the prerequisite was not detected as present on the second run")
    check(f.install2_planned_none, "it was skipped rather than installed again",
          "it was not planned as 'execute: None' - it may have been reinstalled")
    check(f.product_after_2, "the MSI installed again",
          "the MSI did not install on the second run")

    check(f.uninstall2_rc in UNINSTALL_OK, "uninstall 2 succeeded",
          f"uninstall 2 returned {f.uninstall2_rc}")
    check(not f.product_after_uninstall2, "the product is uninstalled",
          "the product survived uninstall")

    return ok, out


def selftest() -> int:
    """Prove the verdict cannot be talked into a PASS. Runs anywhere, installs nothing."""
    logger.info("=== selftest: the verdict logic ===")
    good = Facts()
    ok, _ = verdict(good)
    failures = 0
    if not ok:
        logger.error("a clean run does not pass - the checks are wrong")
        failures += 1
    else:
        logger.success("a clean run passes")

    # Each of these is a way a run can go wrong; none may produce a PASS.
    broken = {
        "install 1 fails with 1603": replace(good, install1_rc=1603),
        "install 1 cancelled (1602)": replace(good, install1_rc=1602),
        "install 2 fails with 1603": replace(good, install2_rc=1603),
        "uninstall fails": replace(good, uninstall2_rc=1603),
        "prerequisite already present on run 1": replace(good, install1_detected_absent=False),
        "prerequisite never planned for install": replace(good, install1_planned_install=False),
        "runtime absent after install": replace(good, runtime_after_1=False),
        "no Add/Remove entry": replace(good, arp_after_1=[]),
        "MSI did not install": replace(good, product_after_1=False),
        "MSI missing on the second run": replace(good, product_after_2=False),
        "prerequisite reinstalled instead of skipped": replace(good, install2_planned_none=False),
        "product survives uninstall": replace(good, product_after_uninstall2=True),
    }
    for name, facts in broken.items():
        ok, _ = verdict(facts)
        if ok:
            logger.error("FAIL {} still reports PASS", name)
            failures += 1
        else:
            logger.success("PASS {} is rejected", name)

    # The OS-architecture guard, both ways. sys.maxsize would describe Python rather than
    # Windows, so a 32-bit interpreter on 64-bit Windows would refuse a machine that is
    # exactly right; these cases pin that it reads the environment instead.
    logger.info("=== selftest: the 64-bit Windows guard ===")
    saved = {k: os.environ.get(k) for k in ("PROCESSOR_ARCHITECTURE", "PROCESSOR_ARCHITEW6432")}
    arch_cases = [
        ({"PROCESSOR_ARCHITECTURE": "AMD64"}, True, "64-bit Windows, 64-bit Python"),
        ({"PROCESSOR_ARCHITECTURE": "x86", "PROCESSOR_ARCHITEW6432": "AMD64"}, True,
         "64-bit Windows, 32-bit Python"),
        ({"PROCESSOR_ARCHITECTURE": "ARM64"}, True, "ARM64 Windows"),
        ({"PROCESSOR_ARCHITECTURE": "x86"}, False, "genuinely 32-bit Windows"),
    ]
    try:
        for env, expected, label in arch_cases:
            for key in saved:
                os.environ.pop(key, None)
            os.environ.update(env)
            got = windows_is_64bit()
            if got == expected:
                logger.success("PASS {} -> {}", label, "accepted" if expected else "refused")
            else:
                logger.error("FAIL {} -> {}, expected {}", label, got, expected)
                failures += 1
    finally:
        for key, value in saved.items():
            os.environ.pop(key, None)
            if value is not None:
                os.environ[key] = value

    if failures:
        logger.error("selftest failed with {} problem(s)", failures)
        return 1
    logger.success("selftest passed: every failure mode is rejected")
    return 0


def run(cmd: list[str]) -> int:
    logger.debug("$ {}", " ".join(str(c) for c in cmd))
    proc = subprocess.run(cmd, capture_output=True, text=True, errors="replace")
    logger.debug("  -> {}", proc.returncode)
    return proc.returncode


def package_lines(log_path: Path) -> list[str]:
    """Burn's detect/plan lines for the x86 prerequisite."""
    if not log_path.exists():
        return []
    text = ""
    for encoding in ("utf-8", "utf-16", "latin-1"):
        try:
            text = log_path.read_text(encoding=encoding)
            break
        except (UnicodeError, LookupError):
            continue
    return [l.strip() for l in text.splitlines()
            if "Prereq_vcredist_2022_x86" in l
            and ("Detected package" in l or "Planned package" in l)]


def report_log(log_path: Path, label: str) -> list[str]:
    logger.info("=== {} ===", label)
    lines = package_lines(log_path)
    if not lines:
        logger.warning("  no detect/plan lines for the x86 prerequisite in {}", log_path.name)
    for line in lines:
        logger.info("  {}", line)
    return lines


def find_bundle() -> Path:
    candidates = [p for p in HERE.glob("*.exe") if not p.name.startswith("vc_redist")]
    if len(candidates) != 1:
        raise SystemExit(f"expected exactly one bundle .exe beside this script, found {candidates}")
    return candidates[0]


def report_state() -> tuple[bool, bool]:
    """Print what this machine has, and return (x86 present, x64 present)."""
    x86, x64 = runtime_installed("x86"), runtime_installed("x64")
    logger.info("=== machine state ===")
    logger.info("  64-bit Windows:      {}", "yes" if windows_is_64bit() else "NO")
    logger.info("  VC++ runtime x86:    {}  (32-bit view, WOW6432Node)",
                "PRESENT" if x86 else "absent")
    logger.info("  VC++ runtime x64:    {}  (64-bit view)", "PRESENT" if x64 else "absent")
    arp = redistributable_in_arp()
    logger.info("  x86 entries in Add/Remove: {}", "; ".join(arp) if arp else "(none)")
    if not windows_is_64bit():
        logger.warning("  -> NOT usable for T3: the case under test is 64-bit Windows")
    elif x86:
        logger.warning("  -> NOT usable for T3: the case under test needs the x86 runtime ABSENT")
    elif not x64:
        logger.warning("  -> usable, but weaker: the case #8 got wrong is x64 present, x86 absent")
    else:
        logger.success("  -> exactly the T3 case: x64 present, x86 absent")
    return x86, x64


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", action="store_true",
                        help="report what this machine has and exit; installs nothing")
    parser.add_argument("--selftest", action="store_true",
                        help="check the verdict logic itself and exit; installs nothing")
    args = parser.parse_args()

    if hasattr(logger, "remove"):
        try:
            logger.remove()
            logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
            logger.add(HERE / "t3-vm.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")
        except Exception:
            pass

    if args.selftest:
        return selftest()
    if args.state:
        report_state()
        return 0

    if not is_elevated():
        logger.error("run this from an ELEVATED shell - it installs a bundle machine-wide")
        return 2

    x86, _ = report_state()
    if not windows_is_64bit():
        logger.error("this is not 64-bit Windows; #8 is about 64-bit Windows specifically")
        return 2
    if x86:
        logger.error(
            "the x86 runtime is already installed, so this run would prove nothing: the "
            "detect condition passes either way, and the MSI's launch condition finds what "
            "it needs no matter what the bundle does. Use a VM image without it and try again.")
        return 2

    bundle = find_bundle()
    logger.info("bundle: {} ({:.1f} MB)", bundle.name, bundle.stat().st_size / 1e6)
    logger.warning("installing the x86 VC++ runtime machine-wide - it is NOT removed by "
                   "uninstalling the probe; roll the VM back to reset")

    f = Facts()

    logger.info("=== install 1: the prerequisite is absent ===")
    first = HERE / "install-1.log"
    f.install1_rc = run([str(bundle), "/quiet", "/log", str(first)])
    lines = report_log(first, "install 1 - detect and plan for the x86 prerequisite")
    f.install1_detected_absent = any("Detected package" in l and "state: Absent" in l for l in lines)
    f.install1_planned_install = any("Planned package" in l and "execute: Install" in l for l in lines)
    f.runtime_after_1 = runtime_installed("x86")
    f.arp_after_1 = redistributable_in_arp()
    f.product_after_1 = INSTALLED_FILE.exists()

    logger.info("=== uninstall, then install again with the runtime present ===")
    f.uninstall1_rc = run([str(bundle), "/uninstall", "/quiet", "/log", str(HERE / "uninstall-1.log")])
    f.product_after_uninstall1 = INSTALLED_FILE.exists()

    second = HERE / "install-2.log"
    f.install2_rc = run([str(bundle), "/quiet", "/log", str(second)])
    lines = report_log(second, "install 2 - the prerequisite should be detected Present and skipped")
    f.install2_detected_present = any("Detected package" in l and "state: Present" in l for l in lines)
    f.install2_planned_none = any("Planned package" in l and "execute: None" in l for l in lines)
    f.product_after_2 = INSTALLED_FILE.exists()

    f.uninstall2_rc = run([str(bundle), "/uninstall", "/quiet", "/log", str(HERE / "uninstall-2.log")])
    f.product_after_uninstall2 = INSTALLED_FILE.exists()

    logger.info("=== verdict ===")
    ok, results = verdict(f)
    for level, message in results:
        (logger.success if level == "PASS" else logger.error)("{} {}", level, message)

    logger.info("the x86 VC++ runtime is still installed - prerequisites are Permanent. "
                "Roll the VM back before running this again.")
    if ok:
        logger.success("T3 PASSED - copy this output back and paste it into issue #8")
    else:
        logger.error("T3 FAILED - copy this output back; it needs a new issue linked from #8")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
