"""#90, build side - a bundle's <search> elements gating <exe> packages (D30).

One bundle (the regular template, run with /quiet: the silent template's bundles do not start,
#95), built from the working tree, chains a tiny MSI and six stand-in <exe> packages. Each
<exe> is a copy of `marker.exe`, which writes `C:\\ProgramData\\MsisProbe90\\markers\\<id>.txt` when
Burn runs it (the path reaches it through `args`, formatted by Burn). Each is gated on a search:

  E32   detect="P32"          P32: HKLM, bitness 32, SOFTWARE\\MsisProbe90, value "flag" exists
  E64   detect="P64"          P64: the same, bitness 64
  EKEY  detect="PKEY"         PKEY: HKLM, bitness 64, SOFTWARE\\MsisProbe90 (the key) exists
  EVAL  detect="PVER > v0.0.0.0"   PVER: HKLM, bitness 32, value "ver", result=value
  ECU   detect="PCU"          PCU: HKCU, bitness 64, Software\\MsisProbe90 exists
  EWV   detect="WebView2Machine > v0.0.0.0", per-machine="yes"   the docs' WebView2 recipe

The VM side sets one registry state per round, runs the bundle, and checks which markers appeared
and what Burn's log says it detected.

This half needs no elevation and installs nothing:

    uv run t90_search_probe.py
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from pathlib import Path

from loguru import logger

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
TOP = "MsisProbe90"
PRODUCT, CODE, INNER_CODE = "MSIS Search Probe 90", "{7A3E9C52-1B4D-4E80-A6F2-8D5C0B9E7A90}", "{2D8F6A13-9C4E-4B71-8E05-6A3B1F9D2C90}"
WV2 = r"SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
EXES = {  # id -> (detect, per-machine)
    "E32": ("P32", False),
    "E64": ("P64", False),
    "EKEY": ("PKEY", False),
    "EVAL": ("PVER &gt; v0.0.0.0", False),
    "ECU": ("PCU", False),
    "EWV": ("WebView2Machine &gt; v0.0.0.0", True),
}


def run(cmd: list[str], cwd: Path, env: dict | None = None) -> subprocess.CompletedProcess:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, errors="replace", env=env)
    if proc.returncode != 0:
        logger.error("$ {}\n{}", " ".join(map(str, cmd)), proc.stdout + proc.stderr)
    return proc


def main() -> int:
    logger.remove()
    logger.add(sys.stderr, format="{level: <7} {message}")
    work = HERE / "work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir()
    exe = work / "msis.exe"
    if run(["go", "build", "-o", str(exe), "./cmd/msis"], REPO).returncode != 0:
        raise SystemExit("cannot build msis")
    import os
    env = dict(os.environ, GOOS="windows", GOARCH="amd64")

    inner = work / "inner"
    inner.mkdir()
    (inner / "app.txt").write_text("the probe's application\n", encoding="utf-8")
    (inner / "inner.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="MSIS Search Probe 90 App"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{INNER_CODE}"/>
  <set name="INSTALLDIR" value="{TOP}\\App"/>
  <set name="BUILD_TARGET" value="inner.msi"/>
  <feature name="Main"><files source="app.txt" target="[INSTALLDIR]"/></feature>
</setup>
""", encoding="utf-8", newline="\r\n")
    if run([str(exe), "/BUILD", f"/TEMPLATEFOLDER:{REPO / 'templates'}", "inner.msis"], inner).returncode != 0:
        raise SystemExit("the inner MSI did not build")

    bundle = work / "bundle"
    bundle.mkdir()
    shutil.copy2(inner / "inner.msi", bundle / "inner.msi")
    packages = ""
    for eid, (detect, machine) in EXES.items():
        # One build per package, each stamped with its id: identical files would share a CacheId.
        if run(["go", "build", "-ldflags", f"-X main.id={eid}", "-o", str(bundle / f"{eid.lower()}.exe"),
                "./testscripts/t90/marker"], REPO, env).returncode != 0:
            raise SystemExit("cannot build the marker")
        pm = ' per-machine="yes"' if machine else ""
        packages += (f'    <exe id="{eid}" source="{eid.lower()}.exe" detect="{detect}"{pm}\n'
                     f'         args="[CommonAppDataFolder]{TOP}\\markers\\{eid}.txt"/>\n')
    (bundle / "bundle.msis").write_text(f"""<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="{PRODUCT}"/>
  <set name="LICENSE_URL" value="https://example.com/license"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{CODE}"/>
  <set name="BUILD_TARGET" value="probe90.exe"/>
  <bundle>
    <search variable="P32" root="HKLM" bitness="32" key="SOFTWARE\\{TOP}" value="flag"/>
    <search variable="P64" root="HKLM" bitness="64" key="SOFTWARE\\{TOP}" value="flag"/>
    <search variable="PKEY" root="HKLM" bitness="64" key="SOFTWARE\\{TOP}"/>
    <search variable="PVER" root="HKLM" bitness="32" key="SOFTWARE\\{TOP}" value="ver" result="value"/>
    <search variable="PCU" root="HKCU" bitness="64" key="Software\\{TOP}"/>
    <search variable="WebView2Machine" root="HKLM" bitness="32" key="{WV2}" value="pv" result="value"/>
{packages}    <msi source="inner.msi"/>
  </bundle>
</setup>
""", encoding="utf-8", newline="\r\n")
    proc = run([str(exe), "/BUILD", "/RETAINWXS", f"/TEMPLATEFOLDER:{REPO / 'templates'}", "bundle.msis"], bundle)
    (HERE / "build-bundle.log").write_text(proc.stdout + proc.stderr, encoding="utf-8")
    if proc.returncode != 0:
        raise SystemExit("the bundle did not build")
    if "Warning" in proc.stdout:
        logger.error("the bundle build warned:\n{}", proc.stdout)
        return 1
    wxs = (bundle / "probe90-bundle.wxs").read_text(encoding="utf-8")
    for banned in ("msi-simplica", "RemoveFolderEx", "REMOVE_FOLDERS", "RemoveRegistryKey"):
        if banned in wxs:
            raise SystemExit(f"the bundle WXS contains {banned}")
    searches = wxs.count("<util:RegistrySearch Id='MSIS_Search_")
    if searches != 6 or wxs.count("PerMachine='yes'") != 1:
        logger.error("the WXS has {} msis searches and {} per-machine packages, want 6 and 1", searches, wxs.count("PerMachine='yes'"))
        return 1

    payload = HERE / "vm-payload"
    shutil.rmtree(payload, ignore_errors=True)
    payload.mkdir(exist_ok=True)  # a shell may still be in it
    shutil.copy2(bundle / "probe90.exe", payload / "probe90.exe")
    manifest = {"top": TOP, "product": PRODUCT, "app_product": "MSIS Search Probe 90 App", "exes": list(EXES),
                "probe_key": f"SOFTWARE\\{TOP}", "probe_key_hkcu": f"Software\\{TOP}",
                "markers": f"PD:{TOP}\\markers", "app_dir": f"PF:{TOP}\\App", "wv2_key": WV2,
                "guard_keys": ["Software\\msis\\Packages\\" + INNER_CODE.strip("{}").upper()]}
    (payload / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    for name in ("t90_vm_probe.py", "README.md"):
        shutil.copy2(HERE / "vm" / name, payload / name)
    logger.info("staged {}", payload)
    return 0


if __name__ == "__main__":
    sys.exit(main())
