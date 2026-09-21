package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The corpus is real msis output: two releases of one product - the second described by TWO
// documents, as msis's own releases are - a bundle whose BOM-Link points at one of them, a
// release-inventory document that carries neither a serial number nor a single bom-ref, and
// one document that is not valid CycloneDX at all. See testdata/README.md.
func corpusDir() string { return filepath.Join("testdata", "corpus") }

func buildIndex(t *testing.T) (*sql.DB, *Report, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sbom-index.db")
	report, err := Build(corpusDir(), path)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, report, path
}

func scalar(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v.String
}

// The acceptance criterion: built, deleted, rebuilt - identical contents.
//
// Compared on the canonical dump, not on the file: SQLite's page layout and freelist
// legitimately differ between two runs that produce the same rows, so comparing bytes would
// fail for a reason that has nothing to do with the index being reproducible.
func TestRebuildingProducesIdenticalContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sbom-index.db")

	first := buildAndDump(t, path)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the database was not deleted: %v", err)
	}

	second := buildAndDump(t, path)

	if first != second {
		t.Errorf("a rebuild produced different contents:\n--- first\n%s\n--- second\n%s",
			first, second)
	}
	// A dump of an empty database would also compare equal, so the comparison is only
	// evidence if there was something in it.
	for _, want := range []string{"== document", "== component", "== hash", "== relationship"} {
		if !strings.Contains(first, want) {
			t.Errorf("the dump has no %s section, so the comparison proves little", want)
		}
	}
	if strings.Count(first, "\n") < 50 {
		t.Errorf("the dump is %d lines; the corpus should produce far more",
			strings.Count(first, "\n"))
	}
}

// And rebuilding over an existing file must replace it rather than add to it: the corpus is
// authoritative, so a document deleted from it has to disappear from the index too.
func TestRebuildingOverAnExistingIndexReplacesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sbom-index.db")
	if _, err := Build(corpusDir(), path); err != nil {
		t.Fatal(err)
	}
	before := buildAndDump(t, path) // built a second time, over itself

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	n := scalar(t, db, `SELECT COUNT(*) FROM document`)
	db.Close()
	if n != "5" {
		t.Errorf("%s documents after rebuilding in place, want 5 - rows were added, not replaced", n)
	}
	if before == "" {
		t.Error("empty dump")
	}
}

