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
escape must **not** be used there, because it would land verbatim. Both halves were installed
and observed in `todo-testme.md` T8 (#48): `a[\[]b` directly → `a[b`, preserved → `a[\[]b`. The build warns about a
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

Related but separate: msis variables (`{{VAR}}`) ARE expanded in `.reg` string values at build
time, as in msis-2.x — restored under [#46](https://github.com/gersonkurz/msis/issues/46), see
D8. That is the build-time half; this entry is the install-time half, and the two combine
(`[INSTALLDIR]{{PRODUCT_NAME}}.exe`).

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
msis has no digest for a file the author supplied. Re-pinning is a routine, not a rediscovery
(#49): each pin carries the mutable `Alias` it was resolved from, `just repin-check` resolves
every alias and exits non-zero on drift — and gates `just release` and `just release-all`, by
the product owner's decision of 2026-09-23, so a release cannot ship a stale pin by oversight
at the price of needing the network — and
`just repin` gathers the same evidence the original pins were taken with and prints the
replacement entry — `tools/repin`, documented in `docs/prerequisites.md` under *Re-pinning*.

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

The one unverified path this left — a file the script supplies with `source=` — closed under
[#50](https://github.com/gersonkurz/msis/issues/50): an optional `sha256=` on `<requires>` and
`<prerequisite>` has the file hashed (the copy WiX will bind, located through the build's bind
paths) and a mismatch refuses the build in the same words a pinned download uses; a supplied
source without one is chained as before, with a build warning, and the SBOM records which of
the three cases each prerequisite was (`msis:prerequisite.verification`).

**What would reopen this:** Microsoft publishing a signed digest manifest msis could fetch; or a
requirement to follow the latest redistributable automatically (then option 2 or 3, with the
reproducibility cost stated).

## D6 — A relative `BUILD_TARGET` resolves against the process working directory, once

**Settled in:** [#41](https://github.com/gersonkurz/msis/issues/41), 2026-09-22, product owner's
decision between the two bases.
**Implemented by:** `internal/wix/builder.go` — `func absPath(`, `checkOutputWritable(b.OutputFile)`, `outputArgs(b.OutputFile)`

`BUILD_TARGET` is a name pattern (#28) whose directory and stem every artifact shares. When it is
relative, something has to say relative to WHAT — and before #41 the code said two different
things. The `.wxs` was written and `wix build -o` was given the value resolved against the
process working directory; the pre-build overwrite check resolved the same value against the
`.msis` directory (MSI) or the `.wxs` directory (bundle), and then deleted what it had resolved.
For a bundle target `dist\setup.exe` whose `.wxs` sits at `dist\setup-bundle.wxs`, that check
removed `dist\dist\setup.exe` while the build wrote `dist\setup.exe` — a file the build was never
going to touch, gone, and the actual stale output left in place to be silently overwritten.

Two bases were possible, and the choice is user-visible because it decides where artifacts land
for anyone who runs msis from a directory other than the script's:

1. **The process working directory** — where the artifacts have effectively always landed in
   msis 3, what the docs promised ("a `BUILD_TARGET` still lands where it always did", written for
   #27), and what the review finding that became #41 asked to retain. Chosen.
2. **The `.msis` directory** — msis-2.x parity: `BuildContext` called
   `Directory.SetCurrentDirectory(WorkingDirectory)` before building, so a relative target
   landed beside the script there. It is also what the no-`BUILD_TARGET` default does. Rejected
   as a behaviour change for existing msis 3 users, with no defect it fixes that option 1 does
   not; the parity gap is documented instead (`docs/Bundle.md`).

The implementation is one resolution, at construction: `Builder.OutputFile` and
`BundleBuilder.OutputFile` are absolute, and the overwrite check, the `-o` handed to `wix`, the
`.wixpdb` cleanup and the "Built:" line main prints all read that one value. There is no second
place that resolves the path, so there is nothing to drift.
`TestOverwriteCheckRemovesOnlyTheFileTheBuildWrites` executes the reviewer's example with a
sentinel file where the old code deleted, and asserts the `-o` argument equals the path checked.

`just release` and `just release-all` run msis from `bootstrap/`, where the two bases coincide,
so the release recipes were never affected.

**What would reopen this:** a decision that msis 3 should match msis-2.x's directory change
(then also the `.wxs` moves beside the script, and the docs and this entry are rewritten), or an
explicit `/OUTDIR`-style flag that makes the base a stated choice rather than an implicit one.

## D7 — The `BUILD_TARGET` directory is created if it does not exist

**Settled in:** [#42](https://github.com/gersonkurz/msis/issues/42), 2026-09-22, by the standing
rule (AGENTS.md: prefer parity with msis-2.x unless requirements explicitly change).
**Implemented by:** `cmd/msis/main.go` — `func writeWxs(`, `os.MkdirAll(dir, 0o755)`

The issue offered two ways out of the raw OS error a missing target directory produced
(`open nodir\probe.wxs: The system cannot find the path specified`): create the directory, or
refuse with a message naming it. msis-2.x created it — `BuildContext.CreateReleaseFolder` split
`BUILD_TARGET` into folder and file pattern and called `Directory.CreateDirectory` on the folder
— and nothing since has asked for that to change, so parity decides it. Creating a directory the
user named in their own script is not a surprising side effect; failing a release build on a
fresh checkout because `dist\` was not there is.

One helper, `writeWxs`, does it for all three `.wxs` writes (MSI, auto-bundle, explicit bundle),
because the `.wxs` is the first artifact written and shares its directory with the `.msi` and the
`.exe` (`BUILD_TARGET` is a name pattern, #28). A directory that cannot be created fails with
"creating output directory <dir>", so the reader is pointed at the directory rather than at a
file inside it. Executed by `TestBuildTargetDirectoryIsCreatedForAnMSI`,
`TestBuildTargetDirectoryIsCreatedForABundle` (both through `processFile`, two levels deep) and
the `writeWxs` unit tests, including the cannot-create case.

`just release-all` never hit the defect because `clean-bootstrap` creates `bootstrap/dist` first;
that recipe step is now redundant but harmless.

**What would reopen this:** a requirement that msis never write outside directories that already
exist (a locked-down CI layout, say) — then the diagnostic option, behind a flag.

## D8 — `{{VAR}}` expands in `.reg` string values; an undefined name stays literal and warns

**Settled in:** [#46](https://github.com/gersonkurz/msis/issues/46), 2026-09-22, product owner's
decision between expanding (msis-2.x parity), not expanding, and expanding in names and key
paths too.
**Implemented by:** `internal/registry/registry.go` — `func (p *Processor) expand(`, `p.Variables.ResolveChecked(value)`

msis-2.x ran every REG_SZ value of a `.reg` file through Handlebars
(`msi-simplified/WxsItem/RegistryKey.cs`, both the plain and the preserved path), so
`"Version"="{{PRODUCT_VERSION}}"` landed as the version. msis 3 wrote it literally, found while
probing for #38, and the tutorial of the time documented a `$$VAR$$` form that no version ever
honoured in a string value. The product owner chose to restore the expansion.

Scope, exactly msis-2.x's: **REG_SZ values only**. Value names, key paths, `REG_EXPAND_SZ`,
DWORD, binary and multi-string values are not touched — msis-2.x did not expand them either,
and widening the scope was the option rejected ("no known user asking for it, more ways for a
literal `{{` to be misread"). A value without `{{` is returned as it came from the parser.

Two deliberate departures from msis-2.x, both in the direction of not losing data silently:

- **An undefined variable is not rendered as empty.** Handlebars renders an unknown name as
  `""`, and so did msis-2.x — a typo in `{{PRODUCT_VERSOIN}}` installed an empty registry value
  with nothing said. `variables.ResolveChecked` (`internal/variables/references.go`) reports
  every undefined name the render **actually reaches**, found the way the dependency resolver
  finds dependencies: the parsed template supplies the candidates (paths that can be variables —
  not helper names like `if`, not syntax like `else`), each undefined candidate gets a sentinel,
  and the render says which were reached — under the key the evaluator looks up, so a
  bracketed literal segment `{{[MY VAR]}}` is checked as `MY VAR`. So every reference form the
  engine accepts is covered (`{{~X~}}`, hyphenated names, `[bracketed]` segments), a reference
  in an untaken branch is not reported, and a name
  used as a **condition** must be defined too — `{{#if FOO}}` with FOO undefined is reported,
  not treated as false; a variable meant to be optional is defined as empty. Any undefined
  reference leaves the whole value **as authored**, with a build warning naming the value and
  the references. (The first version of this guard was a regexp; review found it rejected
  `{{else}}` and missed `{{~X~}}` — the reason the check is now the engine's own evaluation.)
- **Text the engine cannot parse** — `{{VAR, DEFAULT}}` (the form #14 met in item values), a
  stray `{{` — is likewise written as authored with a warning, the policy `resolveOrWarn`
  already applies to item values.

Expansion happens where the string value is read (`convertValue`), so everything downstream
sees the expanded text: the preserved default (`PS_RV_n`), and the Formatted-field warning of
D4, which must judge what will actually land in the Registry table. Build-time `{{VAR}}` and
install-time `[PROPERTY]` are different mechanisms and combine in one value.

Executed by `internal/registry/variables_test.go` and, end to end, by the #38 probe `.reg` re-run
against the built binary: `{{PRODUCT_VERSION}}` → `1.2.3`, `$$PRODUCT_VERSION$$` unchanged.

**What would reopen this:** a user needing `{{VAR}}` in a value name or key path (then the
third option, with the same undefined-stays-literal rule), or evidence that a production `.reg`
carries a literal `{{` that the warning does not make obvious enough.

## D9 — The race detector is optional; a concurrency claim is settled by a coordinated test

**Settled in:** [#51](https://github.com/gersonkurz/msis/issues/51), 2026-09-23, product owner's
decision between three options.
**Implemented by:** `justfile` — `test-race:`, `mingw-w64`

`go test -race` needs cgo, and cgo on Windows needs a gcc-compatible toolchain — mingw-w64; the
Visual Studio compiler that builds the hook DLL will not do, which the issue got wrong when it
suggested the developer shell would suffice. This Go installation has `CGO_ENABLED=0` and no
gcc anywhere on the machine (checked 2026-09-23: nothing on PATH, no MSYS2, MinGW or TDM
install; `CGO_ENABLED=1 go test -race` fails with `C compiler "gcc" not found`).

Three options were weighed: make mingw-w64 a required developer toolchain and add `-race` to
`just check` and Verify; record that the detector is out of reach and add nothing; or the middle
— chosen — an optional `just test-race` that runs the root-module suite under `-race` when gcc
is on PATH and otherwise stops, non-zero, saying what to install, plus this entry (the nested
`tools/sbom-index` module is not included; it is pure Go and `test-tools` covers it). A second toolchain
on every contributor's machine and CI runner, for a project whose concurrency surface is a
handful of goroutines in `internal/prereqcache` and the tools, was judged more cost than
insurance; adding nothing would have left the next reviewer to rediscover the limitation.

What stands in for the detector: **a coordinated test** — one that makes the interleaving happen
by construction (a channel barrier, a server that holds requests) rather than hoping the
scheduler produces it. `TestConcurrentDownloadsUseSeparateTemporaryFiles` (#30) is the model. A
review finding "needs `-race`" is answered by such a test, or by running `just test-race` on a
machine that has gcc; it is not answered by a timing-based test, and it is not a deferred blocker.

**What would reopen this:** a data race that a coordinated test could not have caught and the
detector would have; or a CI runner with gcc, at which point `test-race` can join the gate there
without being required of developer machines.
