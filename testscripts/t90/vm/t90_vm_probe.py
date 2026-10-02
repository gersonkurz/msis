"""#90, VM side - a bundle's <search> elements gating <exe> packages (D30).
Run ELEVATED; take a VM snapshot first.

Each round sets one registry state under the probe's own keys, runs the bundle silently, and
checks, for every <exe>, that it ran (its marker file appeared) exactly when Burn detected it
Absent (from Burn's log), and that this is what the search's documented semantics predict. The
bundle is uninstalled after each round.

  R1  nothing set                         every synthetic exe runs
  R2  flag in HKLM's 32-bit view only     E32 skipped, E64/EKEY run: views are separate
  R3  flag in HKLM's 64-bit view only     E64 and EKEY skipped, E32 runs
  R4  ver = "1.2.3" (32-bit view)         EVAL skipped; E32 runs: the key exists, the value does not
  R5  ver = "0.0.0.0"                     EVAL runs
  R6  ver = ""                            EVAL runs
  R7  flag = DWORD 0 (32-bit view)        E32 skipped: "exists" counts a value holding 0
  R8  HKCU\\Software\\MsisProbe90 exists     ECU skipped
In every round EWV, the docs' WebView2 recipe, is skipped exactly when the machine-wide WebView2
runtime is registered (read here directly); on Windows 11 it is.

The probe refuses to start if anything of its own is on the machine, and always ends by
uninstalling the bundle and its MSI, deleting its marker files and the probe keys' own values,
and removing the folders and keys that are then empty. A failed cleanup fails the run.

    python t90_vm_probe.py --selftest   # checks the parsing and the cleanup; installs nothing
    python t90_vm_probe.py              # ELEVATED, unattended
    python t90_vm_probe.py --cleanup    # ELEVATED: only the final cleanup, after an interrupted run
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
VIEWS = {"32": winreg.KEY_WOW64_32KEY, "64": winreg.KEY_WOW64_64KEY}
PROBE_VALUES = ("flag", "ver")


# ---- pure parts (exercised by --selftest) ----

def detected(log: str) -> dict[str, str]:
    """package id -> the state Burn detected, from its log ("Detected package: E32, state: Absent")."""
    return dict(re.findall(r"Detected package: (\w+), state: (\w+)", log))


def version_tuple(s: str) -> tuple[int, ...] | None:
    try:
        return tuple(int(x) for x in s.split("."))
    except (ValueError, AttributeError):
        return None


def expected_to_run(state: dict, wv2: str | None) -> set[str]:
    """Which exes the documented search semantics say must run (detect false) in a state:
    state[view] is None (no key) or {value name: data}; state["hkcu"] is whether the HKCU key exists."""
    k32, k64 = state.get("32"), state.get("64")
    p32 = k32 is not None and "flag" in k32                     # exists, value named
    p64 = k64 is not None and "flag" in k64
    pkey = k64 is not None                                      # exists, the key itself
    ver = version_tuple(k32["ver"]) if k32 is not None and isinstance(k32.get("ver"), str) else None
    pver = ver is not None and any(ver)                        # PVER > v0.0.0.0
    pcu = bool(state.get("hkcu"))
    wv = version_tuple(wv2) if wv2 else None
    pwv = wv is not None and any(wv)
    return {e for e, present in {"E32": p32, "E64": p64, "EKEY": pkey, "EVAL": pver, "ECU": pcu, "EWV": pwv}.items() if not present}


# ---- the machine ----

BASES: dict[str, Path] | None = None  # the selftest points these at a temporary tree
HKLM = winreg.HKEY_LOCAL_MACHINE      # the selftest uses HKEY_CURRENT_USER for both
HKCU = winreg.HKEY_CURRENT_USER


def resolve(key: str) -> Path:
    base, rel = key.split(":", 1)
    if BASES is not None:
        return BASES[base] / rel
    return Path(os.environ["ProgramW6432" if base == "PF" else "ProgramData"]) / rel


# Only a missing key or value counts as absent (FileNotFoundError). Any other registry error -
# access denied above all - propagates: read as "absent", it would let the probe start over its
# own leftovers and report a cleanup that did not happen.

def read_value(root, key: str, view: str, name: str):
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEWS[view]) as k:
            return winreg.QueryValueEx(k, name)[0]
    except FileNotFoundError:
        return None


def key_exists(root, key: str, view: str = "64") -> bool:
    try:
        winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEWS[view]).Close()
        return True
    except FileNotFoundError:
        return False


def set_state(m: dict, state: dict) -> None:
    if problems := clear_probe_keys(m):
        raise RuntimeError("could not clear the previous round's registry state: " + "; ".join(problems))
    for view in ("32", "64"):
        values = state.get(view)
        if values is None:
            continue
        with winreg.CreateKeyEx(HKLM, m["probe_key"], 0, winreg.KEY_WRITE | VIEWS[view]) as k:
            for name, data in values.items():
                if isinstance(data, int):
                    winreg.SetValueEx(k, name, 0, winreg.REG_DWORD, data)
                else:
                    winreg.SetValueEx(k, name, 0, winreg.REG_SZ, data)
    if state.get("hkcu"):
        winreg.CreateKeyEx(HKCU, m["probe_key_hkcu"], 0, winreg.KEY_WRITE).Close()


def delete_if_empty(root, key: str, view: str) -> bool:
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | VIEWS[view]) as k:
            subkeys, values, _ = winreg.QueryInfoKey(k)
    except FileNotFoundError:
        return True
    if subkeys or values:
        return False
    winreg.DeleteKeyEx(root, key, VIEWS[view])
    return True


def clear_probe_keys(m: dict) -> list[str]:
    """Deletes the probe's own values, then its keys if they are empty. Never a key with content.
    Returns every key it could not clear - one with other content, or one it could not access."""
    problems = []
    for root, key, views in ((HKLM, m["probe_key"], ("32", "64")), (HKCU, m["probe_key_hkcu"], ("64",))):
        for view in views:
            try:
                try:
                    with winreg.OpenKey(root, key, 0, winreg.KEY_SET_VALUE | VIEWS[view]) as k:
                        for name in PROBE_VALUES:
                            try:
                                winreg.DeleteValue(k, name)
                            except FileNotFoundError:
                                pass
                except FileNotFoundError:
                    continue
                if not delete_if_empty(root, key, view):
                    problems.append(f"cleanup: {key} ({view}-bit view) holds more than the probe wrote; left alone")
            except OSError as e:
                problems.append(f"cleanup: {key} ({view}-bit view) could not be cleared: {e}")
    return problems


def arp(product: str) -> dict[str, str]:
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
                    try:
                        with winreg.OpenKey(key, name) as sub:
                            if winreg.QueryValueEx(sub, "DisplayName")[0] == product:
                                found[name] = winreg.QueryValueEx(sub, "DisplayVersion")[0]
                    except FileNotFoundError:  # an entry without a DisplayName is not ours
                        pass
    return found


def run_args(exe: str, *args: str, log: str) -> list[str]:
    return [exe, *args, "/quiet", "/norestart", "/log", str(LOGS / log)]


def run(cmd: list[str]) -> int:
    LOGS.mkdir(exist_ok=True)
    return subprocess.run(cmd).returncode


def run_with_cleanup(m: dict, body, clean) -> None:
    try:
        body()
    finally:
        clean(m)


def cleanup(m: dict, arp=arp, run=run) -> list[str]:
    problems = []
    bundle = str(HERE / "probe90.exe")
    if arp(m["product"]):
        rc = run(run_args(bundle, "/uninstall", log="cleanup-bundle.log"))
        print(f"cleanup: uninstalling the bundle: rc={rc}")
        if rc not in OK:
            problems.append(f"cleanup: uninstalling the bundle returned {rc}")
    for code in arp(m["app_product"]):
        rc = run(["msiexec", "/x", code, "/qn", "/norestart", "/l*v", str(LOGS / f"cleanup-{code}.log")])
        if rc not in OK:
            problems.append(f"cleanup: uninstalling {code} returned {rc}")
    for product in (m["product"], m["app_product"]):
        if still := arp(product):
            problems.append(f"cleanup: {product} is still installed: {still}")
    problems += clear_probe_keys(m)
    for exe in m["exes"]:
        (resolve(m["markers"]) / f"{exe}.txt").unlink(missing_ok=True)
    for base in ("PF", "PD"):
        top = resolve(f"{base}:{m['top']}")
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
            while key.lower().startswith("software\\msis") and delete_if_empty(HKLM, key, "64"):
                key = key.rsplit("\\", 1)[0]
        except OSError as e:
            problems.append(f"cleanup: {key} could not be cleared: {e}")
    for p in problems:
        print(p)
    return problems


def selftest() -> int:
    log = "i101: Detected package: E32, state: Absent, cached: None\ni101: Detected package: EWV, state: Present, cached: None\n"
    assert detected(log) == {"E32": "Absent", "EWV": "Present"}, detected(log)
    every = {"E32", "E64", "EKEY", "EVAL", "ECU"}
    assert expected_to_run({"32": None, "64": None}, "154.0.4258.48") == every
    assert expected_to_run({"32": None, "64": None}, None) == every | {"EWV"}
    assert expected_to_run({"32": None, "64": None}, "0.0.0.0") == every | {"EWV"}
    assert expected_to_run({"32": {"flag": 1}, "64": None}, "1.0") == every - {"E32"}
    assert expected_to_run({"32": None, "64": {"flag": 1}}, "1.0") == every - {"E64", "EKEY"}
    assert expected_to_run({"32": {"ver": "1.2.3"}, "64": None}, "1.0") == every - {"EVAL"}
    for ver in ("0.0.0.0", ""):
        assert expected_to_run({"32": {"ver": ver}, "64": None}, "1.0") == every
    assert expected_to_run({"32": {"flag": 0}, "64": None}, "1.0") == every - {"E32"}
    assert expected_to_run({"32": None, "64": None, "hkcu": True}, "1.0") == every - {"ECU"}
    cmd = run_args("b.exe", log="l.log")
    assert "/quiet" in cmd and "/norestart" in cmd, cmd

    # The real cleanup on a temporary tree and temporary HKCU keys, the installers faked, reached
    # through a failing run: the probe's values and emptied keys go, anything else stays.
    global BASES, HKLM
    with tempfile.TemporaryDirectory() as tmp:
        BASES, HKLM = {"PF": Path(tmp) / "pf", "PD": Path(tmp) / "pd"}, winreg.HKEY_CURRENT_USER
        m = {"top": "Top", "product": "B", "app_product": "A", "exes": ["E32"],
             "probe_key": r"Software\msis-t90-selftest\lm", "probe_key_hkcu": r"Software\msis-t90-selftest\cu",
             "markers": "PD:Top\\markers", "guard_keys": [r"Software\msis-t90-selftest\guard"]}
        kept = r"Software\msis-t90-selftest\kept"
        try:
            set_state(m, {"32": {"flag": 1, "ver": "1.0"}, "64": {"flag": 1}, "hkcu": True})
            with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, kept, 0, winreg.KEY_WRITE) as k:
                winreg.SetValueEx(k, "other", 0, winreg.REG_SZ, "not the probe's")
            resolve("PD:Top\\markers").mkdir(parents=True)
            (resolve("PD:Top\\markers") / "E32.txt").write_text("ran\n", encoding="utf-8")
            resolve("PF:Top\\App").mkdir(parents=True)
            (resolve("PF:Top\\App") / "left.txt").write_text("not the probe's", encoding="utf-8")
            calls, problems = [], []

            def fake_arp(product: str) -> dict[str, str]:
                return {"{X}": "1"} if product == "B" and not calls else {}

            def failing() -> None:
                raise RuntimeError("a round blew up")

            try:
                run_with_cleanup(m, failing, lambda mm: problems.extend(cleanup(mm, arp=fake_arp, run=lambda c: calls.append(c) or 0)))
                raise AssertionError("the failure was swallowed")
            except RuntimeError:
                pass
            assert len(calls) == 1 and "/uninstall" in calls[0], calls
            assert not key_exists(HKLM, m["probe_key"]) and not key_exists(winreg.HKEY_CURRENT_USER, m["probe_key_hkcu"])
            assert key_exists(winreg.HKEY_CURRENT_USER, kept), "a key with someone else's value is never removed"
            assert not resolve("PD:Top").exists(), "the marker and its emptied folders go"
            assert resolve("PF:Top\\App\\left.txt").exists() and any("still holds" in p for p in problems), problems
        finally:
            for k in (kept, m["probe_key"], m["probe_key_hkcu"], r"Software\msis-t90-selftest"):
                try:
                    winreg.DeleteKeyEx(winreg.HKEY_CURRENT_USER, k, winreg.KEY_WOW64_64KEY)
                except OSError:
                    pass
            BASES, HKLM = None, winreg.HKEY_LOCAL_MACHINE
    selftest_access_denied()
    print("selftest: PASS")
    return 0


def selftest_access_denied() -> None:
    """An access-denied registry error is never taken for "absent" or "cleaned" (review of #90)."""
    m = {"probe_key": r"Software\msis-t90-denied\lm", "probe_key_hkcu": r"Software\msis-t90-denied\cu"}
    real_open = winreg.OpenKey

    def denied(*args, **kwargs):
        raise PermissionError(5, "Access is denied")

    winreg.OpenKey = denied
    try:
        for check in (lambda: key_exists(HKLM, m["probe_key"]), lambda: read_value(HKLM, m["probe_key"], "32", "ver"),
                      lambda: delete_if_empty(HKLM, m["probe_key"], "64")):
            try:
                check()
                raise AssertionError("access denied was taken for an answer")
            except PermissionError:
                pass
        problems = clear_probe_keys(m)
        assert len(problems) == 3 and all("could not be cleared" in p for p in problems), problems
        try:
            set_state(m, {"32": None, "64": None})
            raise AssertionError("a round started although the previous state could not be cleared")
        except RuntimeError as e:
            assert "could not clear" in str(e)
    finally:
        winreg.OpenKey = real_open


ROUNDS = [
    ("R1 nothing set", {"32": None, "64": None}),
    ("R2 flag in the 32-bit view only", {"32": {"flag": 1}, "64": None}),
    ("R3 flag in the 64-bit view only", {"32": None, "64": {"flag": 1}}),
    ("R4 ver = 1.2.3, no flag", {"32": {"ver": "1.2.3"}, "64": None}),
    ("R5 ver = 0.0.0.0", {"32": {"ver": "0.0.0.0"}, "64": None}),
    ("R6 ver empty", {"32": {"ver": ""}, "64": None}),
    ("R7 flag = DWORD 0", {"32": {"flag": 0}, "64": None}),
    ("R8 the HKCU key exists", {"32": None, "64": None, "hkcu": True}),
]


def rounds(m: dict, failures: list[str]) -> None:
    bundle = str(HERE / "probe90.exe")
    markers = resolve(m["markers"])
    wv2 = read_value(HKLM, m["wv2_key"], "32", "pv")
    print(f"the machine-wide WebView2 runtime, read directly: pv = {wv2!r}")
    for i, (label, state) in enumerate(ROUNDS, 1):
        set_state(m, state)
        for exe in m["exes"]:
            (markers / f"{exe}.txt").unlink(missing_ok=True)
        rc = run(run_args(bundle, log=f"R{i}.log"))
        log = (LOGS / f"R{i}.log").read_text(encoding="utf-8", errors="replace") if (LOGS / f"R{i}.log").exists() else ""
        states = detected(log)
        ran = {e for e in m["exes"] if (markers / f"{e}.txt").exists()}
        want = expected_to_run(state, wv2)
        found = [] if rc in OK else [f"{label}: the bundle returned {rc}"]
        for e in m["exes"]:
            if (e in ran) != (e in want):
                found.append(f"{label}: {e} {'ran' if e in ran else 'did not run'}, the semantics say it {'should' if e in want else 'should not'}")
            if states.get(e) != ("Absent" if e in want else "Present"):
                found.append(f"{label}: Burn detected {e} as {states.get(e)!r}")
        rc = run(run_args(bundle, "/uninstall", log=f"R{i}-uninstall.log"))
        if rc not in OK:
            found.append(f"{label}: uninstalling the bundle returned {rc}")
        print(f"{label}: {'PASS' if not found else 'FAIL'} (ran: {sorted(ran)})")
        for x in found:
            print(f"  - {x}")
        failures.extend(found)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs per-machine packages and writes HKLM)")
        return 2
    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    if args.cleanup:
        return 0 if not cleanup(m) else 1
    here = [str(resolve(f"{b}:{m['top']}")) for b in ("PF", "PD") if resolve(f"{b}:{m['top']}").exists()]
    here += [m["probe_key"] + f" ({v}-bit)" for v in ("32", "64") if key_exists(HKLM, m["probe_key"], v)]
    here += ["HKCU\\" + m["probe_key_hkcu"]] if key_exists(HKCU, m["probe_key_hkcu"]) else []
    here += [p for p in (m["product"], m["app_product"]) if arp(p)]
    if here:
        print(f"refusing to start: {here} already here. Run --cleanup, or restore the snapshot.")
        return 2
    failures: list[str] = []
    run_with_cleanup(m, lambda: rounds(m, failures), lambda mm: failures.extend(cleanup(mm)))
    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
