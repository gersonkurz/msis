"""#95, VM side - silent bundles install and uninstall (D31). Run ELEVATED; take a VM snapshot first.

For each bundle (explicit, auto) and each unattended mode (/quiet, /passive):
  install   exit 0; the bundle and its MSI registered; the MSI's file installed with its content;
            the MSI ran with no UI of its own (UILevel 2 in its log); for the auto bundle, the VC++
            2022 redistributable detected Present and never executed
  uninstall (through the bundle) exit 0; neither registered; the file gone
The VC++ runtime must still be registered afterwards.

The probe refuses to start if anything of its own is on the machine, and always ends by
uninstalling its bundles and MSIs and removing the folders under MsisProbe95, and the registry
keys up to Software\\msis, that are then empty. A failed cleanup fails the run.

    python t95_vm_probe.py --selftest   # checks the parsing and the cleanup; installs nothing
    python t95_vm_probe.py              # ELEVATED, unattended
    python t95_vm_probe.py --cleanup    # ELEVATED: only the final cleanup, after an interrupted run
"""

from __future__ import annotations

import argparse
import ctypes
import json
import os
import re
import subprocess
import sys
import tempfile
import winreg
from pathlib import Path

HERE = Path(__file__).resolve().parent
LOGS = HERE / "logs"
OK = (0, 3010)
UNINSTALL = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall"
VCRT = r"SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64"


# ---- pure parts (exercised by --selftest) ----

def detected(log: str) -> dict[str, str]:
    return dict(re.findall(r"Detected package: (\w+), state: (\w+)", log))


def executed(log: str) -> set[str]:
    return set(re.findall(r"Applying execute package: (\w+),", log))


def ui_levels(msi_log: str) -> set[str]:
    return set(re.findall(r"Property\([SC]\): UILevel = (\d+)", msi_log))


# ---- the machine ----

BASES: dict[str, Path] | None = None


def resolve(key: str) -> Path:
    base, rel = key.split(":", 1)
    if BASES is not None:
        return BASES[base] / rel
    return Path(os.environ["ProgramW6432" if base == "PF" else "ProgramData"]) / rel


def registrations(product: str) -> dict[str, str]:
    """Uninstall key name -> "bundle" or "msi", for every entry called product (a bundle's entry
    carries BundleCachePath; an MSI's, WindowsInstaller=1, even when it is hidden from the list)."""
    found = {}
    for hive in (winreg.HKEY_LOCAL_MACHINE, winreg.HKEY_CURRENT_USER):
        for view in (winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
            try:
                key = winreg.OpenKey(hive, UNINSTALL, 0, winreg.KEY_READ | view)
            except FileNotFoundError:
                continue
            with key:
                for i in range(winreg.QueryInfoKey(key)[0]):
                    name = winreg.EnumKey(key, i)
                    with winreg.OpenKey(key, name) as sub:
                        try:
                            if winreg.QueryValueEx(sub, "DisplayName")[0] != product:
                                continue
                        except FileNotFoundError:
                            continue
                        try:
                            winreg.QueryValueEx(sub, "BundleCachePath")
                            found[name] = "bundle"
                        except FileNotFoundError:
                            found[name] = "msi"
    return found


def vc_runtime() -> object:
    try:
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, VCRT, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY) as k:
            return winreg.QueryValueEx(k, "Installed")[0]
    except FileNotFoundError:
        return None


def run_args(exe: str, *args: str, log: str) -> list[str]:
    return [exe, *args, "/norestart", "/log", str(LOGS / log)]


def run(cmd: list[str]) -> int:
    LOGS.mkdir(exist_ok=True)
    return subprocess.run(cmd).returncode


def run_with_cleanup(m: dict, body, clean) -> None:
    try:
        body()
    finally:
        clean(m)


def delete_if_empty(key: str) -> bool:
    try:
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY) as k:
            subkeys, values, _ = winreg.QueryInfoKey(k)
    except FileNotFoundError:
        return True
    if subkeys or values:
        return False
    winreg.DeleteKeyEx(winreg.HKEY_LOCAL_MACHINE, key, winreg.KEY_WOW64_64KEY)
    return True


