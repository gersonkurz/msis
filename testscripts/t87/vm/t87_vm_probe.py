"""#87, VM side - feature choices across upgrades, with explicit feature ids (D28).
Run ELEVATED; take a VM snapshot first.

The customer's choice, in every leg: v1 installed with Main and Debug only, so Docs (on by
default) is deselected and Debug (off by default) selected. Each step asks Windows Installer
for every feature's state (MsiQueryFeatureState, by ProductCode and feature id), and checks
each installed feature's file holds that package's version.

Leg 1, insertion with ids, then freeze-all and reorder:
  1.1 install v1 (Main, Debug), seed runtime data
  1.2 upgrade to v2id   -> Main, Debug kept; Docs still absent; NewOn > NewOnPart (new, on by
                           default) installed; NewTool (new, off) absent; runtime data intact
  1.3 REMOVE=NewOn      -> NewOn and NewOnPart absent, nothing else changed
  1.4 upgrade to v3     -> Main, Debug kept; NewOn stays absent (migrated, not defaulted);
                           NewTool absent; Docs gone from the package
  1.5 uninstall         -> only the runtime data left
Leg 2, a release skipped: v1 (Main, Debug) straight to v3 -> Main, Debug; NewOn new, so on.
Leg 3, the control, inserted WITHOUT an id: v1 (Main, Debug) to v2pos -> the #87 failure must
  reproduce (Debug's state goes to NewTool), or the probe cannot tell the two apart.

Every leg decides PASS/FAIL, and so does the final cleanup. The probe refuses to start if
anything of its own is on the machine, and always ends by uninstalling its product, deleting the
runtime file it wrote, and removing the folders under MsisProbe87 and the registry keys under
Software\\msis that are then empty.

    python t87_vm_probe.py --selftest   # checks the comparisons and the cleanup; installs nothing
    python t87_vm_probe.py              # ELEVATED, unattended
    python t87_vm_probe.py --cleanup    # ELEVATED: only the final cleanup, after an interrupted run
"""

from __future__ import annotations

import argparse
import ctypes
import json
import os
import subprocess
import sys
import tempfile
import winreg
from pathlib import Path

HERE = Path(__file__).resolve().parent
LOGS = HERE / "logs"
OK = (0, 3010)
UNINSTALL = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall"
STATES = {3: "local", 2: "absent", 1: "advertised", 4: "source", -1: "unknown"}
RUNTIME = "the customer's data\n"


# ---- pure comparisons (exercised by --selftest) ----

def diff_state(step: str, want: dict, seen: dict) -> list[str]:
    return [f"{step}: {k} is {seen.get(k)!r}, want {v!r}" for k, v in sorted(want.items()) if seen.get(k) != v]


# ---- the machine ----

BASES: dict[str, Path] | None = None  # the selftest points these at a temporary tree
REG_ROOT = winreg.HKEY_LOCAL_MACHINE  # the selftest uses HKEY_CURRENT_USER


def resolve(key: str) -> Path:
    base, rel = key.split(":", 1)
    if BASES is not None:
        return BASES[base] / rel
    return Path(os.environ["ProgramW6432" if base == "PF" else "ProgramData"]) / rel


def feature_state(product_code: str, feature: str) -> str:
    msi = ctypes.windll.msi
    msi.MsiQueryFeatureStateW.argtypes = [ctypes.c_wchar_p, ctypes.c_wchar_p]
    msi.MsiQueryFeatureStateW.restype = ctypes.c_int
    n = msi.MsiQueryFeatureStateW(product_code, feature)
    return STATES.get(n, str(n))


def key_exists(key: str) -> bool:
    try:
        winreg.OpenKey(REG_ROOT, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY).Close()
        return True
    except OSError:
        return False


