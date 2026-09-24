# SBOM: what msis records about what it ships

msis can describe a built installer — every file in it, every byte's SHA-256, where each came
from — as a [CycloneDX 1.6](https://cyclonedx.org/) document. Two commands:

```bash
msis /INSPECT app.msi          # report what is inside it, on the terminal
msis /SBOM    app.msi          # write app.msi.cdx.json beside it
msis /BUILD /SBOM setup.msis   # build it, and describe what was built
```

This document explains what those produce, what the result does and does not claim, and why it
is shaped the way it is. For the `.msis` elements that feed it — `<sbom>` and `<vex>` — see
Tutorials 13 and 14 in the [Tutorial](tutorial.md).

---

## Why this exists

A manufacturer of a product with digital elements has to be able to produce a machine-readable
SBOM. Under the [Cyber Resilience Act](https://eur-lex.europa.eu/eli/reg/2024/2847/oj/eng),
Annex I Part II(1) requires one covering **at least the product's top-level dependencies**, and
Article 13(24) allows the format and required elements to be specified further.

Those provisions do not name a field list. msis's own baseline is
[NTIA's *minimum elements*](https://www.ntia.gov/report/2021/minimum-elements-software-bill-materials-sbom)
— a supplier, a component name, a version, other unique identifiers, dependency relationships,
the author of the SBOM data and a timestamp. That is a **project choice** (#29 D8), made because
it is the floor auditors in practice ask against, not because the CRA adopts it. If the format
and elements are specified further, this is the part that would have to be revisited.

**msis cannot discharge that obligation for you.** An installer file list can omit exactly the
dependencies that matter, because they live inside your own binaries. What msis can do is supply
the evidence for the part that is msis's doing, and answer "what is in the version at customer
X" from artifacts that already exist.

Read that boundary as written: neither reading an artifact, nor merging a supplied SBOM, nor
choosing an accepted format establishes compliance. Confirm the current CRA timeline with
whoever tracks compliance before any date appears in customer-facing material.

---

## It reads the artifact, not the script

`/INSPECT` and `/SBOM` open a built `.msi` or bundle `.exe`. They never parse the `.msis`, and
they never execute the package.

**Why the artifact.** Answering "what is in the version at customer X" means reading things that
already exist — re-resolving an old tag may not reproduce, because sources move, prerequisites
come from mutable URLs and toolchains change. And it is *more accurate*: the generator's file
tree is generated payload only. It misses what a template adds (the installer-hook DLL in the
Binary table), what a merge module contributes, and anything a `/CUSTOMTEMPLATES` overlay
supplied. All of that is in the artifact; none of it is in the script.

**Why never executed.** `msiexec /a` was considered and rejected. `/qn` suppresses UI, not
custom actions, so an administrative install runs `AdminExecuteSequence` and lets an arbitrary
third-party package execute code during what is advertised as inspection. It also omits features
at level zero, so its output is not even the complete payload. Extraction is passive: database
streams, cabinets and supplied loose media, read into memory.

This makes `/INSPECT` and `/SBOM` **Windows-only** — reading an installer database goes through
`msi.dll`, and unpacking a cabinet through `cabinet.dll`. Every release target is `GOOS=windows`,
so that is acceptable, but a Linux box generating `.wxs` cannot produce an SBOM. It still
*compiles* there, and fails with a message saying why — reporting an empty package would read as
"this installer contains nothing", which is the one answer that must never be a guess.

---

## `/INSPECT`

```
Package: fixture.msi
  ProductName      MsiReadFixture
  ProductVersion   1.0.0
  Manufacturer     msis tests
  ProductCode      {6250ED9A-8D61-403F-B48C-8A29BE9846C9}
  UpgradeCode      {6F1E2C3A-9B4D-4A21-8E77-27B1C0D4AF01}

Payload files (4)
       17 B  ee7b38fbbbb19212  [ProgramFiles64Folder]MsiReadFixture\hidden.txt
       18 B  751790d12f213afd  [ProgramFiles64Folder]MsiReadFixture\payload.txt
       ...

Binary streams (1) - custom actions and UI resources; executed, not installed
       18 B  751790d12f213afd  FixtureBinary

Media
  disk 1  cab1.cab             embedded stream, through sequence 4

Not covered
  - what a payload binary was itself built from is opaque; the digest identifies
    the bytes, it says nothing about their provenance
  - install-time conditions decide what actually lands on a machine
  - a cabinet that did not travel with the package cannot be read; any such is
    named under Media above
```

For a bundle it reports the bootstrapper payloads, the bundle's own payloads, and the chain —
with a warning for every payload the bundle does not carry, whatever the reason (see
["Not carried" is three different situations](#not-carried-is-three-different-situations)), since
those bytes are not in the file to hash.

`/INSPECT` prints; it writes nothing.

---

## `/SBOM`

```
msis /SBOM app.msi
  Wrote: app.msi.cdx.json
  5 components, serial urn:uuid:101548c5-edb5-4081-a240-4cc6a3b4eff5
```

The sidecar is named after the **full** artifact filename, not its stem: since #28 a
`BUILD_TARGET` is a name pattern, so an auto-bundle's `.msi` and `.exe` deliberately share a
stem, and a stem-based name would give both artifacts one path.

### Ordering and placement matter for bundles

**Run `/SBOM` over the chained `.msi` files first, where the bundle will look for them.** A
bundle's document links to the document for each installer it chains, and it can only do that if
that document is already there — *there* meaning a specific place:

> msis looks for `<bundle's directory>/<the payload's name in the manifest>.cdx.json`.

The name comes out of the bundle's own manifest, and it is resolved against the **bundle's**
directory, not the directory the installer was built in. So an MSI in `inputs/` with its document
beside it does not link from a bundle in `dist/`: the bundle looks for `dist/app.msi.cdx.json`.
Copy or build the chained installers next to the bundle, `/SBOM` them there, then `/SBOM` the
bundle. A manifest name that is absolute, or that climbs out of the bundle's directory, is
refused rather than followed.

```bash
msis /SBOM app.msi        # first
msis /SBOM setup.exe      # then the bundle, which links to app.msi.cdx.json
```

```
  Wrote: setup.exe.cdx.json
  9 components, serial urn:uuid:df94d55a-…
  Linked: MsiReadFixture -> urn:cdx:101548c5-edb5-4081-a240-4cc6a3b4eff5/1
  No link: external.exe: the bundle does not carry this payload, so no document
           can be matched to the bytes that will actually be installed
```

The other order is not an error — it produces a document that records, for each chain package,
why there is no link.

`msis /BUILD /SBOM` on a `.msis` with `<requires>` does all of this for you: an auto-bundle
produces both artifacts in one directory, and msis writes the MSI's document before the
wrapper's precisely so the link can be made. An explicit `<bundle>` chains installers built
elsewhere, so both halves are yours — each chained installer has to be where the bundle's
manifest names it, relative to the bundle, with its document beside it *there*.

### With `/BUILD`: what the build knew

`/SBOM` on a `.msis` **with** `/BUILD` is a different operation: build it, then describe what was
built, enriched with what only the build knows — each payload's source path, the toolchain
(msis and WiX versions), whether a prerequisite is carried or merely detected, and where a
downloaded one came from. Those facts are not in the artifact and cannot be recovered from it.

Enrichment only ever *adds*. Where it can be checked against the artifact it is: if the file the
build hashed is not the file the package contains, **no document is written at all** — a source
path published against the wrong bytes is worse than no source path.

---

## Retention: a document is never overwritten

Reissuing a document keeps the previous one, under a name derived from its serial:

```
msis /SBOM fixture.msi          (again, later)
  Wrote: fixture.msi.cdx.json
  5 components, serial urn:uuid:c6e76634-…
  Kept the previous document as fixture.msi.101548c5-edb5-4081-a240-4cc6a3b4eff5.cdx.json
```

**The naming is a contract**: `<artifact>.<serial without the urn:uuid: prefix>.cdx.json`, in the
artifact's own directory. An archive that already exists is never overwritten.

This is not tidiness. A BOM-Link addresses a particular **serial number and version**, so stable
component refs do not make replacement documents interchangeable. In the session above, the
bundle's document links to `urn:cdx:101548c5…/1`. Reissuing the MSI's document does not update
that link, and **reissuing the bundle cannot repair a bundle a customer already holds**. A
customer holding the old parent still resolves the old serial, so deleting it breaks exactly the
historical-artifact case this design exists for.

So: **a child document referenced by a distributed parent stays resolvable.** Replacement is
available only for a document that has not been distributed; it is never a substitute for
retention.

Every uncertainty refuses rather than proceeds. If an existing document cannot be read, or
cannot be archived safely — an unparseable file, a serial that is not a well-formed `urn:uuid`,
an archive name already taken — nothing is replaced.

---

## BOM-Links: what is verified before one is made

A bundle's document does not repeat what a chained installer contains. It points at that
installer's own document, and it does so only when that document **provably describes the bytes
this bundle carries**: the child's recorded subject SHA-256 is compared against the digest of the
payload in the bundle.

Matching name and version is not sufficient, and is not used.

Where a link is not made, the component records why, in `msis:bomLink`. The reasons are exact,
because "no link" for 13 different causes is not actionable:

| recorded reason | what it means |
|---|---|
| the manifest declares no installer payload for this package | the chain entry has nothing to describe |
| the bundle does not carry this payload, so no document can be matched to the bytes that will actually be installed | the bytes are not in this file — see below, it is not always a download |
| the manifest gives this payload no file name | there is nothing to look for |
| the payload name `…` is not relative to the bundle | absolute, or carrying a drive letter |
| the payload name `…` points outside the bundle's directory | it climbs out; msis does not follow it |
| no document at `<name>` | nothing where the bundle looked — usually the placement above |
| `<name>` could not be read | it is there and unreadable |
| `<name>` is not readable as a CycloneDX document | |
| `<name>` carries serial `…`, which is not a well-formed urn:uuid | a link cannot be built from it |
| `<name>` declares version N; a BOM-Link addresses a specific version | |
| `<name>` records no SHA-256 for its subject | it cannot be matched to the bytes carried |
| `<name>` records `…` as its subject SHA-256, which is not one | the field is there and malformed |
| `<name>` describes an artifact with SHA-256 `…`, but this bundle carries `…` | a document for a **different build** of the same installer |

The last one is the case the whole check exists for.

### "Not carried" is three different situations

A payload the bundle does not contain has no bytes here to hash, so it gets no SHA-256 and a
`msis:payload.unavailable` saying which case it is. They are not interchangeable:

- **the engine downloads it at install time** — the reason records the URL, and msis cannot hash
  what will actually be fetched;
- **it lives in a detached container** — a separate file, either downloaded from a recorded URL
  or expected beside the bundle; the reason names the container;
- **it is an external payload expected beside the installer** — no container, no URL, just a file
  the engine assumes will be there.

The first downloads the payload itself; the second may download the container holding it, or may
expect that container beside the bundle; the third downloads nothing. So "not carried" does not
mean "downloaded" — read the recorded reason, which says which of the three it is. Assuming a
download would describe a layout that ships its payloads alongside the `.exe` as if it fetched
them from the network.

---

## What the document does not cover

Every document says this in `msis:coverage`, and `/INSPECT` prints its own version of it.

**Visible in the artifact:** every payload file with its bytes, the directory structure and
resolved install targets, product identity, custom-action binaries, merge-module content,
registry and service entries, shortcuts, and anything a template or overlay contributed.

### `/INSPECT` and `/SBOM` do not report the same set

`/INSPECT` prints everything above, registry values, services and shortcuts included. The
**document inventories what is distributed**: a component per payload file and per Binary-table
stream, and nothing for a registry value, a service definition or a shortcut. Those are
installation instructions rather than things with bytes and a digest, and a component for one
would be an entry nothing can be verified against.

They are also in different positions when payload bytes cannot be read. `/INSPECT` reports what
it found and names the cabinet it could not open. `/SBOM` **refuses to write anything**: a
document without a digest for every payload cannot be verified against an installation, so msis
does not emit one. The error names the files and repeats the reader's reason. That is a case
where the artifact cannot be described, not a licence to publish a document that looks complete.

**Not visible, and marked so rather than guessed:**

- **What a payload binary was built from.** `app.exe` is bytes with a hash. It gets a component
  marked `msis:identity = undetermined`, never an invented PURL. Closing this gap is what
  `<sbom>` composition is for (Tutorial 13).
- **What a chained MSI or EXE installs**, until that artifact is itself read.
- **What ends up on a given machine.** Feature conditions, user-selected directories, preserved
  registry values and `<execute>` custom actions decide installed state at install time. A table
  inventory describes what the package *would* install.
- **Payloads the bundle does not carry.** Downloaded at install time, in a detached container,
  or expected beside the installer — see above. Such a component carries whatever digest the
  manifest records, plus `msis:payload.unavailable` saying which case it is.
- **Files in external cabinets or loose media** that did not travel with the package.

### Why almost nothing carries a purl

A PURL asserts identity. A wrong one produces false CVE matches *and hides real ones*, and one
bad match in front of an auditor discredits the whole document. So msis emits a purl only where
identity was actually determined — which, for bytes read out of a package, is essentially never.
Everything else is a file component with its hash, explicitly marked unknown.

msis does **not** read PE version resources, `debug/buildinfo` or .NET assembly references out of
payload files. Each could narrow this, and each would be **evidence, not provenance**: a version
resource is optional in the format, `buildinfo` yields the modules that contributed to a Go build
rather than a graph with edges, and assembly references are not package-level provenance. Were
any of them added, none may produce a purl unless identity is actually determined. Today the
route to what is inside a payload file is a supplied SBOM — msis's own release supplies one for
`msis.exe`, built from its `buildinfo` by `tools/sbom`. Those component documents also carry
licences, as the pair BSI TR-03183-2 asks for ([decisions D12](decisions.md)): the *original*
licence, marked `declared`, and the *distribution* licence, marked `concluded`, which are the same
id for a component under one licence. Each linked Go module's and the standard library's licence
is identified from the licence text in the module cache and GOROOT. The text is compared **in
full** with a reviewed licence text (`tools/sbom/licences`, which may differ only in the notice
above the terms, i.e. title and copyright lines, and in the organisation a BSD clause names), and
any other text or difference stops the release rather than being guessed. The WiX libraries'
licence is the one their packages declare (`MS-RL`). A payload file's licence is never guessed,
only carried when supplied.

A supplied SBOM is different — its author determined the identity, and their purls come across
untouched.

---

## The `msis:*` vocabulary

Namespaced so a consumer can tell msis's properties from anyone else's.

**The document** (`metadata.properties`)

| property | |
|---|---|
| `msis:subject.artifact` | the filename on disk this document describes |
| `msis:coverage` | what the inventory does not cover, in prose |
| `msis:msi.productCode`, `msis:msi.upgradeCode` | product identity; the UpgradeCode is what stays constant across releases |
| `msis:burn.bundleCode`, `msis:burn.engineVersion` | the same for a bundle |

**The subject** (`metadata.component`)

| property | |
|---|---|
| `msis:ntia.unknown` | an NTIA minimum element the artifact does not record, and why — `version: the bundle records no Version`, `supplier: the package records no Manufacturer`. The field itself is left out rather than filled with a placeholder, which would read as an identity. An installer msis builds always records both; this is for an artifact built elsewhere |

**Every component**

| property | |
|---|---|
| `msis:role` | `payload` (installed), `binary-stream` (executed during installation, never installed), `chained-installer`, `bootstrapper`, `supplementary`, `bundle-payload`, `required-runtime` |
| `msis:identity` | why identity is undetermined, where it is |
| `msis:installTarget` | the symbolic install path, e.g. `[ProgramFiles64Folder]App\app.exe` |
| `msis:msi.fileKey`, `msis:msi.component`, `msis:msi.componentGuid`, `msis:msi.sequence` | the MSI table keys this component came from |
| `msis:payload.carried` | whether the bundle contains these bytes |
| `msis:payload.unavailable` | why there is no SHA-256, when there is none |
| `msis:payload.downloadUrl` | where the engine will fetch it from |
| `msis:burn.packageId`, `msis:burn.packageKind`, `msis:burn.installCondition` | chain-package facts |
| `msis:bomLink` | why no link was made |

**Build-time enrichment** (`/BUILD /SBOM` only) — namespaced under `msis:build` so a reader can
tell at a glance which facts came from the artifact and which from the build that produced it;
the two have different evidentiary weight.

| property | |
|---|---|
| `msis:build.path` | `msi`, `auto-bundle`, `bundle` or `standalone` |
| `msis:build.script` | the `.msis`, by name |
| `msis:build.tool.msis`, `msis:build.tool.wix` | the toolchain that built it |
| `msis:build.source`, `msis:build.sourceRoot` | where a payload came from, and which bind path it resolved in |
| `msis:build.unresolved` | an input the build could not account for — recorded, never omitted |
| `msis:build.coverage` | how much of the payload carries a source, and what is not reached |
| `msis:prerequisite.type` | a bundled runtime: `vcredist`, `netfx` |
| `msis:prerequisite.arch` | which architecture of it — a bundle carries several, each its own file |
| `msis:prerequisite.cache` | which cache entry it came from, symbolically (`prerequisite-cache:vcredist/2022/…`), never the machine path |
| `msis:prerequisite.verification` | what the bytes were checked against before being chained: `pinned-digest` (a download msis pins, D5), `script-digest` (a supplied `source=` whose `sha256=` matched, #50) or `unverified` (a supplied `source=` with no digest — a file somebody put there) |
| `msis:launchCondition` | for `/STANDALONE`: the condition that detects a runtime nobody ships |

**Composed from a supplied SBOM** — see [Tutorial 13](tutorial.md) for the `<sbom>` element.

| property | |
|---|---|
| `msis:supplied.from` | which document contributed this component. It is also what admits a component with no digest: msis never held those bytes, so there was never a hash to drop |
| `msis:supplied.document` | per merged document, on the metadata: what the merge did, and what it did **not** establish |
| `msis:supplied.ref` | msis assigned this component's `bom-ref` because its author gave it none. An address is not identity; nothing else about it was invented |

**VEX assessments** — see [Tutorial 14](tutorial.md) for the `<vex>` element. The first three are
**input**: what the author of a statement records about when it applies. The rest are msis's
verdict on it.

| property | |
|---|---|
| `msis:vex.assessedProductVersion` | the release the assessment was made against. Required — without it msis cannot tell an assessment that still applies from one that has been overtaken |
| `msis:vex.appliesToProductVersions` | releases the assessor deliberately widened it to: an exact list, or `*` |
| `msis:vex.assessedComponentDigest` | the bytes it was assessed against |
| `msis:vex.applicability` | `applies` or `needs-review` — whether every recorded condition still holds |
| `msis:vex.reviewReason` | which condition lapsed |
| `msis:vex.previousState` | what a lapsed statement used to say, before it stopped suppressing |
| `msis:vex.observedComponentDigest` | what the build actually carries, so a reassessment starts from a fact |
| `msis:vex.subject` | the BOM-Link of the inventory these statements were evaluated against |
| `msis:vex.coverage` | how many statements still apply and how many need review |

---

## Knowledge states: three, not two

A component's dependency information is **known-empty**, **unknown**, or **incomplete**, and the
document distinguishes them. An empty `dependsOn` is *correct* for something known to have no
dependencies; emitting it for an opaque payload would falsely assert "depends on nothing".
Unknown and incomplete coverage are said through `compositions` instead, which name the
components they apply to **explicitly** — a completeness declaration does not cascade through
containment.

A plain MSI's document therefore says three things at once:

```
complete    dependencies: [the product]     what it CONTAINS is known exactly
incomplete  assemblies:   [the product]     how completely it is DESCRIBED is not
unknown     assemblies + dependencies: [every payload component]
```

---

## What the rules are, and where they are enforced

Ten rules apply to every document msis writes, and they live in an executable form —
`internal/sbom/conformance` — rather than in prose here, because a rule that lives only in prose
drifts:

- no purl unless identity was determined
- SHA-256 on every payload component, with narrow, *declared* exceptions
- sorted by defined keys: components by `bom-ref`, dependencies by `ref` with their members
  sorted, compositions by aggregate, then assemblies, then dependencies, and msis's own
  properties and external references (a supplied component keeps its author's order)
- NTIA fields present or explicitly unknown (`msis:ntia.unknown` on the subject), including the
  generation context: `metadata.lifecycles` is `post-build` for a document read from an artifact,
  and also `build` under `/BUILD /SBOM`, because the build record added what only the build knew;
  a VEX sidecar carries its inventory's
- `metadata.tools` names msis with the SHA-256 of the binary that ran; if msis cannot hash
  itself, no document is written
- `compositions` present and referencing real components
- dependency knowledge state correct: known-empty, unknown and incomplete distinguished
- every `bom-ref` referenced resolves — including references held inside components
- validation against the official CycloneDX 1.6 schema, vendored with its dependencies
- a component may not be marked as both installed payload and supplied by someone else

Some of these cannot be checked from the JSON alone — whether a purl was derived from evidence,
or whether an omitted payload ever existed, is invisible in the output. So the checker takes an
*expected inventory* alongside the document and compares the two, rather than comparing the
document with itself.

### Two fields vary, and exactly two

`metadata.timestamp` and `serialNumber` differ between two runs over identical inputs. Everything
else is byte-identical, which is what makes a diff between two documents a release review.
`sbom.CanonicalForDiff` removes exactly those two.

This is the one rule the conformance package cannot enforce, because it is a property of two
documents, not of one. Each emitter's own tests build twice and compare the canonical forms:
`TestOnlyTimestampAndSerialVary` and `TestDeterministicUnderVariedMapOrder` for an MSI,
`TestTwoBundleDocumentsDifferOnlyInTimestampAndSerial` for a bundle, `TestEnrichmentStaysDeterministic`,
`TestMergingStaysDeterministic` and, for the VEX sidecar, `TestEvaluationIsDeterministic`.

That holds with the emitter and any referenced child documents fixed — regenerating a child
changes a parent's link inputs, which is why the retention rule above exists.

---

## BSI TR-03183-2

msis aims its documents at **BSI TR-03183-2 v2.1.0** (2025-08-20), the most concrete
CRA-adjacent SBOM specification, read from the guideline's own text
([decisions D10](decisions.md)). Where it stands today:

| BSI §5.2 field | msis |
|---|---|
| Timestamp, SBOM-URI (`serialNumber`) | ✓ |
| Hash of the deployable component, **SHA-512** | ✓ beside SHA-256 in `hashes` for every file msis read, and the artifact itself; a `distribution` external reference only where one exists (a payload the engine downloads), see D10 |
| Filename, executable / archive / structured | ✓ `bsi:component:filename`, `:executable`, `:archive`, `:structured`, read from the bytes; a property that cannot be proven is omitted |
| Dependencies, and their completeness | ✓ `dependsOn` plus compositions; an unknown graph is stated, not left empty (D11) |
| Distribution / original licence | ✓ for msis's own components (`tools/sbom`); never guessed for a payload file |
| Component name | ✓ |
| Component version | ✓ where the artifact records one (a PE's version resource). BSI's fallback, the file's modification date, is not yet emitted: a cabinet stores local time with no time zone, and turning that into an RFC 3339 instant would mean guessing the offset |
| Creator of the SBOM | ✓ under `/BUILD /SBOM`: `SBOM_CREATOR` (email or URL) as `metadata.manufacturer`; never inferred |
| Component creator: the product | ✓ `MANUFACTURER_URL` / `MANUFACTURER_EMAIL` are written into the installer (`ARPURLINFOABOUT`, `ARPCONTACT`; a bundle's `AboutUrl`) and read back from it, so even `/SBOM` on the artifact knows them. In a package msis did not build, only a value that IS a URL or an email address is taken |
| Component creator, version, licence: payload files | when the script declares them: `<component for= creator= version= license= purl=/>` (see [Tutorial 13](tutorial.md)), or a supplied `<sbom>`; never guessed. A declared version that contradicts the file's recorded version stops the build (D13) |
| No vulnerability information in the SBOM | ✓ VEX is a separate sidecar |
| Data licence of the document | not a BSI field; `SBOM_DATA_LICENSE` grants one when the script sets it (D14). msis's own releases use CC0-1.0 |

**Only `/BUILD /SBOM` can be compliant.** §5.1 requires a *Build SBOM*, one created as part of
the build. `/SBOM` on an existing artifact produces what the guideline calls an *Analysed
SBOM* (§8.4.4): still useful, and exactly the right tool for "what is at customer X", but not
a TR-03183-2 SBOM by definition.

The `bsi:component:*` properties are BSI's, not msis's, and use BSI's values:
`executable`/`non-executable`, `archive`/`no archive`, `structured`/`unstructured`.

---

## Asking questions across releases

`tools/sbom-index` builds a SQLite index over a directory of documents:

```bash
just sbom-index                       # over bootstrap/dist by default
just sbom-query                       # list the documented questions
just sbom-query match-digest "-arg sha256=69202d58…"
```

The CycloneDX corpus is authoritative; the database is a derived view, rebuilt from scratch, and
nothing in it is a source of truth. It answers "which products ship this DLL, at this version",
"what changed between two releases", "does this file set match anything we shipped", and — with
VEX sidecars — "which releases are affected, minus what we have already assessed". See
[`tools/sbom-index/README.md`](../tools/sbom-index/README.md).

---

## See also

- **[Tutorial 13](tutorial.md)** — `<sbom>`: composing a component SBOM your build system
  produced, which is the only route to what is inside your own binaries
- **[Tutorial 14](tutorial.md)** — `<vex>`: recording which CVE matches are not exploitable, in a
  form that cannot outlive the reason it was true
- **[`tools/sbom-index/README.md`](../tools/sbom-index/README.md)** — the index and its schema
- **[Developer Overview](overview.md)** — architecture and internals
