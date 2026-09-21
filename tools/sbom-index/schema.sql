-- The SBOM index (#35). The CycloneDX corpus is authoritative (#29 D12); this is a derived
-- view of it, rebuilt from scratch whenever it is built. Nothing here is a source of truth.
--
-- EVERY KEY IS NATURAL. There is not a single generated id, because the database is rebuilt:
-- a surrogate id would be a different number after a rebuild, so anything holding one - a
-- saved query, a VEX statement (#37), a report - would silently point at the wrong row. The
-- keys used instead are the ones the documents themselves define: a document's serial number
-- and a component's bom-ref, which A2 made stable across releases for exactly this reason.
--
-- Where a document does not supply one, the key is derived from content rather than from
-- insertion order: a document with no serialNumber is keyed by the digest of its own bytes.
-- The real field is still recorded, and is NULL, so a query can tell a derived key from a
-- declared one instead of guessing.

PRAGMA foreign_keys = ON;

-- A supplier named anywhere in the corpus. Its own table so that a product, a document and a
-- component can all point at one spelling.
CREATE TABLE supplier (
    name TEXT PRIMARY KEY
);

-- A product across all its releases.
--
-- Identity is the UpgradeCode where the corpus states one, because Windows Installer defines
-- that as the identifier constant across a product's releases - the same reason A2 built
-- bom-refs on it. A document that states none (a hand-written or foreign CycloneDX file) falls
-- back to its subject's name, which is weaker, so the id says which was used rather than
-- leaving a consumer to assume.
CREATE TABLE product (
    id           TEXT PRIMARY KEY,  -- 'upgrade:<lowercased guid>' or 'name:<subject name>'
    upgrade_code TEXT,              -- NULL when no document in the corpus states one
    name         TEXT NOT NULL,     -- as of the most recent document naming it; see README
    supplier     TEXT REFERENCES supplier(name)
);

-- One CycloneDX document.
CREATE TABLE document (
    -- The BOM-Link a parent would use to name this document - 'urn:cdx:<uuid>/<version>' -
    -- or 'sha256:<digest>' for one with no serial number. A serial identifies a document; a
    -- serial AND a version identify a REVISION of it, which is what a link addresses and what
    -- CycloneDX permits to coexist. Keying on the serial alone made two legitimate revisions
    -- collide as duplicates, and made a link to /2 resolve happily to version 1.
    id               TEXT PRIMARY KEY,
    serial           TEXT,              -- NULL when the document declares none
    version          INTEGER NOT NULL,  -- the CycloneDX document version, not the product's
    spec_version     TEXT NOT NULL,
    timestamp        TEXT,              -- metadata.timestamp
    source           TEXT NOT NULL,     -- path within the corpus, slash-separated
    sha256           TEXT NOT NULL,     -- of the document file itself
    product_id       TEXT NOT NULL REFERENCES product(id),

    -- metadata.component: the thing the document is ABOUT, which is not one of its components.
    subject_ref      TEXT,
    subject_name     TEXT,
    subject_version  TEXT,              -- the product release this document describes
    subject_sha256   TEXT,              -- #29 D13: the artifact's own digest
    subject_artifact TEXT,              -- msis:subject.artifact, the filename on disk

    supplier         TEXT REFERENCES supplier(name),

    -- A revision is unique; a serial on its own is not.
    UNIQUE (serial, version)
);

-- A release of a product. Derived rather than stored: it is exactly the distinct subject
-- versions of that product's documents, and a table would be a second place for it to be wrong.
CREATE VIEW product_version AS
    SELECT DISTINCT product_id, subject_version AS version
    FROM document
    WHERE subject_version IS NOT NULL AND subject_version <> '';

-- One entry of a document's components[].
--
-- Addressed by ordinal, not by bom-ref, because a bom-ref is optional in CycloneDX and the
-- corpus really does contain documents without one. The ref is recorded as its own nullable
-- column with a unique index, so it is usable as a key exactly where the document provides it
-- and visibly absent where it does not.
CREATE TABLE component (
    document_id    TEXT NOT NULL REFERENCES document(id),
    ordinal        INTEGER NOT NULL,   -- position in components[], from 0
    bom_ref        TEXT,
    type           TEXT NOT NULL,
    name           TEXT NOT NULL,
    version        TEXT,
    purl           TEXT,               -- only where identity was determined (#29 D4)

    -- Promoted from properties because the questions this index exists for need them in a
    -- WHERE clause. Everything else stays in component_property; a promoted property is NOT
    -- repeated there, so each fact lives in exactly one place.
    role           TEXT,               -- msis:role
    install_target TEXT,               -- msis:installTarget
    carried        INTEGER,            -- msis:payload.carried; NULL when the document is silent

    supplier       TEXT REFERENCES supplier(name),

    -- CycloneDX lets a component contain further components - an assembly holding the files
    -- it is made of. Those are indexed too, flattened depth-first into the same ordinal
    -- sequence, with the enclosing component named here. Visiting only the top-level array
    -- made a document's child components vanish while the document itself reported clean,
    -- so a digest lookup returned a false negative and coverage said nothing was missing.
    parent_ref     TEXT,
    depth          INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (document_id, ordinal)
);

CREATE UNIQUE INDEX component_by_ref
    ON component(document_id, bom_ref) WHERE bom_ref IS NOT NULL;

-- Answering "does this file set match anything we shipped" is a lookup by digest, so it is
-- indexed as one.
CREATE INDEX component_by_name ON component(name, version);

CREATE TABLE hash (
    document_id TEXT NOT NULL,
    ordinal     INTEGER NOT NULL,
    alg         TEXT NOT NULL,
    content     TEXT NOT NULL,
    PRIMARY KEY (document_id, ordinal, alg),
    FOREIGN KEY (document_id, ordinal) REFERENCES component(document_id, ordinal)
);

CREATE INDEX hash_by_content ON hash(content, alg);

-- The msis:* vocabulary, minus what was promoted to a component column. Kept whole so that a
-- property added to a future document is indexed without a schema change - the index must not
-- quietly drop what it does not recognise.
CREATE TABLE component_property (
    document_id TEXT NOT NULL,
    ordinal     INTEGER NOT NULL,
    name        TEXT NOT NULL,
    value       TEXT NOT NULL,
    PRIMARY KEY (document_id, ordinal, name, value),
    FOREIGN KEY (document_id, ordinal) REFERENCES component(document_id, ordinal)
);

-- metadata.properties: facts about the document rather than about any one component.
CREATE TABLE document_property (
    document_id TEXT NOT NULL REFERENCES document(id),
    name        TEXT NOT NULL,
    value       TEXT NOT NULL,
    PRIMARY KEY (document_id, name, value)
);

-- Edges between refs. Deliberately text, not a foreign key into component: a dependency may
-- name the document's SUBJECT, which is not one of its components, and a BOM-Link names a
-- document this corpus may not contain.
--
--   'dependsOn'  from dependencies[]: the parent contains or requires the child
--   'bom-link'   from an externalReference of type bom: to_ref is a urn:cdx: link, and
--                resolved_document_id is filled in when that document is in this corpus
CREATE TABLE relationship (
    document_id          TEXT NOT NULL REFERENCES document(id),
    from_ref             TEXT NOT NULL,
    to_ref               TEXT NOT NULL,
    kind                 TEXT NOT NULL,
    resolved_document_id TEXT REFERENCES document(id),
    PRIMARY KEY (document_id, from_ref, to_ref, kind)
);

-- A document that could not be indexed. It is recorded HERE, not merely printed, because an
-- index that silently covers less than the corpus is the failure this whole design is written
-- against: a query returning nothing must be distinguishable from a query whose evidence never
-- made it in. `SELECT * FROM ingest_error` is the answer to "is this index complete".
CREATE TABLE ingest_error (
    source  TEXT PRIMARY KEY,  -- path within the corpus
    problem TEXT NOT NULL
);

-- What produced this index, and from what. A corpus is a directory that changes; without this
-- a stale .db is indistinguishable from a current one.
CREATE TABLE build_info (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
