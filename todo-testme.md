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

## T2 — REG_BINARY preservation, once [#6](https://github.com/gersonkurz/msis/issues/6) is fixed

**Ticket:** [#6](https://github.com/gersonkurz/msis/issues/6) — preserved `REG_BINARY`
default uses per-nibble `#x0#x1` encoding; install fails with Error 1406.

Not started, listed here so it is not tested by `wix build` alone: #6 is an
**install-time** failure (Error 1406), and the current wrong encoding compiles
perfectly happily. Whatever encoding replaces `encodeBinaryForPreserve`'s per-nibble
form must be proven by an actual install, not by a green build.

Same harness as T1: add a `"Blob"=hex:4f,4b` value to `rtprobe.reg`, seed a different
live blob, and check both the fresh-install default and the preserved-live case land
as `REG_BINARY` with the right bytes.

---

## Not in this file

[#9](https://github.com/gersonkurz/msis/issues/9) (preserved defaults are not
XML-escaped) is a build-time failure with a build-time repro, so it needs no machine
test — a unit test plus a `wix build` closes it. It is tracked in `TODO.md`.