def arp(product: str) -> dict[str, str]:
    """ProductCode -> DisplayVersion of every installed product called product."""
    found = {}
    for view in (winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
        try:
            key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, UNINSTALL, 0, winreg.KEY_READ | view)
        except OSError:
            continue
        with key:
            for i in range(winreg.QueryInfoKey(key)[0]):
                name = winreg.EnumKey(key, i)
                try:
                    with winreg.OpenKey(key, name) as sub:
                        if winreg.QueryValueEx(sub, "DisplayName")[0] == product:
                            found[name] = winreg.QueryValueEx(sub, "DisplayVersion")[0]
                except OSError:
                    pass
    return found


def msiexec_args(*args: str, log: str) -> list[str]:
    return ["msiexec", *args, "/qn", "/norestart", "/l*v", str(LOGS / log)]


def msiexec(*args: str, log: str) -> int:
    LOGS.mkdir(exist_ok=True)
    return subprocess.run(msiexec_args(*args, log=log)).returncode


def run_with_cleanup(m: dict, body, clean) -> None:
    try:
        body()
    finally:
        clean(m)


def delete_if_empty(key: str) -> bool:
    try:
        with winreg.OpenKey(REG_ROOT, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY) as k:
            subkeys, values, _ = winreg.QueryInfoKey(k)
    except OSError:
        return True
    if subkeys or values:
        return False
    winreg.DeleteKeyEx(REG_ROOT, key, winreg.KEY_WOW64_64KEY)
    return True


def cleanup(m: dict, arp=arp, msiexec=msiexec) -> list[str]:
    """Uninstalls the probe's product, deletes exactly the runtime files it seeded, then removes
    the folders under MsisProbe87 and the registry keys up to Software\\msis that are left empty.
    Returns what it could not clean, which fails the run."""
    problems = []
    for code in arp(m["product"]):
        rc = msiexec("/x", code, log=f"cleanup-{code}.log")
        print(f"cleanup: uninstalling {m['product']} {code}: rc={rc}")
        if rc not in OK:
            problems.append(f"cleanup: uninstalling {code} returned {rc}")
    if still := arp(m["product"]):
        problems.append(f"cleanup: {m['product']} is still installed: {still}")
    for key in m["runtime"]:
        resolve(key).unlink(missing_ok=True)
    top = resolve(f"PF:{m['top']}")
    if top.exists():
        for folder, _, _ in sorted(os.walk(top, topdown=False), key=lambda w: -len(w[0])):
            try:
                os.rmdir(folder)  # only succeeds when empty
            except OSError:
                pass
        if top.exists():
            problems.append(f"cleanup: {top} still holds {[str(p) for p in top.rglob('*')]}; left alone")
    key = m["registry_key"]
    while key.lower().startswith("software\\msis") and delete_if_empty(key):
        key = key.rsplit("\\", 1)[0]
    if key_exists(m["registry_key"]):
        problems.append(f"cleanup: HKLM\\{m['registry_key']} still holds values; left alone")
    for p in problems:
        print(p)
    return problems


