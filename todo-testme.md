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

## T5 — top-level `<remove-on-uninstall>` actually deletes the right things

**Tickets:** [#15](https://github.com/gersonkurz/msis/issues/15) (the fix that enabled
this), [#3](https://github.com/gersonkurz/msis/issues/3) (the same mechanism, verified
once covers both)

### Why this is open

Until #15, a top-level `<remove-on-uninstall>` **failed the build**, so its component
never installed and never ran. The fix gives those components a feature, which makes
them installable for the first time — and what they do is delete: a recursive folder
removal and a registry key removal.

So the build going from red to green is exactly the moment the destructive behaviour
becomes reachable. Linking proves the references exist; it proves nothing about what
gets deleted. No install has been run. Deferred with the product owner's explicit
acceptance on 2026-09-18.

**Run this together with T7** — same mechanism, same seeding, one session covers both
tickets. T7 carries the full plan for #3; this entry adds what is
specific to top-level placement.

### Setup

```xml
<setup>
  ...
  <set name="INSTALLDIR" value="CleanupProbe"/>
  <remove-on-uninstall folder="<the intended absolute logs dir>"/>
  <remove-on-uninstall registry="HKLM\Software\Vendor\CleanupProbe"/>
  <feature name="Main">
    <files source="readme.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

The `<feature>` is essential: without one the package took the WiX-default-feature path
and this is not the case under test.

**Mind the folder path.** `[APPDATADIR]` already includes the application subdirectory —
it falls back to the `INSTALLDIR` value — so `[APPDATADIR]Vendor\App\logs` resolves to
`C:\ProgramData\<INSTALLDIR>\Vendor\App\logs`. Decide the intended absolute directory
first and assert equality with it; "the stored path is absolute" would pass while
pointing somewhere else.

### Before installing

1. The generated `.wxs` (build with `/RETAINWXS`) contains a `<Feature
   Id='MSIS_PACKAGE_ITEMS' ...>` whose `<ComponentRef>` list includes **both** cleanup
   components.
2. After install, the path remembered in `HKLM\Software\<MFR>\<PRODUCT>` equals the
   intended absolute directory exactly.

### Then seed

- a runtime-created file directly in the target folder
- a runtime-created file in a **nested subfolder** of it
- a sentinel file in the target's **parent**
- a sentinel file in a **sibling** directory
- a registry value under the target key, and a sentinel key beside it

### Cases

| case | expected |
|---|---|
| **Install** | the synthetic feature's components are installed — check the MSI's `FeatureComponents` rows and that the remembered path is present in the registry afterwards |
| **Repair** | seeded runtime data still intact. A repair must never be a data-loss event |
| **Uninstall** | target folder and everything beneath it gone, nested file included; target registry key gone; **both file sentinels and the sibling registry key untouched** |
| **Major upgrade** | `On='uninstall'` means the parent component is removed, which also happens during `RemoveExistingProducts`. Record what is observed; either answer is documentable, but only the observed one |

The sentinels matter as much as the deletions. The value of this mechanism over
`REMOVE_FOLDERS_ON_UNINSTALL` is a narrower blast radius, and that is the claim to check.

### If anything outside the named targets is removed

Stop and reopen #15. Do not adjust the documentation to match — this is the failure mode
that cost a customer their database once already.

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

**Ticket:** [#3](https://github.com/gersonkurz/msis/issues/3) — full-folder removal on
uninstall. The element already exists; the concept was reviewed and approved, and the ticket
stays open **only** until these checks run. Closing it is what this entry is for.

**Run this together with [T5](#t5--top-level-remove-on-uninstall-actually-deletes-the-right-things)**
— same mechanism, same seeding. T5 covers top-level placement, T7 covers feature placement and
the upgrade/repair cases; one session with both fixtures covers both tickets.

This is a recursive delete of a directory the installer does not own, so the plan checks what
it removes **and** what it leaves alone.

### Since the concept was written

Two of its scope limits no longer apply, and the plan below reflects that:

- The concept said documented support was limited to feature-level placement, pending #15.
  **#15 is fixed** — top-level placement works and is T5.
- It said the minimal and silent templates silently drop the element, so they should be tested
  or documented as unsupported. **#19 fixed that**: all five shipped templates now carry
  `{{{REMOVE_ON_UNINSTALL}}}`, and a template that would discard it now fails the build. They
  are therefore worth one confirming pass rather than an exclusion.

### Mind the path — this is the trap

`[APPDATADIR]` **already includes the application subdirectory**: it falls back to the
`INSTALLDIR` value (`context.go:351`), so `[APPDATADIR]Vendor\App\logs` resolves to
`C:\ProgramData\<INSTALLDIR>\Vendor\App\logs`, not `C:\ProgramData\Vendor\App\logs`.

Decide the intended absolute directory first and **assert equality with it**. Checking only
that the stored value "is absolute" would pass while pointing somewhere else entirely — and
somewhere else is precisely what a recursive delete must not be aimed at.

### Authoring checks, in two separate steps

1. **Before install** — the cleanup component carries a `ComponentRef` from a feature. (That is
   #15's failure mode in its silent form.)
2. **After install** — the value stored under `HKLM\Software\<MANUFACTURER>\<PRODUCT_NAME>`,
   name `RemoveFolderPath_RemoveOnUninstall_nnnn`, equals the intended absolute directory
   **exactly**.

### Seeding, from a fresh state for each case

- a runtime-created file directly in the target folder
- a runtime-created file in a **nested subfolder**
- a sentinel in the target's **parent**
- a sentinel in a **sibling** directory

### Cases

| Case | Expected |
|---|---|
| **Uninstall** | target and everything beneath it gone, nested file included; both sentinels untouched |
| **Major upgrade** | `On='uninstall'` removes the parent component, which also happens during `RemoveExistingProducts`. **Record what is observed** — either answer is documentable, but only the observed one. Note the exact from/to versions and the template used; do not generalise to other upgrade arrangements |
| **Repair** | target contents intact — a repair must never be a data-loss event |
| **Folder already empty** | record the behaviour |
| **Folder never existed** | uninstalling a package whose application never ran; record the behaviour |

### Also worth one pass each, now that #19 made them viable

The same feature-level package built with `minimal/template.wxs` and, with `PLATFORM=x86` and
`silent="yes"`, `x86/template-silent.wxs`. Confirm the cleanup runs there too. If either
behaves differently from the regular template, that is a finding, not a documentation note.

### Proof required to close #3

All rows of the table above observed and recorded, plus the two authoring checks, plus the
`.wxs` showing the component referenced by a feature. Document only the placement/template
combinations actually exercised.

### If anything outside the named target is removed

Stop and reopen #3. Do not adjust the documentation to match — this is the failure mode that
cost a customer their database once already.
