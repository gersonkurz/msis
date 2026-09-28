"""#85 and #87, VM side - ids across major upgrades. Run ELEVATED; take a VM snapshot first.

Leg 1, #85 (D26): the first release with path-derived ids, on permissioned folders with runtime
data the MSI never installed.
  1 install ids 1.0.0 (sequence-number ids), then seed runtime data; record the folder ACLs
  2 upgrade to 1.0.1 (D26)       -> 1.0.1's files, runtime data intact, ACLs as after step 1
  3 upgrade to 1.0.2 (D26, adds a __pycache__ folder, an empty folder, a file, a create-folder)
  4 remove the Extra feature     -> its file gone, the folder it shares with Main and its ACL kept
  5 delete a file and an empty create-folder, repair -> both back, ACLs as after step 1
  6 uninstall                    -> only the runtime data is left, no entry in Programs and Features

Leg 2, #87: does inserting a feature migrate the wrong feature states?
  control  install f1 with every feature, upgrade to f2append (no id moves) -> Main + Debug
  insert   install f1 with every feature, upgrade to f2insert (Debug's id is NewTool's now)
           -> the customer's choice is Main + Debug; what is installed is the finding

Leg 1 and leg 2's control decide PASS/FAIL. Leg 2's insert is an observation: the answer to #87.
The probe refuses to start if anything of its own is already on the machine, and whatever
happens it ends by uninstalling its products and removing exactly the runtime files it wrote,
then the folders under MsisProbe85 that are left empty. It never removes a folder with content.

    python t85_vm_probe.py --selftest   # checks the comparisons; installs nothing
    python t85_vm_probe.py              # ELEVATED, unattended
    python t85_vm_probe.py --cleanup    # ELEVATED: only the final cleanup, after an interrupted run
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
USERS_SID = "S-1-5-32-545"  # BUILTIN\Users, whatever the Windows language calls it


# ---- pure comparisons (exercised by --selftest) ----

def diff_files(step: str, want: dict[str, str], seen: dict[str, str]) -> list[str]:
    out = []
    for key, content in sorted(want.items()):
        if key not in seen:
            out.append(f"{step}: {key} is missing")
        elif seen[key] != content:
            out.append(f"{step}: {key} has {seen[key]!r}, want {content!r}")
    out += [f"{step}: {key} should not be there" for key in sorted(set(seen) - set(want))]
    return out


def diff_acl(step: str, want: dict[str, list[str] | None], seen: dict[str, list[str] | None]) -> list[str]:
    """The Users entries of each folder's DACL, against those recorded after the first install."""
    out = []
    for key, aces in sorted(want.items()):
        got = seen.get(key)
        if got is None:
            out.append(f"{step}: {key} is missing, so its ACL cannot be checked")
        elif sorted(got) != sorted(aces or []):
            out.append(f"{step}: {key} grants Users {sorted(got)}, after the first install it was {sorted(aces or [])}")
    return out


def diff_arp(step: str, want: list[str], seen: list[str]) -> list[str]:
    return [] if sorted(want) == sorted(seen) else [f"{step}: Programs and Features lists {sorted(seen)}, want {sorted(want)}"]


def selftest() -> int:
    assert diff_files("s", {"a": "1"}, {"a": "1"}) == []
    got = diff_files("s", {"a": "1", "b": "2"}, {"a": "x", "c": "3"})
    assert len(got) == 3 and "has 'x'" in got[0] and "b is missing" in got[1] and "c should not" in got[2], got
    base = {"d": ["FullControl|Allow|False|ContainerInherit, ObjectInherit"]}
    assert diff_acl("s", base, {"d": list(reversed(base["d"]))}) == []
    assert "grants Users" in diff_acl("s", base, {"d": []})[0]
    assert "missing" in diff_acl("s", base, {"d": None})[0]
    assert diff_arp("s", ["1.0.1"], ["1.0.1"]) == [] and diff_arp("s", [], ["1.0.0"])
    assert resolve("PF:x\\y") == Path(os.environ["ProgramW6432"]) / "x" / "y"

    # Every msiexec is quiet and never reboots: /qn alone lets Windows Installer restart the
    # machine unasked, which would end the run before its cleanup, on the owner's main VM.
    cmd = msiexec_args("/x", "{C}", log="l.log")
    assert cmd[0] == "msiexec" and "/qn" in cmd and "/norestart" in cmd, cmd

    global BASES
    with tempfile.TemporaryDirectory() as tmp:
        BASES = {"PF": Path(tmp) / "pf", "PD": Path(tmp) / "pd"}
        try:
            selftest_cleanup()
            selftest_acls()
        finally:
            BASES = None
    print("selftest: PASS")
    return 0


