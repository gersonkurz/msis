"""#76, VM side - site data across upgrades of a package with <remove-on-uninstall folder=
"[INSTALLDIR]"/>. Run ELEVATED on a snapshotted VM.

  1. install v1 (1.0.0, msis 3.0.5), seed site data in INSTALLDIR
  2. upgrade to v2 (1.0.1, msis 3.0.6)   OBSERVED: does the old package's removal delete it?
     (seed the data again if it went)
  3. upgrade to v3 (1.0.2, msis 3.0.6)   the data MUST survive (D21)
  4. uninstall v3                         the folder MUST go: that is what the element is for

After every step: the application file's version, the site data, one ARP entry.

    python t76_vm_probe.py --selftest   # checks the verdict; installs nothing
    python t76_vm_probe.py              # ELEVATED, unattended
"""

from __future__ import annotations

import argparse
import ctypes
import json
import os
import subprocess
import sys
import winreg
from pathlib import Path

HERE = Path(__file__).resolve().parent
OK = (0, 3010)
UNINSTALL = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall"
SITE = "site data - customer.db\n"


def verdict(step: str, rc: int, want_app: str | None, app: str | None, want_data: bool | None,
            data: bool, want_arp: list[str], arp: list[str], want_folder: bool, folder: bool) -> list[str]:
    """Failures for one step. want_data None: observed, not judged. Pure, for --selftest."""
    f = []
    if rc not in OK:
        f.append(f"{step}: msiexec returned {rc}")
    if app != want_app:
        f.append(f"{step}: app.txt is {app!r}, want {want_app!r}")
    if want_data is not None and data != want_data:
        f.append(f"{step}: the site data is {'there' if data else 'GONE'}, want it {'kept' if want_data else 'removed'}")
    if sorted(arp) != sorted(want_arp):
        f.append(f"{step}: Programs and Features lists {sorted(arp)}, want {sorted(want_arp)}")
    if folder != want_folder:
        f.append(f"{step}: the install folder is {'there' if folder else 'gone'}, want it {'there' if want_folder else 'gone'}")
    return f


def selftest() -> int:
    assert verdict("s", 0, "app 1.0.2\n", "app 1.0.2\n", True, True, ["1.0.2"], ["1.0.2"], True, True) == []
    assert verdict("s", 0, "a", "a", None, False, ["1"], ["1"], True, True) == []  # observed step
    got = verdict("s", 0, "a", "a", True, False, ["1"], ["1"], True, True)
    assert got == ["s: the site data is GONE, want it kept"], got
    assert "folder is there" in verdict("s", 0, None, None, None, False, [], [], False, True)[0]
    assert verdict("s", 1603, None, None, None, False, [], [], False, False) == ["s: msiexec returned 1603"]
    print("selftest: PASS")
    return 0


def arp_versions(product: str) -> list[str]:
    out = []
    for view in (winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
        try:
            key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, UNINSTALL, 0, winreg.KEY_READ | view)
        except OSError:
            continue
        with key:
            for i in range(winreg.QueryInfoKey(key)[0]):
                try:
                    with winreg.OpenKey(key, winreg.EnumKey(key, i)) as sub:
                        if winreg.QueryValueEx(sub, "DisplayName")[0] == product:
                            out.append(winreg.QueryValueEx(sub, "DisplayVersion")[0])
                except OSError:
                    pass
    return out


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    if parser.parse_args().selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs and uninstalls per-machine MSIs)")
        return 2
    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    root = Path(os.environ.get("ProgramFiles(x86)", os.environ["ProgramFiles"])) / m["installdir"]
    data_file = root / "data" / "customer.db"
    v = m["versions"]

    def msiexec(args: list[str], log: str) -> int:
        return subprocess.run(["msiexec", *args, "/qn", "/l*v", str(HERE / log)]).returncode

    def seed() -> None:
        data_file.parent.mkdir(parents=True, exist_ok=True)
        data_file.write_text(SITE, encoding="utf-8")

    def state() -> tuple[str | None, bool, list[str], bool]:
        app = root / "app.txt"
        return (app.read_text(encoding="utf-8") if app.exists() else None,
                data_file.exists() and data_file.read_text(encoding="utf-8") == SITE,
                arp_versions(m["product"]), root.exists())

    failures: list[str] = []

    def step(label: str, rc: int, want_app: str | None, want_data: bool | None, want_arp: list[str], want_folder: bool) -> bool:
        app, data, arp, folder = state()
        f = verdict(label, rc, want_app, app, want_data, data, want_arp, arp, want_folder, folder)
        note = "" if want_data is not None else f"  OBSERVED: the site data is {'KEPT' if data else 'GONE'}"
        print(f"{label}: {'PASS' if not f else 'FAIL'}{note}")
        for x in f:
            print(f"  - {x}")
        failures.extend(f)
        return data

    rc = msiexec(["/i", str(HERE / "v1.msi")], "1-install-v1.log")
    seed()
    step("1 install v1 (msis 3.0.5), seed site data", rc, f"app {v['v1']}\n", True, [v["v1"]], True)
    rc = msiexec(["/i", str(HERE / "v2.msi")], "2-upgrade-v2.log")
    kept = step("2 upgrade v1 -> v2 (the first upgrade to a 3.0.6 package)", rc, f"app {v['v2']}\n", None, [v["v2"]], True)
    if not kept:
        seed()
        print("  (site data seeded again for step 3)")
    rc = msiexec(["/i", str(HERE / "v3.msi")], "3-upgrade-v3.log")
    step("3 upgrade v2 -> v3 (both 3.0.6)", rc, f"app {v['v3']}\n", True, [v["v3"]], True)
    rc = msiexec(["/x", str(HERE / "v3.msi")], "4-uninstall-v3.log")
    step("4 uninstall v3 (the element's job: the folder goes)", rc, None, False, [], False)

    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
