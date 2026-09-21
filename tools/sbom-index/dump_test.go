package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// Dump is the measuring instrument for "built, deleted, rebuilt - identical contents", and an
// unverified measuring instrument is how that acceptance criterion passes while meaning
// nothing. These check the instrument rather than the index.

func scratchDB(t *testing.T, statements ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

// The dump must not depend on the order rows were inserted in. Without an explicit ORDER BY it
// would be relying on SQLite's scan order, which is not part of its contract - so two databases
// holding the same rows could dump differently and a rebuild check would fail, or worse, two
// databases holding DIFFERENT rows could dump the same.
func TestTheDumpDoesNotDependOnInsertionOrder(t *testing.T) {
	const ddl = `CREATE TABLE t (a TEXT PRIMARY KEY, b TEXT)`

	forward := scratchDB(t, ddl,
		`INSERT INTO t VALUES ('a','1')`,
		`INSERT INTO t VALUES ('b','2')`,
		`INSERT INTO t VALUES ('c','3')`)
	backward := scratchDB(t, ddl,
		`INSERT INTO t VALUES ('c','3')`,
		`INSERT INTO t VALUES ('b','2')`,
		`INSERT INTO t VALUES ('a','1')`)

	a, err := Dump(forward)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Dump(backward)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("the same rows dumped differently depending on insertion order:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, `"a" "1"`) || !strings.Contains(a, "3 row(s)") {
		t.Errorf("the dump does not contain the rows it is meant to:\n%s", a)
	}
}

// ...and it must still tell different contents apart, or "identical" would be worthless.
func TestTheDumpDistinguishesDifferentContents(t *testing.T) {
	const ddl = `CREATE TABLE t (a TEXT PRIMARY KEY, b TEXT)`

	one := scratchDB(t, ddl, `INSERT INTO t VALUES ('a','1')`)
	two := scratchDB(t, ddl, `INSERT INTO t VALUES ('a','2')`)

	a, _ := Dump(one)
	b, _ := Dump(two)
	if a == b {
		t.Error("two databases with different values dumped identically")
	}

	// An extra row, and a missing table, are both differences the rebuild check has to see.
	three := scratchDB(t, ddl, `INSERT INTO t VALUES ('a','1')`, `INSERT INTO t VALUES ('b','1')`)
	if c, _ := Dump(three); c == a {
		t.Error("an extra row did not change the dump")
	}
	four := scratchDB(t, ddl, `INSERT INTO t VALUES ('a','1')`, `CREATE TABLE u (x TEXT)`)
	if d, _ := Dump(four); d == a {
		t.Error("an added table did not change the dump; tables must be discovered, not listed")
	}
}

// NULL and the empty string are different answers throughout this schema - "the document does
// not say" versus "the document says nothing" - so a dump that rendered them alike would hide
// a regression between them.
func TestTheDumpDistinguishesNullFromEmpty(t *testing.T) {
	const ddl = `CREATE TABLE t (a TEXT PRIMARY KEY, b TEXT)`

	null := scratchDB(t, ddl, `INSERT INTO t VALUES ('a', NULL)`)
	empty := scratchDB(t, ddl, `INSERT INTO t VALUES ('a', '')`)

	a, _ := Dump(null)
	b, _ := Dump(empty)
	if a == b {
		t.Error("NULL and the empty string dumped identically")
	}
	if !strings.Contains(a, `"a" NULL`) {
		t.Errorf("a NULL is not marked as one:\n%s", a)
	}
	// ...and a value that SPELLS "NULL" is still distinguishable from an actual NULL, because
	// every non-NULL value is quoted and the token is not.
	literal := scratchDB(t, ddl, `INSERT INTO t VALUES ('a', 'NULL')`)
	c, err := Dump(literal)
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Error(`the string "NULL" dumped the same as an actual NULL`)
	}
}

// A database with no tables cannot be compared to anything, so saying so beats returning an
// empty string that would equal another empty string.
func TestDumpingNothingIsAnError(t *testing.T) {
	if _, err := Dump(scratchDB(t)); err == nil {
		t.Fatal("dumping a database with no tables must fail rather than return nothing")
	}
}

// The delimiter must not be forgeable. With cells joined by " | " and left unescaped, the rows
// ("a | b", "c") and ("a", "b | c") serialised identically - two different databases dumping
// the same, in the one function the rebuild acceptance check is measured with.
func TestTheDumpCannotBeCollidedThroughItsDelimiter(t *testing.T) {
	const ddl = `CREATE TABLE t (k TEXT PRIMARY KEY, a TEXT, b TEXT)`

	newline := "a\nb"
	pairs := [][2][2]string{
		{{"a | b", "c"}, {"a", "b | c"}},
		{{"a b", "c"}, {"a", "b c"}},
		{{`a" "b`, "c"}, {"a", "b"}},
		{{newline, "c"}, {"a", "b\nc"}},
	}

	for _, pair := range pairs {
		one := scratchDB(t, ddl, insert(pair[0]))
		two := scratchDB(t, ddl, insert(pair[1]))
		a, err := Dump(one)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Dump(two)
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Errorf("%q and %q dumped identically:\n%s", pair[0], pair[1], a)
		}
	}
}

func insert(cells [2]string) string {
	return `INSERT INTO t VALUES ('k', '` + esc(cells[0]) + `', '` + esc(cells[1]) + `')`
}

// esc quotes a value for a SQL string literal.
func esc(s string) string { return strings.ReplaceAll(s, "'", "''") }
