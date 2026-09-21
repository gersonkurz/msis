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

## Rebuilding

Needs WiX 7 (`msis /SETUP-WIX`). From this directory:

```
wix build fixture.wxs -o fixture.exe --acceptEula wix7
go run shrink.go fixture.exe
```

`fixture.wxs` references `../../msiread/testdata/fixture.msi`, so that fixture must exist first.

Rebuilding changes the **bundle code** (`Registration/@Code`), which WiX generates per build, so
no test asserts it — only its shape, and that the PE section and the manifest agree on it. The
`UpgradeCode` is authored in `fixture.wxs` and is stable; tests do assert that. If a rebuild
changes a payload digest the tests assert, the change is real and worth understanding before it
is accepted.
