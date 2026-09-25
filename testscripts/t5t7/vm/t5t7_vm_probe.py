# /// script
# requires-python = ">=3.11"
# dependencies = ["loguru"]
# ///
"""T5 + T7, machine side - what <remove-on-uninstall> deletes, and what it leaves alone.

Everything needed is in this folder: the MSIs built elsewhere, and manifest.json saying what
each scenario expects. No repo, no Go, no WiX and no msis on this machine.

  T5 / issue #15 - the cleanup elements written at TOP LEVEL. Until #15 that package failed
                   to build, so the components never installed and never ran; giving them a
                   feature is the moment the deletion became reachable.
  T7 / issue #3  - the same elements inside a <feature>, and the four cases that kept #3 open:
                   a MAJOR UPGRADE, the target folder already EMPTY, the target folder that
                   NEVER EXISTED, and the MINIMAL and SILENT-X86 templates.

This is a recursive delete of a directory the installer does not own, so the sentinels matter
as much as the deletions: the value of this mechanism over REMOVE_FOLDERS_ON_UNINSTALL is a
narrower blast radius, and that is the claim under test. A run that removes the target but
also touches the parent or a sibling is a FAIL, not a detail.

Where the runbook says "record what is observed" - what an uninstall does with an empty or
missing folder - the probe does not guess the right answer: it prints OBSERVED lines, collects
them at the end, and leaves the PASS/FAIL to what must hold whatever happens (every sentinel
survives, every uninstall succeeds). The major upgrade was recorded that way until its first
run showed it DELETED the application's data (2026-09-25, #76); the data must now survive it,
and that is judged.

    uv run t5t7_vm_probe.py --selftest   # checks the verdicts AND the runner; installs nothing
    uv run t5t7_vm_probe.py              # ELEVATED: the real thing

TAKE A VM SNAPSHOT FIRST. It installs and uninstalls per-machine MSIs, writes under
C:\\ProgramData and HKLM, and then deletes those directories and keys.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import shutil
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

# The registry view a package's HKLM keys live in: a 64-bit package's are in the 64-bit view, an
# x86 package's in the 32-bit one (WOW6432Node). The manifest says which, per scenario.
VIEWS = {64: winreg.KEY_WOW64_64KEY, 32: winreg.KEY_WOW64_32KEY}

INSTALL_OK = (0, 3010)
UNINSTALL_OK = (0, 1605, 3010)

# What the seeded artefacts contain, so "untouched" can mean untouched rather than merely
# still existing. An emptied sentinel file, or a sibling key stripped of its value, is damage
# to a neighbour's data and must fail.
SENTINEL_TEXT = "seeded sentinel - this must survive the uninstall\n"
RUNTIME_TEXT = "seeded runtime data - the application wrote this\n"
SIBLING_VALUE = "a neighbouring product's data"
TARGET_VALUE = "written by the application"

# Everything the runbook asks to be recorded rather than judged, gathered for the final summary.
OBSERVATIONS: list[str] = []


def observe(scenario: str, text: str) -> None:
    OBSERVATIONS.append(f"{scenario}: {text}")
    logger.warning("OBSERVED {}", text)


def is_elevated() -> bool:
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


# --- the verdicts: pure functions of what was observed, so --selftest can exercise them ---

class Checks:
    def __init__(self) -> None:
        self.ok = True
        self.results: list[tuple[str, str]] = []

    def check(self, cond: bool, good: str, bad: str) -> None:
        if cond:
            self.results.append(("PASS", good))
        else:
            self.results.append(("FAIL", bad))
            self.ok = False


@dataclass
class Neighbours:
    """What must survive every scenario: the sentinels and the sibling registry key."""
    parent_sentinel_after: bool = True
    parent_sentinel_intact: bool = True
    sibling_sentinel_after: bool = True
    sibling_sentinel_intact: bool = True
    registry_sibling_after: str = "present"
    registry_sibling_value: str = "intact"


def check_neighbours(c: Checks, n: Neighbours, when: str) -> None:
    c.check(n.parent_sentinel_after, f"{when}: the sentinel in the PARENT directory is still there",
            f"{when}: the parent directory's sentinel was DELETED - the blast radius is too wide; "
            "stop and reopen the ticket")
    c.check(n.parent_sentinel_intact, "  and its contents are unchanged",
            f"{when}: the parent sentinel's contents were CHANGED - the neighbour's data was damaged")
    c.check(n.sibling_sentinel_after, f"{when}: the sentinel in the SIBLING directory is still there",
            f"{when}: a sibling directory's sentinel was DELETED - the blast radius is too wide; "
            "stop and reopen the ticket")
    c.check(n.sibling_sentinel_intact, "  and its contents are unchanged",
            f"{when}: the sibling sentinel's contents were CHANGED - the neighbour's data was damaged")
    c.check(n.registry_sibling_after == "present", f"{when}: the sibling registry key is still there",
            f"{when}: the sibling registry key is {n.registry_sibling_after} - a neighbouring key "
            "was affected")
    c.check(n.registry_sibling_value == "intact", "  and its value is unchanged",
            f"{when}: the sibling registry key's value was {n.registry_sibling_value}")


def check_path(c: Checks, remembered: str, intended: str, when: str) -> None:
    # Both tickets insist on equality: "the stored path is absolute" would pass while pointing
    # somewhere else, and somewhere else is what a recursive delete must never be aimed at.
    c.check(bool(remembered) and remembered.lower() == intended.lower(),
            f"{when}: the remembered path is exactly {intended}",
            f"{when}: the remembered path is {remembered!r}, expected {intended!r} - the uninstall "
            "would delete the wrong directory")


@dataclass
class Facts:
    """Everything observed for one core scenario: install, seed, repair, uninstall.

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

    neighbours: Neighbours = field(default_factory=Neighbours)
    seeded: list[str] = field(default_factory=list)


