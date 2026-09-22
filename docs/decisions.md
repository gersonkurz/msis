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

## D4 — A `.reg` string value is an MSI Formatted field, and msis does not transform it

**Settled in:** [#14](https://github.com/gersonkurz/msis/issues/14), closed 2026-09-18 (`239dcd7`),
which records the product owner's decision; the contract itself was documented and covered
under [#38](https://github.com/gersonkurz/msis/issues/38).
**Implemented by:** `internal/registry/registry.go` — `escapeXML(val.Value), val.Type, keyPathAttr`, `func (p *Processor) warnFormattedValues`

The Registry table's `Value` column is a Formatted field, so a REG_SZ written from a `.reg`
file is not written literally. Measured during the #11 probe: `a[Foo]b` installs as `ab`
(the undefined property substitutes to nothing), and `a[~]b` installs as a `REG_MULTI_SZ` of
`a` and `b` — the value **changes type**, and the install exits 0. A preserved value takes
the other route, `[PS_RV_n]`, and its content is inserted without a second formatting pass:
`a[Foo]b` installed as `a[Foo]b` in the same probe.

What msis chose, faced with that: **emit the value verbatim (XML-escaped only) and warn.** The
alternative — escaping brackets on the author's behalf — was rejected because `[` legitimately
means two different things in a `.reg` string. `[INSTALLDIR]app.exe` is an install-time
property reference, documented since msis-2.x and used by a large share of real packages;
`a[Foo]b` may be a literal or may be a deliberate mid-string reference such as
`Build [ProductVersion]`. Only the author knows which, and msis-2.x (the reference
implementation) also wrote the value straight into the column
(`msi-simplified/WxsItem/RegistryKey.cs`). Escaping only *mid-string* brackets would make the
meaning of `[` depend on its position, and would break the deliberate mid-string form
silently — the same class of harm in the other direction.

So the contract is: a non-preserved string value is Formatted, and a literal bracket is the
author's job (`[\[]`, spelled `[\\[]` in a `.reg` file). A preserved value is literal, and the
escape must **not** be used there, because it would land verbatim. The build warns about a
non-preserved string that contains an unescaped `[...]` or a `[~]`, and about nothing else: a
value that starts with `[` is the documented reference form, and a warning that fires on it
would train people to ignore the class.

Documented for users in `docs/tutorial.md`, under *Brackets: literal or formatted?*, together
with the `.reg` spelling trap (copying the MSI form `[\[]` into a `.reg` file yields `[[]`,
because the parser reads `\[` as an escaped `[`). Emission is pinned by
`TestFormattedContractValuesAreWrittenVerbatim` and
`TestFormattedContractPreservedValuesBypassFormatting`; the warning's coverage by
`TestWarnFormattedRegistryValues` and `TestWarnFormattedIgnoresEscapedBrackets`, all in
`internal/registry/registry_test.go`.

Related but separate: msis variables (`{{VAR}}`) are not expanded in `.reg` strings at build
time either, where msis-2.x expanded them. That is a parity gap, not a decision, and is
tracked as [#46](https://github.com/gersonkurz/msis/issues/46).

**What would reopen this:** an authoring switch that declares a value literal — an attribute
on `<registry>`, or a per-value marker — so that msis could escape on the author's say-so
rather than guess; or evidence that no production package uses a mid-string reference
deliberately.

## D5 — Prerequisite downloads are pinned to a version-specific URL and SHA-256, not fetched "latest"

**Settled in:** [#30](https://github.com/gersonkurz/msis/issues/30), 2026-09-22, product owner's
decision between three options put to them.
**Implemented by:** `internal/prereqcache/cache.go` — `download.visualstudio.microsoft.com/download/pr/`, `if err := verifyHash(destPath, urlInfo.SHA256); err == nil {`, `verifyHash(tempPath, urlInfo.SHA256)`

Until #30, msis fetched Microsoft's VC++ and .NET Framework redistributables, chained them into
customer bundles, and never checked what it got: no entry in `DownloadURLs` carried a digest,
and `EnsurePrerequisite` returned a cached file before the verification branch was reached at
all. The cache lives in `%LOCALAPPDATA%`, writable by the user and by anything running as them.

Three ways to get a trusted digest were weighed:

1. **Pin a version-specific URL and its SHA-256 in msis; verify on download and on every reuse.**
   Chosen.
2. **Keep the mutable `aka.ms` links and verify the Authenticode signature** (WinVerifyTrust,
   signer Microsoft Corporation). New releases would flow automatically. Windows-only; a
   substituted file that is *also* Microsoft-signed would pass; and two machines building the
   same msis release could chain different bytes.
3. **Both.** The widest net and the most code.

Why pinning: the `aka.ms` and `fwlink` URLs are mutable by design — the same URL serves a new
redistributable when one ships — so a digest recorded against one disagrees with it sooner or
later, and "verify against a pinned hash" and "follow latest" cannot both hold. Pinning makes the
prerequisite bytes a function of the msis release: reproducible across machines, and exactly what
the SBOM (#34) records as the payload's digest. It needs no Windows API, so it verifies on every
platform msis builds on. The cost is staleness: a newer redistributable reaches bundles through
an msis release that re-pins, or through `<requires source=...>`, which remains unverified because
msis has no digest for a file the author supplied.

Where the digests came from, since Microsoft publishes no digest list for these files: each of
the eight was downloaded over TLS from the pinned URL on 2026-09-22, hashed, and its Authenticode
signature checked (`Get-AuthenticodeSignature`: Valid, `CN=Microsoft Corporation`). The Visual
Studio CDN URLs carry the file's SHA-256 in their path; every pinned digest equals that segment,
and `TestEveryDownloadIsPinned` requires it to. So the pin is Microsoft's statement of the
digest, confirmed against the bytes and their signature — not a third party's attestation.

The same probe found that the `fwlink`s for .NET 4.8.1 and 4.7.2 resolved to the 1.4 MB **web**
installers while the cache named the files after the offline ones, so a bundle carried an
installer that needs the network at install time. Pinning the offline installers' direct URLs
fixed that as a side effect; `TestPinnedDownloadNamesMatchTheChainSources` keeps the cache's
names and the chain's in step.

Mismatch policy: a cached file that no longer matches is discarded and downloaded again, once; a
download that does not match is deleted while it still has its temporary name and the build is
refused with both digests. The two paths are executed by `TestTamperedCacheEntryIsReplaced`,
`TestTamperedCacheAndBadDownloadRefuseTheBuild` and `TestCorruptDownloadNeverLandsInTheCache`.
The temporary file is created exclusively, per download (`os.CreateTemp`), because a fixed
`<name>.download` path let two concurrent builds share it, and the one that had verified its
own bytes could publish the other's unchecked ones — found in review, executed by
`TestConcurrentDownloadsUseSeparateTemporaryFiles`.

**What would reopen this:** Microsoft publishing a signed digest manifest msis could fetch; a
requirement to follow the latest redistributable automatically (then option 2 or 3, with the
reproducibility cost stated); or a `sha256=` attribute on `<requires source=...>` so that a
supplied file can be verified too.