def selftest() -> int:
    assert diff_state("s", {"a": "local"}, {"a": "local", "b": "x"}) == []
    assert diff_state("s", {"a": "local"}, {"a": "absent"}) == ["s: a is 'absent', want 'local'"]
    cmd = msiexec_args("/x", "{C}", log="l.log")
    assert "/qn" in cmd and "/norestart" in cmd, cmd
    # An unknown product: Windows Installer answers "unknown", which the steps treat as wrong.
    assert feature_state("{00000000-0000-0000-0000-000000000000}", "Main") == "unknown"

    global BASES, REG_ROOT
    with tempfile.TemporaryDirectory() as tmp:
        BASES, REG_ROOT = {"PF": Path(tmp) / "pf", "PD": Path(tmp) / "pd"}, winreg.HKEY_CURRENT_USER
        base = r"Software\msis-t87-selftest"
        try:
            m = {"top": "Top", "product": "P", "registry_key": r"Software\msis\Packages\SELFTEST-T87",
                 "runtime": ["PF:Top\\App\\data\\user.dat"]}
            resolve("PF:Top\\App\\data").mkdir(parents=True)
            resolve("PF:Top\\App\\data\\user.dat").write_text("seeded", encoding="utf-8")
            resolve("PF:Top\\App\\empty").mkdir(parents=True)
            resolve("PF:Top\\App\\kept.txt").write_text("not the probe's", encoding="utf-8")
            resolve("PF:Outside").mkdir(parents=True)
            had_msis = key_exists(r"Software\msis")
            winreg.CreateKeyEx(REG_ROOT, m["registry_key"], 0, winreg.KEY_WRITE | winreg.KEY_WOW64_64KEY).Close()
            with winreg.CreateKeyEx(REG_ROOT, base, 0, winreg.KEY_WRITE | winreg.KEY_WOW64_64KEY) as k:
                winreg.SetValueEx(k, "v", 0, winreg.REG_SZ, "kept")
            calls, problems = [], []

            def fake_arp(product: str) -> dict[str, str]:
                return {} if calls else {"{CODE}": "1"}

            def failing() -> None:
                raise RuntimeError("a step blew up")

            try:
                run_with_cleanup(m, failing, lambda mm: problems.extend(cleanup(
                    mm, arp=fake_arp, msiexec=lambda *a, log: calls.append(a) or 0)))
                raise AssertionError("the failure was swallowed")
            except RuntimeError:
                pass
            assert calls == [("/x", "{CODE}")], calls
            assert len(problems) == 1 and "still holds" in problems[0], problems
            assert not resolve("PF:Top\\App\\data").exists(), "the seeded file and its emptied folder go"
            assert not resolve("PF:Top\\App\\empty").exists() and resolve("PF:Top\\App\\kept.txt").exists()
            assert resolve("PF:Outside").is_dir()
            assert not key_exists(m["registry_key"]) and key_exists(r"Software\msis") == had_msis
            assert key_exists(base), "a key with values is never removed"
            failed = cleanup(m, arp=lambda p: {"{CODE}": "1"}, msiexec=lambda *a, log: 1603)
            assert any("returned 1603" in p for p in failed) and any("still installed" in p for p in failed), failed
        finally:
            try:
                winreg.DeleteKeyEx(winreg.HKEY_CURRENT_USER, base, winreg.KEY_WOW64_64KEY)
            except OSError:
                pass
            BASES, REG_ROOT = None, winreg.HKEY_LOCAL_MACHINE
    print("selftest: PASS")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs and uninstalls per-machine MSIs)")
        return 2
    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    if args.cleanup:
        return 0 if not cleanup(m) else 1
    top = resolve(f"PF:{m['top']}")
    if top.exists() or arp(m["product"]) or key_exists(m["registry_key"]):
        print(f"refusing to start: {top}, the product or HKLM\\{m['registry_key']} is already here. "
              "Run --cleanup, or restore the snapshot.")
        return 2

    failures: list[str] = []
    run_with_cleanup(m, lambda: legs(m, failures), lambda mm: failures.extend(cleanup(mm)))
    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


