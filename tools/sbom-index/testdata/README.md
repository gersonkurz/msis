# The test corpus

Real CycloneDX documents, produced by the real emitters from the repository's own committed
fixtures. Generated rather than hand-written, because a corpus written to match the index would
only ever prove the index matches itself.

Laid out one directory per release, which is how a release corpus actually looks — and what
makes the bundle's BOM-Link resolve, since the document it points at is the one sitting beside
the artifact it chains.

| file | what it is there for |
|---|---|
| `1.0.0/fixture.msi.cdx.json` | release 1 of a product, from `internal/msiread/testdata/fixture.msi` |
| `2.0.0/fixture.msi.cdx.json` | release 2 of the **same** product: same UpgradeCode, new ProductCode, one file added, one removed, one whose bytes changed. Without a second release there is nothing for `document-diff` to diff. |
| `1.0.0/fixture.exe.cdx.json` | the bundle, from `internal/burnread/testdata/fixture.exe`. Its BOM-Link to release 1 resolves, because the bundle really carries that MSI and the digests agree. It also brings payloads the bundle does **not** carry, so `carried = 0` is exercised. |
| `2.0.0/fixture-x86.msi.cdx.json` | a **second document for release 2.0.0**, describing the same content under a different ProductCode and artifact name — which is what msis's own releases look like. A release naming several documents is the case that made a release-level diff cross-match every document on one side against every on the other. |
| `1.0.0/release-inventory.cdx.json` | msis's own release document from `tools/sbom`, copied verbatim. It has **no `serialNumber` and not one `bom-ref`** — the case that decides how documents and components are keyed, and one no msis `/SBOM` output would have shown. |
| `2.0.0/broken.cdx.json` | not valid CycloneDX. Hand-written, since being invalid is its entire job. It is what makes "reported, not skipped" a checked property of the default corpus rather than of a special test. |

## Release 2 is synthesised, and that is stated

The repository has one fixture MSI, so the second release is produced by the real emitter from
a *modified reading* of that package — the version bumped, the ProductCode regenerated as
Windows Installer does per build, one payload's digest changed, one file dropped and one added.
The **document** is genuine emitter output; the package it describes is hypothetical. Nothing
in the index's tests depends on that package existing, only on the documents.

## Regenerating

Needs Windows (reading an MSI needs `msi.dll`). From this directory:

```
go run gen.go
```

That is the whole procedure — it regenerates the **entire** corpus. The two hand-written
fixtures live in `static/` and are copied in, so nothing has to survive the wipe and no
instruction to "leave a file alone" can be defeated by the tool that deletes it.

`gen.go` pins the two fields that legitimately vary between runs — `metadata.timestamp` and
`serialNumber` — so regenerating reproduces the committed corpus byte for byte. If it does not,
something upstream moved and the change is worth understanding before it is accepted.

Rebuilding `internal/msiread/testdata/fixture.msi` changes its ProductCode and payload digests,
so the corpus has to be regenerated with it. The product's **UpgradeCode** is authored in
`fixture.wxs` and does not move, which is why `query_test.go` can name it as a constant.
