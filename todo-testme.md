# todo-testme.md — checks that need a real machine

Things that cannot be settled by unit tests or by `wix build`, because they only
happen when Windows Installer actually runs. Each entry says what to run, what
proof closes it, and which ticket it belongs to.

**These need an ELEVATED shell.** `msiexec` writes to `HKLM` and installs
per-machine; an unelevated run will fail or, worse, silently redirect.

---

## T1 — `preserve="yes"` runtime semantics

**Ticket:** [#5](https://github.com/gersonkurz/msis/issues/5) — fixed in commit
`645dbf0`, but the runtime half of the claim was never observed.

### Why this is open

#5 replaced the three-element preservation pattern (`PS_RV_` default + `PS_RS_`
search + a `SetProperty` custom action per value) with the two-element form: a
`PS_RV_` property carrying the `.reg` default with the `RegistrySearch` nested
inside it. That removed the per-value custom action, which is what made a few
hundred preserved values unbuildable.

What was proven: the WXS we emit, and that WiX 7.0.0 compiles it at 1500 values.
What was **not** proven: what Windows Installer does with it at install time. The
reviewer (Codex) accepted the change without this, and the product owner
explicitly deferred it on 2026-09-17 — but approval did not establish the runtime
outcome, and this file is where that debt lives.

Three specific unknowns:

1. **The empty-string case.** When the target value already exists as an *empty*
   `REG_SZ` and the `.reg` default is non-empty: `RegistrySearch Type='raw'`
   returns `""`, and setting an MSI property to the empty string undefines it.
   Expected: the live empty value is preserved (written back empty), *not*
   overwritten by the default. The old three-element form wrote the default here,
   because the `SetProperty` condition was false. This is a deliberate behaviour
   change and the one most likely to surprise someone.
2. **The elevated client→server handoff.** AppSearch runs client-side; the
   `RegistryValue` is written server-side during an elevated per-machine install.
   `Secure='yes'` is on every `PS_RV_` property for exactly this reason. Untested.
3. **The unnamed (default) value.** Microsoft's RegLocator documentation qualifies
   retrieval of a key's default value with "if it is not empty", so preservation of
   an unnamed value may behave differently from a named one.

### Setup

Create a folder anywhere outside the repo with these four files.

`rtprobe.reg`:

```
Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MsisPreserveProbe]
"Absent"="default-absent"
"Existing"="default-existing"
"EmptyExisting"="default-empty-existing"
"AbsentDword"=dword:0000002a
"ExistingDword"=dword:0000002a
@="default-unnamed"
```

(The trailing `@=` line covers unknown 3. Verified to parse and generate correctly —
it emits `PS_RV_00005` with a `RegistrySearch` carrying no `Name` attribute — and the
whole package builds against WiX 7.0.0. Only the install step is unproven.)

`rtprobe.msis`:

```xml
<setup>
  <set name="PRODUCT_NAME" value="Msis Preserve Probe"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{7C1B3A4D-1E2F-4A5B-8C6D-9E2F3A4B5C6E}"/>
  <feature name="Probe">
    <files source="readme.txt" target="[INSTALLDIR]"/>
    <registry file="rtprobe.reg" preserve="yes"/>
  </feature>
</setup>
```

`readme.txt`: any content. It exists only so `INSTALLDIR` resolves — a registry-only
package fails to link with `WIX0094: The identifier 'Directory:INSTALLDIR' could not
be found`.

`run-probe.ps1`:

```powershell
# Runtime probe for issue #5 / preserve="yes" semantics. MUST run ELEVATED.
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$K = 'HKLM:\SOFTWARE\MsisPreserveProbe'

if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run this from an ELEVATED PowerShell."
}

function Dump($label) {
    Write-Host "`n=== $label ===" -ForegroundColor Cyan
    if (-not (Test-Path $K)) { Write-Host "(key absent)"; return }
    $k = Get-Item $K
    foreach ($n in $k.GetValueNames()) {
        $v = $k.GetValue($n, $null, 'DoNotExpandEnvironmentNames')
        $t = $k.GetValueKind($n)
        Write-Host ("  {0,-16} {1,-12} [{2}]" -f $n, "'$v'", $t)
    }
}

# clean slate, then seed the "live" values a user would have edited
Remove-Item $K -Recurse -Force -ErrorAction SilentlyContinue
New-Item $K -Force | Out-Null
New-ItemProperty $K -Name 'Existing'      -Value 'live-existing' -PropertyType String | Out-Null
New-ItemProperty $K -Name 'EmptyExisting' -Value ''              -PropertyType String | Out-Null
New-ItemProperty $K -Name 'ExistingDword' -Value 99              -PropertyType DWord  | Out-Null
# 'Absent' and 'AbsentDword' are deliberately NOT seeded -> defaults must win.
Dump "BEFORE install (seeded)"

$log = Join-Path $here 'install.log'
msiexec /i (Join-Path $here 'rtprobe.msi') /qn /l*v $log
if ($LASTEXITCODE -ne 0) { throw "install failed: $LASTEXITCODE (see $log)" }
Dump "AFTER install"

Write-Host "`n=== AppSearch property changes in the MSI log ===" -ForegroundColor Cyan
Select-String -Path $log -Pattern 'PROPERTY CHANGE.*PS_RV_' | ForEach-Object { "  " + $_.Line.Trim() }

