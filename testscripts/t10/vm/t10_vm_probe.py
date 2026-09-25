# /// script
# requires-python = ">=3.11"
# ///
"""T10, machine side - what a silent package does with preserve="yes" registry values.

preserve="yes" writes each value from a property: its default is the .reg value, and a registry
search replaces it with the value already on the machine - so a fresh install writes the
defaults and an upgrade keeps the site's values. msis 3.0.3's silent template dropped those
properties (#19), so its silent packages reference properties that do not exist.

Scenarios, each on a clean key (HKLM\\SOFTWARE\\MsisProbeT10, 32-bit view - the package is x86):

  fresh 3.0.3 silent     install silent-303 alone                        OBSERVED (the field)
  fresh 3.0.6 silent     install silent-306 alone                        must write the defaults
  upgrade 3.0.3 regular  base-303, site values set, then reg-303-1.0.1   must keep them (control)
  upgrade 3.0.3 silent   base-303, site values set, then silent-303      OBSERVED (the field)
  upgrade 3.0.6 silent   base-303, site values set, then silent-306      must keep them

Every value is checked for its data AND its registry type: a DWORD that comes back as a string
is damage even when the digits look right.

    python t10_vm_probe.py --selftest   # checks the verdict; installs nothing
    python t10_vm_probe.py              # ELEVATED: the real thing

TAKE A VM SNAPSHOT FIRST. It installs and uninstalls per-machine MSIs and writes under HKLM.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import subprocess
import sys
import winreg
from pathlib import Path

HERE = Path(__file__).resolve().parent
OK = (0, 3010)
TYPES = {1: "REG_SZ", 2: "REG_EXPAND_SZ", 4: "REG_DWORD", 7: "REG_MULTI_SZ", 11: "REG_QWORD"}
ABSENT = None


def describe(v) -> str:
    return "absent" if v is ABSENT else f"{TYPES.get(v[1], v[1])} {v[0]!r}"


def expected(m: dict, kind: str) -> dict:
    """What each value must be: 'default' after a fresh install, 'site' after an upgrade."""
    out = {p["name"]: (p[kind], p["type"]) for p in m["preserved"]}
    out[m["control"]["name"]] = (m["control"]["default"], m["control"]["type"])
    return out


def failures(want: dict, got: dict) -> list[str]:
    """Pure, for --selftest. An empty REG_SZ default may also come back absent: an empty
    formatted value is not a value the .reg file can be said to demand."""
    out = []
    for name, w in want.items():
        g = got.get(name, ABSENT)
        if g == w or (w == ("", 1) and g is ABSENT):
            continue
        out.append(f"{name}: {describe(g)}, expected {describe(w)}")
    return out


def outcome(judged: bool, rcs: list[int], value_failures: list[str]) -> tuple[str, list[str]]:
    """PASS / FAIL / OBSERVED for one scenario. Pure, for --selftest.

    A failed msiexec FAILs every scenario, observed ones included: an install that did not
    happen establishes nothing about preservation. Value differences decide only the judged
    scenarios; in the observed ones they are the record.
    """
    install = [f"msiexec returned {rc}" for rc in rcs if rc not in OK]
    if install:
        return "FAIL", install + value_failures
    if judged:
        return ("FAIL" if value_failures else "PASS"), value_failures
    return "OBSERVED", value_failures


def selftest() -> int:
    m = {"preserved": [{"name": "S", "type": 1, "default": "d", "site": "s"},
                       {"name": "N", "type": 4, "default": 1, "site": 7},
                       {"name": "E", "type": 1, "default": "", "site": "x"}],
         "control": {"name": "C", "type": 1, "default": "always"}}
    fresh, site = expected(m, "default"), expected(m, "site")
    cases = [
        ("fresh install as designed", fresh, dict(fresh), 0),
        ("an empty default that comes back absent", fresh, {k: v for k, v in fresh.items() if k != "E"}, 0),
        ("the site values kept", site, dict(site), 0),
        ("a site value reset to the default", site, {**site, "S": ("d", 1)}, 1),
        ("a value written empty", fresh, {**fresh, "S": ("", 1)}, 1),
        ("a DWORD turned into a string", site, {**site, "N": ("7", 1)}, 1),
        ("a value gone", site, {k: v for k, v in site.items() if k != "N"}, 1),
        ("the control value lost", fresh, {**fresh, "C": ABSENT}, 1),
    ]
    bad = 0
    for name, want, got, n in cases:
        k = len(failures(want, got))
        bad += k != n
        print(f"{'PASS' if k == n else 'FAIL'}  selftest: {name} -> {k} failure(s), expected {n}")
    for name, args, status in (
        ("a judged scenario as designed", (True, [0, 0], []), "PASS"),
        ("a judged scenario with a wrong value", (True, [0], ["x"]), "FAIL"),
        ("an observed scenario's damage is recorded, not failed", (False, [0], ["x"]), "OBSERVED"),
        ("a failed install fails an observed scenario", (False, [1603], []), "FAIL"),
        ("a failed base install fails a judged scenario", (True, [1603, 0], []), "FAIL"),
        ("3010 (reboot required) is a success", (True, [3010], []), "PASS"),
    ):
        got = outcome(*args)[0]
        bad += got != status
        print(f"{'PASS' if got == status else 'FAIL'}  selftest: {name} -> {got}, expected {status}")
    print("selftest passed" if not bad else f"selftest FAILED: {bad} case(s)")
    return 1 if bad else 0


VIEW = winreg.KEY_WOW64_32KEY


def read(key: str, names: list[str]) -> dict:
    out = {}
    try:
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, key, 0, winreg.KEY_READ | VIEW) as k:
            for n in names:
                try:
                    out[n] = winreg.QueryValueEx(k, n)
                except FileNotFoundError:
                    out[n] = ABSENT
    except FileNotFoundError:
        out = {n: ABSENT for n in names}
    return out


def seed(key: str, m: dict) -> None:
    with winreg.CreateKeyEx(winreg.HKEY_LOCAL_MACHINE, key, 0, winreg.KEY_WRITE | VIEW) as k:
        for p in m["preserved"]:
            winreg.SetValueEx(k, p["name"], 0, p["type"], p["site"])


def clean(key: str) -> None:
    try:
        winreg.DeleteKeyEx(winreg.HKEY_LOCAL_MACHINE, key, VIEW, 0)
    except FileNotFoundError:
        pass


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
    names = [p["name"] for p in m["preserved"]] + [m["control"]["name"]]
    base = HERE / "base-303-1.0.0.msi"
    scenarios = [
        ("fresh 3.0.3 silent", None, "silent-303-1.0.1.msi", "default", False),
        ("fresh 3.0.6 silent", None, "silent-306-1.0.1.msi", "default", True),
        ("upgrade 3.0.3 regular (control)", base, "reg-303-1.0.1.msi", "site", True),
        ("upgrade 3.0.3 silent", base, "silent-303-1.0.1.msi", "site", False),
        ("upgrade 3.0.6 silent", base, "silent-306-1.0.1.msi", "site", True),
    ]
    results, observed = [], []
    for n, (title, first, second, kind, judged) in enumerate(scenarios):
        print(f"\n=== {title} ===")
        for msi in ("silent-306-1.0.1.msi", "reg-303-1.0.1.msi", "base-303-1.0.0.msi"):
            subprocess.run(["msiexec", "/x", str(HERE / msi), "/qn", "/norestart"])  # start clean
        clean(m["key"])
        rcs = []
        if first:
            rcs.append(msiexec(["/i", str(first)], HERE / f"t{n}-base.log"))
            seed(m["key"], m)
            print("    site values set: " + ", ".join(f"{p['name']}={p['site']!r}" for p in m["preserved"]))
        rcs.append(msiexec(["/i", str(HERE / second)], HERE / f"t{n}-install.log"))
        got = read(m["key"], names)
        for name in names:
            print(f"    {name:<9} {describe(got[name])}")
        status, fails = outcome(judged, rcs, failures(expected(m, kind), got))
        msiexec(["/x", str(HERE / second)], HERE / f"t{n}-uninstall.log")
        clean(m["key"])
        if status == "OBSERVED":
            line = (f"{title}: {len(fails)} of {len(names)} values not what preserve=\"yes\" promises"
                    + (" - " + "; ".join(fails) if fails else ""))
            print(f"  OBSERVED {line}")
            observed.append(line)
        else:
            for f in fails:
                print(f"  FAIL {f}")
            print(f"  {status} {title}")
            results.append((title, status == "PASS"))

    print("\n=== summary ===")
    for title, ok in results:
        print(f"  {'PASS' if ok else 'FAIL'} {title}")
    for line in observed:
        print(f"  OBSERVED {line}")
    print("msiexec logs: t<scenario>-*.log beside this script")
    return 0 if all(ok for _, ok in results) else 1


if __name__ == "__main__":
    sys.exit(main())
