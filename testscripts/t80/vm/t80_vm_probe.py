"""#80, VM side - does BrowseDlg's OK set the folder? Run ELEVATED on a snapshotted VM.

For each package in manifest.json this opens the normal install UI with a verbose log, and you
click through it as the prompt says: pick a folder in BrowseDlg and press OK. Then it reads the
log - every value WIXUI_INSTALLDIR / INSTALLDIR took, and INSTALLDIR at the end - checks that
app.txt landed in the folder you picked last, and uninstalls silently.

    python t80_vm_probe.py --selftest   # checks the verdict; installs nothing
    python t80_vm_probe.py              # ELEVATED
"""

from __future__ import annotations

import argparse
import ctypes
import json
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = Path(r"C:\T80")


def folders(name: str, browse_from_customize: bool) -> dict[str, Path]:
    f = {"change": ROOT / f"{name}-change"}
    if browse_from_customize:
        f["browse"] = ROOT / f"{name}-browse"
    return f


def norm(p: str) -> str:
    return p.rstrip("\\").lower()


def verdict(picked: dict[str, Path], log: str, app_exists: bool, rc: int) -> list[str]:
    """Failures, empty when the package passed. Pure, so --selftest can exercise it.

    picked: the folders the user was asked to choose, in click order (change, then browse).
    log: the msiexec verbose log. app_exists: app.txt is in the last picked folder.
    """
    failures = []
    if rc != 0:
        failures.append(f"msiexec returned {rc}")
    # Every value WIXUI_INSTALLDIR or INSTALLDIR was given during the UI sequence.
    values = [norm(v) for v in re.findall(r"Modifying (?:WIXUI_INSTALLDIR|INSTALLDIR) property\..*?new value: '([^']*)'", log)]
    for step, folder in picked.items():
        if norm(str(folder)) not in values:
            failures.append(f"{step}: the folder picked in BrowseDlg ({folder}) never reached the property - OK did nothing")
    final = re.findall(r"Property\(S\): INSTALLDIR = (.*)", log)
    last = list(picked.values())[-1]
    if not final:
        failures.append("the log has no final INSTALLDIR")
    elif norm(final[-1].strip()) != norm(str(last)):
        failures.append(f"installed to {final[-1].strip()}, not to the last picked folder {last}")
    if not app_exists:
        failures.append(f"app.txt is not in {last}")
    return failures


def selftest() -> int:
    picked = {"change": Path(r"C:\T80\full-change"), "browse": Path(r"C:\T80\full-browse")}
    good = ("MSI (c) PROPERTY CHANGE: Modifying WIXUI_INSTALLDIR property. Its current value is 'x'. Its new value: 'C:\\T80\\full-change\\'.\n"
            "MSI (c) PROPERTY CHANGE: Modifying INSTALLDIR property. Its current value is 'y'. Its new value: 'C:\\T80\\full-browse\\'.\n"
            "Property(S): INSTALLDIR = C:\\T80\\full-browse\\\n")
    assert verdict(picked, good, True, 0) == [], verdict(picked, good, True, 0)
    dead = "Property(S): INSTALLDIR = C:\\Program Files\\BrowseProbe80Full\\\n"  # the #80 symptom
    got = verdict(picked, dead, False, 0)
    assert len(got) == 4 and "OK did nothing" in got[0], got
    assert verdict(picked, good, True, 1603) == ["msiexec returned 1603"]
    print("selftest: PASS")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs and uninstalls per-machine MSIs)")
        return 2

    manifest = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    results = {}
    for name, m in manifest.items():
        picked = folders(name, m["browse_from_customize"])
        for f in picked.values():
            f.mkdir(parents=True, exist_ok=True)
        print(f"\n=== {name} ({m['template']} template)")
        print("  1. Welcome: Next.")
        print(f"  2. Install-folder dialog: click Change..., type or navigate to {picked['change']}, press OK.")
        print("     The dialog must close and the path field show that folder. Next.")
        if "browse" in picked:
            print(f"  3. Feature tree: select Main, click Browse..., go to {picked['browse']}, press OK.")
            print("     The dialog must close and the location below the tree change. Next.")
        print("  Then Install and Finish. If OK does nothing, press Cancel in the dialog and finish anyway.")
        input("  Press Enter to open the installer... ")
        log_path = HERE / f"{name}-install.log"
        rc = subprocess.run(["msiexec", "/i", str(HERE / m["msi"]), "/l*v", str(log_path)]).returncode
        raw = log_path.read_bytes() if log_path.exists() else b""
        # msiexec writes the log in the ANSI code page unless a policy asks for Unicode.
        log = raw.decode("utf-16", errors="replace") if raw[:2] == b"\xff\xfe" else raw.decode("mbcs", errors="replace")
        last = list(picked.values())[-1]
        failures = verdict(picked, log, (last / "app.txt").exists(), rc)
        observed = input("  Did each OK close the dialog and show the new folder? [y/n] ").strip().lower()
        if observed != "y":
            failures.append("observed: OK did not behave (your answer)")
        subprocess.run(["msiexec", "/x", str(HERE / m["msi"]), "/qn", "/l*v", str(HERE / f"{name}-uninstall.log")])
        results[name] = failures
        print(f"  {name}: {'PASS' if not failures else 'FAIL'}")
        for f in failures:
            print(f"    - {f}")

    print("\n=== summary")
    for name, failures in results.items():
        print(f"{name}: {'PASS' if not failures else 'FAIL: ' + '; '.join(failures)}")
    return 0 if all(not f for f in results.values()) else 1


if __name__ == "__main__":
    sys.exit(main())