def legs(m: dict, failures: list[str]) -> None:
    folder = resolve(m["dir"])
    pk = m["packages"]
    msi = {n: str(HERE / f"{n}.msi") for n in pk}
    data = resolve(m["runtime"][0])

    def observe(package: str) -> dict:
        """Every feature of package by title: its state, and whether its file says so."""
        codes = list(arp(m["product"]))
        seen = {"arp": sorted(arp(m["product"]).values()), "runtime data": data.read_text(encoding="utf-8") if data.exists() else None}
        if len(codes) != 1:
            return seen
        version = pk[package]["version"]
        for title, fid in pk[package]["ids"].items():
            state = feature_state(codes[0], fid)
            file = folder / (title.lower() + ".txt")
            content = file.read_text(encoding="utf-8") if file.exists() else None
            consistent = (content == f"{title} {version}\n") if state == "local" else content is None
            seen[title] = state if consistent else f"{state}, but its file holds {content!r}"
        return seen

    def check(step: str, rc: int, package: str, local: list[str], absent: list[str], runtime: bool) -> None:
        want = {t: "local" for t in local} | {t: "absent" for t in absent}
        want["arp"] = [pk[package]["version"]]
        want["runtime data"] = RUNTIME if runtime else None
        found = ([] if rc in OK else [f"{step}: msiexec returned {rc}"]) + diff_state(step, want, observe(package))
        print(f"{step}: {'PASS' if not found else 'FAIL'}")
        for x in found:
            print(f"  - {x}")
        failures.extend(found)

    def install_v1(step: str, log: str) -> None:
        ids = pk["v1"]["ids"]
        rc = msiexec("/i", msi["v1"], f"ADDLOCAL={ids['Main']},{ids['Debug']}", log=log)
        check(step, rc, "v1", ["Main", "Debug"], ["Docs"], runtime=False)

    def uninstall(step: str, package: str, log: str, runtime: bool) -> None:
        rc = msiexec("/x", msi[package], log=log)
        found = ([] if rc in OK else [f"{step}: msiexec returned {rc}"])
        if arp(m["product"]):
            found.append(f"{step}: still installed")
        left = sorted(str(p.relative_to(folder)) for p in folder.rglob("*") if p.is_file()) if folder.exists() else []
        want_left = ["data\\user.dat"] if runtime else []
        if left != want_left:
            found.append(f"{step}: left behind {left}, want {want_left}")
        print(f"{step}: {'PASS' if not found else 'FAIL'}")
        for x in found:
            print(f"  - {x}")
        failures.extend(found)

    # Leg 1.
    install_v1("1.1 install v1 (Main, Debug)", "1-1.log")
    data.parent.mkdir(parents=True, exist_ok=True)
    data.write_text(RUNTIME, encoding="utf-8")
    check("1.2 upgrade to v2id", msiexec("/i", msi["v2id"], log="1-2.log"), "v2id",
          ["Main", "Debug", "NewOn", "NewOnPart"], ["Docs", "NewTool"], runtime=True)
    check("1.3 REMOVE=NewOn", msiexec("/i", msi["v2id"], f"REMOVE={pk['v2id']['ids']['NewOn']}", log="1-3.log"), "v2id",
          ["Main", "Debug"], ["Docs", "NewTool", "NewOn", "NewOnPart"], runtime=True)
    check("1.4 upgrade to v3 (frozen, reordered, Docs removed)", msiexec("/i", msi["v3"], log="1-4.log"), "v3",
          ["Main", "Debug"], ["NewTool", "NewOn", "NewOnPart"], runtime=True)
    uninstall("1.5 uninstall", "v3", "1-5.log", runtime=True)
    data.unlink(missing_ok=True)

    # Leg 2: a release skipped.
    install_v1("2.1 install v1 (Main, Debug)", "2-1.log")
    check("2.2 upgrade straight to v3", msiexec("/i", msi["v3"], log="2-2.log"), "v3",
          ["Main", "Debug", "NewOn", "NewOnPart"], ["NewTool"], runtime=False)
    uninstall("2.3 uninstall", "v3", "2-3.log", runtime=False)

    # Leg 3: the control. Without an id the insertion must still break, or the probe proves nothing.
    install_v1("3.1 install v1 (Main, Debug)", "3-1.log")
    rc = msiexec("/i", msi["v2pos"], log="3-2.log")
    seen = observe("v2pos")
    broken = seen.get("NewTool") == "local" and seen.get("Debug") == "absent"
    print(f"3.2 upgrade to v2pos (control): {'PASS' if rc in OK and broken else 'FAIL'}: {seen}")
    if not (rc in OK and broken):
        failures.append(f"3.2: the control did not reproduce #87 (rc={rc}, {seen}), so legs 1 and 2 cannot tell ids apart from luck")
    uninstall("3.3 uninstall", "v2pos", "3-3.log", runtime=False)


if __name__ == "__main__":
    sys.exit(main())
