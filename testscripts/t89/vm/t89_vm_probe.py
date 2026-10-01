"""#89, VM side - installing an older build that differs only in the 4th version field.
Run ELEVATED; take a VM snapshot first.

Leg A, without the guard (msis before D27) - the report, reproduced:
  A1 install old-90, A2 install old-80 over it   -> observed: does core.dll go missing?
  A3 repair old-80                                -> observed: does the repair bring it back?
  A4 uninstall
Leg B, with the guard (D27):
  B1 install new-90                    -> core.dll 1.2.3.90, the version recorded
  B2 install new-80 over it            -> refused, nothing changed
  B3 install new-90b (same version)    -> allowed, every file intact
  B4 install new-95                    -> the upgrade works, 1.2.3.95 recorded
  B5 install new-90 over it            -> refused, nothing changed
  B6 uninstall                         -> no files, nothing recorded
  B7 install new-90 with only the Child sub-feature -> the version is recorded all the same
  B8 install new-95, uninstall, install new-80      -> the supported way back works
Leg C, the documented limit: install old-90 (records nothing), then new-80 -> observed: not refused.
Leg D, the payload directly under <setup> with the only authored feature off:
  D1 install items-90, D2 install items-80 over it -> refused, D3 uninstall

Legs B and D, and the final cleanup, decide PASS/FAIL; legs A and C are observations. The probe refuses to start if anything
of its own is on the machine, and always ends by uninstalling its product and removing the
folders under MsisProbe89 and the registry keys under Software\\msis that are left empty.

    python t89_vm_probe.py --selftest   # checks the comparisons and the cleanup; installs nothing
    python t89_vm_probe.py              # ELEVATED, unattended
    python t89_vm_probe.py --cleanup    # ELEVATED: only the final cleanup, after an interrupted run
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
REFUSED = 1603
UNINSTALL = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall"
REFUSAL_TEXT = "A later version of"


# ---- pure comparisons (exercised by --selftest) ----

def diff_state(step: str, want: dict, seen: dict) -> list[str]:
    """want/seen: file -> version or content (None: absent), plus 'recorded' and 'arp'."""
    return [f"{step}: {k} is {seen.get(k)!r}, want {v!r}" for k, v in sorted(want.items()) if seen.get(k) != v]


def refusal_logged(log: bytes) -> bool:
    for codec in ("utf-16", "mbcs", "utf-8"):
        try:
            if REFUSAL_TEXT in log.decode(codec):
                return True
        except (UnicodeDecodeError, LookupError):
            continue
    return False


# ---- the machine ----

BASES: dict[str, Path] | None = None  # the selftest points these at a temporary tree
REG_ROOT = winreg.HKEY_LOCAL_MACHINE  # the selftest uses HKEY_CURRENT_USER


def resolve(key: str) -> Path:
    base, rel = key.split(":", 1)
    if BASES is not None:
        return BASES[base] / rel
    return Path(os.environ["ProgramW6432" if base == "PF" else "ProgramData"]) / rel


def file_version(path: Path) -> str | None:
    if not path.exists():
        return None
    out = subprocess.run(["powershell", "-NoProfile", "-NonInteractive", "-Command",
                          "$ErrorActionPreference='Stop'; (Get-Item -LiteralPath $env:T89_FILE).VersionInfo.FileVersion"],
                         capture_output=True, text=True, env=dict(os.environ, T89_FILE=str(path)))
    if out.returncode != 0:
        raise SystemExit(f"reading the version of {path} failed:\n{out.stderr}")
    return out.stdout.strip() or "(no version)"


def recorded(key: str) -> str | None:
    """The version a guarded package recorded, from the 64-bit view the x64 packages write."""
    try:
        with winreg.OpenKey(REG_ROOT, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY) as k:
            return winreg.QueryValueEx(k, "Version")[0]
    except OSError:
        return None


def key_exists(key: str) -> bool:
    try:
        winreg.OpenKey(REG_ROOT, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY).Close()
        return True
    except OSError:
        return False


def arp(product: str) -> dict[str, str]:
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
    """Deletes key when it has no values and no subkeys; True when it is gone."""
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
    """Uninstalls the probe's product, then removes the folders under MsisProbe89 and the
    registry keys from its key up to Software\\msis that are left empty. Nothing with content.
    Returns what it could not clean, which fails the run: leg C ends with the product installed,
    so a failed final uninstall must not pass unnoticed."""
    problems = []
    for code in arp(m["product"]):
        rc = msiexec("/x", code, log=f"cleanup-{code}.log")
        print(f"cleanup: uninstalling {m['product']} {code}: rc={rc}")
        if rc not in OK:
            problems.append(f"cleanup: uninstalling {code} returned {rc}")
    if still := arp(m["product"]):
        problems.append(f"cleanup: {m['product']} is still installed: {still}")
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
    assert diff_state("s", {"a": "1", "b": None}, {"a": "1"}) == []
    assert diff_state("s", {"a": "1"}, {"a": None}) == ["s: a is None, want '1'"]
    assert refusal_logged(f"x {REFUSAL_TEXT} y".encode("utf-16"))
    assert refusal_logged(f"x {REFUSAL_TEXT} y".encode("utf-8")) and not refusal_logged(b"nothing")
    cmd = msiexec_args("/x", "{C}", log="l.log")
    assert "/qn" in cmd and "/norestart" in cmd, cmd

    # The real cleanup, on a temporary tree and a temporary HKCU key, the installer faked,
    # reached through a failing run: empty folders and empty keys go, content stays.
    global BASES, REG_ROOT
    with tempfile.TemporaryDirectory() as tmp:
        BASES, REG_ROOT = {"PF": Path(tmp) / "pf", "PD": Path(tmp) / "pd"}, winreg.HKEY_CURRENT_USER
        base = r"Software\msis-t89-selftest"
        try:
            m = {"top": "Top", "product": "P", "registry_key": r"Software\msis\Packages\SELFTEST-T89"}
            resolve("PF:Top\\App\\empty").mkdir(parents=True)
            (resolve("PF:Top\\App") / "kept.txt").write_text("not the probe's", encoding="utf-8")
            resolve("PF:Outside").mkdir(parents=True)
            had_msis = key_exists(r"Software\msis")
            winreg.CreateKeyEx(REG_ROOT, m["registry_key"], 0, winreg.KEY_WRITE | winreg.KEY_WOW64_64KEY).Close()
            with winreg.CreateKeyEx(REG_ROOT, base, 0, winreg.KEY_WRITE | winreg.KEY_WOW64_64KEY) as k:
                winreg.SetValueEx(k, "v", 0, winreg.REG_SZ, "kept")
            calls = []
            problems: list[str] = []

            def fake_arp(product: str) -> dict[str, str]:
                return {} if calls else {"{CODE}": "1"}  # gone once uninstalled

            def failing() -> None:
                raise RuntimeError("a step blew up")

            try:
                run_with_cleanup(m, failing, lambda mm: problems.extend(cleanup(
                    mm, arp=fake_arp, msiexec=lambda *a, log: calls.append(a) or 0)))
                raise AssertionError("the failure was swallowed")
            except RuntimeError:
                pass
            assert calls == [("/x", "{CODE}")], calls
            assert len(problems) == 1 and "still holds" in problems[0], problems  # kept.txt, reported
            # An uninstall that fails, or leaves the product registered, is a problem too.
            failed = cleanup(m, arp=lambda p: {"{CODE}": "1"}, msiexec=lambda *a, log: 1603)
            assert any("returned 1603" in p for p in failed) and any("still installed" in p for p in failed), failed
            assert not resolve("PF:Top\\App\\empty").exists() and resolve("PF:Top\\App\\kept.txt").exists()
            assert resolve("PF:Outside").is_dir()
            assert not key_exists(m["registry_key"]), "the empty probe key is removed"
            assert key_exists(r"Software\msis") == had_msis, "a parent key is removed only if it was empty"
            assert key_exists(base), "a key with values is never removed"
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
        cleanup(m)
        return 0
    top = resolve(f"PF:{m['top']}")
    if top.exists() or arp(m["product"]) or key_exists(m["registry_key"]):
        print(f"refusing to start: {top}, the product or HKLM\\{m['registry_key']} is already here. "
              "Run --cleanup, or restore the snapshot.")
        return 2

    failures: list[str] = []
    observations: list[str] = []
    run_with_cleanup(m, lambda: legs(m, failures, observations), lambda mm: failures.extend(cleanup(mm)))
    print("\n=== observations (legs A and C):")
    for o in observations:
        print(f"  {o}")
    print(f"=== summary (leg B): {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


def legs(m: dict, failures: list[str], observations: list[str]) -> None:
    folder = resolve(m["dir"])
    pk = m["packages"]
    msi = {n: str(HERE / f"{n}.msi") for n in pk}

    def state() -> dict:
        def text(name: str) -> str | None:
            p = folder / name
            return p.read_text(encoding="utf-8") if p.exists() else None
        return {"core.dll": file_version(folder / "core.dll"), "static.txt": text("static.txt"),
                "changing.txt": text("changing.txt"), "extra\\child.txt": text("extra\\child.txt"),
                "recorded": recorded(m["registry_key"]), "arp": sorted(arp(m["product"]).values())}

    def installed(name: str, recorded_version: str | None, *, child: bool = True, core: bool = True) -> dict:
        v = pk[name]["version"]
        return {"core.dll": v if core else None, "static.txt": "never changes\n" if core else None,
                "changing.txt": pk[name]["changing"] if core else None,
                "extra\\child.txt": "the child feature's file\n" if child else None,
                "recorded": recorded_version, "arp": [v]}

    nothing = {"core.dll": None, "static.txt": None, "changing.txt": None, "extra\\child.txt": None,
               "recorded": None, "arp": []}

    def padded(v: str) -> str:
        return ".".join(f"{int(x):05d}" for x in (v.split(".") + ["0"] * 4)[:4])

    def check(step: str, rc: int, want_rc: tuple, want: dict, log: str | None = None) -> None:
        found = [] if rc in want_rc else [f"{step}: msiexec returned {rc}, want {want_rc}"]
        if log and want_rc == (REFUSED,) and not refusal_logged((LOGS / log).read_bytes()):
            found.append(f"{step}: the log does not carry the refusal message {REFUSAL_TEXT!r}")
        found += diff_state(step, want, state())
        print(f"{step}: {'PASS' if not found else 'FAIL'}")
        for x in found:
            print(f"  - {x}")
        failures.extend(found)

    def observe(step: str, rc: int) -> dict:
        s = state()
        observations.append(f"{step}: msiexec rc={rc}; {json.dumps(s)}")
        return s

    # Leg A: the report, without the guard.
    check("A1 install old-90", msiexec("/i", msi["old-90"], log="A1.log"), OK, installed("old-90", None))
    s = observe("A2 install old-80 over old-90", rc := msiexec("/i", msi["old-80"], log="A2.log"))
    observations.append("A2: the report REPRODUCES: success, core.dll missing" if rc in OK and s["core.dll"] is None
                        else "A2: the report does NOT reproduce as described")
    s = observe("A3 repair old-80", msiexec("/fa", msi["old-80"], log="A3.log"))
    observations.append(f"A3: after the repair core.dll is {s['core.dll']!r}")
    check("A4 uninstall", msiexec("/x", msi["old-80"], log="A4.log"), OK, nothing)

    # Leg B: the guard.
    check("B1 install new-90", msiexec("/i", msi["new-90"], log="B1.log"), OK, installed("new-90", padded("1.2.3.90")))
    check("B2 install new-80 over new-90: refused", msiexec("/i", msi["new-80"], log="B2.log"), (REFUSED,),
          installed("new-90", padded("1.2.3.90")), log="B2.log")
    check("B3 install new-90b (same version)", msiexec("/i", msi["new-90b"], log="B3.log"), OK,
          installed("new-90b", padded("1.2.3.90")))
    check("B4 upgrade to new-95", msiexec("/i", msi["new-95"], log="B4.log"), OK, installed("new-95", padded("1.2.3.95")))
    check("B5 install new-90 over new-95: refused", msiexec("/i", msi["new-90"], log="B5.log"), (REFUSED,),
          installed("new-95", padded("1.2.3.95")), log="B5.log")
    check("B6 uninstall", msiexec("/x", msi["new-95"], log="B6.log"), OK, nothing)
    if key_exists(m["registry_key"]):
        observations.append("B6: the uninstall left the (empty) registry key behind")
    child = pk["new-90"]["features"]["Child"]
    check("B7 install new-90, only Child", msiexec("/i", msi["new-90"], f"ADDLOCAL={child}", log="B7.log"), OK,
          installed("new-90", padded("1.2.3.90"), core=False))
    check("B7 uninstall", msiexec("/x", msi["new-90"], log="B7x.log"), OK, nothing)
    check("B8 install new-95", msiexec("/i", msi["new-95"], log="B8a.log"), OK, installed("new-95", padded("1.2.3.95")))
    check("B8 uninstall new-95", msiexec("/x", msi["new-95"], log="B8b.log"), OK, nothing)
    check("B8 install new-80", msiexec("/i", msi["new-80"], log="B8c.log"), OK, installed("new-80", padded("1.2.3.80")))
    check("B8 uninstall new-80", msiexec("/x", msi["new-80"], log="B8d.log"), OK, nothing)

    # Leg C: an install from before the guard records nothing, so it cannot be protected.
    check("C1 install old-90", msiexec("/i", msi["old-90"], log="C1.log"), OK, installed("old-90", None))
    s = observe("C2 install new-80 over old-90", rc := msiexec("/i", msi["new-80"], log="C2.log"))
    observations.append("C2: not refused, as D27 states" if rc in OK else f"C2: refused (rc={rc}), which D27 does not claim")
    for code in arp(m["product"]):  # whichever of old-90 and new-80 C2 left installed
        msiexec("/x", code, log=f"C3-{code}.log")
    check("C3 uninstall", 0, OK, nothing)

    # Leg D: the payload directly under <setup>, through the generated package-items feature.
    check("D1 install items-90", msiexec("/i", msi["items-90"], log="D1.log"), OK,
          installed("items-90", padded("1.2.3.90"), child=False))
    check("D2 install items-80 over items-90: refused", msiexec("/i", msi["items-80"], log="D2.log"), (REFUSED,),
          installed("items-90", padded("1.2.3.90"), child=False), log="D2.log")
    check("D3 uninstall", msiexec("/x", msi["items-90"], log="D3.log"), OK, nothing)


if __name__ == "__main__":
    sys.exit(main())