def verdict(f: Facts) -> tuple[bool, list[tuple[str, str]]]:
    """PASS/FAIL from observations alone. Pure, so --selftest can prove that a damaged
    sentinel - the failure that matters here - can never produce a PASS."""
    c = Checks()
    c.check(f.install_rc in INSTALL_OK, "install succeeded", f"install returned {f.install_rc}")
    c.check(f.product_installed, "the product installed",
            "the product did not install, so nothing below was exercised")
    check_path(c, f.remembered_path, f.intended_path, "after install")

    c.check(f.repair_rc in INSTALL_OK, "repair succeeded", f"repair returned {f.repair_rc}")
    c.check(f.target_file_after_repair and f.nested_file_after_repair,
            "runtime files survived the repair",
            "repair destroyed runtime files - a repair must never be a data-loss event")
    # Checked before the uninstall on purpose: if a repair deleted the key, its absence
    # afterwards would otherwise be credited to the uninstall.
    c.check(f.registry_target_value_after_repair == "intact",
            "the runtime registry value survived the repair unchanged",
            f"the seeded registry value was {f.registry_target_value_after_repair} after the "
            "repair - a repair must not touch the user's data")

    c.check(f.uninstall_rc in UNINSTALL_OK, "uninstall succeeded", f"uninstall returned {f.uninstall_rc}")
    c.check(not f.target_dir_after, "the target folder is gone",
            "the target folder survived uninstall - the cleanup did not run")
    c.check(not f.nested_file_after, "the nested file is gone with it",
            "a file in a nested subfolder survived - the removal was not recursive")
    c.check(f.registry_target_after == "absent", "the target registry key is gone",
            f"the target registry key is {f.registry_target_after} - only a key that is "
            "genuinely missing counts as deleted")
    check_neighbours(c, f.neighbours, "after uninstall")
    return c.ok, c.results


@dataclass
class UpgradeFacts:
    """A major upgrade 1.0.0 -> 1.0.1 of a package carrying the cleanup, then its uninstall."""

    old_install_rc: int = 0
    old_version_installed: str = "1.0.0"
    expected_old: str = "1.0.0"
    remembered_path: str = ""
    intended_path: str = ""

    new_install_rc: int = 0
    new_version_installed: str = "1.0.1"
    expected_new: str = "1.0.1"
    remembered_after_upgrade: str = ""
    # What the upgrade did to the application's data. Recorded as OBSERVED on the first VM run
    # (2026-09-25: DELETED); since #76 the data must survive, and it is judged.
    target_file_after_upgrade: bool = True
    nested_file_after_upgrade: bool = True
    registry_value_after_upgrade: str = "intact"
    neighbours_after_upgrade: Neighbours = field(default_factory=Neighbours)

    uninstall_rc: int = 0
    target_dir_after: bool = False
    nested_file_after: bool = False
    registry_target_after: str = "absent"
    neighbours_after_uninstall: Neighbours = field(default_factory=Neighbours)


