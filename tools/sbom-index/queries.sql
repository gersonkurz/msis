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
  -- Inventories only. A VEX sidecar is a document about this release too, but it inventories
  -- nothing, so diffing one against an inventory would report every component as removed.
  -- `assessments` is the query for those.
  AND d.kind = 'inventory'
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

-- name: affected-unassessed
--
-- "Which of our products ship this component, MINUS the ones we have already assessed as not
-- exploitable?"
--
-- The question a VEX sidecar exists to answer (#37). A CVE match against a component is not an
-- exploitable vulnerability in the product that ships it, and an assessment that says so is
-- worth having only if it can be subtracted from the next alert without being re-litigated.
--
-- Only an assessment that STILL APPLIES subtracts. msis records, per statement, whether the
-- conditions it was made under still held for the release it was evaluated against
-- (msis:vex.applicability); one that needs review is deliberately NOT subtracted here, because
-- the whole failure this is written against is an assessment outliving the reason it was true.
--
-- :name     the component's file name, e.g. 'zlib1.dll'
-- :version  the component's version, or '' for any version
-- :cve      the vulnerability identifier, e.g. 'CVE-2024-1234'
SELECT p.name             AS product,
       d.subject_version  AS release,
       c.name             AS component,
       c.version          AS component_version,
       c.bom_ref,
       d.source
FROM component c
JOIN document d ON d.id = c.document_id
JOIN product  p ON p.id = d.product_id
WHERE c.name = :name
  AND (:version = '' OR c.version = :version)
  AND NOT EXISTS (
        SELECT 1
        FROM vulnerability v
        JOIN vulnerability_affects a
          ON a.document_id = v.document_id AND a.ordinal = v.ordinal
        JOIN document vd ON vd.id = v.document_id
        WHERE v.id = :cve
          -- The inventory it was actually evaluated against, not merely one of the same
          -- product and version. A release commonly has several inventories - x64, x86,
          -- arm64 - carrying the same component refs, and an assessment made against one of
          -- them says nothing about the others.
          AND vd.assesses = d.id
          AND a.ref = c.bom_ref
          AND v.applicability = 'applies'
          AND v.state IN ('not_affected', 'false_positive', 'resolved', 'resolved_with_pedigree')
  )
ORDER BY p.name, d.subject_version, c.name, d.source;

-- name: assessments
--
-- "What have we said about this vulnerability, and does it still hold?"
--
-- The other half of the question above: the statements themselves, with msis's verdict on
-- whether their recorded conditions still held. A statement that needs review is the one to act
-- on - it was true once, and the release it was written for is not the release it was made for.
--
-- :cve  the vulnerability identifier, or '' for every one in the corpus
SELECT p.name            AS product,
       d.subject_version AS release,
       d.assesses        AS inventory,
       v.id              AS vulnerability,
       v.state,
       v.justification,
       v.applicability,
       v.assessed_version,
       v.review_reason,
       (SELECT GROUP_CONCAT(a.ref, ' ')
          FROM vulnerability_affects a
         WHERE a.document_id = v.document_id AND a.ordinal = v.ordinal) AS affects,
       d.source
FROM vulnerability v
JOIN document d ON d.id = v.document_id
JOIN product  p ON p.id = d.product_id
WHERE (:cve = '' OR v.id = :cve)
ORDER BY p.name, d.subject_version, v.id, v.ordinal;

-- name: rejected
--
-- Every document in the corpus that is NOT in this index, and why.
--
-- No parameters.
SELECT source, problem FROM ingest_error ORDER BY source;
