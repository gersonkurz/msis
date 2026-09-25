# todo-testme.md — checks that need a real machine

Things that cannot be settled by unit tests or by `wix build`, because they only
happen when Windows Installer actually runs. Each entry says what to run, what
proof closes it, and which ticket it belongs to.

**These need an ELEVATED shell.** `msiexec` writes to `HKLM` and installs
per-machine; an unelevated run will fail or, worse, silently redirect.

---

## T1 — `preserve="yes"` runtime semantics: DONE 2026-09-18

**Ticket:** [#5](https://github.com/gersonkurz/msis/issues/5), fixed in `645dbf0`. The runtime
half was executed on 2026-09-18 via `testscripts/t1/`, both required passes (silent `/qn` and
full UI), and the results are posted on #5. **#5 stays closed.**

Five of the six rows matched. The sixth became [#26](https://github.com/gersonkurz/msis/issues/26).

### What was observed — identical in both passes

| Value | Seeded | After install | Type | |
|---|---|---|---|---|
| `Absent` | *(absent)* | `default-absent` | `REG_SZ` | as predicted |
| `AbsentDword` | *(absent)* | `42` | `REG_DWORD` | as predicted |
| `Existing` | `live-existing` | `live-existing` | `REG_SZ` | as predicted |
| `ExistingDword` | `99` | `99` | `REG_DWORD` | as predicted |
| `EmptyExisting` | `""` | `default-empty-existing` | `REG_SZ` | **mismatch → #26** |
| *(unnamed)* | *(absent)* | `default-unnamed` | `REG_SZ` | as predicted |

Key gone after uninstall, both runs. Types round-trip: the `#` prefix survives in both
directions, so DWORDs stay DWORDs rather than becoming `REG_SZ "42"`.

### The three unknowns, answered

1. **Empty existing value — NOT preserved.** The `.reg` default overwrites it. The search runs
   (`AppSearch: Property: PS_RV_00002, Signature: PS_RV_00002_Registry`) and reads the empty
   value, but AppSearch makes no assignment from an empty result: there is no `PROPERTY CHANGE`
   line for `PS_RV_00002`, while `00003` and `00004` each have one. The property therefore still
   held the `.reg` default when the write happened — and it was that default, not an empty
   string, that landed, which also rules out the property having been removed. Filed as #26.
2. **Elevated client → server handoff — works.** In the full-UI log AppSearch runs once, on
   the client, and the server explicitly declines to repeat it:

   ```
   MSI (c): Switching to server: PS_RV_00003="live-existing" PS_RV_00004="#99" ...
   MSI (s): PROPERTY CHANGE: Modifying PS_RV_00003 ... new value: 'live-existing'
   MSI (s): Skipping AppSearch action: already done on client side
   ```

   The server-side `PROPERTY CHANGE` lines are the **transfer** of those properties, not a second
   search: the two values AppSearch found on the client reach the side that performs the registry
   write, and the preserved values land correctly. The silent run is the same path without a UI
   sequence.

   **What this does not establish:** that `Secure='yes'` was required for it. The probe ran
   elevated (`AdminUser = 1`, `MsiTrueAdminUser = 1`), and the same `Switching to server` line
   also carries `INSTALLDIR`, `INSTALLFOLDER` and `TARGETDIR`, none of which are in
   `SecureCustomProperties` — so this run says nothing about what would happen without it.
   Microsoft's [restricted public properties](https://learn.microsoft.com/en-us/windows/win32/msi/restricted-public-properties)
   documents the restriction for non-administrator installs, which is the case not exercised
   here. Proving the attribute necessary would need a package built without it, installed by a
   non-administrator; `Secure='yes'` stays on for the documented reason regardless.
3. **Unnamed (default) value — works.** `PS_RV_00005` carries a search with no `Name`, and
   `default-unnamed` was written as `REG_SZ`.

### What this entry got wrong, kept as a warning

Two mistakes, both mine, both the kind worth not repeating:

- **The `.msis` here omitted `INSTALLDIR`.** The first real run died on it:
  `Error 25521. Failed to set security descriptor on object C:\Program Files\`, because with no
  value `INSTALLDIR` resolves to `C:\Program Files\` itself and msis emits its usual
  `util:PermissionEx` component for it — refused even elevated. The same trap invalidated the
  first #6 probe, and it was copied in here anyway. Any probe package needs a real `INSTALLDIR`.
- **The prediction for `EmptyExisting` was aspirational.** This entry expected the new form to
  preserve empty values while stating in the same paragraph that the old form wrote the default.
  Nothing ever preserved them: msis-2.x emits the same property-with-nested-search construct, so
  the behaviour is as old as the feature. A prediction table is a claim about the code and has
  to be derived from it, not from what the change ought to have done.

### Rerunning it

`testscripts/t1/` is a uv + loguru project that writes its own fixtures, builds msis from the
working tree, installs, compares and uninstalls:

```powershell
uv run t1_preserve_probe.py --build-only   # no elevation
uv run t1_preserve_probe.py                # ELEVATED, silent
uv run t1_preserve_probe.py --ui           # ELEVATED, full UI
```

It is kept as a regression guard: its table now encodes the **observed** behaviour, so a future
change to any of the six rows fails it. If #26 is fixed, flip the `EmptyExisting` expectation
back to `""` in the same change.


---

## T2 — preserved binary and type handling: DONE, kept for the record

**Tickets:** [#6](https://github.com/gersonkurz/msis/issues/6),
[#10](https://github.com/gersonkurz/msis/issues/10),
[#11](https://github.com/gersonkurz/msis/issues/11)

This entry originally said #6 still needed an install probe. It got one, and so did
#10 and #11. **Nothing is outstanding here** — summarised so nobody re-runs it:

- `#x01AB` installs as `REG_BINARY 01 AB`. A zero-byte binary needs a bare `#x`;
  omitting the `Value` attribute stores an empty `REG_SZ` instead.
- `REG_EXPAND_SZ` and `REG_QWORD` are no longer preserved at all. The `Type='raw'`
  search expands the former (a live `%TEMP%` came back as a literal machine path) and
  returns the latter's raw bytes as UTF-16 mojibake.
- A preserved `REG_SZ` starting with `#` needs `##`, or the install fails with
  Error 1406.

---

## T3 — x86 auto-bundle on 64-bit Windows: CLOSED 2026-09-18, partly executed

**Ticket:** [#8](https://github.com/gersonkurz/msis/issues/8), fixed in `3e9c171`.

Closed by the product owner on 2026-09-18 on the evidence below, **not** by completing the
original plan. The one check still missing needs a machine that in practice does not exist; see
"Why the last check was abandoned".

### What was executed

`testscripts/t3/` builds the package and stages a payload for a VM; a run on a 64-bit test VM
produced this.

| Proof item | Status |
|---|---|
| 1. The chain carries `DetectCondition='VcppRuntimeX86Installed'` and **no** `InstallCondition` | **executed**, build side |
| 2. The x86 runtime is installed by the bundle | **not executed** — needs a machine without it |
| 3. The MSI installs, no launch-condition failure | **executed**, but with the runtime already present |
| 4. Log shows the prerequisite detected absent, then planned for install | **not executed** — it was detected present |
| "Second install / repair": detected present and skipped | **executed** |

From the VM, with the x86 runtime present:

```
Detected package: Prereq_vcredist_2022_x86, state: Present, ...
Planned package:  Prereq_vcredist_2022_x86, state: Present, ..., execute: None, ...
PASS the MSI installed: C:\Program Files (x86)\X86BundleProbe\readme.txt
PASS the product is uninstalled
```

That is worth more than it first appears, because it settles the part of #8's structural
argument that was actually uncertain: the bundle's `util:RegistrySearch` with
`Bitness="always32"` and the 32-bit MSI's own launch condition under WOW64 redirection **agree
about the same physical key**. Both saw the runtime; the MSI installed rather than refusing. It
also covers the repair/skip case, which the original plan listed as the half the structural
argument was weakest on.

### What is still argued rather than observed

That on 64-bit Windows **without** the x86 runtime, Burn installs it and the MSI then passes.

The argument, now narrower than when #8 shipped: the whole content of the fix is which XML is
emitted. Before, the package carried `InstallCondition='NOT VersionNT64'`, which made it
inapplicable on 64-bit Windows, so it was never installed. After, `singlePrereqArch` suppresses
the InstallCondition when the bundle chains exactly one architecture, and applicability is
governed by the `DetectCondition` alone — which the VM run shows evaluating correctly. What
remains untested is that Burn installs an applicable, Absent, Vital package, which is its most
basic behaviour and is what every prerequisite in every msis bundle depends on.

### Why the last check was abandoned

It needs 64-bit Windows with **no** x86VC++ runtime. In practice that means a pristine image and
nothing else: the test VM used here reported

```
x86 entries in Add/Remove: Microsoft Visual C++ 2022 X86 Debug Runtime - 14.51.36247
```

a Visual Studio component, which cannot be uninstalled from Apps & Features. Anything that
touches a dev toolchain drags the runtime in, and the probe cannot remove what it did not
install.

If a genuinely pristine image ever exists, `testscripts/t3/` still does the whole thing:
`uv run t3_x86_bundle_probe.py` here, then `t3_vm_probe.py --state` there to confirm the machine
qualifies before installing anything. Deleting
`HKLM\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\14.0\VC\Runtimes\x86` on a snapshotted VM
would simulate absence for the detect condition, but it is not a clean substitute: the
redistributable may then decide it is already installed, no-op, and leave the key missing, which
would look like a failure of msis rather than of the simulation.

### A defect in the probe, recorded so it is not repeated

The first VM run should never have started. The probe's guard — whose only job is to refuse
when the runtime is present, because the run then proves nothing — read the **64-bit** registry
view for an **x86** runtime:

```
x86   64-bit view                absent
x86   32-bit view (WOW6432Node)  Installed=1 Version=v14.51.36247.00
```

The x86 redistributable is a 32-bit installer, so it registers under `WOW6432Node`, which is
also exactly where the bundle looks (`Bitness="always32"`). Reading the wrong view reported the
runtime as absent on a machine that had it, and the run went ahead and proved nothing. A second
bug in the same script filtered Add/Remove entries on `"2015"` and a case-sensitive `"x86"`,
matching none of the real names (`Microsoft Visual C++ v14 Redistributable (x86)`,
`... 2022 X86 Minimum Runtime`). Both produced confident FAIL lines that were the script's own.

Both are fixed. The lesson is the one T1 also produced: a probe's preconditions need checking
as carefully as its assertions, because a broken guard does not fail loudly — it lets a
worthless run look like a real one.


---

## T4 — ARM64 auto-bundle installs the ARM64 VC++ runtime

**Ticket:** [#12](https://github.com/gersonkurz/msis/issues/12)

### Why this is open

Same shape as T3 and the same reason: the bug only reproduces on ARM64 Windows without
the ARM64 VC++ runtime, and no ARM64 hardware is available here. The fix was shipped on
generated output plus a structural argument.

What **is** verified: the ARM64 package is emitted (it was silently absent without a
cached path); its detect condition is `VcppRuntimeArm64Installed`; both bundle templates
define that variable; a real ARM64 bundle builds against WiX 7.0.0; and an automated
test now fails if any detect condition names a variable the templates do not define.

That last guard exists because a bundle referencing an **undefined** Burn variable
**builds without error** — the condition is simply false forever. Discovered by
accident, when a first build "passed" against the installed templates rather than the
edited ones. Keep it in mind when reading a green build here: compiling proves the
authoring, not the detection.

What is **not** verified: that the runtime is actually detected as absent, installed,
and then skipped on a second run.

### Setup

An ARM64 Windows machine with **no** ARM64 VC++ 2015-2022 runtime. Confirm — this must
print nothing:

```powershell
Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\arm64' -ErrorAction SilentlyContinue
```

**Leave the x64 runtime installed if it is there.** That is the exact case the old code
got wrong: it detected the x64 runtime and declared the machine satisfied.

Build an ARM64 package requiring the runtime:

```xml
<set name="PLATFORM" value="arm64"/>
<requires type="vcredist" version="2022"/>
```

`msis /BUILD probe.msis` produces the auto-bundle `.exe`. Copy it to the ARM64 machine.

### Proof that closes this

Paste into #12:

1. **The chain in the generated `.wxs`** (build with `/RETAINWXS`):
   `DetectCondition='VcppRuntimeArm64Installed'` and
   `InstallCondition='NativeMachine = 43620'` on `Prereq_vcredist_2022_arm64`, and a
   `<util:RegistrySearch Id="VcppRuntimeArm64" .../>` present in the same file. If that
   search is missing, the build used different templates — see the note above.
2. **The ARM64 runtime is installed afterwards**: the registry check above now returns a
   value, and the ARM64 redistributable appears in Apps & Features.
3. **The MSI installed** — no `VCREDIST_ARM64_2022` launch-condition dialog.
4. **A second run skips it**: with the runtime now present, the package must be detected
   as installed and not reinstalled. This is the half the structural argument covers
   least well, exactly as in T3.

### Also worth one run

An ARM64 machine that already has **only the x64** runtime, and no ARM64 one, must still
install the ARM64 runtime. That is the original bug stated as a test, and the most
direct confirmation that the fix works.

---

## T5 — top-level `<remove-on-uninstall>` actually deletes the right things: DONE 2026-09-19

**Tickets:** [#15](https://github.com/gersonkurz/msis/issues/15) (the fix that made this
reachable), [#3](https://github.com/gersonkurz/msis/issues/3) (same mechanism — see T7).

Executed on a snapshotted VM on 2026-09-19 with `testscripts/t5t7/`. All sixteen checks passed.

Until #15 a top-level `<remove-on-uninstall>` **failed the build**, so its components never
installed and never ran. The fix gives them a feature, and that is the moment a recursive
folder delete and a registry key delete become reachable — which is why this was recorded as
debt rather than treated as closed by a green build.

### What was observed

Package: cleanup elements written directly under `<setup>`, alongside a `<feature>` that
installs one file. The `.wxs` check runs on the build machine: both `C_RemoveOnUninstall_0000`
and `C_RemoveOnUninstall_0001` are referenced by `<Feature Id='MSIS_PACKAGE_ITEMS'>`, the
synthetic feature #15 introduced. That is #15's failure mode in its silent form, and it is
clean.

On the VM:

| Check | Result |
|---|---|
| install | rc 0, product installed |
| remembered path **equals** `C:\ProgramData\CleanupProbeTop\Vendor\logs` | PASS |
| repair (`msiexec /f`) leaves the seeded files in place | PASS |
| repair leaves the seeded **registry value** unchanged | PASS |
| uninstall | rc 0 |
| target folder gone | PASS |
| file in a **nested subfolder** gone with it | PASS |
| target registry key gone — genuinely absent, not merely unreadable | PASS |
| sentinel in the **parent** directory still there | PASS |
| — and its **contents unchanged** | PASS |
| sentinel in a **sibling** directory still there | PASS |
| — and its **contents unchanged** | PASS |
| **sibling registry key** still there | PASS |
| — and its **value unchanged** | PASS |

The sentinels are the point. The value of this mechanism over `REMOVE_FOLDERS_ON_UNINSTALL` is
a narrower blast radius, and that is now observed rather than argued: the delete took the named
tree and its nested contents, and the parent, the sibling directory and the neighbouring
registry key came through unchanged — contents and values compared, not merely counted as
present.

The path check is the trap both tickets are built around. `[APPDATADIR]` already includes the
product folder — the MSI's Directory table shows `APPDATADIR | CommonAppDataFolder |
CleanupProbeTop` — so `[APPDATADIR]Vendor\logs` resolves to
`C:\ProgramData\CleanupProbeTop\Vendor\logs`, not `C:\ProgramData\Vendor\logs`. The probe
asserts equality with the intended absolute path, because "the stored value is absolute" would
pass while aiming the delete somewhere else.

### The first run of this probe established less than it appeared to

A run on 2026-09-19 passed every check it had, and review then found four ways the probe could
report success wrongly: the runner tested an `(ok, facts)` tuple and so could never fail; the
seeded registry value was never read after the repair, so a repair that deleted it would have
had that deletion credited to the uninstall; "untouched" meant only "still exists"; and an
unreadable key counted as a deleted one. The table above is from the strengthened probe, after
all four were fixed.

Kept here because the lesson generalises: a probe guarding a destructive path needs its own
failure modes exercised, or a green run means less than it looks. `--selftest` now covers
twenty verdict failure modes, the runner, and the registry observers driven against a real key
— that last one because a selftest that only mutates the observations by hand cannot catch a
bug in the code that makes them.

### Not covered here

The **major upgrade** case: `On='uninstall'` removes the parent component, which also happens
during `RemoveExistingProducts`. Whether the cleanup fires mid-upgrade is recorded as an open
question in T7, which carries the remaining cases for both tickets.

### Rerunning it

`testscripts/t5t7/` — `uv run t5t7_cleanup_probe.py` here, copy `vm-payload/` to a snapshotted
VM, `uv run t5t7_vm_probe.py` elevated there. `--selftest` checks the verdict, the runner and
the observers without installing anything, and is worth running first.


---

## T6 — upgrading into and out of a duplicated source file (issue #21)

**Ticket:** [#21](https://github.com/gersonkurz/msis/issues/21) — same source installed to two
targets. Fixed by giving each destination its own component GUID.

**Why this needs a real machine:** the fix changes an *existing* component's GUID when a later
release adds a second destination for a source file that previously had one, and changes it
back if that destination is removed again. A shipped v1 with a single copy absolutely can
exist, so this transition is reachable in the field even though a duplicating package never
could be built before.

**Accepted as a deferred check by Gerson on 2026-09-18**, on the structural evidence below,
after being shown that it could not be executed in the development session (no elevation).

### What is already established, executed

Read out of a built MSI's `InstallExecuteSequence` table:

```
  1400  InstallValidate
  1401  RemoveExistingProducts     <- previous product removed here
  1500  InstallInitialize
  3500  RemoveFiles
  4000  InstallFiles               <- new files installed here
```

That is WiX's `afterInstallValidate` default for `<MajorUpgrade>`, which every msis template
uses. The old product is therefore fully uninstalled **before** any new file is laid down, so a
component GUID change between releases cannot produce the classic failure where the outgoing
product's removal deletes a file the incoming one just installed.

What is *not* established is that this holds on a real machine, which is what T6 is for.

### Steps

Build three packages that differ only as described, all sharing one `UPGRADE_CODE`:

| Version | `.msis` content |
|---|---|
| 1.0.0 | `<files source="app.txt" target="[INSTALLDIR]"/>` |
| 2.0.0 | the same, plus `<files source="app.txt" target="[APPDATADIR]PonyDup\seed"/>` |
| 3.0.0 | back to the 1.0.0 content |

Then, elevated, with verbose logging (`msiexec /i pkg.msi /l*v step.log /qn`):

1. Install 1.0.0.
2. Upgrade to 2.0.0.
3. Upgrade to 3.0.0.
4. Uninstall 3.0.0.

### Proof required

- **After step 1:** `[INSTALLDIR]\app.txt` exists.
- **After step 2:** *both* copies exist — `[INSTALLDIR]\app.txt` **and**
  `[APPDATADIR]\PonyDup\seed\app.txt`. The first must still be present: it is the copy whose
  GUID changed, and its disappearance is the specific failure this check exists to catch.
- **After step 3:** `[INSTALLDIR]\app.txt` exists and the `[APPDATADIR]` copy is **gone** —
  no orphan left behind by the component that no longer exists.
- **After step 4:** neither copy remains, and neither directory is left holding a stray file.
- In each log, `RemoveExistingProducts` appears **before** the first `InstallFiles` entry,
  confirming on a real machine what the sequence table says.

### If any step fails

Reopen #21. The likely shape of a failure is a missing `[INSTALLDIR]\app.txt` after step 2 (the
re-keyed component treated as removed rather than replaced) or a surviving `[APPDATADIR]` copy
after step 3 (an orphan). Either means component identity is being changed in a way the upgrade
sequence does not absorb, and the answer would be to keep the first destination on its historic
GUID — accepting the reordering instability that trade-off brings — rather than to document
around it.

---

## T7 — `<remove-on-uninstall folder=>` deletes the right tree and nothing else (issue #3)

**Ticket:** [#3](https://github.com/gersonkurz/msis/issues/3). Core executed and passed
2026-09-19; **four cases remain** before #3 closes. Shares a fixture with T5, which is done.

### Executed on 2026-09-25, second run — the #76 fix: PASS

The VM was rolled back to its snapshot and the harness re-run. All six packages were rebuilt
with the #76 fix: both cleanups carry `Condition="NOT UPGRADINGPRODUCTCODE"`, checked in both
upgrade MSIs' `Wix4RemoveFolderEx` and `Wix4RemoveRegistryKeyEx` tables, and no standard
`RemoveRegistry` row is left. The overall verdict was PASS.

- **Major upgrade 1.0.0 → 1.0.1, now judged:**
  - `PASS the application's files survived the upgrade` and `PASS the application's registry
    value survived the upgrade unchanged`;
  - every sentinel intact after the upgrade;
  - after the uninstall of 1.0.1: `the target folder is gone`, `the nested file with it`,
    `the target registry key is gone`, and every sentinel intact.
- **The four core passes** (top level, feature, minimal, silent x86) passed as in the first run.
- **The empty and never-existed folder cases:** unchanged. The install creates the target folder;
  after the uninstall it is gone and the key is absent. The uninstall succeeds, and every
  sentinel is intact.

This closes the four cases above, and with them #3 and #76.

### Executed on 2026-09-25 — all seven scenarios; the major upgrade found #76

On the snapshotted VM, with the harness at 9fdcfa7 (packages built with msis at 457f8a6), the
overall verdict was PASS. Every PASS/FAIL check passed:
- the four core passes: top level, in a feature, the minimal template, and the silent x86
  template (its registry checked in the 32-bit view);
- the major upgrade 1.0.0 → 1.0.1;
- the folder already empty;
- the folder never created.

The recorded lines:

| Scenario | Observed |
|---|---|
| major upgrade 1.0.0 → 1.0.1 (regular x64 template) | **the upgrade DELETED the application's files in the target folder** (runtime.log and deep\nested.txt gone), and the target registry key (missing-key). The neighbours were untouched |
| folder already empty | the install created the target folder; after the uninstall it is gone, and the target registry key is absent |
| folder never existed | the install created the target folder; with it removed before the uninstall, the uninstall succeeds, the folder is still gone, and the key is absent |

The upgrade result is a data-loss bug, filed as
[#76](https://github.com/gersonkurz/msis/issues/76). The cleanup was not gated on
`NOT UPGRADINGPRODUCTCODE`, so the old version's removal during `RemoveExistingProducts` ran
it. The fix gates both cleanups (decisions D21), and the harness now JUDGES the upgrade: the
data must survive it. **Re-run the harness on the VM with the fix** before #3 and #76 close.

### Executed on 2026-09-19 — the core

Package: the cleanup elements inside a `<feature>`. On the build machine, both
`C_RemoveOnUninstall_0000` and `C_RemoveOnUninstall_0001` are referenced by
`<Feature Id='FEATURE_00000'>`. On a snapshotted VM, all sixteen checks passed:

| Case | Result |
|---|---|
| install; remembered path **equals** `C:\ProgramData\CleanupProbeFeat\Vendor\logs` | PASS |
| **repair** leaves the seeded files in place | PASS |
| **repair** leaves the seeded registry value unchanged | PASS |
| **uninstall**: target folder gone, nested file gone with it | PASS |
| target registry key gone — genuinely absent, not merely unreadable | PASS |
| parent sentinel, sibling sentinel and sibling registry key all still there | PASS |
| — and their **contents and values unchanged** | PASS |

So the recursive delete takes the named tree and nothing around it, a repair is not a data-loss
event for files or for registry values, and the neighbours came through unchanged rather than
merely present. Those were the substance of the concern.

The same package shape at top level passed identically — see T5, which also records the four
defects found in the probe itself before these results were trusted.

### Still to run — what keeps #3 open

| Case | Why it matters |
|---|---|
| **Major upgrade** | `On='uninstall'` removes the parent component, and so does `RemoveExistingProducts`. If the cleanup fires mid-upgrade it would delete the user's data during what is meant to be an update. **Record what is observed**; either answer is documentable, but only the observed one. Note the exact from/to versions and the template |
| **Folder already empty** | record the behaviour |
| **Folder never existed** | uninstalling a package whose application never ran; record the behaviour |
| **minimal and silent-x86 templates** | #19 made these viable — all five shipped templates now carry `{{{REMOVE_ON_UNINSTALL}}}` and a template that would discard it fails the build. One confirming pass each. A difference from the regular template is a finding, not a documentation note |

The major upgrade is the one with teeth. The other three are "record what happens".

**All four are in the harness since 2026-09-25.** The upgrade is from 1.0.0 to 1.0.1 of one
product with the regular x64 template (`upg-1.0.0.msi` → `upg-1.0.1.msi`). The two template passes
are `feat-minimal.msi` and `feat-silentx86.msi`; the latter's registry keys are checked in the
32-bit view. The two folder cases reuse `feat.msi`. Build with `uv run t5t7_cleanup_probe.py`,
copy `vm-payload/` to a snapshotted VM, and there run `t5t7_vm_probe.py --selftest`, then
`t5t7_vm_probe.py` elevated.

The run passes or fails on what must hold whatever happens:
- every install and uninstall succeeds;
- the remembered path is exact;
- every sentinel survives.

What the cases above ask to be recorded is printed as `OBSERVED` and listed again at the end.
Copy those lines here with the date.

### The path trap, confirmed

`[APPDATADIR]` already includes the product folder: the MSI's Directory table shows
`APPDATADIR | CommonAppDataFolder | CleanupProbeFeat`, so `[APPDATADIR]Vendor\logs` resolves to
`C:\ProgramData\CleanupProbeFeat\Vendor\logs`, **not** `C:\ProgramData\Vendor\logs`. Anyone
writing the latter and expecting the former would be aiming a recursive delete at the wrong
directory. The probe asserts equality with the intended absolute path rather than checking it
"looks absolute", and that check passed.

Worth documenting in the tutorial independently of how the remaining cases turn out.

### Proof required to close #3

The four cases above. The core is done: contents and registry values compared, absence
distinguished from an access error. `testscripts/t5t7/` is the harness — `uv run
t5t7_cleanup_probe.py` here, copy `vm-payload/` to a snapshotted VM, run `t5t7_vm_probe.py`
elevated. `--selftest` exercises the verdict, the runner and the registry observers without
installing anything.

### If anything outside the named target is removed

Stop and reopen #3. Do not adjust the documentation to match — this is the failure mode that
cost a customer their database once already.

---

## T8 — the `[\[]` escape in a `.reg` string: formatted when written directly, verbatim when preserved: DONE 2026-09-23

**Ticket:** [#38](https://github.com/gersonkurz/msis/issues/38); the contract is
`docs/decisions.md` D4.

### Why this is open

The #11 probe measured the two things D4 rests on: a non-preserved `a[Foo]b` installs as
`ab`, and a preserved `a[Foo]b` installs as `a[Foo]b` — the property's content is inserted
without a second formatting pass. Two consequences follow from the same mechanism and are
argued, not observed:

1. a non-preserved `a[\[]b` installs as `a[b` (the escape is resolved);
2. a preserved `a[\[]b` installs as `a[\[]b` (the escape lands verbatim) — the reason the
   tutorial says not to escape in a value that will be preserved.

Neither destroys anything, which is why they were not installed before documenting. But the
tutorial gives both as fact-by-inference and says so; one run turns them into fact.

### Setup

`esc.reg` — in a `.reg` string `\\` is one backslash, so this is the MSI escape `[\[]`:

```
Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MsisEscapeProbe]
"Escaped"="a[\\[]b"
"MidRef"="a[Foo]b"
```

Two packages from the same file: one `<registry file="esc.reg"/>`, one with
`preserve="yes"`. Build both; confirm the first emits `Value='a[\[]b'` and the second
`<Property Id='PS_RV_00000' Value='a[\[]b' ...>` (the unit tests already pin this shape).
Make sure `HKLM\SOFTWARE\MsisEscapeProbe` is absent, so the `.reg` default is what the
preserved path writes.

### Proof that closes this

Install each **elevated**, read the key back (`reg query HKLM\SOFTWARE\MsisEscapeProbe`),
uninstall between runs:

| Package | `Escaped` | `MidRef` |
|---|---|---|
| direct | `a[b`, REG_SZ | `ab` (re-confirms #11) |
| preserved | `a[\[]b`, REG_SZ | `a[Foo]b` (re-confirms #11) |

If the direct row matches and the preserved row does not, the tutorial's "do not escape a
preserved value" is wrong and D4's preserved-path paragraph needs rewriting — say what was
observed. If the direct row does not match, the escape advice in the build warning is wrong,
which is worse: fix `warnFormattedValues` and the tutorial together.

### What was observed (2026-09-23, #48)

Windows 11 (10.0.26200), elevated shell, WiX 6.0.2, msis built from `d5cd37a`. Key absent
beforehand. Each package installed silently (`msiexec /i … /qn /l*v`), the key read back with
`Get-Item HKLM:\SOFTWARE\MsisEscapeProbe`, then uninstalled; all four `msiexec` runs exited 0,
and afterwards the key and the install folder were both gone.

| Package | `Escaped` | `MidRef` |
|---|---|---|
| direct | `a[b`, String | `ab` |
| preserved | `a[\[]b`, String | `a[Foo]b` |

**Both rows match the prediction.** The escape is resolved when the value is written directly
into the Registry table, and lands verbatim when it goes through a preserved `PS_RV_n`
property; `MidRef` re-confirms #11 on both paths. The verbose log of the direct install shows
the same thing at the operation level: `RegAddValue(Name=Escaped,Value=a[b)` and
`RegAddValue(Name=MidRef,Value=ab)`. The tutorial and D4 now state both as observed.

The emitted shapes were as the Setup section says: direct `Value='a[\[]b'`; preserved
`<Property Id='PS_RV_00000' Value='a[\[]b' …>` with the value written as `[PS_RV_00000]`.

### Two probe-setup traps, recorded so the next probe avoids them

- **A package whose only content is a `<registry>` item does not build**: WiX fails with
  `WIX0094: The identifier 'Directory:INSTALLDIR' could not be found` — the feature carries
  `ConfigurableDirectory='INSTALLDIR'`, but no directory is emitted when there are no files.
  Filed as [#54](https://github.com/gersonkurz/msis/issues/54); the probe packages carry one
  small payload file to get past it.
- **Without `<set name="INSTALLDIR" …/>` the payload installs straight into
  `C:\Program Files\`**, and msis's folder-permission component then tries to change the ACL
  of `C:\Program Files\` itself: `Error 25521. Failed to set security descriptor on object
  C:\Program Files\` (access denied), install rolled back, 1603. Set `INSTALLDIR`. Filed as
  [#55](https://github.com/gersonkurz/msis/issues/55) and fixed there: an unnamed root
  directory no longer gets a permission component, so this failure no longer occurs — but the
  payload still lands in `C:\Program Files\` itself, so set `INSTALLDIR` regardless.

### Rerunning it

The exact files used, in one directory. Write `esc.reg` with an editor or a file tool, not a
shell heredoc — the `\\` must survive — and check it shows `"Escaped"="a[\\[]b"`. `readme.txt`
is any small file.

`esc.reg`: as in *Setup* above.

`direct.msis` (`preserved.msis` is identical except the product name, the last hex digit of
the upgrade code, `BUILD_TARGET` and `preserve="yes"` on the `<registry>` element):

```xml
<setup>
  <set name="PRODUCT_NAME" value="MSIS T8 Direct"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis probe"/>
  <set name="UPGRADE_CODE" value="{5A1D7E30-8C24-4F9B-A3E6-1B2C4D5E8F01}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="INSTALLDIR" value="MsisT8Probe"/>
  <set name="BUILD_TARGET" value="direct.msi"/>
  <feature name="Main">
    <files source="readme.txt" target="[INSTALLDIR]"/>
    <registry file="esc.reg"/>
  </feature>
</setup>
```

Build both with `msis /BUILD /RETAINWXS direct.msis` and `… preserved.msis` from that
directory, then run `run-t8.ps1` from an **elevated** PowerShell
(`powershell -ExecutionPolicy Bypass -File run-t8.ps1`):

```powershell
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
$key = 'HKLM:\SOFTWARE\MsisEscapeProbe'

function Invoke-Msi($verb, $msi, $log) {
    $p = Start-Process -FilePath msiexec.exe -ArgumentList @($verb, "`"$PSScriptRoot\$msi`"", '/qn', '/l*v', "`"$PSScriptRoot\$log`"") -Wait -PassThru
    Write-Output ("msiexec {0} {1} -> exit {2}" -f $verb, $msi, $p.ExitCode)
}

function Show-Key($label) {
    if (Test-Path $key) {
        $k = Get-Item $key
        foreach ($name in 'Escaped', 'MidRef') {
            Write-Output ("{0}: {1} = {2} ({3})" -f $label, $name, $k.GetValue($name), $k.GetValueKind($name))
        }
    } else {
        Write-Output ("{0}: key {1} is ABSENT" -f $label, $key)
    }
}

if (Test-Path $key) { throw "precondition: $key exists; remove it first" }

foreach ($pkg in 'direct', 'preserved') {
    Write-Output "=== $pkg ==="
    Invoke-Msi '/i' "$pkg.msi" "$pkg-install.log"
    Show-Key "$pkg after install"
    Invoke-Msi '/x' "$pkg.msi" "$pkg-uninstall.log"
    Show-Key "$pkg after uninstall"
}
Write-Output ("install dir present afterwards: {0}" -f (Test-Path 'C:\Program Files\MsisT8Probe'))
```

The expected output is the table above, with every msiexec exit 0 and the key absent after each
uninstall.


## T9 — a `<service>` in another feature than its executable (issue #77): DONE 2026-09-25

Probe: `testscripts/t77` (build side `uv run t77_service_probe.py`, VM side
`vm/t77_vm_probe.py`, see its README). Package: feature Complete installs `svcprobe.exe`,
optional feature Service registers a service (registered, never started). After every step the
probe checks each copy of the executable (present exactly while its feature is installed,
contents unchanged) and the service registration (present exactly while Service is installed,
ImagePath naming its copy).

### Executed on 2026-09-25, first run — msis 19e7dae, the #77 layout: FAIL

Service named Complete's file; msis installed it twice, in two components.

| Scenario | Result |
|---|---|
| install both → remove Service | FAIL: the exe missing, Complete still installed |
| install both → remove Complete | FAIL: the exe missing, the service still registered |
| Complete → add Service → remove Service | FAIL: the exe missing |
| Service only → uninstall | PASS |

Every msiexec returned 0. Recorded on #77.

### Executed on 2026-09-25, second run — the #77 fix (D22): PASS

msis now refuses that layout at build time (the build side checks it). The VM ran the layout the
error proposes: the Service feature installs its own copy under `service\` and registers that.
All four scenarios PASS: removing either feature leaves the other's copy intact, and the service
is registered exactly while Service is installed. Every msiexec returned 0.