def verdict_upgrade(f: UpgradeFacts) -> tuple[bool, list[tuple[str, str]], list[str]]:
    c = Checks()
    c.check(f.old_install_rc in INSTALL_OK, f"{f.expected_old} installed",
            f"installing {f.expected_old} returned {f.old_install_rc}")
    c.check(f.old_version_installed == f.expected_old, f"  and {f.expected_old} is what is installed",
            f"after installing {f.expected_old}, version.txt reads {f.old_version_installed!r}")
    check_path(c, f.remembered_path, f.intended_path, f"after installing {f.expected_old}")

    c.check(f.new_install_rc in INSTALL_OK, f"the major upgrade to {f.expected_new} succeeded",
            f"installing {f.expected_new} over {f.expected_old} returned {f.new_install_rc}")
    c.check(f.new_version_installed == f.expected_new, f"  and {f.expected_new} is what is installed now",
            f"after the upgrade, version.txt reads {f.new_version_installed!r} - the upgrade did not happen")
    check_path(c, f.remembered_after_upgrade, f.intended_path, "after the upgrade")
    # #76: an update must not delete what the cleanup names - it runs on a real uninstall only.
    c.check(f.target_file_after_upgrade and f.nested_file_after_upgrade,
            "the application's files survived the upgrade",
            "the major upgrade DELETED the application's files in the target folder "
            f"(runtime.log {'kept' if f.target_file_after_upgrade else 'gone'}, "
            f"deep\\nested.txt {'kept' if f.nested_file_after_upgrade else 'gone'}) - #76 is not fixed")
    c.check(f.registry_value_after_upgrade == "intact",
            "the application's registry value survived the upgrade unchanged",
            f"after the major upgrade the target registry value is {f.registry_value_after_upgrade} "
            "- #76 is not fixed")
    # Nor must the upgrade reach beyond the application's own folder.
    check_neighbours(c, f.neighbours_after_upgrade, "after the upgrade")

    c.check(f.uninstall_rc in UNINSTALL_OK, f"uninstalling {f.expected_new} succeeded",
            f"uninstalling {f.expected_new} returned {f.uninstall_rc}")
    c.check(not f.target_dir_after, "the target folder is gone after the uninstall",
            "the target folder survived the uninstall of the upgraded product - the cleanup did "
            "not run")
    c.check(not f.nested_file_after, "  and the nested file with it",
            "a nested file survived the uninstall of the upgraded product")
    c.check(f.registry_target_after == "absent", "the target registry key is gone",
            f"the target registry key is {f.registry_target_after}")
    check_neighbours(c, f.neighbours_after_uninstall, "after the uninstall")
    return c.ok, c.results, []


@dataclass
class EdgeFacts:
    """Uninstall with the target folder already EMPTY, or with it NEVER CREATED."""

    kind: str = "empty"
    install_rc: int = 0
    product_installed: bool = True
    remembered_path: str = ""
    intended_path: str = ""
    target_dir_after_install: bool = True

    uninstall_rc: int = 0
    product_removed: bool = True
    target_dir_after: bool = False
    registry_target_after: str = "absent"
    neighbours: Neighbours = field(default_factory=Neighbours)


def verdict_edge(f: EdgeFacts) -> tuple[bool, list[tuple[str, str]], list[str]]:
    c = Checks()
    c.check(f.install_rc in INSTALL_OK, "install succeeded", f"install returned {f.install_rc}")
    c.check(f.product_installed, "the product installed", "the product did not install")
    check_path(c, f.remembered_path, f.intended_path, "after install")
    # A package that cannot be uninstalled because its application never ran, or left nothing
    # behind, would strand the customer: that is a failure, not an observation.
    c.check(f.uninstall_rc in UNINSTALL_OK, "uninstall succeeded",
            f"uninstall returned {f.uninstall_rc} - the product cannot be removed when the folder "
            f"is {'empty' if f.kind == 'empty' else 'missing'}")
    c.check(f.product_removed, "the product is gone", "the product's own files are still installed")
    check_neighbours(c, f.neighbours, "after uninstall")
    observations = [
        f"the install {'created' if f.target_dir_after_install else 'did NOT create'} the target folder",
        f"with the folder {'empty' if f.kind == 'empty' else 'missing'} at uninstall, it is "
        f"{'still there' if f.target_dir_after else 'gone'} afterwards; the target registry key "
        f"is {f.registry_target_after}",
    ]
    return c.ok, c.results, observations


def run_all(entries: list[dict], probe_fn: Callable[[dict], tuple[bool, object]]) -> bool:
    """Aggregate per-scenario results. Split out and injectable because the first version
    tested the (ok, facts) TUPLE, which is always truthy - a failing package still reported
    an overall PASS. --selftest exercises this with a failing probe."""
    overall = True
    for entry in entries:
        ok, _ = probe_fn(entry)
        if not ok:
            overall = False
    return overall


# --- the selftest ---

