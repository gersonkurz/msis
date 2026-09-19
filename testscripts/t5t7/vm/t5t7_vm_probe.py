# /// script
# requires-python = ">=3.11"
# dependencies = ["loguru"]
# ///
"""T5 + T7, machine side - what <remove-on-uninstall> deletes, and what it leaves alone.

Everything needed is in this folder: two MSIs built elsewhere, and manifest.json saying what
each one is expected to remove. No repo, no Go, no WiX, no msis on this machine.

  T5 / issue #15 - the cleanup elements written at TOP LEVEL. Until #15 that package failed
                   to build, so the components never installed and never ran; giving them a
                   feature is the moment the deletion became reachable.
  T7 / issue #3  - the same elements inside a <feature>.

This is a recursive delete of a directory the installer does not own, so the sentinels matter
as much as the deletions: the value of this mechanism over REMOVE_FOLDERS_ON_UNINSTALL is a
narrower blast radius, and that is the claim under test. A run that removes the target but
also touches the parent or a sibling is a FAIL, not a detail.

    uv run t5t7_vm_probe.py --selftest   # checks the verdict AND the runner; installs nothing
    uv run t5t7_vm_probe.py              # ELEVATED: the real thing

TAKE A VM SNAPSHOT FIRST. It installs and uninstalls per-machine MSIs, writes under
C:\\ProgramData and HKLM, and then deletes those directories and keys.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import subprocess
import sys
import winreg
from dataclasses import dataclass, field, replace
from pathlib import Path
from typing import Callable

try:
    from loguru import logger
except ImportError:  # a bare VM without network need not be a blocker
    class _Logger:
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
VIEW = winreg.KEY_WOW64_64KEY

INSTALL_OK = (0, 3010)
UNINSTALL_OK = (0, 1605, 3010)

# What the seeded artefacts contain, so "untouched" can mean untouched rather than merely
# still existing. An emptied sentinel file, or a sibling key stripped of its value, is damage
# to a neighbour's data and must fail.
SENTINEL_TEXT = "seeded sentinel - this must survive the uninstall\n"
RUNTIME_TEXT = "seeded runtime data - the application wrote this\n"
SIBLING_VALUE = "a neighbouring product's data"
TARGET_VALUE = "written by the application"


def is_elevated() -> bool:
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


@dataclass
class Facts:
    """Everything observed for one package. The verdict is a pure function of this, so every
    failure mode can be exercised without a VM - see --selftest.

    Registry observations are three-state strings ("present" / "absent" / "error: ..."),
    because an unreadable key is not a deleted key: treating a PermissionError as absence
    would credit the uninstall with a deletion that may not have happened.
    """

    install_rc: int = 0
    product_installed: bool = True
    remembered_path: str = ""
    intended_path: str = ""

    repair_rc: int = 0
    target_file_after_repair: bool = True
    nested_file_after_repair: bool = True
    registry_target_value_after_repair: str = "intact"  # repair must not touch the value

    uninstall_rc: int = 0
    target_dir_after: bool = False
    nested_file_after: bool = False
    registry_target_after: str = "absent"

    parent_sentinel_after: bool = True
    parent_sentinel_intact: bool = True
    sibling_sentinel_after: bool = True
    sibling_sentinel_intact: bool = True
    registry_sibling_after: str = "present"
    registry_sibling_value: str = "intact"

    seeded: list[str] = field(default_factory=list)


def verdict(f: Facts) -> tuple[bool, list[tuple[str, str]]]:
    """PASS/FAIL from observations alone. Pure, so --selftest can prove that a damaged
    sentinel - the failure that matters here - can never produce a PASS."""
    out: list[tuple[str, str]] = []
    ok = True

    def check(cond: bool, good: str, bad: str) -> None:
        nonlocal ok
        if cond:
            out.append(("PASS", good))
        else:
            out.append(("FAIL", bad))
            ok = False

    check(f.install_rc in INSTALL_OK, "install succeeded", f"install returned {f.install_rc}")
    check(f.product_installed, "the product installed",
          "the product did not install, so nothing below was exercised")

    # Both tickets insist on equality: "the stored path is absolute" would pass while pointing
    # somewhere else, and somewhere else is what a recursive delete must never be aimed at.
    check(bool(f.remembered_path) and f.remembered_path.lower() == f.intended_path.lower(),
          f"the remembered path is exactly {f.intended_path}",
          f"the remembered path is {f.remembered_path!r}, expected {f.intended_path!r} - "
          "the uninstall would delete the wrong directory")

    check(f.repair_rc in INSTALL_OK, "repair succeeded", f"repair returned {f.repair_rc}")
    check(f.target_file_after_repair and f.nested_file_after_repair,
          "runtime files survived the repair",
          "repair destroyed runtime files - a repair must never be a data-loss event")
    # Checked before the uninstall on purpose: if a repair deleted the key, its absence
    # afterwards would otherwise be credited to the uninstall.
    check(f.registry_target_value_after_repair == "intact",
          "the runtime registry value survived the repair unchanged",
          f"the seeded registry value was {f.registry_target_value_after_repair} after the "
          "repair - a repair must not touch the user's data")

    check(f.uninstall_rc in UNINSTALL_OK, "uninstall succeeded",
          f"uninstall returned {f.uninstall_rc}")
    check(not f.target_dir_after, "the target folder is gone",
          "the target folder survived uninstall - the cleanup did not run")
    check(not f.nested_file_after, "the nested file is gone with it",
          "a file in a nested subfolder survived - the removal was not recursive")
    check(f.registry_target_after == "absent", "the target registry key is gone",
          f"the target registry key is {f.registry_target_after} - only a key that is "
          "genuinely missing counts as deleted")

    check(f.parent_sentinel_after, "the sentinel in the PARENT directory is still there",
          "the parent directory's sentinel was DELETED - the blast radius is too wide; "
          "stop and reopen the ticket")
    check(f.parent_sentinel_intact, "  and its contents are unchanged",
          "the parent sentinel's contents were CHANGED - the neighbour's data was damaged")
    check(f.sibling_sentinel_after, "the sentinel in the SIBLING directory is still there",
          "a sibling directory's sentinel was DELETED - the blast radius is too wide; "
          "stop and reopen the ticket")
    check(f.sibling_sentinel_intact, "  and its contents are unchanged",
          "the sibling sentinel's contents were CHANGED - the neighbour's data was damaged")
    check(f.registry_sibling_after == "present", "the sibling registry key is still there",
          f"the sibling registry key is {f.registry_sibling_after} - a neighbouring key was "
          "affected")
    check(f.registry_sibling_value == "intact", "  and its value is unchanged",
          f"the sibling registry key's value was {f.registry_sibling_value}")

    return ok, out


def run_all(entries: list[dict], probe_fn: Callable[[dict], tuple[bool, Facts]]) -> bool:
    """Aggregate per-package results. Split out and injectable because the first version
    tested the (ok, facts) TUPLE, which is always truthy - a failing package still reported
    an overall PASS. --selftest exercises this with a failing probe."""
    overall = True
    for entry in entries:
        ok, _ = probe_fn(entry)
        if not ok:
            overall = False
    return overall


def selftest_observations() -> int:
    """Exercise the registry OBSERVERS against a real key, not against made-up Facts.

    This is the gap the previous round left: the verdict was right, the observation was
    wrong - it asked whether the KEY existed and reported that the VALUE had survived - and
    a selftest that mutates Facts by hand cannot see that. So this drives the real registry
    through every state the probe must tell apart.

    Uses HKCU, so it needs no elevation and touches nothing the probe cares about.
    """
    key = r"Software\MsisT5T7SelftestScratch"
    root = winreg.HKEY_CURRENT_USER
    expected = "the seeded value"
    failures = 0

    def expect(label: str, got: str, want: str) -> None:
        nonlocal failures
        if got == want:
            logger.success("PASS {} -> {}", label, got)
        else:
            logger.error("FAIL {} -> {}, expected {}", label, got, want)
            failures += 1

    try:
        write_registry(key, "V", expected, root=root)
        expect("value present and equal", registry_value_state(key, "V", expected, root), "intact")
        expect("key present", registry_key_state(key, root), "present")

        # The exact case that slipped through: the value is deleted, the key remains.
        with winreg.OpenKey(root, key, 0, winreg.KEY_WRITE | VIEW) as k:
            winreg.DeleteValue(k, "V")
        expect("value deleted, key kept", registry_value_state(key, "V", expected, root),
               "missing-value")
        expect("  and the key still reads as present", registry_key_state(key, root), "present")

        write_registry(key, "V", "something else", root=root)
        expect("value rewritten", registry_value_state(key, "V", expected, root), "changed")

        with winreg.OpenKey(root, key, 0, winreg.KEY_WRITE | VIEW) as k:
            winreg.SetValueEx(k, "V", 0, winreg.REG_DWORD, 42)
        expect("value rewritten with a different type",
               registry_value_state(key, "V", expected, root), "changed")

        winreg.DeleteKeyEx(root, key, VIEW, 0)
        expect("key deleted", registry_key_state(key, root), "absent")
        expect("  and the value reports the missing key",
               registry_value_state(key, "V", expected, root), "missing-key")
    finally:
        try:
            winreg.DeleteKeyEx(root, key, VIEW, 0)
        except OSError:
            pass
    return failures


def selftest() -> int:
    """Prove neither the verdict nor the runner can be talked into a PASS."""
    logger.info("=== selftest: the verdict logic ===")
    good = Facts(remembered_path=r"C:\ProgramData\X\Vendor\logs",
                 intended_path=r"C:\ProgramData\X\Vendor\logs")
    failures = 0
    ok, _ = verdict(good)
    if ok:
        logger.success("a clean run passes")
    else:
        logger.error("a clean run does not pass - the checks are wrong")
        failures += 1

    broken = {
        "install fails": replace(good, install_rc=1603),
        "product never installed": replace(good, product_installed=False),
        "remembered path points elsewhere":
            replace(good, remembered_path=r"C:\ProgramData\Vendor\logs"),
        "remembered path empty": replace(good, remembered_path=""),
        "repair fails": replace(good, repair_rc=1603),
        "repair destroys runtime data": replace(good, target_file_after_repair=False),
        "repair destroys nested data": replace(good, nested_file_after_repair=False),
        "repair deletes the target registry VALUE":
            replace(good, registry_target_value_after_repair="missing-value"),
        "repair deletes the target registry KEY":
            replace(good, registry_target_value_after_repair="missing-key"),
        "repair rewrites the target registry value":
            replace(good, registry_target_value_after_repair="changed"),
        "uninstall fails": replace(good, uninstall_rc=1603),
        "target folder survives": replace(good, target_dir_after=True),
        "nested file survives (not recursive)": replace(good, nested_file_after=True),
        "target registry key survives": replace(good, registry_target_after="present"),
        "target registry key merely unreadable":
            replace(good, registry_target_after="error: PermissionError"),
        "PARENT sentinel deleted": replace(good, parent_sentinel_after=False),
        "PARENT sentinel emptied": replace(good, parent_sentinel_intact=False),
        "SIBLING sentinel deleted": replace(good, sibling_sentinel_after=False),
        "SIBLING sentinel emptied": replace(good, sibling_sentinel_intact=False),
        "sibling registry key deleted": replace(good, registry_sibling_after="absent"),
        "sibling registry key unreadable":
            replace(good, registry_sibling_after="error: PermissionError"),
        "sibling registry value changed": replace(good, registry_sibling_value="changed"),
        "sibling registry value deleted": replace(good, registry_sibling_value="missing-value"),
    }
    for name, facts in broken.items():
        ok, _ = verdict(facts)
        if ok:
            logger.error("FAIL {} still reports PASS", name)
            failures += 1
        else:
            logger.success("PASS {} is rejected", name)

    ok, _ = verdict(replace(good, remembered_path=r"c:\programdata\X\VENDOR\Logs"))
    if ok:
        logger.success("PASS a differently-cased but identical path is accepted")
    else:
        logger.error("FAIL a differently-cased identical path was rejected")
        failures += 1

    # The runner, which is where a failing package used to be swallowed.
    logger.info("=== selftest: the runner ===")
    entries = [{"key": "a"}, {"key": "b"}]
    if run_all(entries, lambda e: (True, good)):
        logger.success("PASS two passing packages give an overall pass")
    else:
        logger.error("FAIL two passing packages did not give an overall pass")
        failures += 1
    for failing in ("a", "b"):
        if run_all(entries, lambda e, f=failing: (e["key"] != f, good)):
            logger.error("FAIL a failing package ({}) still gives an overall pass", failing)
            failures += 1
        else:
            logger.success("PASS a failing package ({}) fails the whole run", failing)

    logger.info("=== selftest: the registry observers ===")
    failures += selftest_observations()

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


def read_registry_value(key: str, name: str, root: int = winreg.HKEY_LOCAL_MACHINE) -> str:
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEW) as k:
            value, _ = winreg.QueryValueEx(k, name)
            return str(value)
    except (FileNotFoundError, OSError):
        return ""


def registry_key_state(key: str, root: int = winreg.HKEY_LOCAL_MACHINE) -> str:
    """"present", "absent", or "error: ...".

    Only a missing key counts as absence. Catching every OSError and calling it "gone" would
    let an unreadable key satisfy "the target registry key was deleted", crediting the
    uninstall with something it may not have done.
    """
    try:
        winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEW).Close()
        return "present"
    except FileNotFoundError:
        return "absent"
    except OSError as err:
        return f"error: {type(err).__name__}: {err}"


def registry_value_state(key: str, name: str, expected: str,
                         root: int = winreg.HKEY_LOCAL_MACHINE) -> str:
    """"intact", "changed", "missing-value", "missing-key", or "error: ...".

    Checking that the KEY still exists is not the same as checking the value survived: a
    repair that deleted RuntimeValue while leaving its key behind passed the earlier version
    of this probe, because the message said "value" while the code asked about the key. The
    type is compared too - a value rewritten as a REG_DWORD is not the user's string.
    """
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEW) as k:
            try:
                value, kind = winreg.QueryValueEx(k, name)
            except FileNotFoundError:
                return "missing-value"
            if kind != winreg.REG_SZ or str(value) != expected:
                return "changed"
            return "intact"
    except FileNotFoundError:
        return "missing-key"
    except OSError as err:
        return f"error: {type(err).__name__}: {err}"


def write_registry(key: str, name: str, value: str,
                   root: int = winreg.HKEY_LOCAL_MACHINE) -> None:
    with winreg.CreateKeyEx(root, key, 0, winreg.KEY_WRITE | VIEW) as k:
        winreg.SetValueEx(k, name, 0, winreg.REG_SZ, value)


def file_intact(path: str, expected: str) -> bool:
    try:
        return Path(path).read_text(encoding="utf-8") == expected
    except OSError:
        return False


def seed(entry: dict) -> list[str]:
    """Create the runtime data a real application would have left behind, plus the sentinels
    that must survive untouched: one in the target's PARENT, one in a SIBLING directory, and a
    neighbouring registry key."""
    made = []
    for path_key, text in (("target_file", RUNTIME_TEXT), ("nested_file", RUNTIME_TEXT),
                           ("parent_sentinel", SENTINEL_TEXT), ("sibling_sentinel", SENTINEL_TEXT)):
        path = Path(entry[path_key])
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        made.append(str(path))
    write_registry(entry["registry_target"], "RuntimeValue", TARGET_VALUE)
    write_registry(entry["registry_sibling"], "KeepMe", SIBLING_VALUE)
    made.append("HKLM\\" + entry["registry_target"] + " (value)")
    made.append("HKLM\\" + entry["registry_sibling"] + " (sentinel key)")
    for m in made:
        logger.info("  seeded {}", m)
    return made


def probe(entry: dict) -> tuple[bool, Facts]:
    logger.info("=== {} ===", entry["title"])
    msi = HERE / entry["msi"]
    f = Facts(intended_path=entry["target_dir"])

    run(["msiexec", "/x", str(msi), "/qn"])   # a previous run must not decide this one

    logger.info("installing")
    f.install_rc = run(["msiexec", "/i", str(msi), "/qn",
                        "/l*v", str(HERE / f"install-{entry['key']}.log")])
    f.product_installed = Path(entry["installed_file"]).exists()
    f.remembered_path = read_registry_value(entry["remembered_key"], entry["remembered_name"])
    logger.info("  remembered path: {!r}", f.remembered_path)
    logger.info("  intended  path: {!r}", entry["target_dir"])

    logger.info("seeding runtime data and sentinels")
    f.seeded = seed(entry)

    logger.info("repairing - runtime data must survive")
    f.repair_rc = run(["msiexec", "/f", str(msi), "/qn",
                       "/l*v", str(HERE / f"repair-{entry['key']}.log")])
    f.target_file_after_repair = Path(entry["target_file"]).exists()
    f.nested_file_after_repair = Path(entry["nested_file"]).exists()
    f.registry_target_value_after_repair = registry_value_state(
        entry["registry_target"], "RuntimeValue", TARGET_VALUE)

    logger.info("uninstalling - the target must go, the neighbours must not")
    f.uninstall_rc = run(["msiexec", "/x", str(msi), "/qn",
                          "/l*v", str(HERE / f"uninstall-{entry['key']}.log")])

    f.target_dir_after = Path(entry["target_dir"]).exists()
    f.nested_file_after = Path(entry["nested_file"]).exists()
    f.registry_target_after = registry_key_state(entry["registry_target"])

    f.parent_sentinel_after = Path(entry["parent_sentinel"]).exists()
    f.parent_sentinel_intact = file_intact(entry["parent_sentinel"], SENTINEL_TEXT)
    f.sibling_sentinel_after = Path(entry["sibling_sentinel"]).exists()
    f.sibling_sentinel_intact = file_intact(entry["sibling_sentinel"], SENTINEL_TEXT)
    f.registry_sibling_after = registry_key_state(entry["registry_sibling"])
    f.registry_sibling_value = registry_value_state(
        entry["registry_sibling"], "KeepMe", SIBLING_VALUE)

    ok, results = verdict(f)
    for level, message in results:
        (logger.success if level == "PASS" else logger.error)("{} {}", level, message)
    return ok, f


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--selftest", action="store_true",
                        help="check the verdict and runner logic and exit; installs nothing")
    args = parser.parse_args()

    if hasattr(logger, "remove"):
        try:
            logger.remove()
            logger.add(sys.stderr, format="<level>{level: <8}</level> {message}", colorize=True)
            logger.add(HERE / "t5t7-vm.log", format="{time:HH:mm:ss} {level: <8} {message}", mode="w")
        except Exception:
            pass

    if args.selftest:
        return selftest()

    if not is_elevated():
        logger.error("run this from an ELEVATED shell - it installs per-machine MSIs")
        return 2

    manifest_path = HERE / "manifest.json"
    if not manifest_path.exists():
        logger.error("manifest.json is missing - copy the whole vm-payload folder over")
        return 2
    entries = json.loads(manifest_path.read_text(encoding="utf-8"))

    logger.warning("this installs MSIs and then DELETES directories under C:\\ProgramData "
                   "and keys under HKLM - roll the VM back afterwards")

    ok = run_all(entries, probe)

    if ok:
        logger.success("T5 + T7 PASSED - copy this output back; it closes #3 and #15's risk")
    else:
        logger.error("FAILED - copy this output back. If anything outside the named target "
                     "was removed, the tickets say to reopen rather than document it.")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