def selftest_cleanup() -> None:
    """The real cleanup on a temporary tree, with the installer faked: it removes exactly the
    seeded runtime files and the folders left empty under the probe's top folders, and nothing
    else - not a file of the user's inside them, not the folders holding it, nothing outside.
    Reached through a failing run, as the probe reaches it."""
    m = {"top": "Top", "ids": {"product": "P1", "runtime": {
        "PF:Top\\App\\conf\\user.ini": "r", "PD:Top\\App\\logs\\runtime.log": "r"}},
         "features": {"product": "P2"}}
    for key in m["ids"]["runtime"]:
        write(key, "seeded")
    write("PF:Top\\App\\keep\\users-own.txt", "not the probe's")
    resolve("PF:Top\\App\\empty\\deeper").mkdir(parents=True)
    write("PF:Other\\other.txt", "outside the probe's folders")
    resolve("PF:EmptyOutside").mkdir(parents=True)
    calls: list[tuple] = []

    def fake_arp(product: str) -> dict[str, str]:
        return {"{CODE-1}": "1.0.2"} if product == "P1" else {}

    def fake_msiexec(*args: str, log: str) -> int:
        calls.append(args)
        return 0

    def failing_steps() -> None:
        raise RuntimeError("a step blew up")

    try:
        run_with_cleanup(m, failing_steps, lambda mm: cleanup(mm, arp=fake_arp, msiexec=fake_msiexec))
        raise AssertionError("the failure was swallowed")
    except RuntimeError as e:
        assert str(e) == "a step blew up"
    assert calls == [("/x", "{CODE-1}")], calls
    for key in m["ids"]["runtime"]:
        assert not resolve(key).exists(), key
    assert resolve("PF:Top\\App\\keep\\users-own.txt").read_text(encoding="utf-8") == "not the probe's"
    assert not resolve("PF:Top\\App\\empty").exists() and not resolve("PF:Top\\App\\conf").exists()
    assert not resolve("PD:Top").exists(), "a top folder left empty is removed"
    assert resolve("PF:Other\\other.txt").exists() and resolve("PF:EmptyOutside").is_dir()


def selftest_acls() -> None:
    """The ACL reader on this machine: an existing folder gives a list, a missing one None. A
    PowerShell failure stops the run rather than reading as 'no entries'."""
    resolve("PF:AclProbe").mkdir(parents=True)
    got = acls(["PF:AclProbe", "PF:Missing"])
    assert isinstance(got["PF:AclProbe"], list) and got["PF:Missing"] is None, got


def write(key: str, content: str) -> None:
    resolve(key).parent.mkdir(parents=True, exist_ok=True)
    resolve(key).write_text(content, encoding="utf-8")


# ---- the machine ----

BASES: dict[str, Path] | None = None  # the selftest points these at a temporary tree


def resolve(key: str) -> Path:
    """PF:rel -> C:\\Program Files\\rel, PD:rel -> C:\\ProgramData\\rel."""
    base, rel = key.split(":", 1)
    if BASES is not None:
        return BASES[base] / rel
    return Path(os.environ["ProgramW6432" if base == "PF" else "ProgramData"]) / rel


def files_under(*keys: str) -> dict[str, str]:
    out = {}
    for key in keys:
        root = resolve(key)
        if root.exists():
            for p in root.rglob("*"):
                if p.is_file():
                    out[key + "\\" + str(p.relative_to(root))] = p.read_text(encoding="utf-8", errors="replace")
    return out