def selftest_observations() -> int:
    """Exercise the registry OBSERVERS against a real key, not against made-up Facts, in both
    views. Uses HKCU, so it needs no elevation and touches nothing the probe cares about."""
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

    for view in (64, 32):
        mask = VIEWS[view]
        try:
            write_registry(key, "V", expected, view, root=root)
            expect(f"[{view}-bit] value present and equal",
                   registry_value_state(key, "V", expected, view, root), "intact")
            expect(f"[{view}-bit] key present", registry_key_state(key, view, root), "present")
            # The exact case that once slipped through: the value is deleted, the key remains.
            with winreg.OpenKey(root, key, 0, winreg.KEY_WRITE | mask) as k:
                winreg.DeleteValue(k, "V")
            expect(f"[{view}-bit] value deleted, key kept",
                   registry_value_state(key, "V", expected, view, root), "missing-value")
            write_registry(key, "V", "something else", view, root=root)
            expect(f"[{view}-bit] value rewritten",
                   registry_value_state(key, "V", expected, view, root), "changed")
            with winreg.OpenKey(root, key, 0, winreg.KEY_WRITE | mask) as k:
                winreg.SetValueEx(k, "V", 0, winreg.REG_DWORD, 42)
            expect(f"[{view}-bit] value rewritten with a different type",
                   registry_value_state(key, "V", expected, view, root), "changed")
            winreg.DeleteKeyEx(root, key, mask, 0)
            expect(f"[{view}-bit] key deleted", registry_key_state(key, view, root), "absent")
            expect(f"[{view}-bit]   and the value reports the missing key",
                   registry_value_state(key, "V", expected, view, root), "missing-key")
        finally:
            try:
                winreg.DeleteKeyEx(root, key, mask, 0)
            except OSError:
                pass
    return failures


def selftest_edge_preparation() -> int:
    """Run probe_edge for real in a temporary directory, both kinds.

    The empty and never scenarios DELETE files to prepare their state, and that must be shown
    to establish it - the target empty, or absent, at the moment of the uninstall - without
    touching the neighbours. So the filesystem work is real; only msiexec and the registry are
    replaced: the fake install creates the product file and the target folder with a leftover
    in it, the fake uninstall records what it found and then removes the target, as the
    cleanup would. Needs no elevation and touches nothing outside the temporary directory.
    """
    import tempfile

    failures = 0

    def expect(label: str, cond: bool) -> None:
        nonlocal failures
        if cond:
            logger.success("PASS {}", label)
        else:
            logger.error("FAIL {}", label)
            failures += 1

    saved = {n: globals()[n] for n in ("msiexec", "write_registry", "registry_key_state",
                                       "registry_value_state", "read_registry_value")}
    observations_before = len(OBSERVATIONS)
    try:
        for kind in ("empty", "never"):
            root = Path(tempfile.mkdtemp(prefix=f"t5t7-{kind}-"))
            appdata = root / "ProgramData" / "Probe"
            target = appdata / "Vendor" / "logs"
            entry = {
                "kind": kind, "key": f"selftest-{kind}", "msi": "selftest.msi", "view": 64,
                "target_dir": str(target), "installed_file": str(root / "Program Files" / "Probe" / "readme.txt"),
                "parent_sentinel": str(appdata / "Vendor" / "keep-me.txt"),
                "sibling_sentinel": str(appdata / "Vendor" / "other" / "keep-me.txt"),
                "remembered_key": "k", "remembered_name": "n",
                "registry_target": "target", "registry_sibling": "sibling",
            }
            # A leftover from an earlier run, which the probe must clear before it installs.
            (target / "stale").mkdir(parents=True)
            (target / "stale" / "old.log").write_text("from an earlier run", encoding="utf-8")

            registry: dict[str, str] = {}
            seen: dict[str, object] = {}

            def fake_msiexec(action: str, msi: Path, log: str | None, entry=entry, seen=seen) -> int:
                installed, tgt = Path(entry["installed_file"]), Path(entry["target_dir"])
                if action == "/i":
                    installed.parent.mkdir(parents=True, exist_ok=True)
                    installed.write_text("installed", encoding="utf-8")
                    tgt.mkdir(parents=True, exist_ok=True)   # <create-folder>
                    (tgt / "created-by-the-app.txt").write_text("x", encoding="utf-8")
                elif action == "/x" and log:                  # the uninstall under test
                    seen["exists"] = tgt.exists()
                    seen["contents"] = sorted(p.name for p in tgt.iterdir()) if tgt.exists() else None
                    seen["parent"] = file_intact(entry["parent_sentinel"], SENTINEL_TEXT)
                    seen["sibling"] = file_intact(entry["sibling_sentinel"], SENTINEL_TEXT)
                    installed.unlink(missing_ok=True)
                    shutil.rmtree(tgt, ignore_errors=True)
                return 0

            globals()["msiexec"] = fake_msiexec
            globals()["write_registry"] = lambda key, name, value, view, root=None, r=registry: r.__setitem__(f"{key}\\{name}", value)
            globals()["registry_key_state"] = lambda key, view, root=None, r=registry: (
                "present" if any(k.startswith(key + "\\") for k in r) else "absent")
            globals()["registry_value_state"] = lambda key, name, expected, view, root=None, r=registry: (
                "intact" if r.get(f"{key}\\{name}") == expected else "missing-value")
            globals()["read_registry_value"] = lambda key, name, view, root=None, t=str(target): t

            ok, facts = probe_edge(entry)
            expect(f"[{kind}] the probe passes against the fake installer", ok)
            if kind == "empty":
                expect("[empty] the target existed and was EMPTY at the uninstall",
                       seen.get("exists") is True and seen.get("contents") == [])
            else:
                expect("[never] the target was ABSENT at the uninstall", seen.get("exists") is False)
            expect(f"[{kind}] the earlier run's leftover was cleared", not (target / "stale").exists())
            expect(f"[{kind}] the PARENT sentinel was intact at the uninstall", seen.get("parent") is True)
            expect(f"[{kind}] the SIBLING sentinel was intact at the uninstall", seen.get("sibling") is True)
            expect(f"[{kind}] and both are still intact afterwards",
                   file_intact(entry["parent_sentinel"], SENTINEL_TEXT)
                   and file_intact(entry["sibling_sentinel"], SENTINEL_TEXT))
            shutil.rmtree(root, ignore_errors=True)
    finally:
        globals().update(saved)
        del OBSERVATIONS[observations_before:]
    return failures


