# `fixture.exe` — the bundle fixture

A real Burn bundle, built to be **read, never run**. It exists because `internal/burnread`
parses a format with no self-describing framing: everything after the engine stub is opaque
appended data, located only by the offsets in the `.wixburn` PE section. A fixture is the only
way to test that against something WiX actually produced.

It carries one of each shape the reader has to distinguish:

| Shape | In the fixture | Why it needs a purpose-built bundle |
|---|---|---|
| **Bootstrapper payloads** | `fakeba.exe`, `extra.txt`, plus the two `Bootstrapper*Data.xml` files WiX adds | They run the install and are never installed. WiX contributes two the `.wxs` never mentions — the case for reading the artifact rather than the script. |
| **A carried chained installer** | `Embedded` → `fixture.msi` | The bytes are in the file, so msis extracts and hashes them. |
| **A supplementary payload** | `Embedded` → `sidecar.txt` | A chain package may carry files beside its installer; they are payload too, with a different role. |
| **A payload that is NOT carried** | `External` → `external.exe` (`Compressed="no"`) | The engine expects it beside the installer, so there are no bytes here to hash. This is the one case where a component legitimately has no SHA-256, and the release bundle has no example of it. |
| **A payload no package references, carried** | `layout.txt`, via the `LooseFiles` payload group | Burn writes every non-UX payload at bundle level, including ones outside the chain; WiX marks this one `LayoutOnly`. Walking only the chain's `PayloadRef`s dropped it silently — a file that ships inside the bundle and appears in no inventory. |
| **A payload no package references, NOT carried** | `beside.txt` (`Compressed="no"` in the same group) | The two axes are independent: outside the chain *and* not in the file. It is the case that proves the CLI's no-digest warning covers bundle-level payloads and not only a chain package's. |

The chained `fixture.msi` is `internal/msiread`'s fixture, which makes the BOM-Link test real:
the bundle's document links to a document written for that same MSI, and only because the
digests agree.

## Two economies, and why they are invisible to the reader

**The bootstrapper application is a text file.** A real one (`wixstdba.exe`) is 444 kB. Burn
packages whatever it is given as the BA, and nothing in the reader cares that it is not an
executable.

**The engine's code sections are zeroed after the build** by `shrink.go`. `.text`, `.rdata`,
`.rsrc` and `.reloc` are 787 kB of the 802 kB file and the reader never looks at them — it reads
the PE headers, the `.wixburn` section, and the cabinets appended behind it. Zeroing leaves
every offset exactly where it was, costs almost nothing in git (~12 kB packed), and makes the
file plainly non-runnable, which is the right property for something that exists to be parsed.

Committing the built bundle rather than building it during the test is what lets these tests run
in a clean checkout with no WiX toolchain, as for `internal/msiread/testdata/fixture.msi`.
`*.exe` and `*.wxs` are gitignored repo-wide; `.gitignore` carries a narrow negation for this
directory.

## The other fixtures (#43)

Three shapes the reader supports could not be exercised against `fixture.exe`, because WiX
never produces them from that authoring. They were covered by synthetic tests — a header
patched by hand, a manifest driven through `readChain` with a stand-in container opener — which
prove the arithmetic but not that WiX writes what the arithmetic assumes. These fixtures are
the executed counterparts. The synthetic tests remain; each pair says the same thing two ways.

| Fixture | Shape | Test |
|---|---|---|
| **`unsigned.exe`** | `fixture.wxs` built by WiX 6, exactly as `wix build` left it. The twin of `signed.exe`: same build, same bundle code, same payload digests. | `TestAGenuinelySignedBundleReadsToTheSameInventory` |
| **`signed.exe`** | `unsigned.exe` **genuinely signed** the way WiX supports it — `wix burn detach`, signtool on the engine, `wix burn reattach`, signtool on the whole file — with a self-signed certificate created for the purpose and discarded (`sign-fixture.ps1`). After the reattach the engine's signature sits between the bootstrapper's container and the attached containers, and the `.wixburn` header's `OriginalSignature*` fields are the only thing that says so. The reader has to find the containers where they moved to. | `TestAGenuinelySignedBundleIsSigned`, `TestAGenuinelySignedBundleReadsToTheSameInventory` |
| **`shapes.exe`** | `shapes.wxs`: **four containers** (the bootstrapper's, WiX's default attached one, and two explicit attached ones, `Second` and `Third`), so the offset arithmetic for index ≥ 2 runs against a real header; a **detached container** `Far`, written as `far.cab` beside the bundle at build time and deliberately **not committed**, whose package must be reported not carried, by container name and `DownloadUrl`, without the file being needed; and a **remote payload** `Remote`, authored with `wix burn remotepayload` from `remote.exe` — no `SourceFile`, only a URL, a size and a SHA-512 — which must come back not carried, with the URL and the recorded digest and no SHA-256 of its own. | `TestFourContainersAreReadAtTheirRecordedOffsets`, `TestADetachedContainerPayloadIsReportedNotCarried`, `TestARemotePayloadIsReportedWithItsURLAndRecordedDigest` |

`second.exe`, `third.exe`, `third.txt`, `far.exe` and `remote.exe` are the payload sources —
text files with an `.exe` name where the authoring wants an executable, like `fakeba.exe`. The
carried ones are what the tests hash the extracted bytes against; `remote.exe` is what the
recorded SHA-512 is checked against; `far.exe` is inside `far.cab` and is committed only so the
fixture can be rebuilt.

The signature on `signed.exe` no longer verifies: `shrink.go` zeroes the engine's code sections
after signing, and the certificate is gone anyway. That is fine — the reader checks that a
certificate table is present and that the reattach record is there, never who signed or whether
the signature still holds. Nothing here is run.

## Rebuilding

Needs WiX 6 or 7 (`msis /SETUP-WIX`); `fixture.exe` was built with WiX 7, the #43 fixtures
with WiX 6.0.2. From this directory:

```
wix build fixture.wxs -o fixture.exe --acceptEula wix7
go run shrink.go fixture.exe
```

`fixture.wxs` references `../../msiread/testdata/fixture.msi`, so that fixture must exist first.

The signed pair, with the Windows SDK's signtool on the machine (creates and removes a
throwaway certificate in `Cert:\CurrentUser\My`, shrinks both outputs):

```
pwsh -File sign-fixture.ps1
```

The shapes fixture. Building it writes the detached `far.cab` beside the output; **delete it** —
`TestADetachedContainerPayloadIsReportedNotCarried` fails while it is there, because the
fixture's point is that the reader does not need it (`*.cab` is also gitignored here, so it
cannot be committed by accident):

```
wix build shapes.wxs -o shapes.exe
Remove-Item far.cab
go run shrink.go shapes.exe
```

The remote payload's `Hash` and `Size` in `shapes.wxs` were generated from `remote.exe` with
`wix burn remotepayload remote.exe -du https://example.invalid/remote.exe -o remote-payload.wxs`
(the output is a fragment to copy the attributes from, not to commit); if `remote.exe`
changes, regenerate them, or the reader will (correctly) report a digest that no longer matches
the source the test hashes.

Rebuilding changes the **bundle code** (`Registration/@Code`), which WiX generates per build, so
no test asserts it — only its shape, and that the PE section and the manifest agree on it. The
`UpgradeCode` is authored in `fixture.wxs` and is stable; tests do assert that. If a rebuild
changes a payload digest the tests assert, the change is real and worth understanding before it
is accepted.