def acls(keys: list[str]) -> dict[str, list[str] | None]:
    """Each folder's DACL entries for BUILTIN\\Users (by SID), or None where it does not exist."""
    script = r"""
$ErrorActionPreference = 'Stop'
$r = @{}
foreach ($p in ($env:T85_PATHS | ConvertFrom-Json)) {
  if (Test-Path -LiteralPath $p) {
    $r[$p] = @((Get-Acl -LiteralPath $p).Access | Where-Object {
      try { $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value -eq $env:T85_SID } catch { $false }
    } | ForEach-Object { '{0}|{1}|{2}|{3}' -f $_.FileSystemRights, $_.AccessControlType, $_.IsInherited, $_.InheritanceFlags })
  } else { $r[$p] = $null }
}
$r | ConvertTo-Json -Compress -Depth 3
"""
    paths = {str(resolve(k)): k for k in keys}
    env = dict(os.environ, T85_PATHS=json.dumps(list(paths)), T85_SID=USERS_SID)
    proc = subprocess.run(["powershell", "-NoProfile", "-NonInteractive", "-Command", script],
                          capture_output=True, text=True, env=env)
    if proc.returncode != 0 or not proc.stdout.strip():
        raise SystemExit(f"reading ACLs failed (rc={proc.returncode}):\n{proc.stderr}")
    raw = json.loads(proc.stdout)
    return {paths[p]: (None if v is None else ([v] if isinstance(v, str) else list(v))) for p, v in raw.items()}


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
    """Runs the probe's steps, and the cleanup whatever they do."""
    try:
        body()
    finally:
        clean(m)


def cleanup(m: dict, arp=arp, msiexec=msiexec) -> None:
    """Uninstalls the probe's products, removes exactly the runtime files it seeded, then the
    folders under the probe's own top folders that are left empty. Reports what remains."""
    for product in (m["ids"]["product"], m["features"]["product"]):
        for code in arp(product):
            print(f"cleanup: uninstalling {product} {code}: rc={msiexec('/x', code, log=f'cleanup-{code}.log')}")
    for key in m["ids"]["runtime"]:
        resolve(key).unlink(missing_ok=True)
    for base in ("PF", "PD"):
        top = resolve(f"{base}:{m['top']}")
        if not top.exists():
            continue
        for folder, _, _ in sorted(os.walk(top, topdown=False), key=lambda w: -len(w[0])):
            try:
                os.rmdir(folder)  # only succeeds when empty
            except OSError:
                pass
        if top.exists():
            print(f"cleanup: {top} still holds {[str(p) for p in top.rglob('*')]}; left alone")


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

    tops = [resolve(f"{b}:{m['top']}") for b in ("PF", "PD")]
    installed = {p: arp(p) for p in (m["ids"]["product"], m["features"]["product"])}
    if any(t.exists() for t in tops) or any(installed.values()):
        print(f"refusing to start: {[str(t) for t in tops if t.exists()]} {installed} are already here. "
              "Run --cleanup, or restore the snapshot.")
        return 2

    failures: list[str] = []
    observations: list[str] = []

    def report(step: str, found: list[str]) -> None:
        print(f"{step}: {'PASS' if not found else 'FAIL'}")
        for x in found:
            print(f"  - {x}")
        failures.extend(found)

    def legs() -> None:
        leg1(m, report)
        leg2(m, report, observations)

    run_with_cleanup(m, legs, cleanup)

    print("\n=== leg 2 insert (#87):")
    for o in observations:
        print(f"  {o}")
    print(f"=== summary (leg 1 and leg 2 control): {'PASS' if not failures else f'FAIL ({len(failures)})'}")
    return 0 if not failures else 1