def selftest() -> int:
    """Prove neither the verdicts nor the runner can be talked into a PASS."""
    failures = 0

    def rejects(name: str, ok: bool) -> None:
        nonlocal failures
        if ok:
            logger.error("FAIL {} still reports PASS", name)
            failures += 1
        else:
            logger.success("PASS {} is rejected", name)

    def accepts(name: str, ok: bool) -> None:
        nonlocal failures
        if ok:
            logger.success("PASS {}", name)
        else:
            logger.error("FAIL {} - a clean run was rejected", name)
            failures += 1

    path = r"C:\ProgramData\X\Vendor\logs"
    damaged = {
        "PARENT sentinel deleted": Neighbours(parent_sentinel_after=False),
        "PARENT sentinel emptied": Neighbours(parent_sentinel_intact=False),
        "SIBLING sentinel deleted": Neighbours(sibling_sentinel_after=False),
        "SIBLING sentinel emptied": Neighbours(sibling_sentinel_intact=False),
        "sibling registry key deleted": Neighbours(registry_sibling_after="absent"),
        "sibling registry key unreadable": Neighbours(registry_sibling_after="error: PermissionError"),
        "sibling registry value changed": Neighbours(registry_sibling_value="changed"),
        "sibling registry value deleted": Neighbours(registry_sibling_value="missing-value"),
    }

    logger.info("=== selftest: the core verdict ===")
    good = Facts(remembered_path=path, intended_path=path)
    accepts("a clean core run passes", verdict(good)[0])
    broken = {
        "install fails": replace(good, install_rc=1603),
        "product never installed": replace(good, product_installed=False),
        "remembered path points elsewhere": replace(good, remembered_path=r"C:\ProgramData\Vendor\logs"),
        "remembered path empty": replace(good, remembered_path=""),
        "repair fails": replace(good, repair_rc=1603),
        "repair destroys runtime data": replace(good, target_file_after_repair=False),
        "repair destroys nested data": replace(good, nested_file_after_repair=False),
        "repair deletes the target registry VALUE": replace(good, registry_target_value_after_repair="missing-value"),
        "repair deletes the target registry KEY": replace(good, registry_target_value_after_repair="missing-key"),
        "repair rewrites the target registry value": replace(good, registry_target_value_after_repair="changed"),
        "uninstall fails": replace(good, uninstall_rc=1603),
        "target folder survives": replace(good, target_dir_after=True),
        "nested file survives (not recursive)": replace(good, nested_file_after=True),
        "target registry key survives": replace(good, registry_target_after="present"),
        "target registry key merely unreadable": replace(good, registry_target_after="error: PermissionError"),
        **{name: replace(good, neighbours=n) for name, n in damaged.items()},
    }
    for name, facts in broken.items():
        rejects(name, verdict(facts)[0])
    accepts("a differently-cased but identical path is accepted",
            verdict(replace(good, remembered_path=r"c:\programdata\X\VENDOR\Logs"))[0])

    logger.info("=== selftest: the upgrade verdict ===")
    up = UpgradeFacts(remembered_path=path, intended_path=path, remembered_after_upgrade=path)
    accepts("a clean upgrade run passes", verdict_upgrade(up)[0])
    for name, facts in {
        # #76: what the first VM run observed, and what the fix must prevent.
        "the upgrade deletes the application's files": replace(up, target_file_after_upgrade=False,
                                                               nested_file_after_upgrade=False),
        "the upgrade deletes only the nested file": replace(up, nested_file_after_upgrade=False),
        "the upgrade deletes the application's registry key": replace(up, registry_value_after_upgrade="missing-key"),
        "the upgrade deletes the application's registry value": replace(up, registry_value_after_upgrade="missing-value"),
        "the old version fails to install": replace(up, old_install_rc=1603),
        "the old version is not what got installed": replace(up, old_version_installed=""),
        "the upgrade fails": replace(up, new_install_rc=1603),
        "the upgrade did not replace the old version": replace(up, new_version_installed="1.0.0"),
        "the remembered path is lost by the upgrade": replace(up, remembered_after_upgrade=""),
        "the uninstall after the upgrade fails": replace(up, uninstall_rc=1603),
        "the target survives the uninstall after the upgrade": replace(up, target_dir_after=True),
        **{f"after the upgrade: {n}": replace(up, neighbours_after_upgrade=d) for n, d in damaged.items()},
        **{f"after the uninstall: {n}": replace(up, neighbours_after_uninstall=d) for n, d in damaged.items()},
    }.items():
        rejects(name, verdict_upgrade(facts)[0])

    logger.info("=== selftest: the empty / never-existed verdict ===")
    for kind in ("empty", "never"):
        edge = EdgeFacts(kind=kind, remembered_path=path, intended_path=path)
        accepts(f"a clean {kind} run passes", verdict_edge(edge)[0])
        accepts(f"[{kind}] a target folder that stays is recorded, not failed",
                verdict_edge(replace(edge, target_dir_after=True))[0])
        for name, facts in {
            f"[{kind}] the uninstall fails": replace(edge, uninstall_rc=1603),
            f"[{kind}] the product stays installed": replace(edge, product_removed=False),
            f"[{kind}] the install fails": replace(edge, install_rc=1603),
            **{f"[{kind}] {n}": replace(edge, neighbours=d) for n, d in damaged.items()},
        }.items():
            rejects(name, verdict_edge(facts)[0])

    # The runner, which is where a failing package used to be swallowed.
    logger.info("=== selftest: the runner ===")
    entries = [{"key": "a"}, {"key": "b"}]
    accepts("two passing scenarios give an overall pass", run_all(entries, lambda e: (True, good)))
    for failing in ("a", "b"):
        rejects(f"a failing scenario ({failing}) fails the whole run",
                run_all(entries, lambda e, f=failing: (e["key"] != f, good)))

    logger.info("=== selftest: the registry observers ===")
    failures += selftest_observations()

    logger.info("=== selftest: the empty / never preparation, executed on a temporary directory ===")
    failures += selftest_edge_preparation()

    if failures:
        logger.error("selftest failed with {} problem(s)", failures)
        return 1
    logger.success("selftest passed: every failure mode is rejected")
    return 0


