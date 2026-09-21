# `fixture.msi` — the inspection fixture

A 32 kB package built solely to exercise the four shapes `internal/msiread` has to handle, none
of which the repo's own release packages contain:

| Shape | In the fixture | Why it needs a purpose-built package |
|---|---|---|
| **Merge-module contribution** | `F_Merged.6F1E…` at `merged\PFiles64\MergedIn\merged.txt` | A file that exists *only* because a `<Merge>` contributed it. The release builds with the minimal template, which declares no `<Merge>`, so no released package can prove this. |
| **Level-zero feature** | `F_Hidden` in feature `Unselected` (`Level="0"`) | Its payload is in the package and must be inventoried even though it is not installed by default. An administrative install would omit it — one of the reasons that route was rejected. |
| **A subdirectory named `SourceDir`** | `F_Nested` at `MsiReadFixture\SourceDir\nested.txt` | `SourceDir` is special as the *root's* `DefaultDir` and ordinary anywhere else. Skipping it everywhere silently dropped a real directory from every path beneath it. |
| **A custom action in `AdminExecuteSequence`** | `AdminSentinel`, which would write `sentinel.txt` | Inspection must never run it. The test asserts the sentinel does not appear. |

It also carries a Binary-table stream (`FixtureBinary`) and an LZX-compressed embedded cabinet,
so the database-stream and cabinet paths both run against it.

Committing the built package rather than building it during the test is what lets these tests
run in a clean checkout with no WiX toolchain. `*.msi` is gitignored repo-wide; `.gitignore`
carries a narrow negation for this directory.

## Rebuilding

Needs WiX 7 (`msis /SETUP-WIX`). From this directory:

```
wix build module.wxs  -arch x64 --acceptEula wix7 -o fixture.msm
wix build fixture.wxs -arch x64 --acceptEula wix7 -o fixture.msi
```

The module has to be built first: `fixture.wxs` merges `fixture.msm`. The intermediate `.msm` is
not committed — it is reproducible from `module.wxs`.

Rebuilding changes the package's `ProductCode`, which is generated per build, so a test must not
assert on it. Nothing else in the fixture is expected to move; if a rebuild changes the payload
digests the tests assert, the change is real and worth understanding before it is accepted.