def leg1(m: dict, report) -> None:
    ids = m["ids"]
    versions = ids["versions"]
    roots = [f"PF:{m['top']}\\IdProbe", f"PD:{m['top']}\\IdProbe"]
    runtime = ids["runtime"]
    product = ids["product"]

    def state(step: str, rc: int, version: str | None, *, with_runtime: bool, drop: tuple[str, ...] = (),
              baseline: dict | None = None) -> list[str]:
        found = [] if rc in OK else [f"{step}: msiexec returned {rc}"]
        want = dict(versions[version]["files"]) if version else {}
        for d in drop:
            want = {k: v for k, v in want.items() if not k.endswith(d)}
        if with_runtime:
            want.update(runtime)
        found += diff_files(step, want, files_under(*roots))
        if version:
            found += [f"{step}: folder {d} does not exist" for d in versions[version]["dirs"] if not resolve(d).is_dir()]
        found += diff_arp(step, [version] if version else [], list(arp(product).values()))
        if baseline is not None:
            found += diff_acl(step, baseline, acls(list(baseline)))
        return found

    msi = {v: str(HERE / f"ids-{v}.msi") for v in versions}
    report("L1.1 install 1.0.0", state("L1.1", msiexec("/i", msi["1.0.0"], log="L1-1-install.log"), "1.0.0", with_runtime=False))
    baseline = acls(ids["acl_dirs"])
    print(f"  Users on the probe's folders after the first install: {json.dumps(baseline, indent=1)}")
    if any(not v for v in baseline.values()):
        report("L1.1 permissions", [f"L1.1: {k} grants Users nothing, so the ACL checks prove nothing" for k, v in baseline.items() if not v])
    for key, content in runtime.items():
        resolve(key).parent.mkdir(parents=True, exist_ok=True)
        resolve(key).write_text(content, encoding="utf-8")

    report("L1.2 upgrade to 1.0.1", state("L1.2", msiexec("/i", msi["1.0.1"], log="L1-2-upgrade.log"), "1.0.1",
                                          with_runtime=True, baseline=baseline))
    report("L1.3 upgrade to 1.0.2", state("L1.3", msiexec("/i", msi["1.0.2"], log="L1-3-upgrade.log"), "1.0.2",
                                          with_runtime=True, baseline=baseline))
    rc = msiexec("/i", msi["1.0.2"], f"REMOVE={ids['extra_feature']}", log="L1-4-remove-extra.log")
    report("L1.4 remove Extra", state("L1.4", rc, "1.0.2", with_runtime=True, drop=("extra.txt",), baseline=baseline))
    resolve(f"PF:{m['top']}\\IdProbe\\conf\\settings.ini").unlink(missing_ok=True)
    cache = resolve(f"PD:{m['top']}\\IdProbe\\cache")
    if cache.is_dir() and not any(cache.iterdir()):
        cache.rmdir()
    report("L1.5 repair", state("L1.5", msiexec("/fa", msi["1.0.2"], log="L1-5-repair.log"), "1.0.2",
                                with_runtime=True, drop=("extra.txt",), baseline=baseline))
    found = state("L1.6", msiexec("/x", msi["1.0.2"], log="L1-6-uninstall.log"), None, with_runtime=True)
    # The package's own folders go; the ones holding runtime data stay, with only that in them.
    for d in versions["1.0.2"]["dirs"]:
        if resolve(d).exists() and not any(k.startswith(d + "\\") for k in runtime):
            found.append(f"L1.6: the package's folder {d} is still there")
    report("L1.6 uninstall", found)


def leg2(m: dict, report, observations: list[str]) -> None:
    f = m["features"]
    folder = f["dir"]
    files = f["files"]  # feature title -> its file

    def installed() -> list[str]:
        have = files_under(folder)
        return sorted(t for t, name in files.items() if f"{folder}\\{name}" in have)

    def step(label: str, rc: int, want: list[str], version: list[str]) -> list[str]:
        found = [] if rc in OK else [f"{label}: msiexec returned {rc}"]
        if installed() != sorted(want):
            found.append(f"{label}: installed features {installed()}, want {sorted(want)}")
        return found + diff_arp(label, version, list(arp(f["product"]).values()))

    msi = {n: str(HERE / f"{n}.msi") for n in f["ids"]}
    report("L2 control: install f1, every feature",
           step("L2c.1", msiexec("/i", msi["f1"], "ADDLOCAL=ALL", log="L2c-1.log"), ["Main", "Debug"], ["1.0.0"]))
    report("L2 control: upgrade to f2append",
           step("L2c.2", msiexec("/i", msi["f2append"], log="L2c-2.log"), ["Main", "Debug"], ["1.0.1"]))
    report("L2 control: uninstall", step("L2c.3", msiexec("/x", msi["f2append"], log="L2c-3.log"), [], []))

    report("L2 insert: install f1, every feature",
           step("L2i.1", msiexec("/i", msi["f1"], "ADDLOCAL=ALL", log="L2i-1.log"), ["Main", "Debug"], ["1.0.0"]))
    rc = msiexec("/i", msi["f2insert"], log="L2i-2.log")
    seen = installed()
    if rc not in OK:
        report("L2 insert: upgrade to f2insert", [f"L2i.2: msiexec returned {rc}"])
    elif seen == ["Debug", "Main"]:
        observations.append(f"the customer's choice survived: {seen}. #87 does not reproduce.")
    else:
        observations.append(f"#87 REPRODUCES: after the upgrade {seen} are installed; the customer had chosen "
                            f"['Debug', 'Main'] (f1 ids {f['ids']['f1']}, f2insert ids {f['ids']['f2insert']})")
    report("L2 insert: uninstall", step("L2i.3", msiexec("/x", msi["f2insert"], log="L2i-3.log"), [], []))


if __name__ == "__main__":
    sys.exit(main())