# --- observing the machine ---

def run(cmd: list[str]) -> int:
    logger.debug("$ {}", " ".join(str(c) for c in cmd))
    proc = subprocess.run(cmd, capture_output=True, text=True, errors="replace")
    logger.debug("  -> {}", proc.returncode)
    return proc.returncode


def msiexec(action: str, msi: Path, log: str | None) -> int:
    cmd = ["msiexec", action, str(msi), "/qn"]
    if log:
        cmd += ["/l*v", str(HERE / log)]
    return run(cmd)


def read_registry_value(key: str, name: str, view: int, root: int = winreg.HKEY_LOCAL_MACHINE) -> str:
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEWS[view]) as k:
            value, _ = winreg.QueryValueEx(k, name)
            return str(value)
    except (FileNotFoundError, OSError):
        return ""


def registry_key_state(key: str, view: int, root: int = winreg.HKEY_LOCAL_MACHINE) -> str:
    """"present", "absent", or "error: ...".

    Only a missing key counts as absence. Catching every OSError and calling it "gone" would
    let an unreadable key satisfy "the target registry key was deleted", crediting the
    uninstall with something it may not have done.
    """
    try:
        winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEWS[view]).Close()
        return "present"
    except FileNotFoundError:
        return "absent"
    except OSError as err:
        return f"error: {type(err).__name__}: {err}"


def registry_value_state(key: str, name: str, expected: str, view: int,
                         root: int = winreg.HKEY_LOCAL_MACHINE) -> str:
    """"intact", "changed", "missing-value", "missing-key", or "error: ...".

    Checking that the KEY still exists is not the same as checking the value survived, and the
    type is compared too - a value rewritten as a REG_DWORD is not the user's string.
    """
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEWS[view]) as k:
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


def write_registry(key: str, name: str, value: str, view: int,
                   root: int = winreg.HKEY_LOCAL_MACHINE) -> None:
    with winreg.CreateKeyEx(root, key, 0, winreg.KEY_WRITE | VIEWS[view]) as k:
        winreg.SetValueEx(k, name, 0, winreg.REG_SZ, value)


def file_intact(path: str, expected: str) -> bool:
    try:
        return Path(path).read_text(encoding="utf-8") == expected
    except OSError:
        return False


def read_text(path: str) -> str:
    try:
        return Path(path).read_text(encoding="utf-8").strip()
    except OSError:
        return ""


def seed_file(path: str, text: str) -> str:
    p = Path(path)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text, encoding="utf-8")
    return str(p)


