# /// script
# requires-python = ">=3.11"
# ///
"""#77, machine side - an optional service feature that survives feature changes.

The package is the layout msis proposes since #77: feature Complete installs svcprobe.exe;
the optional feature Service installs its OWN copy under service\\ and registers that one as a
service. Each scenario installs, changes features and uninstalls, and after EVERY step checks
three things against what that step must leave behind:

  - the application's copy, present exactly while Complete is installed, contents unchanged;
  - the service's copy, present exactly while Service is installed, contents unchanged;
  - the service registration (HKLM\\SYSTEM\\CurrentControlSet\\Services\\<name>), present
    exactly while Service is installed, with an ImagePath naming the service's copy.

Whenever the service is registered, its configuration is checked too (#78): the display name
(msis-2.x's default, the service name), description, type, error control, start type and the
failure actions restart="yes" sets - read from the same registry key, against the manifest.

The layout #77 deprecates installs ONE path from two components; on this VM (2026-09-25)
removing either feature deleted the executable the other still needed. msis warns about it and
/STRICT refuses it, which the build side checks.

    python t77_vm_probe.py --selftest   # checks the verdict; installs nothing
    python t77_vm_probe.py              # ELEVATED: the real thing

TAKE A VM SNAPSHOT FIRST. It installs and uninstalls a per-machine MSI and registers a service.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import struct
import subprocess
import sys
import winreg
from pathlib import Path

HERE = Path(__file__).resolve().parent
OK = (0, 3010)

# Each step: (what it does, msiexec arguments, Complete installed, Service installed).
# {c} and {s} are the Complete and Service feature ids from the manifest.
SCENARIOS = {
    "both, then remove Service": [
        ("install both features", ["/i", "{msi}", "ADDLOCAL={c},{s}"], True, True),
        ("remove Service only", ["/i", "{msi}", "REMOVE={s}"], True, False),
        ("uninstall", ["/x", "{msi}"], False, False),
    ],
    "both, then remove Complete": [
        ("install both features", ["/i", "{msi}", "ADDLOCAL={c},{s}"], True, True),
        ("remove Complete only", ["/i", "{msi}", "REMOVE={c}"], False, True),
        ("uninstall", ["/x", "{msi}"], False, False),
    ],
    "Complete, add Service, remove Service": [
        ("install the defaults (Complete)", ["/i", "{msi}"], True, False),
        ("add Service", ["/i", "{msi}", "ADDLOCAL={s}"], True, True),
        ("remove Service", ["/i", "{msi}", "REMOVE={s}"], True, False),
        ("uninstall", ["/x", "{msi}"], False, False),
    ],
    "Service only": [
        ("install Service only", ["/i", "{msi}", "ADDLOCAL={s}"], False, True),
        ("uninstall", ["/x", "{msi}"], False, False),
    ],
}


def verdict(steps: list[tuple[str, bool, bool]], observed: list[tuple[int, str, str, str]]) -> list[str]:
    """Failures, empty when the scenario passed. Pure, so --selftest can exercise it.

    steps: (label, Complete installed, Service installed); observed per step: (msiexec rc,
    application copy, service copy, service) where a copy is intact/missing/changed and the
    service registered/absent/elsewhere.
    """
    failures = []
    for (label, complete, service), (rc, app, svc_exe, svc) in zip(steps, observed, strict=True):
        if rc not in OK:
            failures.append(f"{label}: msiexec returned {rc}")
        for what, got, want in (
            ("the application's copy", app, "intact" if complete else "missing"),
            ("the service's copy", svc_exe, "intact" if service else "missing"),
            ("the service", svc, "registered" if service else "absent"),
        ):
            if got != want:
                failures.append(f"{label}: {what} is {got}, expected {want}")
    return failures


def config_failures(label: str, expected: dict, observed: dict) -> list[str]:
    """What the registered service's configuration gets wrong (#78). Pure, for --selftest.

    observed holds the same keys as expected, read from the service's registry key; a value
    that is not there is None.
    """
    return [f"{label}: the service's {key} is {observed.get(key)!r}, expected {want!r}"
            for key, want in expected.items() if observed.get(key) != want]


def selftest() -> int:
    bad = 0
    want = {"DisplayName": "svc", "Type": 0x20,
            "FailureActions": {"reset_seconds": 86400, "actions": [[1, 30000]] * 3}}
    for name, observed, n in (
        ("#78: the configuration as set", dict(want), 0),
        ("#78: service-type dropped (ownProcess)", {**want, "Type": 0x10}, 1),
        ("#78: restart dropped (no failure actions)", {**want, "FailureActions": None}, 1),
        ("#78: a different restart delay", {**want, "FailureActions": {"reset_seconds": 86400,
                                                                      "actions": [[1, 60000]] * 3}}, 1),
    ):
        got = len(config_failures("step", want, observed))
        bad += got != n
        print(f"{'PASS' if got == n else 'FAIL'}  selftest: {name} -> {got} failure(s), expected {n}")
    return selftest_verdict(bad)


def selftest_verdict(bad: int) -> int:
    steps = [(label, c, s) for label, _, c, s in SCENARIOS["both, then remove Complete"]]
    good = [(0, "intact", "intact", "registered"), (0, "missing", "intact", "registered"),
            (0, "missing", "missing", "absent")]
    cases = {
        "the passing run": (good, 0),
        "#77: the service's exe lost when Complete is removed": (
            [good[0], (0, "missing", "missing", "registered"), good[2]], 1),
        "the application's copy left behind": ([good[0], (0, "intact", "intact", "registered"), good[2]], 1),
        "a copy's contents changed": ([(0, "intact", "changed", "registered"), good[1], good[2]], 1),
        "the service unregistered with its feature still in": (
            [good[0], (0, "missing", "intact", "absent"), good[2]], 1),
        "the service pointing elsewhere": ([(0, "intact", "intact", "elsewhere"), good[1], good[2]], 1),
        "an msiexec failure": ([(1603, "intact", "intact", "registered"), good[1], good[2]], 1),
        "a copy left after uninstall": ([good[0], good[1], (0, "missing", "intact", "absent")], 1),
    }
    for name, (observed, want) in cases.items():
        got = len(verdict(steps, observed))
        ok = got == want
        bad += not ok
        print(f"{'PASS' if ok else 'FAIL'}  selftest: {name} -> {got} failure(s), expected {want}")
    print("selftest passed" if not bad else f"selftest FAILED: {bad} case(s)")
    return 1 if bad else 0


def file_state(path: str, text: str) -> str:
    p = Path(path)
    if not p.exists():
        return "missing"
    return "intact" if p.read_text(encoding="utf-8", errors="replace") == text else "changed"


def service_state(name: str, exe: str) -> str:
    try:
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                            rf"SYSTEM\CurrentControlSet\Services\{name}") as key:
            image, _ = winreg.QueryValueEx(key, "ImagePath")
    except FileNotFoundError:
        return "absent"
    return "registered" if exe.lower() in str(image).lower() else "elsewhere"


def service_config(name: str, keys: list[str]) -> dict:
    """The named values of the service's registry key; FailureActions decoded.

    FailureActions is SERVICE_FAILURE_ACTIONS as the SCM stores it: DWORD reset period (s),
    two DWORD placeholders for the reboot message and command, DWORD action count, a DWORD
    placeholder for the array, then one (DWORD type, DWORD delay ms) pair per action.
    """
    out: dict = {}
    with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, rf"SYSTEM\CurrentControlSet\Services\{name}") as key:
        for k in keys:
            try:
                value, _ = winreg.QueryValueEx(key, k)
            except FileNotFoundError:
                value = None
            if k == "FailureActions" and value is not None:
                reset, _, _, count = struct.unpack_from("<4I", value, 0)
                actions = [list(struct.unpack_from("<2I", value, 20 + 8 * i)) for i in range(count)]
                value = {"reset_seconds": reset, "actions": actions}
            out[k] = value
    return out


def msiexec(args: list[str], log: Path) -> int:
    cmd = ["msiexec", *args, "/qn", "/norestart", "/l*v", str(log)]
    print(f"    $ {' '.join(cmd)}")
    return subprocess.run(cmd).returncode


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--selftest", action="store_true")
    if parser.parse_args().selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this ELEVATED (and on a snapshotted VM)")
        return 1

    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    msi = str(HERE / m["msi"])
    fmt = {"msi": msi, "c": m["complete"], "s": m["service"]}

    failed = []
    for n, (title, steps) in enumerate(SCENARIOS.items()):
        print(f"\n=== {title} ===")
        subprocess.run(["msiexec", "/x", msi, "/qn", "/norestart"])  # start clean; 1605 is fine
        observed, config = [], []
        want = m["service_config"]
        for i, (label, args, _, _) in enumerate(steps):
            rc = msiexec([a.format(**fmt) for a in args], HERE / f"s{n}-{i}.log")
            obs = (rc, file_state(m["app_exe"], m["exe_text"]), file_state(m["service_exe"], m["exe_text"]),
                   service_state(m["service_name"], m["service_exe"]))
            print(f"  {label}: rc={obs[0]} app={obs[1]} service-exe={obs[2]} service={obs[3]}")
            observed.append(obs)
            if obs[3] == "registered":
                got = service_config(m["service_name"], list(want))
                print(f"    service configuration: {got}")
                config += config_failures(label, want, got)
        failures = verdict([(label, c, s) for label, _, c, s in steps], observed) + config
        for f in failures:
            print(f"  FAIL {f}")
        print(f"  {'FAIL' if failures else 'PASS'} {title}")
        if failures:
            failed.append(title)

    print("\n=== summary ===")
    for title in SCENARIOS:
        print(f"  {'FAIL' if title in failed else 'PASS'} {title}")
    print("msiexec logs: s<scenario>-<step>.log beside this script")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