func buildAndDump(t *testing.T, path string) string {
	t.Helper()
	if _, err := Build(corpusDir(), path); err != nil {
		t.Fatalf("Build: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dump, err := Dump(db)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	return dump
}

// "A document that fails validation is reported, not skipped." Three ways, because a build
// script reads one of them: the returned report, the terminal, and - the one that matters
// afterwards - the index itself, so that a query returning nothing can be told apart from a
// query whose evidence never made it in.
func TestAnInvalidDocumentIsReportedNotSkipped(t *testing.T) {
	db, report, _ := buildIndex(t)

	if len(report.Failures) != 1 {
		t.Fatalf("%d failures reported, want 1: %+v", len(report.Failures), report.Failures)
	}
	f := report.Failures[0]
	if f.Source != "2.0.0/broken.cdx.json" {
		t.Errorf("failure source = %q", f.Source)
	}
	if !strings.Contains(f.Problem, "not a valid CycloneDX document") {
		t.Errorf("problem = %q, want it to say the document is not valid", f.Problem)
	}

	// Recorded in the database, not only printed.
	if got := scalar(t, db, `SELECT problem FROM ingest_error WHERE source = ?`, f.Source); got == "" {
		t.Error("the rejected document is not in ingest_error, so the index cannot say it is incomplete")
	}
	// ...and genuinely not indexed, so "reported" does not mean "reported and then used".
	if n := scalar(t, db, `SELECT COUNT(*) FROM document WHERE source = ?`, f.Source); n != "0" {
		t.Errorf("the invalid document was indexed anyway (%s rows)", n)
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM component WHERE name = 'wrong.dll'`); n != "0" {
		t.Errorf("a component from the invalid document reached the index (%s rows)", n)
	}

	// The valid documents around it still loaded: one bad file must not cost the corpus.
	if n := scalar(t, db, `SELECT COUNT(*) FROM document`); n != "5" {
		t.Errorf("%s documents indexed, want 5", n)
	}
}

// A document may carry no serialNumber and no bom-refs - the repository's own release
// inventory is exactly that - so the keys have to work without them. The derived key is
// content-based rather than positional, or it would not survive a rebuild.
func TestADocumentWithNoSerialIsStillKeyed(t *testing.T) {
	db, _, _ := buildIndex(t)

	const source = "1.0.0/release-inventory.cdx.json"
	id := scalar(t, db, `SELECT id FROM document WHERE source = ?`, source)
	if id == "" {
		t.Fatal("the release inventory was not indexed")
	}

	// The key is the digest of the file, computed here independently. "Starts with sha256:"
	// would also be true of a constant, and a constant would collide the moment a corpus
	// held two documents with no serial - so the value is checked, not its shape.
	data, err := os.ReadFile(filepath.Join(corpusDir(), filepath.FromSlash(source)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if want := "sha256:" + hex.EncodeToString(sum[:]); id != want {
		t.Errorf("id = %q, want %q - the key must be derived from the document's own bytes", id, want)
	}
	if scalar(t, db, `SELECT sha256 FROM document WHERE id = ?`, id) != hex.EncodeToString(sum[:]) {
		t.Error("the recorded document digest does not match the file")
	}
	// The real field stays NULL, so a consumer can tell a derived key from a declared one.
	var serial sql.NullString
	if err := db.QueryRow(`SELECT serial FROM document WHERE id = ?`, id).Scan(&serial); err != nil {
		t.Fatal(err)
	}
	if serial.Valid {
		t.Errorf("serial = %q, want NULL: the document declares none", serial.String)
	}
	// Its components have no bom-ref either, and are addressed by ordinal.
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM component WHERE document_id = ? AND bom_ref IS NULL`, id); n == "0" {
		t.Error("a document whose components have no bom-ref indexed none of them")
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM component WHERE document_id = ?`, id); n != "15" {
		t.Errorf("%s components from the release inventory, want 15", n)
	}
}

// A document with a serial is keyed by the REVISION it is - serial and version together -
// spelled exactly as the BOM-Link that addresses it. The serial alone identifies a document,
// not a revision of it, and CycloneDX lets revisions coexist.
func TestADocumentWithASerialIsKeyedByItsRevision(t *testing.T) {
	db, _, _ := buildIndex(t)

	id := scalar(t, db, `SELECT id FROM document WHERE source = '1.0.0/fixture.msi.cdx.json'`)
	serial := scalar(t, db, `SELECT serial FROM document WHERE id = ?`, id)
	version := scalar(t, db, `SELECT version FROM document WHERE id = ?`, id)

	if serial == "" {
		t.Fatal("the document declares a serial and it was not recorded")
	}
	want := "urn:cdx:" + strings.TrimPrefix(serial, "urn:uuid:") + "/" + version
	if id != want {
		t.Errorf("id = %q, want %q - the key is the link that addresses this revision", id, want)
	}
	// The serial stays its own column: the id answers "how is this addressed", the serial
	// answers "which document is this a revision of".
	if !strings.HasPrefix(serial, "urn:uuid:") {
		t.Errorf("serial = %q", serial)
	}
}

// Two revisions of one document are legitimate - they differ in `version` - and must both be
// indexed. Keyed by serial alone they collided as duplicates and NEITHER was indexed, which
// silently removed a real document from the corpus.
func TestTwoRevisionsOfOneDocumentCoexist(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}

	original, err := os.ReadFile(filepath.Join(corpusDir(), "1.0.0", "fixture.msi.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpus, "v1.cdx.json"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	// The same document, revised: same serial, version 2.
	revised := strings.Replace(string(original), `"version": 1,`, `"version": 2,`, 1)
	if revised == string(original) {
		t.Fatal("could not produce a second revision; the fixture's shape changed")
	}
	if err := os.WriteFile(filepath.Join(corpus, "v2.cdx.json"), []byte(revised), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Build(corpus, filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failures) != 0 {
		t.Fatalf("two revisions were rejected: %+v", report.Failures)
	}
	if report.Documents != 2 {
		t.Fatalf("%d documents indexed, want both revisions", report.Documents)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := scalar(t, db, `SELECT COUNT(DISTINCT id) FROM document`); n != "2" {
		t.Errorf("%s distinct document ids, want 2", n)
	}
	if n := scalar(t, db, `SELECT COUNT(DISTINCT serial) FROM document`); n != "1" {
		t.Errorf("%s distinct serials, want 1: they are revisions of one document", n)
	}
}

// A BOM-Link asks for a particular revision. Answering with a different one is worse than
// answering "not in this corpus", because the link then looks satisfied.
func TestABOMLinkToAnAbsentRevisionDoesNotResolve(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}

	// The bundle and the MSI it links to, but the MSI bumped to revision 2 - so the link,
	// which names revision 1, no longer has a target.
	bundle, err := os.ReadFile(filepath.Join(corpusDir(), "1.0.0", "fixture.exe.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	msi, err := os.ReadFile(filepath.Join(corpusDir(), "1.0.0", "fixture.msi.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bundle), "/1") {
		t.Fatal("the bundle's link does not name revision 1; the fixture changed")
	}
	revised := strings.Replace(string(msi), `"version": 1,`, `"version": 2,`, 1)

	for name, data := range map[string]string{
		"bundle.cdx.json": string(bundle),
		"msi.cdx.json":    revised,
	} {
		if err := os.WriteFile(filepath.Join(corpus, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Build(corpus, filepath.Join(dir, "x.db")); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var resolved sql.NullString
	err = db.QueryRow(`SELECT resolved_document_id FROM relationship WHERE kind = 'bom-link'`).
		Scan(&resolved)
	if err != nil {
		t.Fatalf("the link is not recorded at all: %v", err)
	}
	if resolved.Valid {
		t.Errorf("a link naming revision 1 resolved to %q, though only revision 2 is here",
			resolved.String)
	}
	// ...and with revision 1 present it does resolve, so this is about the version and not
	// about links being broken generally.
	if err := os.WriteFile(filepath.Join(corpus, "msi.cdx.json"), msi, 0o644); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Build(corpus, filepath.Join(dir, "y.db")); err != nil {
		t.Fatal(err)
	}
	db2, err := sql.Open("sqlite", filepath.Join(dir, "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.QueryRow(`SELECT resolved_document_id FROM relationship WHERE kind='bom-link'`).
		Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.Valid {
		t.Error("with the revision it names present, the link must resolve")
	}
}

// A BOM-Link is recorded whether or not its target is in the corpus, and resolution is a
// column rather than a filter: "this link points outside what we hold" is the answer an
// auditor needs, and a link silently dropped would look like no link at all.
func TestABOMLinkResolvesToTheDocumentItNames(t *testing.T) {
	db, _, _ := buildIndex(t)

	var from, to, resolved sql.NullString
	err := db.QueryRow(`
		SELECT from_ref, to_ref, resolved_document_id
		FROM relationship WHERE kind = 'bom-link'`).Scan(&from, &to, &resolved)
	if err != nil {
		t.Fatalf("the bundle's BOM-Link is not in the index: %v", err)
	}
	if !strings.HasPrefix(to.String, "urn:cdx:") {
		t.Errorf("to_ref = %q, want a BOM-Link", to.String)
	}
	if !resolved.Valid {
		t.Fatalf("the link %q did not resolve, though its target is in the corpus", to.String)
	}
	// It must resolve to the MSI document, not to itself or to anything else.
	source := scalar(t, db, `SELECT source FROM document WHERE id = ?`, resolved.String)
	if source != "1.0.0/fixture.msi.cdx.json" {
		t.Errorf("the link resolved to %q, want the MSI document beside the bundle", source)
	}
}

// Two releases of one product share a product row, because the UpgradeCode is what Windows
// Installer keeps constant across releases. A ProductCode changes every build, so keying on
// that would make every release a different product and the release-diff query impossible.
func TestReleasesOfOneProductShareAProduct(t *testing.T) {
	db, _, _ := buildIndex(t)

	id := scalar(t, db,
		`SELECT product_id FROM document WHERE source = '1.0.0/fixture.msi.cdx.json'`)
	other := scalar(t, db,
		`SELECT product_id FROM document WHERE source = '2.0.0/fixture.msi.cdx.json'`)
	if id != other || id == "" {
		t.Fatalf("the two releases have product ids %q and %q; they are one product", id, other)
	}
	if !strings.HasPrefix(id, "upgrade:") {
		t.Errorf("product id = %q, want it derived from the UpgradeCode", id)
	}

	// Their ProductCodes really do differ, so this is not passing by accident.
	a := scalar(t, db, `SELECT value FROM document_property
		WHERE document_id = (SELECT id FROM document WHERE source = '1.0.0/fixture.msi.cdx.json')
		  AND name = 'msis:msi.productCode'`)
	b := scalar(t, db, `SELECT value FROM document_property
		WHERE document_id = (SELECT id FROM document WHERE source = '2.0.0/fixture.msi.cdx.json')
		  AND name = 'msis:msi.productCode'`)
	if a == "" || a == b {
		t.Errorf("the two releases have ProductCodes %q and %q; the test needs them to differ", a, b)
	}

	// And the view over releases sees both.
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM product_version WHERE product_id = ?`, id); n != "2" {
		t.Errorf("%s releases of the product, want 2", n)
	}
}

// A document with no UpgradeCode falls back to its subject's name, and the key says which rule
// produced it - a consumer must not have to assume it got the strong one.
func TestAProductWithNoUpgradeCodeSaysSo(t *testing.T) {
	db, _, _ := buildIndex(t)
	id := scalar(t, db,
		`SELECT product_id FROM document WHERE source = '1.0.0/release-inventory.cdx.json'`)
	if !strings.HasPrefix(id, "name:") {
		t.Errorf("product id = %q, want the name-based fallback to be visible in the key", id)
	}
	var code sql.NullString
	if err := db.QueryRow(`SELECT upgrade_code FROM product WHERE id = ?`, id).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code.Valid {
		t.Errorf("upgrade_code = %q, want NULL", code.String)
	}
}

// A property promoted to a component column is not also left in component_property. One fact,
// one place: two copies can disagree, and a query would then have two answers.
func TestPromotedPropertiesAreNotStoredTwice(t *testing.T) {
	db, _, _ := buildIndex(t)

	for name := range promoted {
		if n := scalar(t, db,
			`SELECT COUNT(*) FROM component_property WHERE name = ?`, name); n != "0" {
			t.Errorf("%s is a component column but is also in component_property (%s rows)", name, n)
		}
	}
	// ...and it really did land in the column, so "not duplicated" is not "dropped".
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM component WHERE role IS NOT NULL`); n == "0" {
		t.Error("no component has a role; the promotion dropped the property instead of moving it")
	}
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM component WHERE install_target IS NOT NULL`); n == "0" {
		t.Error("no component has an install target")
	}
	// carried is a three-state column: true, false, and "the document does not say".
	if n := scalar(t, db, `SELECT COUNT(*) FROM component WHERE carried = 0`); n == "0" {
		t.Error("no component is marked as not carried, though the bundle has two")
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM component WHERE carried IS NULL`); n == "0" {
		t.Error("no component has carried NULL; absent must not be stored as false")
	}
}

// Unpromoted properties are kept whole, so a property added to a future document is indexed
// without a schema change. The index must not quietly drop what it does not recognise.
func TestUnpromotedPropertiesAreKept(t *testing.T) {
	db, _, _ := buildIndex(t)
	for _, name := range []string{
		"msis:msi.component", "msis:msi.fileKey", "msis:identity", "msis:burn.packageId",
	} {
		if n := scalar(t, db,
			`SELECT COUNT(*) FROM component_property WHERE name = ?`, name); n == "0" {
			t.Errorf("no component carries %s; the index dropped it", name)
		}
	}
}

// Two documents claiming one identity cannot both be indexed, and picking one silently would
// make a link to that identity resolve to whichever was inserted last.
func TestDuplicateIdentitiesAreRejectedNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(filepath.Join(corpus, "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	original, err := os.ReadFile(filepath.Join(corpusDir(), "1.0.0", "fixture.msi.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.cdx.json", filepath.Join("b", "copy.cdx.json")} {
		if err := os.WriteFile(filepath.Join(corpus, name), original, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := Build(corpus, filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Documents != 0 {
		t.Errorf("%d documents indexed, want 0: neither copy can be chosen", report.Documents)
	}
	if len(report.Failures) != 2 {
		t.Fatalf("%d failures, want both copies reported: %+v", len(report.Failures), report.Failures)
	}
	for _, f := range report.Failures {
		if !strings.Contains(f.Problem, "shares its identity") {
			t.Errorf("%s: problem = %q", f.Source, f.Problem)
		}
	}
}

// An empty corpus is not an error, but it must not look like a successful index of something.
func TestAnEmptyCorpusIndexesNothing(t *testing.T) {
	dir := t.TempDir()
	report, err := Build(dir, filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatalf("an empty corpus is not an error: %v", err)
	}
	if report.Documents != 0 || len(report.Failures) != 0 {
		t.Errorf("report = %+v, want nothing indexed and nothing rejected", report)
	}
}

// A corpus directory that is not there is an error, not an empty index: silently producing a
// database describing nothing is how a build script reports success having indexed nothing.
func TestAMissingCorpusIsAnError(t *testing.T) {
	dir := t.TempDir()
	if _, err := Build(filepath.Join(dir, "nope"), filepath.Join(dir, "x.db")); err == nil {
		t.Fatal("a corpus directory that does not exist must fail")
	}
}