def seed_runtime(entry: dict) -> list[str]:
    """The data a real application would have left in its folder and registry key."""
    made = [seed_file(entry["target_file"], RUNTIME_TEXT), seed_file(entry["nested_file"], RUNTIME_TEXT)]
    write_registry(entry["registry_target"], "RuntimeValue", TARGET_VALUE, entry["view"])
    made.append(f"HKLM\\{entry['registry_target']} (value, {entry['view']}-bit view)")
    return made


def seed_neighbours(entry: dict) -> list[str]:
    """What must survive untouched: a file in the target's PARENT, one in a SIBLING directory,
    and a neighbouring registry key."""
    made = [seed_file(entry["parent_sentinel"], SENTINEL_TEXT),
            seed_file(entry["sibling_sentinel"], SENTINEL_TEXT)]
    write_registry(entry["registry_sibling"], "KeepMe", SIBLING_VALUE, entry["view"])
    made.append(f"HKLM\\{entry['registry_sibling']} (sentinel key, {entry['view']}-bit view)")
    return made


def report_seeded(made: list[str]) -> None:
    for m in made:
        logger.info("  seeded {}", m)


def observe_neighbours(entry: dict) -> Neighbours:
    return Neighbours(
        parent_sentinel_after=Path(entry["parent_sentinel"]).exists(),
        parent_sentinel_intact=file_intact(entry["parent_sentinel"], SENTINEL_TEXT),
        sibling_sentinel_after=Path(entry["sibling_sentinel"]).exists(),
        sibling_sentinel_intact=file_intact(entry["sibling_sentinel"], SENTINEL_TEXT),
        registry_sibling_after=registry_key_state(entry["registry_sibling"], entry["view"]),
        registry_sibling_value=registry_value_state(entry["registry_sibling"], "KeepMe", SIBLING_VALUE,
                                                    entry["view"]),
    )


def remembered(entry: dict) -> str:
    return read_registry_value(entry["remembered_key"], entry["remembered_name"], entry["view"])


def report(ok: bool, results: list[tuple[str, str]], observations: list[str], key: str) -> None:
    for level, message in results:
        (logger.success if level == "PASS" else logger.error)("{} {}", level, message)
    for o in observations:
        observe(key, o)


# --- the scenarios ---

def probe_core(entry: dict) -> tuple[bool, Facts]:
    """Install, seed, repair, uninstall: the target goes, the neighbours stay."""
    msi = HERE / entry["msi"]
    f = Facts(intended_path=entry["target_dir"])
    msiexec("/x", msi, None)   # a previous run must not decide this one

    logger.info("installing")
    f.install_rc = msiexec("/i", msi, f"install-{entry['key']}.log")
    f.product_installed = Path(entry["installed_file"]).exists()
    f.remembered_path = remembered(entry)
    logger.info("  remembered path: {!r}", f.remembered_path)
    logger.info("  intended  path: {!r}", entry["target_dir"])

    logger.info("seeding runtime data and sentinels")
    f.seeded = seed_runtime(entry) + seed_neighbours(entry)
    report_seeded(f.seeded)

    logger.info("repairing - runtime data must survive")
    f.repair_rc = msiexec("/f", msi, f"repair-{entry['key']}.log")
    f.target_file_after_repair = Path(entry["target_file"]).exists()
    f.nested_file_after_repair = Path(entry["nested_file"]).exists()
    f.registry_target_value_after_repair = registry_value_state(
        entry["registry_target"], "RuntimeValue", TARGET_VALUE, entry["view"])

    logger.info("uninstalling - the target must go, the neighbours must not")
    f.uninstall_rc = msiexec("/x", msi, f"uninstall-{entry['key']}.log")
    f.target_dir_after = Path(entry["target_dir"]).exists()
    f.nested_file_after = Path(entry["nested_file"]).exists()
    f.registry_target_after = registry_key_state(entry["registry_target"], entry["view"])
    f.neighbours = observe_neighbours(entry)

    ok, results = verdict(f)
    report(ok, results, [], entry["key"])
    return ok, f