msiexec /x (Join-Path $here 'rtprobe.msi') /qn
Dump "AFTER uninstall"
```

### Run

```powershell
msis /BUILD rtprobe.msis        # unelevated is fine for the build
.\run-probe.ps1                 # ELEVATED
```

Then repeat the whole thing **without** `/qn` (full UI) — AppSearch runs in the UI
sequence there, and Windows Installer skips its execute-sequence invocation when it
already ran in the UI sequence. Both paths need to give the same answer.

### Proof that closes this

Paste into #5: the three `Dump` blocks, the `PROPERTY CHANGE.*PS_RV_` lines from the
MSI log, and whether the run was `/qn` or full UI. The table below is what the change
predicts; a mismatch on **any** row means #5 needs reopening, not just a doc tweak.

| Value | Seeded before install | Expected after install | Expected type |
|---|---|---|---|
| `Absent` | *(not present)* | `default-absent` | `REG_SZ` |
| `AbsentDword` | *(not present)* | `42` | `REG_DWORD` |
| `Existing` | `live-existing` | `live-existing` | `REG_SZ` |
| `ExistingDword` | `99` | `99` | `REG_DWORD` |
| `EmptyExisting` | `""` | `""` — **not** `default-empty-existing` | `REG_SZ` |
| *(unnamed)* | *(not present)* | `default-unnamed` | `REG_SZ` |

Also needed:

- **Types, not just values.** `AbsentDword` landing as `REG_SZ` `"42"` instead of
  `REG_DWORD` `42` is a real defect — the `#`-prefix encoding is what makes MSI
  write an integer, and it is easy to break.
- **After uninstall**, the key is gone.
- For the elevated handoff (unknown 2): the `/qn` run *is* the elevated
  client→server path. If `Existing` survives it, the handoff works and `Secure='yes'`
  is doing its job. Say so explicitly in the ticket.

### If a row does not match

Do **not** reinstate the per-value `SetProperty` — that is the #5 bug. The fix would
be to condition the write differently, or to accept and document the behaviour. Open
a new issue with the dump output and link it from #5.

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

## T3 — x86 auto-bundle installs the x86 VC++ runtime on 64-bit Windows

**Ticket:** [#8](https://github.com/gersonkurz/msis/issues/8)

### Why this is open

This is the one fix in this series that **was never run**. The bug only reproduces on
a 64-bit machine that does **not** have the x86 VC++ runtime, and every development
machine here has both runtimes already, so the detect condition passes regardless and
a local run proves nothing. The change was shipped on the generated XML plus a
structural argument, with the product owner's agreement that staff would verify.

The structural argument, so you know what you are confirming rather than just clicking
through: the bundle's `VcppRuntimeX86Installed` comes from a `util:RegistrySearch`
with `Bitness="always32"`, which on 64-bit Windows reads
`HKLM\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\14.0\VC\Runtimes\x86`. The 32-bit
MSI's own launch-condition search reads `HKLM\SOFTWARE\...\VC\Runtimes\x86` under
WOW64 redirection — **the same physical key**. So bundle and MSI should now agree on
whether the runtime is present. The point of the test is to confirm they actually do.

### Setup

A 64-bit Windows machine (VM or fresh image) with **no** VC++ 2015-2022 x86 runtime.
Confirm before starting — this must print nothing:

```powershell
Get-ItemProperty 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\14.0\VC\Runtimes\x86' -ErrorAction SilentlyContinue
```

If it returns a value, uninstall "Microsoft Visual C++ 2015-2022 Redistributable (x86)"
from Apps & Features first. The x64 runtime may be present — in fact **leave it
present**, because that is the exact case the old code got wrong: it reported the
machine as satisfied on the strength of the x64 runtime.

Build an x86 package that requires the runtime:

```xml
<setup>
  <set name="PRODUCT_NAME" value="X86 Bundle Probe"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="Probe Co"/>
  <set name="UPGRADE_CODE" value="{BB1B3A4D-1E2F-4A5B-8C6D-9E2F3A4B5C66}"/>
  <set name="INSTALLDIR" value="X86BundleProbe"/>
  <set name="PLATFORM" value="x86"/>
  <requires type="vcredist" version="2022"/>
  <feature name="Probe">
    <files source="readme.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>
```

`msis /BUILD probe.msis` produces `probe.exe` (the auto-bundle). Copy it to the clean
machine.

### Run

```
probe.exe /log bundle.log
```

### Proof that closes this

Paste into #8:

1. **The chain in the generated `.wxs`** (build with `/RETAINWXS`). It must read
   `DetectCondition='VcppRuntimeX86Installed'` with **no** `InstallCondition` on the
   `Prereq_vcredist_2022_x86` package. If `InstallCondition='NOT VersionNT64'` is still
   there, the build did not pick up the fix.
2. **The x86 runtime is installed afterwards** — the registry check above now returns a
   value, and "Microsoft Visual C++ 2015-2022 Redistributable (x86)" appears in Apps &
   Features.
3. **The MSI installed**, i.e. no `VCREDIST_X86_2022` launch-condition dialog. That is
   the user-visible symptom and the thing that was impossible before.
4. **From `bundle.log`**: the lines showing the x86 package being detected as absent
   and then planned for install. Search for `Prereq_vcredist_2022_x86` and
   `VcppRuntimeX86Installed`.

### Also worth one run each, same machine

- **Second install / repair.** With the runtime now present, the package must be
  detected as installed and **skipped**, not installed again. This is the half the
  structural argument is weakest on: it depends on the detect condition being read the
  same way once the key exists.
- **A 32-bit Windows VM**, if one is available — as a *regression* check, not a new
  case. `NOT VersionNT64` was already true on 32-bit Windows, so the package installed
  there before and must still install there now; dropping the condition is meant to add
  64-bit Windows, not to change anything about 32-bit.

### Not covered by this change

`PLATFORM=arm64` — fixed separately under issue #12, and it needs its own machine
check. See T4.

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
