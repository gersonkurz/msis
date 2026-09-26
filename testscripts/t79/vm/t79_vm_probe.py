"""#79, VM side - two components owning one file. Run ELEVATED on a snapshotted VM.

Every scenario starts from nothing and ends with an uninstall. After each step it reads the
shared file. PASS/FAIL is the presence rule - the file exists exactly while a feature owning it
is installed, with the right copy where only one owner is installed. Which copy wins while both
are installed is OBSERVED and printed, since that is what the issue asks about.

  same      install -> delete the file, repair -> uninstall
  features  A: default (Standard) -> add Variant -> remove Variant -> uninstall
            B: both -> remove Standard -> uninstall
            C: Variant only -> uninstall

    python t79_vm_probe.py --selftest   # checks the verdict; installs nothing
    python t79_vm_probe.py              # ELEVATED, unattended
"""

from __future__ import annotations

import argparse
import ctypes
import json
import os
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
OK = (0, 3010)
ANY = "either copy"

# package -> (shared file below INSTALLDIR, {copy label: source key in manifest files})
SHARED = {
    "same": ("CONFIG\\CURRENCY.TXT", {"core": "core\\CONFIG\\CURRENCY.TXT", "ng": "ng\\CONFIG\\CURRENCY.TXT"}),
    "features": ("config.json", {"standard": "a\\config.json", "variant": "b\\config.json"}),
}

# (package, scenario, [(step label, msiexec args, expected: None = absent, ANY, or a copy label,
#   delete the shared file first)])
SCENARIOS = [
    ("same", "one feature, two sources", [
        ("install", ["/i"], ANY, False),
        ("delete it, repair", ["/fa"], ANY, True),
        ("uninstall", ["/x"], None, False),
    ]),
    ("features", "A: Standard, then Variant added and removed", [
        ("install default (Standard)", ["/i"], "standard", False),
        ("add Variant", ["/i", "ADDLOCAL={Variant}"], ANY, False),
        ("remove Variant", ["/i", "REMOVE={Variant}"], "standard", False),
        ("uninstall", ["/x"], None, False),
    ]),
    ("features", "B: both, then Standard removed", [
        ("install both", ["/i", "ADDLOCAL={Standard},{Variant}"], ANY, False),
        ("remove Standard", ["/i", "REMOVE={Standard}"], "variant", False),
        ("uninstall", ["/x"], None, False),
    ]),
    ("features", "C: Variant only", [
        ("install Variant only", ["/i", "ADDLOCAL={Variant}"], "variant", False),
        ("uninstall", ["/x"], None, False),
    ]),
]


def verdict(label: str, rc: int, expected: str | None, observed: str | None) -> list[str]:
    """Failures for one step. observed: the copy label on disk, None when absent, "?" when the
    content matches neither copy. Pure, so --selftest can exercise it."""
    failures = []
    if rc not in OK:
        failures.append(f"{label}: msiexec returned {rc}")
    if expected is None and observed is not None:
        failures.append(f"{label}: the file is still there ({observed})")
    elif expected is not None and observed is None:
        failures.append(f"{label}: the file is GONE although a feature owning it is installed")
    elif observed == "?":
        failures.append(f"{label}: the file matches neither copy")
    elif expected not in (None, ANY) and observed != expected:
        failures.append(f"{label}: the {observed} copy is on disk, want the {expected} copy")
    return failures


def selftest() -> int:
    assert verdict("s", 0, ANY, "ng") == []
    assert verdict("s", 0, "standard", "standard") == []
    assert "GONE" in verdict("s", 0, "standard", None)[0]  # the #77 failure, for files
    assert "variant copy is on disk" in verdict("s", 0, "standard", "variant")[0]
    assert "still there" in verdict("s", 0, None, "core")[0]
    assert verdict("s", 1603, None, None) == ["s: msiexec returned 1603"]
    print("selftest: PASS")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--selftest", action="store_true")
    if parser.parse_args().selftest:
        return selftest()
    if not ctypes.windll.shell32.IsUserAnAdmin():
        print("run this elevated (it installs and uninstalls per-machine MSIs)")
        return 2

    manifest = json.loads((HERE / "manifest.json").read_text(encoding="utf-8"))
    failures: list[str] = []
    observations: list[str] = []
    for n, (pkg, scenario, steps) in enumerate(SCENARIOS, 1):
        m = manifest[pkg]
        rel, copies = SHARED[pkg]
        target = Path(os.environ["ProgramW6432"]) / m["installdir"] / rel
        contents = {label: m["files"][key] for label, key in copies.items()}
        print(f"\n=== {pkg}: {scenario}")
        for i, (label, args, expected, delete_first) in enumerate(steps, 1):
            if delete_first:
                target.unlink(missing_ok=True)
            cmd = ["msiexec", args[0], str(HERE / m["msi"])] + [a.format(**m["features"]) for a in args[1:]]
            rc = subprocess.run(cmd + ["/qn", "/l*v", str(HERE / f"{n}-{i}-{pkg}.log")]).returncode
            observed = None
            if target.exists():
                text = target.read_text(encoding="utf-8")
                observed = next((c for c, t in contents.items() if t == text), "?")
            f = verdict(f"{pkg} {scenario[:1]}{i} {label}", rc, expected, observed)
            print(f"  {label}: {'PASS' if not f else 'FAIL'} - on disk: {observed or 'nothing'}")
            for x in f:
                print(f"    - {x}")
            failures.extend(f)
            if expected == ANY and observed:
                observations.append(f"{pkg} / {scenario} / {label}: the {observed} copy")
        if Path(os.environ["ProgramW6432"], m["installdir"]).exists():
            failures.append(f"{pkg} {scenario}: the install folder is still there after uninstall")
            print("  FAIL - the install folder is still there after uninstall")

    print("\n=== observed: which copy is on disk while both owners are installed")
    for o in observations:
        print(f"  {o}")
    print(f"\n=== summary: {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    for f in failures:
        print(f"  - {f}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
