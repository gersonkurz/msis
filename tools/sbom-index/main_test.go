package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// An incomplete build must stay incomplete however the command is spelled. Returning the
// failure only when no query followed meant `-corpus X -query coverage` could reject a
// document, print its answer and exit 0 - and a build script checking the exit status would
// have been told the corpus was fully indexed.
func TestAskingAQueryDoesNotForgiveAnIncompleteBuild(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "x.db")

	// Build alone fails, because the committed corpus holds one invalid document.
	err := run(corpusDir(), db, "", false, false, argList{})
	if err == nil {
		t.Fatal("a corpus with a rejected document must not build successfully")
	}
	if !strings.Contains(err.Error(), "not indexed") {
		t.Errorf("error = %v, want it to say documents were not indexed", err)
	}

	// Build plus a query still fails, and the query still ran.
	for _, tc := range []struct {
		name  string
		query string
		args  argList
	}{
		{"coverage", "coverage", argList{}},
		{"rejected", "rejected", argList{}},
		{"explain", "coverage", argList{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(corpusDir(), filepath.Join(dir, tc.name+".db"), tc.query,
				false, tc.name == "explain", tc.args)
			if err == nil {
				t.Fatal("asking a question turned an incomplete build into a successful one")
			}
			if !strings.Contains(err.Error(), "not indexed") {
				t.Errorf("error = %v, want the ingestion failure to survive", err)
			}
		})
	}
}

// A clean corpus must not be reported as incomplete, or the check above would be satisfied by
// a command that always fails.
func TestACleanCorpusSucceeds(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(corpusDir(), "1.0.0", "fixture.msi.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpus, "one.cdx.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(corpus, filepath.Join(dir, "x.db"), "", false, false, argList{}); err != nil {
		t.Errorf("a corpus with nothing wrong in it failed: %v", err)
	}
	if err := run(corpus, filepath.Join(dir, "y.db"), "coverage", false, false, argList{}); err != nil {
		t.Errorf("a clean build plus a query failed: %v", err)
	}
}

// -queries lists what there is to ask, and needs no database - it is what a reader runs first.
func TestListingQueriesNeedsNoDatabase(t *testing.T) {
	if err := run("", "", "", true, false, argList{}); err != nil {
		t.Errorf("listing the queries failed: %v", err)
	}
}

// Naming a query that does not exist says so, and says what does exist.
func TestAnUnknownQueryIsNamed(t *testing.T) {
	dir := t.TempDir()
	err := run("", filepath.Join(dir, "x.db"), "no-such-query", false, false, argList{})
	if err == nil {
		t.Fatal("an unknown query must be refused")
	}
	for _, want := range []string{"no-such-query", "coverage"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// Neither -corpus nor -db is a usage error, not an empty index built somewhere unexpected.
func TestNeitherCorpusNorDatabaseIsAnError(t *testing.T) {
	if err := run("", "", "", false, false, argList{}); err == nil {
		t.Fatal("with neither -corpus nor -db there is nothing to do; say so")
	}
}

// Go's flag package stops parsing at the first positional argument, so anything after it -
// including -query - is silently ignored and the command exits 0 having done nothing. An
// unquoted path containing a space produces exactly that, which is how a quoting mistake in a
// recipe became a successful no-op rather than an error.
func TestPositionalArgumentsAreRefused(t *testing.T) {
	if err := rejectPositional(nil); err != nil {
		t.Errorf("no positional arguments is the normal case: %v", err)
	}
	if err := rejectPositional([]string{}); err != nil {
		t.Errorf("an empty list is not an error: %v", err)
	}

	// The shape a split path arrives in: the tail of "C:\My Projects\dist" as its own
	// argument, with every later flag swallowed.
	const stray = `Projects\dist`
	err := rejectPositional([]string{stray, "-query", "coverage"})
	if err == nil {
		t.Fatal("a stray positional argument must be refused, not ignored")
	}
	// Quoted as %q renders it - a Windows path's backslashes come back doubled, so looking
	// for the raw string would fail for a reason that has nothing to do with the guard.
	for _, want := range []string{strconv.Quote(stray), "quoted"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %s", err, want)
		}
	}
}
