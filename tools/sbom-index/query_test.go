package main

import (
	"fmt"
	"strings"
	"testing"
)

// The product the corpus's two releases describe; its UpgradeCode is authored in
// internal/msiread/testdata/fixture.wxs and is stable across rebuilds of that fixture.
const fixtureProduct = "upgrade:6f1e2c3a-9b4d-4a21-8e77-27b1c0d4af01"

func queryByName(t *testing.T, name string) Query {
	t.Helper()
	all, err := Queries()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range all {
		if q.Name == name {
			return q
		}
	}
	t.Fatalf("queries.sql declares no query %q", name)
	return Query{}
}

func ask(t *testing.T, name string, args map[string]string) ([]string, [][]string) {
	t.Helper()
	db, _, _ := buildIndex(t)
	cols, rows, err := Run(db, queryByName(t, name), args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return cols, rows
}

// EVERY documented query has to run. queries.sql is the documentation, so a query that no
// longer executes cannot be allowed to sit there looking correct - and the list is read from
// the file rather than repeated here, so a query added to it is covered without anyone
// remembering to register it.
func TestEveryDocumentedQueryRuns(t *testing.T) {
	db, _, _ := buildIndex(t)

	all, err := Queries()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 5 {
		t.Fatalf("%d queries parsed from queries.sql, expected the documented set", len(all))
	}

	// A plausible value per parameter name. A query introducing a parameter nobody has
	// supplied fails here rather than silently going untested.
	sample := map[string]string{
		"name":    "payload.txt",
		"version": "",
		"product": fixtureProduct,
		"from":    "urn:cdx:11111111-1111-4111-8111-111111111111/1",
		"to":      "urn:cdx:22222222-2222-4222-8222-222222222222/1",
		"sha256":  "0000000000000000000000000000000000000000000000000000000000000000",
	}

	for _, q := range all {
		t.Run(q.Name, func(t *testing.T) {
			args := map[string]string{}
			for _, p := range q.Params {
				v, ok := sample[p]
				if !ok {
					t.Fatalf("no sample value for parameter %q; add one so this query is "+
						"actually executed", p)
				}
				args[p] = v
			}
			if _, _, err := Run(db, q, args); err != nil {
				t.Errorf("%v", err)
			}
			if q.Comment == "" {
				t.Error("this query has no comment; queries.sql is the documentation")
			}
		})
	}
}

// "Which products ship this DLL version" - across every release, which is what makes it the
// answer to "who is affected" as well.
func TestProductsShippingAComponent(t *testing.T) {
	cols, rows := ask(t, "products-shipping", map[string]string{
		"name": "payload.txt", "version": "",
	})

	// Three rows, not two: release 2.0.0 is described by two documents, and each is a place
	// the component really ships. Collapsing them would hide which artifact it is in.
	if len(rows) != 3 {
		t.Fatalf("%d rows, want one per document that ships it: %v", len(rows), rows)
	}
	col := index(cols, "release")
	releases := map[string]int{}
	sources := map[string]bool{}
	for _, r := range rows {
		releases[r[col]]++
		sources[r[index(cols, "source")]] = true
	}
	if releases["1.0.0"] != 1 || releases["2.0.0"] != 2 {
		t.Errorf("rows per release = %v, want one for 1.0.0 and two for 2.0.0", releases)
	}
	if len(sources) != 3 {
		t.Errorf("%d distinct sources for 3 rows; each row must name the document it is in",
			len(sources))
	}
	if p := rows[0][index(cols, "product")]; p != "MsiReadFixture" {
		t.Errorf("product = %q", p)
	}
	// The install target comes back, because "which products ship this" is usually followed
	// by "and where does it land".
	if tgt := rows[0][index(cols, "install_target")]; !strings.Contains(tgt, "payload.txt") {
		t.Errorf("install_target = %q", tgt)
	}

	// A component nothing ships returns nothing, rather than everything.
	_, none := ask(t, "products-shipping", map[string]string{"name": "nothing.dll", "version": ""})
	if len(none) != 0 {
		t.Errorf("%d rows for a component no product ships", len(none))
	}
}

// A release is not one document. msis's own 3.0.5 ships an x64, an x86 and an arm64 MSI under
// one UpgradeCode and one ProductVersion, and the corpus reproduces that: release 2.0.0 has
// two. Diffing RELEASES joined every document on one side to every document on the other, so
// two unchanged variants came back as two false changes pointing opposite ways.
func TestAReleaseCanHaveSeveralDocuments(t *testing.T) {
	cols, rows := ask(t, "release-documents", map[string]string{"product": fixtureProduct})

	perRelease := map[string]int{}
	for _, r := range rows {
		perRelease[r[index(cols, "release")]]++
	}
	if perRelease["1.0.0"] != 1 {
		t.Errorf("release 1.0.0 has %d documents, want 1", perRelease["1.0.0"])
	}
	if perRelease["2.0.0"] != 2 {
		t.Fatalf("release 2.0.0 has %d documents, want 2 - the ambiguity this query exists "+
			"for is not in the corpus, so nothing here is being tested", perRelease["2.0.0"])
	}
	// Each is distinguishable, or picking one would be guesswork.
	for _, r := range rows {
		if r[index(cols, "subject_artifact")] == "" && r[index(cols, "source")] == "" {
			t.Error("a document is named by neither its artifact nor its source")
		}
	}
}

// "What changed between 4.1 and 4.2" - between two DOCUMENTS, which is the only form of the
// question with one answer. The corpus's two releases differ by exactly one added file, one
// removed file and one whose bytes changed, so all three answers are exercised.
func TestDocumentDiff(t *testing.T) {
	db, _, _ := buildIndex(t)

	from := scalar(t, db, `SELECT id FROM document WHERE source = '1.0.0/fixture.msi.cdx.json'`)
	to := scalar(t, db, `SELECT id FROM document WHERE source = '2.0.0/fixture.msi.cdx.json'`)
	if from == "" || to == "" {
		t.Fatal("the corpus documents are not where the test expects them")
	}

	cols, rows := ask(t, "document-diff", map[string]string{"from": from, "to": to})

	changes := map[string]string{}
	for _, r := range rows {
		changes[r[index(cols, "name")]] = r[index(cols, "change")]
	}
	want := map[string]string{
		"added.txt":  "added",
		"nested.txt": "removed",
		"hidden.txt": "changed",
	}
	for name, kind := range want {
		if changes[name] != kind {
			t.Errorf("%s: change = %q, want %q (all of: %v)", name, changes[name], kind, changes)
		}
	}
	// Unchanged components are NOT listed: an answer to "what changed" that is mostly noise
	// is not an answer. Both these documents have bom-refs throughout, so nothing here is
	// "not comparable" either.
	if len(rows) != len(want) {
		t.Errorf("%d rows, want exactly %d: %v", len(rows), len(want), changes)
	}

	// The digests that moved are shown, which is what makes the answer checkable.
	for _, r := range rows {
		if r[index(cols, "change")] != "changed" {
			continue
		}
		f, tt := r[index(cols, "from_sha256")], r[index(cols, "to_sha256")]
		if f == "" || tt == "" || f == tt {
			t.Errorf("a changed component reports %q -> %q", f, tt)
		}
	}

	// Reversed, added and removed swap - a diff reporting the same either way round would be
	// ignoring one of its arguments.
	cols2, rows2 := ask(t, "document-diff", map[string]string{"from": to, "to": from})
	for _, r := range rows2 {
		if r[index(cols2, "name")] == "added.txt" && r[index(cols2, "change")] != "removed" {
			t.Errorf("reversed, added.txt is %q, want removed", r[index(cols2, "change")])
		}
	}
}

// Two documents of ONE release are two builds of the same thing, so a diff between them finds
// nothing. Before the query took document ids this pair was joined by bom-ref across both
// documents of release 2.0.0 and reported changes that do not exist.
func TestDiffingTwoVariantsOfOneReleaseFindsNoFalseChanges(t *testing.T) {
	db, _, _ := buildIndex(t)

	a := scalar(t, db, `SELECT id FROM document WHERE source = '2.0.0/fixture.msi.cdx.json'`)
	b := scalar(t, db, `SELECT id FROM document WHERE source = '2.0.0/fixture-x86.msi.cdx.json'`)
	if a == "" || b == "" || a == b {
		t.Fatalf("the two variants of release 2.0.0 are not both indexed: %q %q", a, b)
	}

	cols, rows := ask(t, "document-diff", map[string]string{"from": a, "to": b})
	for _, r := range rows {
		t.Errorf("two identical builds of one release differ: %s is %q",
			r[index(cols, "name")], r[index(cols, "change")])
	}
}

// A component with no bom-ref cannot be matched, and must be REPORTED as such rather than
// dropped: an empty result has to mean "nothing changed", not "nothing could be compared".
// The repository's own release inventory is a document of exactly that kind.
func TestUncomparableComponentsAreReportedNotDropped(t *testing.T) {
	db, _, _ := buildIndex(t)

	inventory := scalar(t, db,
		`SELECT id FROM document WHERE source = '1.0.0/release-inventory.cdx.json'`)
	if inventory == "" {
		t.Fatal("the release inventory is not indexed")
	}
	if n := scalar(t, db,
		`SELECT COUNT(*) FROM component WHERE document_id = ? AND bom_ref IS NOT NULL`,
		inventory); n != "0" {
		t.Fatalf("%s of the inventory's components have a bom-ref; the test needs a document "+
			"with none", n)
	}

	// Diffed against itself, nothing can have changed - but every component has to be
	// accounted for, or the empty result would be indistinguishable from "all equal".
	cols, rows := ask(t, "document-diff", map[string]string{"from": inventory, "to": inventory})
	if len(rows) == 0 {
		t.Fatal("a document whose components cannot be matched produced no rows at all, " +
			"which reads as 'nothing changed'")
	}
	for _, r := range rows {
		if got := r[index(cols, "change")]; got != "not comparable" {
			t.Errorf("%s: change = %q, want 'not comparable'", r[index(cols, "name")], got)
		}
	}
	total := scalar(t, db, `SELECT COUNT(*) FROM component WHERE document_id = ?`, inventory)
	if got := len(rows); got == 0 || total != itoa(got) {
		t.Errorf("%d rows for %s components; every unmatched component must be listed",
			got, total)
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// "Does this customer's file set match anything we shipped" - by digest, because that is the
// only field that answers it. #29 D5 requires a SHA-256 on every payload for this reason.
func TestMatchDigest(t *testing.T) {
	db, _, _ := buildIndex(t)

	// A digest the corpus really contains, taken from the index rather than hard-coded, so
	// the test does not go stale when the fixture is rebuilt.
	digest := scalar(t, db, `
		SELECT h.content FROM hash h
		JOIN component c ON c.document_id = h.document_id AND c.ordinal = h.ordinal
		WHERE h.alg = 'SHA-256' AND c.name = 'payload.txt'
		ORDER BY h.content LIMIT 1`)
	if digest == "" {
		t.Fatal("the corpus has no digest for payload.txt")
	}

	cols, rows := ask(t, "match-digest", map[string]string{"sha256": digest})
	if len(rows) == 0 {
		t.Fatal("a digest the corpus contains matched nothing")
	}

	names := map[string]bool{}
	for _, r := range rows {
		names[r[index(cols, "component")]] = true
		if r[index(cols, "release")] == "" {
			t.Error("a match must say which release it is in; that is the question")
		}
		if r[index(cols, "product")] == "" {
			t.Error("a match must say which product it is in")
		}
	}
	if !names["payload.txt"] {
		t.Errorf("the file the digest was taken from is not among the matches: %v", names)
	}

	// The question is about BYTES, so everything carrying them comes back. The fixture uses
	// payload.txt as its Binary-table stream, so one digest really does belong to two
	// differently named components in two different roles - and a customer's file matching
	// either of them is a true answer. Filtering by name would have hidden one of them.
	if !names["FixtureBinary"] {
		t.Errorf("the same bytes ship as a Binary stream too, and that match was not "+
			"returned: %v", names)
	}

	// Every row really carries the digest asked for - the join must not have widened.
	for _, r := range rows {
		n := scalar(t, db, `
			SELECT COUNT(*) FROM hash h
			JOIN component c ON c.document_id = h.document_id AND c.ordinal = h.ordinal
			WHERE h.alg = 'SHA-256' AND h.content = ? AND c.name = ?`,
			digest, r[index(cols, "component")])
		if n == "0" {
			t.Errorf("%q was returned but carries no such digest", r[index(cols, "component")])
		}
	}

	// Bytes nobody shipped match nothing. A query that matched anything would be worse than
	// no query: it would clear a file that was never ours.
	_, none := ask(t, "match-digest", map[string]string{
		"sha256": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	})
	if len(none) != 0 {
		t.Errorf("%d rows for bytes the corpus does not contain", len(none))
	}
}

// Coverage is asked first, not last: every other answer is bounded by it, and a rejected
// document is a release the other queries silently know nothing about.
func TestCoverageReportsWhatTheIndexDoesNotCover(t *testing.T) {
	cols, rows := ask(t, "coverage", nil)

	got := map[string]string{}
	for _, r := range rows {
		got[r[index(cols, "item")]] = r[index(cols, "value")]
	}
	for item, want := range map[string]string{
		"documents indexed":                     "5",
		"documents rejected":                    "1",
		"products":                              "3",
		"BOM-Links that resolve in this corpus": "1",
		"BOM-Links that do not":                 "0",
	} {
		if got[item] != want {
			t.Errorf("%s = %q, want %q (all: %v)", item, got[item], want, got)
		}
	}
	// The two honest gaps the corpus really has.
	if got["components with no SHA-256"] == "0" {
		t.Error("the corpus does contain components with no SHA-256; coverage must say so")
	}
	if got["payloads the artifact does not carry"] == "0" {
		t.Error("the bundle has payloads it does not carry; coverage must say so")
	}
}

// Parameters are required, not defaulted: `-query match-digest` with no -arg must not return
// nothing and look like the answer.
func TestAMissingParameterIsRefused(t *testing.T) {
	db, _, _ := buildIndex(t)

	_, _, err := Run(db, queryByName(t, "match-digest"), nil)
	if err == nil {
		t.Fatal("a query run without its parameter must fail, not return no rows")
	}
	if !strings.Contains(err.Error(), ":sha256") {
		t.Errorf("error = %v, want it to name the missing parameter", err)
	}

	// A parameter the query does not take is a typo, and a typo that is ignored is a wrong
	// answer: `-arg sha255=...` would otherwise match nothing and look like a clean bill.
	//
	// The REQUIRED parameter is supplied here as well. Passing only the misspelt one would
	// fail for the other reason - the required one being absent - and the check above
	// already covers that, so this would have proved nothing.
	_, _, err = Run(db, queryByName(t, "match-digest"), map[string]string{
		"sha256": "0000000000000000000000000000000000000000000000000000000000000000",
		"sha255": "x",
	})
	if err == nil {
		t.Fatal("an unknown parameter must be refused")
	}
	if !strings.Contains(err.Error(), "sha255") {
		t.Errorf("error = %v, want it to name the parameter that does not exist", err)
	}
}

// The parser has to find the parameters, or the check above would pass every query vacuously.
func TestQueryParametersAreParsed(t *testing.T) {
	cases := map[string][]string{
		"coverage":          nil,
		"rejected":          nil,
		"match-digest":      {"sha256"},
		"products-shipping": {"name", "version"},
		"document-diff":     {"from", "to"},
		"release-documents": {"product"},
	}
	for name, want := range cases {
		q := queryByName(t, name)
		if strings.Join(q.Params, ",") != strings.Join(want, ",") {
			t.Errorf("%s parameters = %v, want %v", name, q.Params, want)
		}
	}
}

func index(cols []string, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	return -1
}
