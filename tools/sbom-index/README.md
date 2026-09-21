# `sbom-index` — a SQLite index over the SBOM corpus

A pile of CycloneDX documents answers *"what is in this release"*. It does not answer the
questions the work exists for:

- which products ship this DLL version?
- what changed between 4.1 and 4.2?
- does this customer's file set match anything we shipped?

This builds a SQLite database that does. **The corpus is authoritative** (#29 D12) — the
documents are what you hand an auditor. This is an index: if the two ever disagree, delete the
database and build it again.

```
just sbom-index                       # index bootstrap/dist
just sbom-index path/to/corpus        # index somewhere else
just sbom-query                       # list the documented questions
just sbom-query coverage
just sbom-query match-digest "-arg sha256=69202d58…"
```

This is a **different module**, so `go run ./tools/sbom-index` from the repository root does
not work — the root module does not contain the package, which is the whole point. Use `go -C`,
which is also what the `just` recipes do:

```
go -C tools/sbom-index run . -corpus /absolute/path/to/corpus
go -C tools/sbom-index run . -db /absolute/path/X.db -query document-diff -explain
```

`go -C` changes directory, so paths given to the tool are resolved from inside the module.
Pass them absolute; the `just` recipes do that for you.

The deliverable is the **file**. A `.db` opens in DB Browser, DBeaver, Python's stdlib,
PowerShell and Excel with nobody writing a parser, which is the whole reason it is SQLite and
not something better on every other axis.

## Its own module, on purpose

`tools/sbom-index` has its own `go.mod`. Not importing `modernc.org/sqlite` into `cmd/msis`
would keep it out of that *binary*, but **not** out of the root module's requirement graph —
anyone building msis at all would still fetch and verify it. A nested module is what actually
isolates it, and `TestTheRootModuleDoesNotRequireSQLite` (in the root module, so it runs during
`just check`) verifies that directly rather than inferring it from a built binary.

`modernc.org/sqlite` rather than cgo bindings: the shipped binaries build `CGO_ENABLED=0` and
cross-compile to three architectures.

Because `go vet ./...` and `go test ./...` are module-scoped, the root module's `./...` does
**not** reach this directory. `just vet` and `just check` invoke `vet-tools` and `test-tools`
for it; running the checks by hand needs the same.

## The schema

Read `schema.sql` — it is commented, and it is the authority. Three things about it are worth
knowing before writing a query:

**Every key is natural.** There is not one generated id. The database is rebuilt from scratch,
so a surrogate id would be a different number afterwards and anything holding one — a saved
query, a VEX statement (#37), a report — would silently point at the wrong row. The keys are
the ones the documents define: a document's `serialNumber`, and a component's `bom-ref`, which
A2 made stable across releases for exactly this purpose.

**A document is keyed by its REVISION, not its serial.** A serial identifies a document; a
serial *and* a `version` identify a revision of it, and that pair is what a BOM-Link addresses.
So `document.id` is spelled exactly as the link — `urn:cdx:<uuid>/<version>` — which makes
resolving a link a lookup rather than a reconstruction. Two revisions of one document coexist,
as CycloneDX intends, and a link to `/2` does not resolve to version 1.

**Where a document supplies no key, the derived one is content-based.** A document with no
`serialNumber` (the repository's own release inventory is one) is keyed `sha256:<digest of the
file>`, and `document.serial` stays NULL so a consumer can tell a derived key from a declared
one. A component with no `bom-ref` is addressed by its ordinal in `components[]`.

**Components nest, and nested ones are indexed.** CycloneDX lets a component contain
components. They are flattened depth-first into the same ordinal sequence, with `parent_ref`
and `depth` recording the shape. msis emits none, but the index reads documents msis did not
write and such a document validates cleanly — so dropping its children would have produced
false negatives from `match-digest` with nothing in `coverage` to suggest anything was wrong.

**Promoted properties are not duplicated.** `role`, `install_target` and `carried` are columns
on `component` because the questions above need them in a `WHERE` clause; they are therefore
*not* also in `component_property`. Everything else msis emits is, whole, so a property added
to a future document is indexed without a schema change.

| table | holds |
|---|---|
| `product` | a product across its releases, keyed on UpgradeCode where one is stated |
| `product_version` (view) | the distinct releases of each product |
| `document` | one CycloneDX document, its subject, and its digest |
| `component` | one entry of a document's `components[]` |
| `hash` | every digest of a component |
| `component_property` / `document_property` | the `msis:*` vocabulary, minus what was promoted |
| `relationship` | `dependsOn` edges and BOM-Links, with the link's target resolved where the corpus holds **that revision** |
| `vulnerability` | one VEX statement, with msis's verdict on whether its conditions still hold |
| `vulnerability_affects` | the refs a statement is about |
| `vulnerability_property` | the `msis:vex` vocabulary, minus what was promoted |
| `ingest_error` | every document that did **not** make it in, and why |
| `build_info` | what produced this index, and from where |

### VEX statements are documents too

A VEX sidecar (#37) is a CycloneDX document, so the same corpus scan finds it, the same keys
address it, and it joins to the same product as the inventory it annotates — no second tool and
no second corpus. That is what makes

```
just sbom-query affected-unassessed "-arg name=zlib1.dll -arg version= -arg cve=CVE-2024-1234"
```

— "which of our releases ship this, minus the ones we have already assessed as not exploitable"
— one query.

`vulnerability.applicability` is the column that makes it safe. It is **msis's verdict**, not
the assessor's: whether the conditions a statement recorded still held for the release it was
evaluated against. Only `applies` subtracts. A statement needing review is reported, because an
assessment outliving the reason it was true is the precise failure the sidecar exists to
prevent — a library can be byte-identical between two releases while the application around it
starts calling the vulnerable path.

### What it deliberately does not project

`metadata.tools` (which msis produced the document), external references other than BOM-Links,
and licence data. None is needed by the questions above, and the corpus remains authoritative
for everything — this is an index, not a second copy. Add a column when a question needs one.

## Incompleteness is recorded, not implied

A document that fails validation is **reported, not skipped**: in the terminal, in the exit
status, and — the one that still matters a month later — in the `ingest_error` table. A query
returning no rows has to be distinguishable from a query whose evidence never made it in, so
`-query coverage` is the first thing to run and `-query rejected` says what is missing.

The exit status survives a query in the same command. Building and asking a question at once
must not turn an incomplete build into a successful one.

**A release is not one document.** msis's own 3.0.5 ships an x64, an x86 and an arm64 MSI under
one UpgradeCode and one ProductVersion, so a product and a version name three documents. That
is why the diff is `document-diff` and takes two document ids: comparing *releases* joined every
document on one side to every document on the other, and two unchanged variants came back as two
changes pointing in opposite directions. Use `release-documents` to pick the pair.

Components that cannot be matched — those with no `bom-ref` — are listed by `document-diff` as
`not comparable` rather than dropped, so an empty result means "nothing changed" and not
"nothing could be compared".

## Rebuilding is the recovery procedure

Building over an existing index replaces it. There is no incremental mode, because incremental
would mean deciding what to do about a document that has since been deleted or rewritten, and
the cheap always-correct answer is to hold no such state. `TestRebuildingProducesIdenticalContents`
builds, deletes, rebuilds and compares — on a canonical dump of the rows, not on the file, since
SQLite's page layout legitimately differs between two runs that produce identical contents.

## The test corpus

`testdata/corpus` is real msis output; see `testdata/README.md` for what is in it and how to
regenerate it.