def probe_upgrade(entry: dict) -> tuple[bool, UpgradeFacts]:
    """Install the old version, seed, upgrade to the new one - the application's data must
    survive it (#76) - then re-seed and uninstall the new version, which must remove it."""
    old, new = HERE / entry["msi_old"], HERE / entry["msi_new"]
    f = UpgradeFacts(intended_path=entry["target_dir"], expected_old=entry["version_old"],
                     expected_new=entry["version_new"])
    msiexec("/x", new, None)
    msiexec("/x", old, None)

    logger.info("installing {}", entry["version_old"])
    f.old_install_rc = msiexec("/i", old, f"install-{entry['key']}-{entry['version_old']}.log")
    f.old_version_installed = read_text(entry["version_file"])
    f.remembered_path = remembered(entry)
    logger.info("  installed version: {!r}; remembered path: {!r}", f.old_version_installed, f.remembered_path)

    logger.info("seeding runtime data and sentinels")
    report_seeded(seed_runtime(entry) + seed_neighbours(entry))

    logger.info("MAJOR UPGRADE to {} - the application's data must survive it (#76)", entry["version_new"])
    f.new_install_rc = msiexec("/i", new, f"upgrade-{entry['key']}-{entry['version_new']}.log")
    f.new_version_installed = read_text(entry["version_file"])
    f.remembered_after_upgrade = remembered(entry)
    f.target_file_after_upgrade = Path(entry["target_file"]).exists()
    f.nested_file_after_upgrade = Path(entry["nested_file"]).exists()
    f.registry_value_after_upgrade = registry_value_state(
        entry["registry_target"], "RuntimeValue", TARGET_VALUE, entry["view"])
    f.neighbours_after_upgrade = observe_neighbours(entry)
    logger.info("  installed version: {!r}; remembered path: {!r}", f.new_version_installed,
                f.remembered_after_upgrade)

    # Re-seed, so the uninstall of the new version is tested whatever the upgrade did.
    logger.info("re-seeding the runtime data, then uninstalling {}", entry["version_new"])
    report_seeded(seed_runtime(entry))
    f.uninstall_rc = msiexec("/x", new, f"uninstall-{entry['key']}-{entry['version_new']}.log")
    f.target_dir_after = Path(entry["target_dir"]).exists()
    f.nested_file_after = Path(entry["nested_file"]).exists()
    f.registry_target_after = registry_key_state(entry["registry_target"], entry["view"])
    f.neighbours_after_uninstall = observe_neighbours(entry)

    ok, results, observations = verdict_upgrade(f)
    report(ok, results, observations, entry["key"])
    return ok, f


def probe_edge(entry: dict) -> tuple[bool, EdgeFacts]:
    """Uninstall with the target folder EMPTY ("empty") or REMOVED ("never", as if the
    application never ran). Nothing is seeded in the target; the neighbours are."""
    msi = HERE / entry["msi"]
    f = EdgeFacts(kind=entry["kind"], intended_path=entry["target_dir"])
    msiexec("/x", msi, None)
    target = Path(entry["target_dir"])
    if target.exists():  # leftovers of an earlier scenario must not decide this one
        shutil.rmtree(target)

    logger.info("installing")
    f.install_rc = msiexec("/i", msi, f"install-{entry['key']}.log")
    f.product_installed = Path(entry["installed_file"]).exists()
    f.remembered_path = remembered(entry)
    f.target_dir_after_install = target.exists()
    logger.info("  remembered path: {!r}; the target folder exists after install: {}",
                f.remembered_path, f.target_dir_after_install)

    logger.info("seeding the sentinels only")
    report_seeded(seed_neighbours(entry))
    if entry["kind"] == "empty":
        target.mkdir(parents=True, exist_ok=True)
        for child in target.iterdir():
            shutil.rmtree(child) if child.is_dir() else child.unlink()
        logger.info("  the target folder exists and is empty")
    else:
        if target.exists():
            shutil.rmtree(target)
        logger.info("  the target folder has been removed, as if the application never ran")

    logger.info("uninstalling")
    f.uninstall_rc = msiexec("/x", msi, f"uninstall-{entry['key']}.log")
    f.product_removed = not Path(entry["installed_file"]).exists()
    f.target_dir_after = target.exists()
    f.registry_target_after = registry_key_state(entry["registry_target"], entry["view"])
    f.neighbours = observe_neighbours(entry)

    ok, results, observations = verdict_edge(f)
    report(ok, results, observations, entry["key"])
    return ok, f


PROBES: dict[str, Callable[[dict], tuple[bool, object]]] = {
    "core": probe_core, "upgrade": probe_upgrade, "empty": probe_edge, "never": probe_edge,
}


def probe(entry: dict) -> tuple[bool, object]:
    logger.info("=== {} ===", entry["title"])
    return PROBES[entry["kind"]](entry)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--selftest", action="store_true",
                        help="check the verdicts and the runner and exit; installs nothing")
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
    unknown = sorted({e.get("kind", "?") for e in entries} - PROBES.keys())
    if unknown:
        logger.error("manifest.json has scenarios this probe does not know: {} - copy the "
                     "whole vm-payload folder over, script and manifest together", unknown)
        return 2

    logger.warning("this installs MSIs and then DELETES directories under C:\\ProgramData "
                   "and keys under HKLM - roll the VM back afterwards")

    ok = run_all(entries, probe)

    logger.info("=== what the runbook asks to be RECORDED (todo-testme.md T7) ===")
    for o in OBSERVATIONS:
        logger.warning("OBSERVED {}", o)

    if ok:
        logger.success("T5 + T7 PASSED - copy this whole output back, the OBSERVED lines included")
    else:
        logger.error("FAILED - copy this whole output back. If anything outside the named target "
                     "was removed, the tickets say to reopen rather than document it.")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
