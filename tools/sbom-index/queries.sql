-- The questions this index exists for (#35), as runnable SQL.
--
-- They live in this file rather than in prose so that the documentation IS the thing the tests
-- execute: a query that stopped working could not sit here looking correct. Run one with
--
--     sbom-index -db sbom-index.db -query <name> -arg key=value
--
-- Each query is introduced by `-- name: <name>` and documents its own parameters. Parameters
-- are SQLite named parameters (:name); every one must be supplied.

-- name: products-shipping
--
-- "Which products ship this DLL, at this version?"
--
-- Answers across every release in the corpus, so it is also the answer to "who is affected" when
-- a version of some component turns out to be vulnerable.
--
-- :name     the component's file name, e.g. 'msi-simplica.dll'
-- :version  the component's version, or '' for any version
SELECT p.name         AS product,
       d.subject_version AS release,
       c.name         AS component,
       c.version      AS component_version,
       c.install_target,
       d.source
FROM component c
JOIN document d ON d.id = c.document_id
JOIN product  p ON p.id = d.product_id
WHERE c.name = :name
  AND (:version = '' OR c.version = :version)
ORDER BY p.name, d.subject_version, c.install_target, d.source;

-- name: release-documents
--
-- "Which documents describe this release?"
--
-- Ask this before diffing. A release is not one document: msis's own 3.0.5 ships an x64, an
-- x86 and an arm64 MSI, all with the same UpgradeCode and the same ProductVersion, so a
-- product and a version name THREE documents. Comparing releases rather than documents joined
-- every document on one side to every document on the other, and two unchanged variants came
-- back as two false changes in opposite directions.
--
-- :product  a product id, e.g. 'upgrade:e7a3b8c1-5d2f-4a9e-b6c4-8f1d3e2a7b5c'
SELECT d.id, d.subject_version AS release, d.subject_artifact, d.source,
       (SELECT COUNT(*) FROM component c WHERE c.document_id = d.id) AS components
FROM document d
WHERE d.product_id = :product
ORDER BY d.subject_version, d.subject_artifact, d.id;

-- name: document-diff
--
-- "What changed between 4.1 and 4.2?" - between two DOCUMENTS, which is the only form of the
-- question with one answer. Use release-documents to pick the pair (x64 against x64, say).
--
-- Matched on bom-ref, which A2 made stable across releases precisely so this is possible, and
-- which is unique only WITHIN a document. Matching on name would report a renamed file as one
-- removal plus one addition and would confuse two files of one name installed to different
-- places.
--
-- Components with no bom-ref cannot be matched at all, and are listed as 'not comparable'
-- rather than dropped: an empty result has to mean "nothing changed", not "nothing could be
-- compared". The repository's own release inventory is exactly such a document.
--
-- :from  the earlier document's id
-- :to    the later document's id
WITH side AS (
    SELECT c.document_id, c.bom_ref, c.name, c.version, c.install_target,
           (SELECT h.content FROM hash h
             WHERE h.document_id = c.document_id AND h.ordinal = c.ordinal
               AND h.alg = 'SHA-256') AS sha256
    FROM component c
    WHERE c.document_id IN (:from, :to)
),
a AS (SELECT * FROM side WHERE document_id = :from AND bom_ref IS NOT NULL),
b AS (SELECT * FROM side WHERE document_id = :to   AND bom_ref IS NOT NULL)
SELECT COALESCE(a.bom_ref, b.bom_ref) AS bom_ref,
       COALESCE(a.name, b.name)       AS name,
       CASE
           WHEN a.bom_ref IS NULL THEN 'added'
           WHEN b.bom_ref IS NULL THEN 'removed'
           ELSE 'changed'
       END AS change,
       a.version AS from_version, b.version AS to_version,
       a.sha256  AS from_sha256,  b.sha256  AS to_sha256
FROM a FULL OUTER JOIN b ON a.bom_ref = b.bom_ref
WHERE a.bom_ref IS NULL
   OR b.bom_ref IS NULL
   OR COALESCE(a.sha256, '') <> COALESCE(b.sha256, '')
   OR COALESCE(a.version, '') <> COALESCE(b.version, '')

UNION ALL

-- The components neither side could be matched on. Reported, not dropped.
SELECT NULL, name, 'not comparable',
       CASE WHEN document_id = :from THEN version END,
       CASE WHEN document_id = :to   THEN version END,
       CASE WHEN document_id = :from THEN sha256  END,
       CASE WHEN document_id = :to   THEN sha256  END
FROM side WHERE bom_ref IS NULL

ORDER BY change, bom_ref, name;

-- name: match-digest
--
-- "Does this file from a customer's machine match anything we shipped?"
--
-- The digest is the only thing that answers this: a filename and a version can be anything, and
-- #29 D5 requires a SHA-256 on every payload for exactly this reason. Returns every release the
-- bytes appear in, so "which version does this customer have" is the same query.
--
-- :sha256  a lowercase hex SHA-256
SELECT p.name            AS product,
       d.subject_version AS release,
       c.name            AS component,
       c.version         AS component_version,
       c.install_target,
       c.role,
       d.source
FROM hash h
JOIN component c ON c.document_id = h.document_id AND c.ordinal = h.ordinal
JOIN document  d ON d.id = c.document_id
JOIN product   p ON p.id = d.product_id
WHERE h.alg = 'SHA-256' AND h.content = :sha256
ORDER BY p.name, d.subject_version, c.install_target;

-- name: coverage
--
-- "Is this index complete, and what does it not cover?"
--
-- Asked first, not last: every other answer here is bounded by it. A payload with no digest
-- cannot be matched by match-digest, and a document that failed to load is a release the other
-- queries silently know nothing about.
--
-- No parameters.
SELECT 'documents indexed' AS item, CAST(COUNT(*) AS TEXT) AS value FROM document
UNION ALL
SELECT 'documents rejected', CAST(COUNT(*) AS TEXT) FROM ingest_error
UNION ALL
SELECT 'products', CAST(COUNT(*) AS TEXT) FROM product
UNION ALL
SELECT 'components', CAST(COUNT(*) AS TEXT) FROM component
UNION ALL
SELECT 'components with no SHA-256', CAST(COUNT(*) AS TEXT) FROM component c
  WHERE NOT EXISTS (SELECT 1 FROM hash h
                     WHERE h.document_id = c.document_id AND h.ordinal = c.ordinal
                       AND h.alg = 'SHA-256')
UNION ALL
SELECT 'payloads the artifact does not carry', CAST(COUNT(*) AS TEXT) FROM component
  WHERE carried = 0
UNION ALL
SELECT 'BOM-Links that resolve in this corpus', CAST(COUNT(*) AS TEXT) FROM relationship
  WHERE kind = 'bom-link' AND resolved_document_id IS NOT NULL
UNION ALL
SELECT 'BOM-Links that do not', CAST(COUNT(*) AS TEXT) FROM relationship
  WHERE kind = 'bom-link' AND resolved_document_id IS NULL;

-- name: rejected
--
-- Every document in the corpus that is NOT in this index, and why.
--
-- No parameters.
SELECT source, problem FROM ingest_error ORDER BY source;
