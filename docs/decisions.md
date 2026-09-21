# Settled questions

Some things in msis look like defects, are reported as defects, and are not. They were
investigated, often with an install probe on a real machine, and settled deliberately — sometimes
as "this is the lesser harm", sometimes as "Windows Installer cannot express it".

Without a record, each one gets found again. It happened: #10 settled `REG_QWORD` handling in
September 2026 with an elevated install probe, and the same question came back as #40 because a
note predating the fix outlived it.

**This file is that record.** It is not a changelog — a fix that simply works needs no entry. An
entry earns its place by being a decision someone will reasonably want to reopen.

## How to use it

- **Before filing a `[task]` finding**, or migrating an old note into the tracker, check here. A
  finding that restates a settled question is closed with a pointer to its entry, not filed
  again.
- **Before treating an entry as gospel**, read it. Each says what was decided and on what
  evidence. Evidence that contradicts it is a new issue — one that cites the entry and says what
  changed. A decision is not a prohibition on thinking, only on re-deriving.
- **Each entry names the code that implements it**, and
  `TestEverySettledDecisionIsStillImplemented` checks those anchors are still present in the
  named file. That is an **anchor-presence check and nothing more**: it catches an entry whose
  code was deleted, moved or renamed, and it does NOT catch a decision reversed in place —
  flipping a `return false` to `return true` leaves every anchor exactly where it was. Keeping
  the prose true is a job for review, which is how all three corrections to this file's first
  draft were found.

---

## D1 — `REG_QWORD` is written as a 32-bit truncation, not rejected

**Settled in:** [#10](https://github.com/gersonkurz/msis/issues/10), closed 2026-09-17 (`40f3699`).
**Re-raised as:** [#40](https://github.com/gersonkurz/msis/issues/40), closed as settled.
**Implemented by:** `internal/registry/registry.go` — `val.Value = fmt.Sprintf("%d", uint32(full))`

MSI's Registry table can express REG_SZ, REG_EXPAND_SZ, REG_BINARY, REG_MULTI_SZ and REG_DWORD,
and nothing wider. **No installer built on Windows Installer can write a REG_QWORD** through the
standard registry tables. That part is not msis's choice.

What msis chose, faced with that: write the low 32 bits as a REG_DWORD rather than refuse to
build, since refusing would break existing packages that carry a QWORD and never depended on the
upper half.

The build warns **when upper bits are actually lost**, and only then. A QWORD inside 32-bit range
is silent (`TestWarnQwordThatFitsIsSilent`): its value survives intact, the type narrowing is
unavoidable for every QWORD, and a warning nobody can act on trains people to ignore the ones
that matter.

`RegistryValue.FromQword` keeps the QWORD distinguishable where that matters — it is what
excludes the value from preservation (D2) — so "the distinction is discarded" is not accurate of
the code, even though the emitted *type* is necessarily DWORD.

Documented for users in `docs/tutorial.md`, under *QWORD values are truncated to 32 bits*, which
separates the limitation from the policy. Boundary behaviour is covered by
`internal/registry/registry_test.go`.

**What would reopen this:** a way to write a REG_QWORD from an MSI without a custom action, or a
decision that refusing to build is better than truncating.

## D2 — `preserve="yes"` skips `REG_EXPAND_SZ` and `REG_QWORD` entirely

**Settled in:** [#10](https://github.com/gersonkurz/msis/issues/10), closed 2026-09-17 (`40f3699`).
**Re-raised as:** [#39](https://github.com/gersonkurz/msis/issues/39), closed as settled.
**Implemented by:** `internal/registry/registry.go` — `if val.Type == "expandable" {`, `if val.FromQword {`

Preservation reads the live value with a `Type='raw'` `RegistrySearch`, and an elevated install
probe (WiX 7.0.0 / Windows 11) showed that search **damaging the value before any encoding gets a
say**:

- a live `REG_EXPAND_SZ` of `%TEMP%` arrived in the property as
  `C:\Users\<name>\AppData\Local\Temp` — expanded, type marker stripped — and was written back as
  a plain REG_SZ: a machine-specific path baked into the customer's registry;
- a live `REG_QWORD` of `0xFEDCBA9876543210` arrived as `㈐癔몘ﻜ`, which is those eight bytes read
  as UTF-16LE, and was written back as REG_SZ. **The install exits 0**, so nothing flags it.

No default encoding can fix either, because the damage happens in the search. Both types are
therefore left unpreserved and written fresh from the `.reg` file — a correct unexpanded
REG_EXPAND_SZ, and for QWORD the truncation of D1. The cost is that a user's live edit to one of
these two types is overwritten by the `.reg` default. That is the lesser harm against silent
corruption.

The original issue proposed correcting the type marker instead (`#%`). That would have fixed the
fresh-install half of the first case and nothing at all of the second.

**What would reopen this:** a preservation mechanism that does not go through `Type='raw'`
`RegistrySearch` — which, given msis has only one CA binary and it is opt-in (see D3), is a
larger change than it sounds.

## D3 — `preserve="yes"` does not preserve an existing EMPTY value

**Settled in:** [#26](https://github.com/gersonkurz/msis/issues/26), closed 2026-09-19 as
won't-fix, product owner's decision.
**Implemented by:** `internal/registry/registry.go` — `// Preservation is handled entirely by the RegistrySearch`

The install probe recorded in `todo-testme.md` (T1) shows the mechanism, and it is not the
obvious one. The search **runs** and **reads** the empty value — and then AppSearch makes no
assignment from an empty result: the MSI log has no `PROPERTY CHANGE` line for that property,
while every value that *was* preserved has one. `PS_RV_n` therefore still holds the `.reg`
default it was initialised with, and that default is what gets written.

It is worth being precise about this, because the plausible explanation is wrong: the property
does not end up empty and format to nothing. What landed was the `.reg` default, which is also
what rules out the property having been removed. "Found, but empty" and "not found" are
indistinguishable by the time the result is a property value.

Not a regression: msis-2.x emits the same property-with-nested-search construct
(`msi-simplified/WxsItem/RegistryKey.cs`), so this is as old as the feature.

A single custom action handling every preserved value would sidestep it, but msis has one CA
binary — the installer-hook DLL, opt-in via `USE_INSTALLER_HOOKS` and off by default. Making a
core feature depend on an optional native component to recover one edge case was judged the wrong
trade.

Documented in `docs/tutorial.md` under *Preserving User-Modified Values*, with the practical
advice: if a blank has to mean something to your application, keep that value out of the `.reg`
file and let the application write its own default on first run.

**What would reopen this:** a second CA binary that is not opt-in, or evidence that MSI can carry
the distinction after all.