def cleanup(m: dict, registrations=registrations, run=run) -> list[str]:
    problems = []
    for name, b in m["bundles"].items():
        if "bundle" in registrations(b["bundle_product"]).values():
            rc = run(run_args(str(HERE / b["exe"]), "/uninstall", "/quiet", log=f"cleanup-{name}.log"))
            if rc not in OK:
                problems.append(f"cleanup: uninstalling the {name} bundle returned {rc}")
        for code, kind in registrations(b["msi_product"]).items():
            if kind == "msi":
                rc = run(["msiexec", "/x", code, "/qn", "/norestart", "/l*v", str(LOGS / f"cleanup-{code}.log")])
                if rc not in OK:
                    problems.append(f"cleanup: uninstalling {code} returned {rc}")
        for product in {b["bundle_product"], b["msi_product"]}:
            if left := registrations(product):
                problems.append(f"cleanup: {product} is still registered: {left}")
    top = resolve(f"PF:{m['top']}")
    if top.exists():
        for folder, _, _ in sorted(os.walk(top, topdown=False), key=lambda w: -len(w[0])):
            try:
                os.rmdir(folder)
            except OSError:
                pass
        if top.exists():
            problems.append(f"cleanup: {top} still holds {[str(p) for p in top.rglob('*')]}; left alone")
    for key in m["guard_keys"]:
        try:
            while key.lower().startswith("software\\msis") and delete_if_empty(key):
                key = key.rsplit("\\", 1)[0]
        except OSError as e:
            problems.append(f"cleanup: {key} could not be cleared: {e}")
    for p in problems:
        print(p)
    return problems


def selftest() -> int:
    log = ("i101: Detected package: Prereq_vcredist_2022_x64, state: Present, cached: No\n"
           "i101: Detected package: MainPackage, state: Absent, cached: No\n"
           "i301: Applying execute package: MainPackage, action: Install, path: x\n")
    assert detected(log) == {"Prereq_vcredist_2022_x64": "Present", "MainPackage": "Absent"}
    assert executed(log) == {"MainPackage"}
    assert ui_levels("Property(S): UILevel = 2\nProperty(C): UILevel = 2\n") == {"2"}
    assert "/norestart" in run_args("b.exe", "/quiet", log="l")
    global BASES
    with tempfile.TemporaryDirectory() as tmp:
        BASES = {"PF": Path(tmp) / "pf", "PD": Path(tmp) / "pd"}
        try:
            m = {"top": "Top", "guard_keys": [], "bundles": {"b": {"exe": "b.exe", "bundle_product": "B", "msi_product": "M"}}}
            resolve("PF:Top\\Empty").mkdir(parents=True)
            resolve("PF:Top\\Kept").mkdir(parents=True)
            (resolve("PF:Top\\Kept") / "x.txt").write_text("not the probe's", encoding="utf-8")
            calls = []
            state = {"B": {"{B}": "bundle"}, "M": {"{M}": "msi"}}

            def fake_reg(product: str) -> dict[str, str]:
                return {} if calls else state.get(product, {})

            def failing() -> None:
                raise RuntimeError("a step blew up")

            problems: list[str] = []
            try:
                run_with_cleanup(m, failing, lambda mm: problems.extend(cleanup(mm, registrations=fake_reg, run=lambda c: calls.append(c) or 0)))
                raise AssertionError("the failure was swallowed")
            except RuntimeError:
                pass
            assert calls and "/uninstall" in calls[0], calls
            assert not resolve("PF:Top\\Empty").exists() and resolve("PF:Top\\Kept\\x.txt").exists()
            assert len(problems) == 1 and "still holds" in problems[0], problems
        finally:
            BASES = None
    # Without the VC++ runtime the probe refuses before anything runs; with it, it may start.
    m = {"top": "NoSuchProbeFolder95", "bundles": {"b": {"bundle_product": "B", "msi_product": "M"}}}
    assert "not registered" in preflight(m, vc=lambda: None, registrations=lambda p: {})
    assert "not registered" in preflight(m, vc=lambda: 0, registrations=lambda p: {})
    assert preflight(m, vc=lambda: 1, registrations=lambda p: {}) == ""
    assert "already here" in preflight(m, vc=lambda: 1, registrations=lambda p: {"{X}": "msi"} if p == "M" else {})
    print("selftest: PASS")
    return 0


