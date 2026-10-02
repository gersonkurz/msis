"""#93 and #94, VM side - prerequisites detected and run per-machine (D32).
Run from a NON-elevated shell: Burn must elevate the per-machine packages itself, so expect a UAC
prompt for each install and each uninstall (four in all) - click Yes. Take a VM snapshot first.

For each bundle (old: msis before the fix, the control; new: with it), install with /quiet, read
the markers the stand-ins left, then uninstall with /quiet:
  new   NETFX and VC detected Present and not run (#93)
  old   (the control) NETFX detected Absent and run: NETFRAMEWORK45 was never set
Every stand-in that runs runs ELEVATED - EPM (per-machine="yes"), EPU (no attribute) and the old
bundle's NETFX (no PerMachine) alike - because a package without PerMachine takes the bundle's
scope, which is per-machine (#94: not a bug). Both: the bundle and its MSI registered after
install, neither after uninstall.

The probe refuses to start if it is elevated, if .NET 4.8.1 or the VC++ x64 runtime is missing
(the stand-ins would then be expected to run, which the probe does not assess), or if anything of
its own is on the machine. It always ends by uninstalling its bundles and MSI, deleting its
markers, and removing the folders that are then empty. A failed cleanup fails the run.

    python t93_vm_probe.py --selftest
    python t93_vm_probe.py              # NOT elevated; click Yes on each UAC prompt
    python t93_vm_probe.py --cleanup    # after an interrupted run (also prompts)
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


def detected(log: str) -> dict[str, str]:
    return dict(re.findall(r"Detected package: (\w+), state: (\w+)", log))


def markers_dir() -> Path:
    return Path(os.environ["LOCALAPPDATA"]) / "MsisProbe93" / "markers"


def read_markers(stubs: list[str], which: str) -> dict[str, str | None]:
    """stub -> "elevated=True"/"elevated=False", or None when it did not run."""
    out = {}
    for s in stubs:
        p = markers_dir() / f"{s}-{which}.txt"
        out[s] = p.read_text(encoding="utf-8").strip() if p.exists() else None
    return out


# The marker writes Go's %v of a bool: "elevated=true" / "elevated=false".
ELEVATED = "elevated=true"


def expected(which: str) -> dict[str, str | None]:
    if which == "new":
        return {"NETFX": None, "VC": None, "EPM": ELEVATED, "EPU": ELEVATED}
    return {"NETFX": ELEVATED, "VC": None, "EPM": ELEVATED, "EPU": ELEVATED}


def registrations(product: str) -> list[str]:
    found = []
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
                            if winreg.QueryValueEx(sub, "DisplayName")[0] == product:
                                found.append(name)
                        except FileNotFoundError:
                            pass
    return sorted(set(found))


def runtimes() -> tuple[object, object]:
    def read(key: str, value: str):
        try:
            with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY) as k:
                return winreg.QueryValueEx(k, value)[0]
        except FileNotFoundError:
            return None
    return (read(r"SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full", "Release"),
            read(r"SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64", "Installed"))


def run(cmd: list[str]) -> int:
    LOGS.mkdir(exist_ok=True)
    return subprocess.run(cmd).returncode


def bundle_args(exe: str, *args: str, log: str) -> list[str]:
    return [exe, *args, "/quiet", "/norestart", "/log", str(LOGS / log)]


def preflight(m: dict, elevated: bool, rt: tuple, registrations=registrations) -> str:
    if elevated:
        return "this shell is elevated; run the probe from a NON-elevated one, so Burn has to elevate the per-machine packages itself"
    release, vc = rt
    if not isinstance(release, int) or release < 533320 or vc != 1:
        return f".NET 4.8.1 (Release {release!r}) or the VC++ x64 runtime (Installed {vc!r}) is missing; this probe needs both present"
    here = [p for p in [m["app_product"]] + [b["product"] for b in m["bundles"].values()] if registrations(p)]
    if markers_dir().exists():
        here.append(str(markers_dir()))
    return f"{here} already here. Run --cleanup, or restore the snapshot." if here else ""


def cleanup(m: dict, registrations=registrations, run=run) -> list[str]:
    problems = []
    for which, b in m["bundles"].items():
        if registrations(b["product"]):
            rc = run(bundle_args(str(HERE / b["exe"]), "/uninstall", log=f"cleanup-{which}.log"))
            if rc not in OK:
                problems.append(f"cleanup: uninstalling the {which} bundle returned {rc}")
    for code in registrations(m["app_product"]):
        rc = run(["msiexec", "/x", code, "/qn", "/norestart", "/l*v", str(LOGS / f"cleanup-{code}.log")])
        if rc not in OK:
            problems.append(f"cleanup: uninstalling {code} returned {rc}")
    for p in [m["app_product"]] + [b["product"] for b in m["bundles"].values()]:
        if left := registrations(p):
            problems.append(f"cleanup: {p} still registered: {left}")
    for which in m["bundles"]:
        for s in m["stubs"]:
            (markers_dir() / f"{s}-{which}.txt").unlink(missing_ok=True)
    for folder in (markers_dir(), markers_dir().parent):
        try:
            folder.rmdir()
        except FileNotFoundError:
            pass
        except OSError:
            problems.append(f"cleanup: {folder} is not empty; left alone")
    for p in problems:
        print(p)
    return problems


def selftest() -> int:
    assert detected("i101: Detected package: Prereq_netfx_4_8_1, state: Present, cached: No") == {"Prereq_netfx_4_8_1": "Present"}
    m = {"app_product": "A", "bundles": {"new": {"product": "B"}}, "stubs": ["EPM"]}
    assert "NON-elevated" in preflight(m, True, (533509, 1), registrations=lambda p: [])
    assert "missing" in preflight(m, False, (None, 1), registrations=lambda p: [])
    assert "missing" in preflight(m, False, (533509, None), registrations=lambda p: [])
    with tempfile.TemporaryDirectory() as tmp:
        saved = os.environ["LOCALAPPDATA"]
        os.environ["LOCALAPPDATA"] = tmp
        try:
            assert preflight(m, False, (533509, 1), registrations=lambda p: []) == ""
            markers_dir().mkdir(parents=True)
            # What the marker really writes (marker/main.go formats "elevated=%v" with a Go bool).
            (markers_dir() / "EPM-new.txt").write_text("elevated=true\n", encoding="utf-8")
            assert read_markers(["EPM"], "new") == {"EPM": expected("new")["EPM"]}
            assert "already here" in preflight(m, False, (533509, 1), registrations=lambda p: [])
            assert cleanup(m, registrations=lambda p: [], run=lambda c: 0) == []
            assert not markers_dir().parent.exists()
        finally:
            os.environ["LOCALAPPDATA"] = saved
    assert "/quiet" in bundle_args("b.exe", log="l") and "/norestart" in bundle_args("b.exe", log="l")
    print("selftest: PASS")
    return 0


def legs(m: dict, failures: list[str]) -> None:
    for which in ("old", "new"):
        b = m["bundles"][which]
        exe = str(HERE / b["exe"])
        rc = run(bundle_args(exe, log=f"{which}.log"))
        log = (LOGS / f"{which}.log").read_text(encoding="utf-8", errors="replace") if (LOGS / f"{which}.log").exists() else ""
        found = [] if rc in OK else [f"{which}: install returned {rc}"]
        seen, want = read_markers(m["stubs"], which), expected(which)
        for s in m["stubs"]:
            if seen[s] != want[s]:
                found.append(f"{which}: {s} {'did not run' if seen[s] is None else seen[s]}, want {'not run' if want[s] is None else want[s]}")
        states = detected(log)
        netfx = next((v for k, v in states.items() if k.startswith("Prereq_netfx")), None)
        if netfx != ("Present" if which == "new" else "Absent"):
            found.append(f"{which}: Burn detected the .NET prerequisite as {netfx!r}")
        if not registrations(b["product"]) or not registrations(m["app_product"]):
            found.append(f"{which}: the bundle or its MSI is not registered after install")
        print(f"{which} install: {'PASS' if not found else 'FAIL'} {seen}")
        for x in found:
            print(f"  - {x}")
        failures.extend(found)
        rc = run(bundle_args(exe, "/uninstall", log=f"{which}-uninstall.log"))
        found = [] if rc in OK else [f"{which}: uninstall returned {rc}"]
        if registrations(b["product"]) or registrations(m["app_product"]):
            found.append(f"{which}: still registered after uninstall")
        print(f"{which} uninstall: {'PASS' if not found else 'FAIL'}")
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
    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    if args.cleanup:
        return 0 if not cleanup(m) else 1
    if refusal := preflight(m, bool(ctypes.windll.shell32.IsUserAnAdmin()), runtimes()):
        print(f"refusing to start: {refusal}")
        return 2
    failures: list[str] = []
    try:
        legs(m, failures)
    finally:
        failures.extend(cleanup(m))
    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
