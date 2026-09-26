"""#81, VM side - a major upgrade across the one-time component GUID change. Run ELEVATED on a
snapshotted VM.

  1. install v1 (1.0.0, built by msis before #81)       -> v1's files, one ARP entry 1.0.0
  2. install v2 (1.0.1, built by msis with D23's GUIDs)  -> v2's files, one ARP entry 1.0.1
  3. delete a v2 file, repair v2 (msiexec /fa)          -> the file is back
  4. uninstall v2                                       -> no files, no folder, no ARP entry

v1 and v2 share no component GUID. If the upgrade did not remove v1 completely first, a
component of each would own the same file, and steps 2-4 would show it: a file left at v1's
content, a second ARP entry, a file the repair does not restore, or files left behind.

    python t81_vm_probe.py --selftest   # checks the verdict; installs nothing
    python t81_vm_probe.py              # ELEVATED
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


def verdict(step: str, rc: int, want_files: dict[str, str], seen_files: dict[str, str],
            want_arp: list[str], seen_arp: list[str], folder_exists: bool) -> list[str]:
    """Failures for one step, empty when it passed. Pure, so --selftest can exercise it.

    want_files/seen_files: path below INSTALLDIR -> content; want_arp/seen_arp: DisplayVersions.
    """
    failures = []
    if rc not in OK:
        failures.append(f"{step}: msiexec returned {rc}")
    for rel, content in sorted(want_files.items()):
        if rel not in seen_files:
            failures.append(f"{step}: {rel} is missing")
        elif seen_files[rel] != content:
            failures.append(f"{step}: {rel} has {seen_files[rel]!r}, want {content!r}")
    for rel in sorted(set(seen_files) - set(want_files)):
        failures.append(f"{step}: {rel} should not be there")
    if sorted(seen_arp) != sorted(want_arp):
        failures.append(f"{step}: Programs and Features lists {sorted(seen_arp)}, want {sorted(want_arp)}")
    if not want_files and folder_exists:
        failures.append(f"{step}: the install folder is still there")
    return failures


def selftest() -> int:
    v2 = {"app.exe": "2", "conf\\added.txt": "n"}
    assert verdict("s", 0, v2, v2, ["1.0.1"], ["1.0.1"], True) == []
    got = verdict("s", 0, v2, {"app.exe": "1"}, ["1.0.1"], ["1.0.0", "1.0.1"], True)
    assert len(got) == 3 and "has '1'" in got[0] and "missing" in got[1] and "lists" in got[2], got
    assert verdict("s", 0, {}, {}, [], [], True) == ["s: the install folder is still there"]
    assert verdict("s", 1603, {}, {}, [], [], False) == ["s: msiexec returned 1603"]
    print("selftest: PASS")
    return 0


def files_under(root: Path) -> dict[str, str]:
    if not root.exists():
        return {}
    return {str(p.relative_to(root)): p.read_text(encoding="utf-8") for p in root.rglob("*") if p.is_file()}


def arp_versions(product: str) -> list[str]:
    versions = []
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
                            versions.append(winreg.QueryValueEx(sub, "DisplayVersion")[0])
                except OSError:
                    pass
    return versions


def msiexec(*args: str, log: str) -> int:
    return subprocess.run(["msiexec", *args, "/qn", "/l*v", str(HERE / log)]).returncode


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    if parser.parse_args().selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs and uninstalls per-machine MSIs)")
        return 2

    m = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    root = Path(os.environ["ProgramW6432"]) / m["installdir"]

    def of(version: str) -> dict[str, str]:
        return {rel: c[version] for rel, c in m["files"].items() if c[version] is not None}

    failures: list[str] = []

    def check(step: str, rc: int, want: dict[str, str], arp: list[str]) -> None:
        f = verdict(step, rc, want, files_under(root), arp, arp_versions(m["product"]), root.exists())
        print(f"{step}: {'PASS' if not f else 'FAIL'}")
        for x in f:
            print(f"  - {x}")
        failures.extend(f)

    check("1 install v1", msiexec("/i", str(HERE / "v1.msi"), log="1-install-v1.log"), of("1.0.0"), ["1.0.0"])
    check("2 upgrade to v2", msiexec("/i", str(HERE / "v2.msi"), log="2-upgrade-v2.log"), of("1.0.1"), ["1.0.1"])
    (root / "conf" / "static.txt").unlink(missing_ok=True)
    check("3 repair v2", msiexec("/fa", str(HERE / "v2.msi"), log="3-repair-v2.log"), of("1.0.1"), ["1.0.1"])
    check("4 uninstall v2", msiexec("/x", str(HERE / "v2.msi"), log="4-uninstall-v2.log"), {}, [])

    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