def preflight(m: dict, vc=None, registrations=registrations) -> str:
    """Why the probe must not start, or "" when it may. Checked before anything is installed: the
    auto bundle chains the REAL VC++ redistributable, which is permanent, so on a machine without
    the runtime it would install it for good - no cleanup can undo that (review of #95)."""
    installed = vc_runtime() if vc is None else vc()
    if installed != 1:
        return (f"the VC++ 2015-2022 x64 runtime is not registered (Installed = {installed!r}), so auto.exe would install "
                "the real, permanent redistributable. This probe needs a machine that already has it")
    here = [str(resolve(f"PF:{m['top']}"))] if resolve(f"PF:{m['top']}").exists() else []
    here += [p for b in m["bundles"].values() for p in {b["bundle_product"], b["msi_product"]} if registrations(p)]
    if here:
        return f"{here} already here. Run --cleanup, or restore the snapshot."
    return ""


def legs(m: dict, failures: list[str]) -> None:
    vc_before = vc_runtime()
    print(f"VC++ 2015-2022 x64 runtime registered before: Installed = {vc_before!r}")
    for name, b in m["bundles"].items():
        exe = str(HERE / b["exe"])
        target = resolve(b["file"])
        for mode in ("/quiet", "/passive"):
            label = f"{name} {mode}"
            log_name = f"{name}-{mode.strip('/')}"
            rc = run(run_args(exe, mode, log=f"{log_name}.log"))
            log = (LOGS / f"{log_name}.log").read_text(encoding="utf-8", errors="replace") if (LOGS / f"{log_name}.log").exists() else ""
            found = [] if rc in OK else [f"{label}: install returned {rc}"]
            kinds = set(registrations(b["bundle_product"]).values()) | set(registrations(b["msi_product"]).values())
            if kinds != {"bundle", "msi"}:
                found.append(f"{label}: registered {sorted(kinds)}, want the bundle and its MSI")
            content = target.read_text(encoding="utf-8") if target.exists() else None
            if content != b["content"]:
                found.append(f"{label}: {target} holds {content!r}")
            msi_logs = sorted(LOGS.glob(f"{log_name}_*MainPackage*.log"))
            levels = set().union(*(ui_levels(p.read_text(encoding="utf-16", errors="replace") if p.read_bytes()[:2] == b"\xff\xfe"
                                                 else p.read_text(encoding="utf-8", errors="replace")) for p in msi_logs)) if msi_logs else set()
            if levels != {"2"}:
                found.append(f"{label}: the MSI's UILevel was {sorted(levels) or 'not found'} ({[p.name for p in msi_logs]}), want 2 (no UI)")
            if b["prerequisite"]:
                states = {k: v for k, v in detected(log).items() if k.startswith(b["prerequisite"])}
                if not states or set(states.values()) != {"Present"}:
                    found.append(f"{label}: the prerequisite was detected as {states}, want Present")
                if ran := {p for p in executed(log) if p.startswith(b["prerequisite"])}:
                    found.append(f"{label}: the prerequisite was executed: {sorted(ran)}")
            print(f"{label} install: {'PASS' if not found else 'FAIL'}")
            for x in found:
                print(f"  - {x}")
            failures.extend(found)

            rc = run(run_args(exe, "/uninstall", mode, log=f"{log_name}-uninstall.log"))
            found = [] if rc in OK else [f"{label}: uninstall returned {rc}"]
            if left := {**registrations(b["bundle_product"]), **registrations(b["msi_product"])}:
                found.append(f"{label}: still registered after uninstall: {left}")
            if target.exists():
                found.append(f"{label}: {target} is still there after uninstall")
            print(f"{label} uninstall: {'PASS' if not found else 'FAIL'}")
            for x in found:
                print(f"  - {x}")
            failures.extend(found)
    if vc_runtime() != vc_before:
        failures.append(f"the VC++ runtime registration changed: {vc_before!r} -> {vc_runtime()!r}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs per-machine packages)")
        return 2
    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    if args.cleanup:
        return 0 if not cleanup(m) else 1
    if refusal := preflight(m):
        print(f"refusing to start: {refusal}")
        return 2
    failures: list[str] = []
    run_with_cleanup(m, lambda: legs(m, failures), lambda mm: failures.extend(cleanup(mm)))
    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
