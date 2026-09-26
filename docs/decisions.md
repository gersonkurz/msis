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

## D10 — BSI TR-03183-2 is read from the text, v2.1.0; SHA-512 goes in `hashes`, and in a distribution reference only where one exists

**Settled in:** [#63](https://github.com/gersonkurz/msis/issues/63), 2026-09-24. Product owner's
decision between three options for the SHA-512 placement.
**Implemented by:** `internal/sbom/bundle.go` — `where the engine downloads this payload from`
**Implemented by:** `internal/sbom/conformance/conformance.go` — `has a SHA-256 but no SHA-512`, `bsiValues`

**The yardstick is BSI's own text, not a checker's reading of it.** The BSI landing page names
**v2.1.0 (2025-08-20)** as current. The file published as `BSI-TR-03183-2_v2_2_0.pdf` contains
the 1.1 text: its document history ends at 1.1. sbomqs's `--bsi-v2` profile checks 2.0.0, so
its score is a second opinion on an older version, not the requirement. §7 says only the most
recent version counts (plus the immediately preceding one for six months).

**SHA-512.** §5.2.2 requires the deployable component's hash as SHA-512. Table 9 maps it to
`externalReferences[{type: "distribution", hashes: [SHA-512]}]`, but CycloneDX requires a `url`
on every external reference, and a file inside an MSI has no distribution location. Three options
were weighed:
- emit a distribution reference for every file, pointing at the containing artifact;
- put SHA-512 only in `hashes` and ignore BSI's placement;
- **(chosen)** both, honestly: SHA-512 goes beside SHA-256 in `hashes` wherever msis held the
  bytes (payload files, Binary-table streams, bundle payloads, the artifact itself). A
  `distribution` reference, carrying that SHA-512, is added only where a real location exists:
  a payload the Burn engine downloads.

Pointing a "distribution" at the MSI would name a place the file is not distributed from. The
cost of this choice: a checker that looks for the SHA-512 only in the distribution reference
still scores the in-MSI files 0, and this entry is the answer to that score.

**The other §5.2.2 file facts** — `bsi:component:filename`, `bsi:component:executable`,
`bsi:component:archive`, `bsi:component:structured` — are read from the bytes (`internal/filekind`).
A property that cannot be proven is omitted, not guessed; §3.2.1 allows omitting what is not
available. A PE with unexplained appended data may be a self-extracting archive, so it gets no
archive or structured property.

**What would reopen this:** a newer BSI version that moves the hash, or that defines a
distribution reference without a location; or evidence that consumers the customers actually
use reject SHA-512 in `hashes`.

## D11 — An unknown dependency graph is stated in compositions, not as an empty `dependsOn`; sbomqs's "orphans" are not a defect

**Settled in:** [#63](https://github.com/gersonkurz/msis/issues/63), 2026-09-24, from reading
CycloneDX 1.6 and BSI TR-03183-2 v2.1.0 against sbomqs's report.
**Implemented by:** `internal/sbom/conformance/conformance.go` — `nothing in the document says what %q depends on, or that it is unknown`

sbomqs reported "13 orphan components" and scored dependency-graph completeness 5/10 for msis's
MSI documents. Traced on the 3.0.5 release documents: **every component is reachable from the
primary component**. What the components have in common is that they have no `dependencies`
entry of their own: payload files, Binary-table streams, supplied libraries. That is deliberate
and correct. CycloneDX 1.6 says components that do not have their own dependencies MUST be
declared as empty elements, and that components not represented in the graph MAY have unknown
dependencies. For an opaque file msis does not know the dependencies, so an empty `dependsOn`
would assert "depends on nothing". #29 settled that as the error to avoid, with the three
knowledge states. The graph instead lists these components under an `unknown` composition,
which is BSI §5.2.2's "the completeness of this enumeration MUST be clearly indicated".

**What would reopen this:** a BSI or CycloneDX revision that requires every component to have a
graph entry even when its dependencies are unknown, with a defined way to mark that entry unknown.

## D12 — msis's own components carry BSI's licence pair as two `license.id` entries, not two expressions

**Settled in:** [#63](https://github.com/gersonkurz/msis/issues/63), 2026-09-24, from reading BSI
TR-03183-2 v2.1.0 §3.2.8 and Table 9/11 against the vendored CycloneDX 1.6 schema.
**Implemented by:** `tools/sbom/licences.go` — `func licensed(id string) []licenseChoice`

BSI distinguishes the *original* licence (assigned by the component's creator) from the
*distribution* licence (under which the licensee may use it). It maps the first to a CycloneDX
licence with `acknowledgement: declared`, and the second to one with `acknowledgement: concluded`,
each written as an `expression`. CycloneDX 1.6's `licenseChoice` holds **either** a list of
`license` objects **or** a tuple of exactly one `expression`, so two expressions on one
component are invalid. msis therefore uses the list form: `{license: {id, acknowledgement:
declared}}` and `{license: {id, acknowledgement: concluded}}`. For a single SPDX id, `license.id`
states what an expression would.

Both entries have the same id for every component msis describes today. Nothing downstream
chose among licences, which is the case BSI names where they differ (Qt's GPL or commercial
choice). A licence that genuinely needs an SPDX expression (`MIT OR Apache-2.0`, `WITH` an
exception) cannot be expressed this way next to a second entry. The reviewed-text classifier
refuses dual texts today, so such a component stops the release rather than being flattened.

**What would reopen this:** a CycloneDX version that allows several expressions, each with its
acknowledgement; or a dependency whose licence is an expression, at which point the list form
no longer suffices and one of the two statements has to go.

## D13 — A declared version that contradicts the file's version resource stops the build

**Settled in:** [#64](https://github.com/gersonkurz/msis/issues/64), 2026-09-24, product owner's
decision between refusing the build and recording both values with a warning.
**Implemented by:** `internal/sbom/declare.go` — `declares version %s, but the package records version %s`
**Implemented by:** `internal/sbom/merge.go` — `func sameVersion(a, b string) bool`

`<component version=>` is the author's statement about a file. Where the package also records a
version for that file (a PE's version resource, which WiX writes into the File table), the two
are compared. Only trailing `.0` groups may differ, so `2.3.1` is `2.3.1.0`. A mismatch stops
the build and names both values. Publishing either one would be a claim msis knows is doubtful,
and keeping both with a flag would ship a document that contradicts itself. This is the stance
#36 already takes for a supplied document whose SHA-256 does not match the file.

The check applies to `<component>` only. A supplied CycloneDX document (`<sbom>`) describes what
a file contains, and its subject's version is the component's, which need not be the file's
own (a Go module `v1.2.3` inside a binary versioned `1.0.0.0`). Since D16 the declared facts are
written onto the file's own component; the check is the same, and is applied where they are.

**What would reopen this:** a legitimate case where a file's version resource is known to be
wrong and the declaration right, which would need an explicit override rather than a silent
preference for either.

## D14 — An SBOM's data licence is its creator's grant: none by default, CC0-1.0 for msis's own

**Settled in:** [#62](https://github.com/gersonkurz/msis/issues/62), 2026-09-24, product owner's
decision. First "not worth a field", then reversed to CC0-1.0 for msis's own documents with a
README section explaining why. The scope (a variable, not a default) was chosen over "CC0 on
every document".
**Implemented by:** `internal/sbom/enrich.go` — `if l := rec.DataLicense; l != ""`
**Implemented by:** `tools/sbom/main.go` — `var dataLicense = []dataLicenseChoice{{Expression: "CC0-1.0"}}`

CycloneDX's `metadata.licenses` is optional, and neither BSI TR-03183-2 nor NTIA asks for it; it
is an SPDX convention (every SPDX document is `CC0-1.0`). msis's own SBOMs carry CC0-1.0 all the
same. An SBOM exists to be passed on (to customers, auditors, scanners, SBOM indexes), and
public domain answers the redistribution question before anyone has to ask it. The README's
"SBOM" section states that reasoning for users.

msis also writes SBOMs for its users' products, and those documents are their creator's, not
msis's. So the licence is `SBOM_DATA_LICENSE`, an SPDX expression validated like `<component
license=>`, granted only when the `.msis` sets it. `bootstrap/setup.msis` and `setup-bundle.msis`
set `CC0-1.0`. The release-wide and component documents `tools/sbom` writes are msis's own and
state CC0-1.0 directly.

**What would reopen this:** a standard that requires a data licence, or a customer need for a
default. Either would still have to leave the grant to the document's creator.

## D15 — BSI's version fallback is the source file's modification date, and only the build has it

**Settled in:** [#63](https://github.com/gersonkurz/msis/issues/63), 2026-09-24, product owner's
decision ("build-time only", over leaving the field out).
**Implemented by:** `internal/sbom/enrich.go` — `c.Version = f.Modified.UTC().Format(time.RFC3339)`
**Implemented by:** `internal/sbom/declare.go` — `fallback := propertyValueOf(c.Properties, propBuildVersionFrom) != ""`

BSI TR-03183-2 v2.1.0 §5.2.2: a component with no version takes "the modification date of the
file expressed as date-time according to RFC 3339", from the file's metadata. The artifact does
carry a date: a cabinet stores each file's date as local time with no zone. Turning that into
an RFC 3339 instant would mean guessing the zone (and Windows' own conversion applies the
current daylight-saving bias, not the date's). So msis does not read it.

Under `/BUILD /SBOM` there is a better source. The build record hashes each payload's source file
where WiX resolves it, and records its last-write time from the file system, an exact instant.
Enrichment uses it, in UTC, only for a file that has no version of its own, only after the
SHA-256 check has shown the source is the packaged file, and marks it `msis:build.versionFrom`.
`/SBOM` on an artifact alone has no source, and leaves the version out.

The date is what BSI asks for, but it is not a meaningful version. It is when the source file
was written, which for a checked-out tree is the checkout time. That is why it is marked as a
fallback. A `<component version=>` declaration (D13) is not held to it: D13 compares with the
version the package records, and this one msis supplied.

**What would reopen this:** a BSI revision that drops the fallback, or one that accepts the
artifact's own date with an explicit unknown zone.

## D16 — `<component>` facts go onto the file's own component, not onto a supplied one

**Settled in:** [#65](https://github.com/gersonkurz/msis/issues/65), 2026-09-24, product owner's
decision, on evidence from re-scoring msis's own release SBOMs against BSI TR-03183-2 v2.1.0.
It reverses #64's first implementation.
**Implemented by:** `internal/sbom/declare.go` — `func applyDeclarations(doc *Document, declared []Declaration) error`, `propDeclaredBy`

#64 first turned each `<component>` into a generated supplied document and merged it by #36's
rules. The declared facts then sat on a **separate** component that the file depended on.
Declaring msis's own payload files that way made the SBOM *worse* by BSI's measure: the MSI
document went from 6.8 to 6.3, and components without a licence from 28 to 44. The file
components still carried no licence or creator, and every declaration added a half-empty
component with no filename, digest or file properties. The model is right for `<sbom>`, which
describes what is **inside** a file. It is wrong for `<component>`, whose facts are about **the
file itself**.

So a declaration now writes onto the file's own component:
- name, version (only where the package records none, or where D15's date fallback stood in),
  creator (email, else URL), purl and cpe;
- the licence as BSI's pair, `declared` and `concluded`, or as one `concluded` expression when it
  is compound (D12's limit);
- `msis:declared.by` and `msis:declared.fields`, saying where each declared value came from.

No component is added. D13 still refuses a version that contradicts the recorded one, and one
file still has one description, whether a `<component>`, a folder `<component>` or an `<sbom>`.

**What would reopen this:** a consumer that needs the declaration as a separately addressable
component. That is what `<sbom>` is for.

## D17 — msis does not sign its SBOMs

**Settled in:** [#63](https://github.com/gersonkurz/msis/issues/63), 2026-09-24, product owner's
decision.
**Implemented by:** `docs/sbom.md` — `the integrity of a release's documents comes from the release itself (D17)`

BSI TR-03183-2 v2.1.0 §5.4 makes a signature on the SBOM optional, and CycloneDX 1.6 can carry one
inline (JSF). msis produces none, and sbomqs's optional signature field stays at 0.

The reasons:
- A signature is only as good as its key handling. Signing inside msis means msis holding,
  protecting and rotating a private key on every build machine. That is a feature of its own,
  and one a build tool gets wrong easily.
- The integrity a consumer needs is already available outside the document. The release that
  publishes it can be signed or checksummed, and the installer it describes can carry its
  publisher's Authenticode signature. The document names that installer by its digest.
- An inline signature fixes the document's bytes, and msis's documents are composed and
  re-composed: a supplied `<sbom>` is merged, a VEX sidecar is evaluated against the document.
  Signing belongs at the end of that chain, which is the publisher's step, not msis's.

**What would reopen this:** a regulation or customer that requires the signature inside the
document, or a signing service the build can call without msis holding a key.

## D18 — WiX's Binary-table streams are attributed by their bytes, and their licence is ScanCode's id for the WiX agreement

**Settled in:** [#67](https://github.com/gersonkurz/msis/issues/67), 2026-09-24. Product owner's
decisions: pin the package facts and check them against nuget.org; the ScanCode id.
**Implemented by:** `internal/sbom/enrich.go` — `func enrichExtensionFiles(doc *Document, rec *buildrecord.Record)`
**Implemented by:** `internal/wix/extensions.go` — `LicenseRef-scancode-os-maintenance-fee-eula`, `func ExtensionPayloads(dll string)`, `func ResolveExtensions(`

An MSI built with WixUI embeds WiX's bitmaps and icons (`WixUI_Bmp_Banner`, ...) and the Util
custom-action DLL (`Wix4UtilCA_X64`) as Binary-table streams. They had no creator, licence or
version, the largest honest gap left in msis's own SBOMs.

**By bytes, never by name.** A template can define a stream called `WixUI_Bmp_Banner` itself.
At `/BUILD`, msis reads the extension DLLs the build loaded - the `.wixlib` each embeds is a
zip - and attributes a stream only when its SHA-256 is that of a file in one of them. The
document then says which package, which version, which file: `msis:build.extension`.
- **The build and the attribution use the same file.** msis resolves each extension exactly as
  WiX's `ExtensionManager.Load` resolves a bare id (`wix.ResolveExtensions`), then hands
  `wix build -ext` the resolved DLL path, which WiX loads as it is. WiX's order:
  1. the build directory's `.wix` cache;
  2. the user's (`WIX_EXTENSIONS`, else the profile);
  3. the machine's under Common Files.

  In the first location that has the extension, WiX takes the latest version by `WixVersion`
  order. msis does the same, so the version stated is the one that built the installer.
  - WiX first tries the reference as a file in its working directory, so a file named like the
    id beside the `.wxs` is left to WiX.
  - A relative cache root, such as `WIX_EXTENSIONS=cache`, is resolved against WiX's working
    directory, not msis's.
  - A location msis cannot determine exactly ends the resolution, rather than being skipped.

  In every case msis cannot reproduce, including an extension found only in the wix tool's own
  folder, the extension is handed on by id and attributed nothing.
- A plain `/SBOM` has no build to consult, and attributes nothing.
- A stream still has no filename (BSI §3.2.1: what is not available is omitted). It is not a
  file on disk.
- A stream gets no purl. It is a part of the package, not the package.

**What the package declares, pinned.** The extension cache keeps only the DLL, not the
`.nuspec`. So the facts are pinned per package version in `internal/wix/extensions.go`:
authors, repository, licence file and that file's digest. `just wix-packages-check`, which
gates a release, compares every pin with nuget.org.
- WiX 6 and 7 packages declare no project URL, so the creator is reached through the
  repository they name, as msis names itself.
- A WiX version that is not pinned is not attributed.
- `TestTheDefaultWixVersionIsPinned` keeps the version msis provisions pinned.

**The licence.** Every WiX 6 and 7 extension package declares `<license type="file">OSMFEULA.txt</license>`:
the Open Source Maintenance Fee Agreement. Its own text says the source is MS-RL and the binary
release comes under the agreement. BSI TR-03183-2 v2.1.0 §6.1 names a licence by its SPDX id,
else by the ScanCode LicenseDB id, else by an own `LicenseRef`. The agreement has no SPDX id.
ScanCode has `LicenseRef-scancode-os-maintenance-fee-eula` and lists WiX's own `OSMFEULA.txt`
as a reference. WiX's text fills the template's placeholders (project, software, OSI licence),
which §6.1 says is not a modification.
- WiX 7.0.0's text also limits the fee to users with an annual gross revenue of US$10,000 or
  more, which neither ScanCode's text nor the WiX commit it cites has.
- The product owner chose ScanCode's id over an own `LicenseRef-msis-...`. It is the
  established identifier for this agreement, and the one ScanCode's own detector assigns.
- The two texts (6.x and 7.0.0) are pinned by digest, so a further change stops the release
  and this entry is read again.
- A `LicenseRef-` is not on CycloneDX's SPDX list, so it is one concluded expression, D12's
  limit: there is no declared/concluded pair.

**What would reopen this:** SPDX adding an id for the agreement; ScanCode adding one for WiX's
current text; or a WiX version whose package declares something else.

## D19 — `/SCAN` runs grype and applies msis's evaluated VEX itself; a finding never fails a run

**Settled in:** [#69](https://github.com/gersonkurz/msis/issues/69), 2026-09-24. Product owner's
decisions: grype first, grype's JSON kept verbatim, the release scan a report and never a gate.
**Implemented by:** `internal/scan/scan.go` — `func vexAnswers(d document, vexDoc []byte)`, `func Grype(doc string)`

The product owner's purpose for SBOMs is vulnerability scanning. msis drives a scanner; it does
not become one.

- **grype, found on PATH, never downloaded.** grype reads CycloneDX natively, and it reports each
  finding's component by the document's own `bom-ref` (`artifact.id`), so a finding joins its
  component exactly. osv-scanner can join later behind the same flag.
- **msis applies the VEX, not grype.** grype 0.119 does not read CycloneDX VEX. Passing msis's
  sidecar with `--vex` fails with "unable to detect document format". Even a scanner that read it
  could not know which statements still hold: that is what msis's evaluation (#37) decides, and
  it already moved every lapsed statement out of a suppressing state. So a finding is answered
  only by a statement from a sidecar evaluated against this very document (its subject's
  BOM-Link), by id or alias, for that component, in `vex.Suppresses`' states. grype's own report
  is left untouched: the answers are msis's reading of it, stated in the terminal.
- **Coverage is always stated.** The count of components without purl or CPE, and every BOM-Link
  not scanned in the same run. A clean scan of unidentifiable components is the misleading
  result this exists to prevent.
- **A finding never fails a run.** A newly published CVE would otherwise break a build that did
  not change. msis's own release scans as a report and releases without grype.
  - A `--fail-on` threshold in the user's grype configuration (or `GRYPE_FAIL_ON_SEVERITY`) makes
    grype exit 2 with its report. That is grype reporting findings, so the report is kept and
    the scan stands.
- **The report is verbatim and names the local grype database path.** msis's own release keeps
  its reports outside `dist/`.

**What would reopen this:** a grype that reads CycloneDX VEX and evaluates it with the same care;
a need for a CI gate on findings, which would be an opt-in threshold and never the default; a
second scanner.

## D20 — Two builds of one script are identical except for documented fields; the ProductCode is derived from the package's inputs

**Settled in:** [#66](https://github.com/gersonkurz/msis/issues/66), 2026-09-25. Product owner's
decisions: identical except documented fields, not byte-identical; a ProductCode derived from
the inputs, content included.
**Implemented by:** `cmd/msis/productcode.go` — `func productCode(wxs string`, `func referencedFiles(wxs string)`

The experiment recorded on #66: the same script built twice (WiX 7.0.0). The WXS msis generates
was byte-identical. The MSIs differed in exactly four fields across all tables, streams and the
summary information:
- the ProductCode, which the templates did not set;
- the PackageCode;
- the create and last-saved times.

With those four forced equal, two more remained: a FILETIME in the compound file's directory,
and the payload files' dates inside the cabinet, which WiX copies from the source files.

**What msis makes reproducible.** The ProductCode, since `Package/@ProductCode` is authorable.
Unless the script sets `PRODUCT_CODE`, msis renders the WXS with a placeholder and hashes
everything the package is built from:
- the UpgradeCode, version and platform;
- the msis and WiX versions, and the version of each WiX extension, resolved as the build
  resolves them;
- the WXS itself;
- the SHA-256 of every file the WXS references, found through the bind paths as WiX finds them:
  each `Source` and `SourceFile`, and the file-valued WixVariables (bitmaps, icons, licence
  text);
- the `-loc` file.

The GUID is marked name-based (version 8). Identical inputs give the same code, and any change
gives a new one, so a rebuild with a changed file at the same version still major-upgrades, as
before.

In these cases msis leaves the code to WiX (random, as before), and the build says why:
- a file the WXS references cannot be resolved;
- an extension msis cannot resolve;
- the WXS uses WiX's preprocessor (an `<?include?>`, a `<?define?>`, any `$(...)`), which can
  add inputs the hash never sees. The check reads the decoded document, as WiX does, so a
  character reference such as `&#36;(env.X)` counts too; WiX's escaped `$$` is a plain dollar and
  does not.

The placeholder attribute is then removed however the template spells it. A template that uses
`{{PRODUCT_CODE}}` anywhere else stops the build instead. A code that cannot see an input
must not stay the same when that input changes. That would put two different packages under
one ProductCode, and Windows Installer refuses the plain install of the second one.

**What stays different, documented, not fixed:**
- the PackageCode;
- the two summary times;
- the container FILETIME;
- the cabinet's file dates.

WiX 7 offers no attribute for the first three (`<SummaryInformation>` accepts only Codepage,
Comments, Description, Keywords and Manufacturer; the binder calls `CreateGuid()` and
`DateTime.Now`). The last two are below WiX. Making them equal means msis rewriting WiX's
output after the build, which the product owner chose not to do. Every payload file's bytes and
hash are identical across builds, so an SBOM verifies an installer's contents without the
installer's own hash.

**What would reopen this:** a WiX that lets a package author its PackageCode and dates; or a
requirement for byte-identical installers, which is the post-processing path #66 describes.

## D21 — `<remove-on-uninstall>` runs on a real uninstall only, not when an upgrade removes the previous version

**Settled in:** [#76](https://github.com/gersonkurz/msis/issues/76), 2026-09-25, product owner's
decision (fix, not document), on the T7 VM probe's evidence.
**Implemented by:** `internal/generator/context.go` — `const onRealUninstall = "NOT UPGRADINGPRODUCTCODE"`

The VM probe (`testscripts/t5t7`, todo-testme.md T7) installed 1.0.0 of a package with
`<remove-on-uninstall folder=... registry=...>`, seeded the application's data, and installed
1.0.1 over it. **The upgrade deleted the data**: the folder's files and the registry key. The
templates' `<MajorUpgrade>` removes the previous version completely before installing the new
one (`RemoveExistingProducts` after `InstallValidate`). The cleanups fired on any removal of
their component:
- `util:RemoveFolderEx On='uninstall'`;
- the standard `RemoveRegistryKey Action='removeOnUninstall'`.

So every update of every such product lost the data it names.

Both cleanups now carry `Condition="NOT UPGRADINGPRODUCTCODE"`, the guard msis's own destructive
hook actions already use:
- the folder through `util:RemoveFolderEx`'s `Condition`;
- the registry key through `util:RemoveRegistryKey On="uninstall"`, which, unlike the standard
  element, takes a condition.

Both exist in WiX 6 and 7. WiX's util custom actions evaluate the condition with
`MsiEvaluateCondition` in the running session. When an upgrade removes the old version,
`UPGRADINGPRODUCTCODE` is set in that session, so the cleanup stays off. A real uninstall runs it
as before. The components keep their keypaths, so their GUIDs do not change.

**The limit:** Windows Installer removes the old version with the old version's cached package.
So an upgrade from a version built by an earlier msis still runs that version's unconditional
cleanup. The protection holds for upgrades from the first fixed version on; the tutorial says
so. The harness's upgrade scenario judges the data's survival, and must pass on the VM.

**What would reopen this:** a need to clear the data on updates too, which would be an opt-in
per element, never the default; or a WiX change to how the util custom actions evaluate their
condition.

## D22 — A `<service>` sharing its executable with another feature is deprecated: msis warns, `/STRICT` refuses, msis 4 will refuse

**Settled in:** [#77](https://github.com/gersonkurz/msis/issues/77), 2026-09-25, product owner's
decision, on the #77 VM probe's evidence and the 3.0.3 regression QA. First settled as "refuse"
(7a65687), revised the same day to "deprecate" once the QA showed the teams' shipping scripts
use the layout.
**Implemented by:** `internal/generator/context.go` — `func (c *Context) checkServiceFileOwnership() error`, `func (c *Context) serviceFileConflict(`, `Strict bool`

Windows Installer registers a service from the key file of the component carrying its
`ServiceInstall`, so that component must own the executable, and one file can have only one
owning component. A `<service>` in feature B naming a file that feature A installs therefore
cannot have both properties people want from it: the executable installed with A, and the
service registered only with B.

msis 3.x (801875d) answered by installing the file a second time, in a component of B at the
same target. The VM probe (`testscripts/t77`) showed what that does. Removing B deleted the
executable A still needed. Removing A left a registered service pointing at a deleted binary.
msiexec reported success both times. msis-2.x used the same two components, guarded by
mutually exclusive `ADDLOCAL >< "feature"` component conditions. A condition that turns false
does not remove a component already installed, unless the component is transitive and is being
reinstalled. So by Windows Installer's documented rules, "install A, then add B, then remove B"
leaves both components installed and loses the file as above. That is reasoned from those
rules; the 2.x layout was not probed.

**Why it is not refused.** The 3.0.3 regression QA found the layout in the shipping scripts of
two products: five of ProAKT 3.6.0.73's six scripts with a service, and NG1 2.4.0. Refusing it would
stop builds that work today. The hazard is also narrower than it sounds. It needs a
maintenance-mode feature change on an installed product. A first install is unaffected, and so
is a major upgrade, which removes the old version completely first. Those products set
`ARPNOMODIFY`, which disables Change in Programs and Features and in the MSI's own maintenance
dialog. For them, only `msiexec REMOVE=`/`ADDLOCAL=` from a command line reaches it.

So msis builds the layout exactly as 3.0.5 did: the same components, checked against the
3.0.3-built ProAKT reference (their GUIDs follow D23 since 3.0.6, like every file component's). It prints a warning for each service it finds. The
warning names the hazard, says the layout is deprecated, that msis 4 will refuse it and that
`/STRICT` refuses it now, and proposes the two layouts that are sound:
- the `<service>` in A, registered whenever A is installed;
- B installing its own copy at a target of its own (`[INSTALLDIR]service\`) and naming that
  copy. The probe ran this second layout through every feature change on the VM, and it
  passed (2026-09-25, todo-testme.md T9).

The check covers every way a script reaches the layout: a bare or anchored `file-name`, and a
`<service>` written before the `<files>` that installs the same target. A bare `file-name` whose
file is installed elsewhere (say `[INSTALLDIR]bin\`) still gets its own copy at the INSTALLDIR
root; that copy shares no target, so it is left alone. With no features declared, the service
attaches to the file's component, since WiX's default feature holds both.

**What would reopen this:** msis 4, which refuses the layout outright; or a mechanism that
lets one component's service registration follow a feature other than the component's own.
Windows Installer has none today.

---

## D23 — A file component's identity is the product plus where it installs, not where it was built from

**Settled in:** [#81](https://github.com/gersonkurz/msis/issues/81), 2026-09-26, product owner's
decision.
**Implemented by:** `internal/generator/context.go` — `func (c *Context) resolveFileGUIDs()`, `func (c *Context) relativeSource(source string) string`
**Implemented by:** `cmd/msis/productcode.go` — `func hashLocationFree(h io.Writer, wxs string, sums map[string]string) error`

Until 3.0.5 a file component's GUID was the SHA-256 of its source path as the build saw it, and
its id was derived from the same path. For a script naming its sources by absolute path, that is
the build machine's folder. The 3.0.3/3.0.5 regression QA of NG1 found the consequence: its
reference MSI, built on CI from `D:\CI\ng1-2.4.0-banking\...`, and the same script built from a
Downloads folder differed in **all 10,565** component GUIDs and in nothing else of the payload.
Because the CI folder is named after the version, every NG1 release already got all-new GUIDs.
msis-2.x was no better: it gave every component a random GUID on every build.

Windows Installer's component rules want one GUID per resource for the product's lifetime. So a
file component's GUID is now the product's UpgradeCode plus its destination: the root key and
the path below it, case-folded, as in `{UPGRADE-CODE}/installdir\conf\fastcgi.conf`. The id is
the destination without the product. The install folder's own name (`INSTALLDIR`'s value) is not
part of either, so renaming it does not move the components. Non-file components were already
keyed on the UpgradeCode plus a name (`productScopedID`).
- **Two products** installing the same destination, each into its own folder, get different
  GUIDs. Sharing one would make Windows Installer refcount one component at two paths.
- **Several components at one destination** (feature-based overrides of one file, #79) cannot be
  told apart by the destination. Each also carries its source path relative to the script's
  folder, and every one of them does, so the GUIDs do not depend on the order the `<files>` are
  written in. A source on another drive than the script has no relative path and stays absolute.
- **The ProductCode** (D20) hashed the WXS, absolute `Source` paths included, so it moved with
  the folder too. The hash now replaces each file reference with the SHA-256 of the file it
  resolves to, and keeps its file name, since WiX installs a `<File>` without `Name` under its
  `Source`'s name. Which content sits where, under which name, is still an input; the folder
  the sources sit in is not.

**The switch changes every file component's GUID and id once.** That is harmless for upgrades:
every msis template uses WiX's default `MajorUpgrade` schedule, which removes the previous
version completely (`RemoveExistingProducts` after `InstallValidate`) before installing the new
one, so no component is shared between the two. It is the same thing NG1's CI has done on every
release. The VM probe `testscripts/t81` (todo-testme.md T81, 2026-09-26) installed a package built
with the old scheme, upgraded it to one built with D23 from another folder (no file-component
GUID in common), repaired and uninstalled it: every step passed, with one ARP entry throughout and
nothing left behind.

What changes visibly is the SBOM: a file's `bom-ref` carries its component GUID, so refs change
once, and a VEX document written against a 3.0.5 SBOM's refs needs its refs updated (msis flags
a statement whose ref the build does not contain). From here on they stay stable across releases
and build folders, which is what the ref was supposed to provide.

Checked by `TestFileGUIDIsProductAndDestination`, which pins the values,
`TestFileGUIDsDoNotDependOnTheBuildFolder`, `TestFileGUIDsAreScopedToTheProduct`,
`TestSharedDestinationGetsDistinctStableGUIDs`, `TestTheProductCodeIgnoresWhereSourcesSit`, and,
with the real wix, `TestTheBuildFolderDoesNotReachComponentIdentity`. That last one builds one
script with absolute sources from two folders and compares the MSIs' components and ProductCode.

**What would reopen this:** patches or minor upgrades, which need component identity to hold
across a change of destination too; or a template moving `RemoveExistingProducts` late. Both
would need the component rules checked release against release, which msis does not do.

---

## D24 — Two `<files>` installing one target: allowed in one feature, deprecated across features

**Settled in:** [#79](https://github.com/gersonkurz/msis/issues/79), 2026-09-26, product owner's
decision ("split by shape"), on the T79 VM probe's evidence.
**Implemented by:** `internal/generator/context.go` — `func (c *Context) checkSharedFileTargets() error`

Each `<files>` makes its own component, as it did in msis-2.x. So two `<files>` installing
different sources to one target make two components own one file. The build accepts this: the
second copy gets a generated ShortName, and since D23 each component's GUID carries its source.
Field scripts produce it in two shapes, and the VM probe `testscripts/t79` (todo-testme.md T79,
2026-09-26) showed they behave differently.
- **One feature.** ProAKT 3.6.0.73's `setup-ngbt.msis` installs `Files_Core`, then `Files_NG`,
  to `INSTALLDIR`. 32 paths under `CONFIG\CURRENCY\` get two owners, two of them with different
  content: an intended override. The feature installs and removes both components together,
  so nothing is lost: on the VM uninstall removed the file, and it was present after install and
  after a repair. **Allowed, no warning**, documented in the tutorial as the override idiom.
  Which copy ends up on disk is Windows Installer's file-replacement rules' decision, not msis's.
  On the VM, with unversioned text, it was the copy written last, after install and after a
  repair of the deleted file. That is what was measured; msis does not promise it for versioned
  files or for a repair over an existing, modified file.
- **Different features.** The issue's repro: Standard installs `a\config.json`, Variant (off by
  default) `b\config.json`, both to `[INSTALLDIR]`. On the VM, removing Variant from an install
  with both deleted the file although Standard was still installed, and so did removing Standard
  with Variant installed. msiexec reported success each time, the #77 failure for plain files.
  While both were installed the Variant copy was on disk. **Deprecated**, with D22's reasoning:
  scripts in the field build it, so msis builds it as before, warns, `/STRICT` refuses it, and
  msis 4 will. The warning names the file, every source grouped by feature, and three fixes:
  - a target of its own for each feature's copy;
  - the copies in one feature;
  - leaving the file out, when it does not belong in the package: an `<exclude folder="...">`
    line for each source that comes from a directory walk, and removing the `<files>` for each
    source a `<files>` names directly, since `<exclude>` applies only to a walk.

The warning found two field scripts before it shipped. The Poste Italiane 4.2.0.90 and
pro2127 scripts install `desktop.ini`, which Explorer writes into customised folders, from four
source folders into `[INSTALLDIR]` and `[INSTALLDIR]Python`. The owners are the main feature and
"Debug Symbols", so removing Debug Symbols deletes the main feature's copy. The file is harmless,
and so is its loss, but it does not belong in the package, which is the third fix. None of the
other scripts checked warns: ProAKT 3.6.0.x, Poste Italiane 4.2.0.78/4.2.0.82 and chimera's
buildable scripts. NG1 was not checked, because its payload lives on the CI machine.

A component carrying a service is left to `checkServiceFileOwnership` (#77, D22), which reports
that layout with its own advice, so one layout never gets two warnings.

**What would reopen this:** msis 4, which refuses the cross-feature shape; or a mechanism that
lets one component follow either of two features. Windows Installer has none.
