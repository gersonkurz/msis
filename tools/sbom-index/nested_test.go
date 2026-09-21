package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// CycloneDX lets a component contain components - an assembly holding the files it is made of.
// msis emits none, but the index consumes documents msis did not write, and such a document
// PASSES validation. Walking only the top-level array made its children vanish while the
// document reported clean: a digest lookup returned a false negative and coverage said nothing
// was missing, which is the worst combination available.
//
// The supplier on `inner.dll` is named NOWHERE ELSE in this document, deliberately. Suppliers
// are registered in a pass before components are written, and that pass walked only the top
// level - so a supplier found solely on a child was never registered and the child's foreign
// key failed, aborting the whole build. Reading the nesting and writing it are two halves of
// one fix and the first fixture had neither half under test.
const nestedDocument = `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "serialNumber": "urn:uuid:55555555-5555-4555-8555-555555555555",
  "version": 1,
  "metadata": {
    "timestamp": "2026-09-21T12:00:00Z",
    "component": { "type": "application", "bom-ref": "root", "name": "Assembly", "version": "1.0" }
  },
  "components": [
    {
      "type": "library", "bom-ref": "outer", "name": "outer.dll",
      "hashes": [{"alg":"SHA-256","content":"1111111111111111111111111111111111111111111111111111111111111111"}],
      "components": [
        {
          "type": "file", "bom-ref": "inner", "name": "inner.dll",
          "supplier": { "name": "Only Named On A Child" },
          "hashes": [{"alg":"SHA-256","content":"2222222222222222222222222222222222222222222222222222222222222222"}],
          "properties": [{"name":"msis:role","value":"payload"}],
          "components": [
            {
              "type": "file", "bom-ref": "deepest", "name": "deepest.dll",
              "hashes": [{"alg":"SHA-256","content":"3333333333333333333333333333333333333333333333333333333333333333"}]
            }
          ]
        }
      ]
    },
    { "type": "file", "bom-ref": "sibling", "name": "sibling.dll",
      "hashes": [{"alg":"SHA-256","content":"4444444444444444444444444444444444444444444444444444444444444444"}] }
  ]
}`

func nestedIndex(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpus, "nested.cdx.json"),
		[]byte(nestedDocument), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Build(corpus, filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failures) != 0 {
		t.Fatalf("the nested document was rejected: %+v", report.Failures)
	}
	if report.Components != 4 {
		t.Errorf("the report counts %d components, want all 4 including the nested ones",
			report.Components)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNestedComponentsAreIndexed(t *testing.T) {
	db := nestedIndex(t)

	if n := scalar(t, db, `SELECT COUNT(*) FROM component`); n != "4" {
		t.Fatalf("%s components indexed, want 4 - nesting is three deep plus a sibling", n)
	}
	for _, name := range []string{"outer.dll", "inner.dll", "deepest.dll", "sibling.dll"} {
		if n := scalar(t, db, `SELECT COUNT(*) FROM component WHERE name = ?`, name); n != "1" {
			t.Errorf("%s is not in the index", name)
		}
	}

	// Their digests are indexed too, or "does this customer's file match" would return a
	// false negative for a file we really did ship.
	for _, digest := range []string{
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
	} {
		if n := scalar(t, db,
			`SELECT COUNT(*) FROM hash WHERE alg = 'SHA-256' AND content = ?`, digest); n != "1" {
			t.Errorf("the digest of a nested component is not indexed: %s", digest)
		}
	}

	// ...and so are their properties.
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM component WHERE name = 'inner.dll' AND role = 'payload'`); n != "1" {
		t.Error("a nested component's promoted property was dropped")
	}
}

// Flattening must not lose the containment, or the document's shape is gone.
func TestNestingIsRecordedNotFlattenedAway(t *testing.T) {
	db := nestedIndex(t)

	for _, tc := range []struct{ name, parent, depth string }{
		{"outer.dll", "", "0"},
		{"inner.dll", "outer", "1"},
		{"deepest.dll", "inner", "2"},
		{"sibling.dll", "", "0"},
	} {
		var parent sql.NullString
		var depth int
		err := db.QueryRow(`SELECT parent_ref, depth FROM component WHERE name = ?`, tc.name).
			Scan(&parent, &depth)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.parent == "" {
			if parent.Valid {
				t.Errorf("%s: parent_ref = %q, want NULL (it is top-level)", tc.name, parent.String)
			}
		} else if parent.String != tc.parent {
			t.Errorf("%s: parent_ref = %q, want %q", tc.name, parent.String, tc.parent)
		}
		if got := itoa(depth); got != tc.depth {
			t.Errorf("%s: depth = %s, want %s", tc.name, got, tc.depth)
		}
	}
}

// The flattening order is depth-first in document order, so two builds of one corpus assign the
// same ordinals - which is what makes the ordinal usable as an address at all.
func TestTheFlatteningOrderIsDeterministic(t *testing.T) {
	db := nestedIndex(t)

	rows, err := db.Query(`SELECT ordinal, name FROM component ORDER BY ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var ord int
		var name string
		if err := rows.Scan(&ord, &name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{"outer.dll", "inner.dll", "deepest.dll", "sibling.dll"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("flattened order = %v, want depth-first document order %v", got, want)
		}
	}
}

// A supplier named only on a nested component must still reach the supplier table. It is the
// half of the nesting fix that writes rows: without it the child's foreign key fails and the
// ENTIRE build aborts, so one such document in a corpus would take every other document with it.
func TestASupplierNamedOnlyOnANestedComponentIsRegistered(t *testing.T) {
	db := nestedIndex(t)

	if n := scalar(t, db,
		`SELECT COUNT(*) FROM supplier WHERE name = 'Only Named On A Child'`); n != "1" {
		t.Errorf("a supplier named only on a nested component is not in the supplier table")
	}
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM component WHERE name = 'inner.dll' AND supplier = 'Only Named On A Child'`); n != "1" {
		t.Errorf("the nested component does not carry its supplier")
	}
	// And the document really does name it nowhere else, or the test would pass on the
	// top-level pass having picked it up.
	data := nestedDocument
	if strings.Count(data, "Only Named On A Child") != 1 {
		t.Errorf("the fixture names that supplier %d times; it must appear only on the child",
			strings.Count(data, "Only Named On A Child"))
	}
}
