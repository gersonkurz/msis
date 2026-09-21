package main

import (
	"database/sql"
	"strings"
	"testing"
)

// VEX in the index (#37).
//
// The corpus carries three VEX documents, and the first two are produced BY THE REAL EVALUATOR
// from the inventories they annotate (see testdata/gen.go). That matters: a hand-written sidecar
// once carried the product identity `vex.Apply` was failing to copy, so the index joined it to
// the right product and the defect was invisible.
//
//	1.0.0/fixture.msi.vex       three statements, evaluated against release 1's inventory
//	2.0.0/fixture-x86.msi.vex   two, evaluated against ONE of release 2's two inventories
//	2.0.0/foreign.vex           hand-written on purpose: a document msis did not produce and
//	                            therefore never evaluated

func queryRows(t *testing.T, db *sql.DB, name string, args map[string]string) [][]string {
	t.Helper()
	all, err := Queries()
	if err != nil {
		t.Fatal(err)
	}
	var q *Query
	for i := range all {
		if all[i].Name == name {
			q = &all[i]
		}
	}
	if q == nil {
		t.Fatalf("no query named %q", name)
	}
	named := make([]any, 0, len(args))
	for k, v := range args {
		named = append(named, sql.Named(k, v))
	}
	rows, err := db.Query(q.SQL, named...)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for rows.Next() {
		cells := make([]sql.NullString, len(cols))
		into := make([]any, len(cols))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cells))
		for i, c := range cells {
			row[i] = c.String
		}
		out = append(out, row)
	}
	return out
}

// sourcesFor runs affected-unassessed and returns the documents still reported, by their path in
// the corpus - which is what says WHICH inventory was not covered.
func sourcesFor(t *testing.T, db *sql.DB, cve string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, r := range queryRows(t, db, "affected-unassessed", map[string]string{
		"name": "payload.txt", "version": "", "cve": cve,
	}) {
		out[r[len(r)-1]] = true // source is the last column
	}
	return out
}

// A VEX sidecar is a CycloneDX document, so it is found and indexed by the same corpus scan as
// everything else - nothing had to learn about a new file type. And it lands under the same
// PRODUCT as the inventory it annotates, which is the join everything below depends on.
func TestAVEXSidecarIsIndexedLikeAnyOtherDocument(t *testing.T) {
	db, _, _ := buildIndex(t)

	if n := scalar(t, db, `SELECT COUNT(*) FROM vulnerability`); n != "6" {
		t.Fatalf("%s statements indexed, want 6", n)
	}
	// The identity join, which is the whole point of copying the UpgradeCode into the
	// sidecar: a document identified only by product NAME would be a different product here.
	if n := scalar(t, db, `
		SELECT COUNT(DISTINCT d.product_id) FROM document d WHERE d.kind = 'vex'`); n != "1" {
		t.Errorf("the VEX documents are filed under %s products; they annotate one", n)
	}
	if got := scalar(t, db, `
		SELECT p.id FROM document d JOIN product p ON p.id = d.product_id
		WHERE d.kind = 'vex' LIMIT 1`); !strings.HasPrefix(got, "upgrade:") {
		t.Errorf("a sidecar is filed under %q; without the UpgradeCode it joins to nothing "+
			"its inventory is under", got)
	}
	// It says which inventory it was evaluated against, spelled as that document's own id.
	if n := scalar(t, db, `
		SELECT COUNT(*) FROM document v
		JOIN document i ON i.id = v.assesses
		WHERE v.kind = 'vex'`); n != "2" {
		t.Errorf("%s of the sidecars resolve to the inventory they assess, want 2", n)
	}
	// What a statement is about resolves to a component of that product's inventory.
	if n := scalar(t, db, `
		SELECT COUNT(*) FROM vulnerability_affects a
		JOIN component c ON c.bom_ref = a.ref
		WHERE a.ref LIKE '%payload.txt%'`); n == "0" {
		t.Error("the statement's affected ref matches no component in the corpus")
	}
	// And the vocabulary msis did not promote to a column is still there.
	if n := scalar(t, db, `
		SELECT COUNT(*) FROM vulnerability_property
		WHERE name = 'msis:vex.previousState'`); n == "0" {
		t.Error("the index dropped the vex vocabulary it did not promote")
	}
}

// #37's acceptance criterion as a query: the assessment covers release 1.0.0 and nothing else,
// so 2.0.0 - which ships the SAME component under the SAME bom-ref - is still reported.
func TestAnAssessmentSubtractsOnlyTheReleaseItCovers(t *testing.T) {
	db, _, _ := buildIndex(t)

	sources := sourcesFor(t, db, "CVE-2024-4321")
	if len(sources) == 0 {
		t.Fatal("no documents reported at all; the query cannot be subtracting correctly")
	}
	if sources["1.0.0/fixture.msi.cdx.json"] {
		t.Error("1.0.0 is still reported although an applicable assessment covers it")
	}
	for _, want := range []string{"2.0.0/fixture.msi.cdx.json", "2.0.0/fixture-x86.msi.cdx.json"} {
		if !sources[want] {
			t.Errorf("%s was subtracted; an assessment made for 1.0.0 does not cover the "+
				"release that came after it, even though the component is byte-identical", want)
		}
	}
}

// One release, two inventories - x64 and x86, which is what msis's own releases look like.
// They carry the same version and the same component refs, so matching an assessment on those
// alone lets one made against the x86 build suppress a finding in the x64 one, which nobody
// assessed. An assessment covers the inventory it was evaluated against and no other.
func TestAnAssessmentSubtractsOnlyTheInventoryItWasMadeAgainst(t *testing.T) {
	db, _, _ := buildIndex(t)

	// Both 2.0.0 inventories are in the corpus, under one product and one version.
	if n := scalar(t, db, `
		SELECT COUNT(*) FROM document
		WHERE kind = 'inventory' AND subject_version = '2.0.0'`); n != "2" {
		t.Fatalf("%s inventories for 2.0.0; this test needs the two-artifact release", n)
	}

	sources := sourcesFor(t, db, "CVE-2024-1234")
	if sources["2.0.0/fixture-x86.msi.cdx.json"] {
		t.Error("the assessed inventory is still reported")
	}
	if !sources["2.0.0/fixture.msi.cdx.json"] {
		t.Error("the OTHER inventory of the same release was subtracted by an assessment " +
			"made against its sibling; nobody assessed this build")
	}
}

// A statement that no longer applies subtracts nothing. It was true once; the release it was
// written for is not the one being asked about, and that is exactly the case this index must
// not quietly resolve in the reassuring direction.
func TestAStatementNeedingReviewSubtractsNothing(t *testing.T) {
	db, _, _ := buildIndex(t)

	if got := scalar(t, db, `
		SELECT applicability FROM vulnerability WHERE id = 'CVE-2024-9999' LIMIT 1`); got != "needs-review" {
		t.Fatalf("applicability = %q; the fixture is not the lapsed case any more", got)
	}
	if !sourcesFor(t, db, "CVE-2024-9999")["1.0.0/fixture.msi.cdx.json"] {
		t.Error("a release was subtracted by a statement msis flagged as needing review")
	}
}

// Only what msis CHECKED subtracts. A VEX document written by another tool carries no verdict -
// msis never evaluated whether its conditions hold - and a suppressing statement nobody checked
// must not quietly remove a build from the list of ones to look at.
func TestAnUnevaluatedStatementSubtractsNothing(t *testing.T) {
	db, _, _ := buildIndex(t)

	if got := scalar(t, db, `
		SELECT COALESCE(applicability, '(none)') FROM vulnerability WHERE id = 'CVE-2024-5678'`); got != "(none)" {
		t.Fatalf("applicability = %q; the fixture is not the unevaluated case any more", got)
	}
	if got := scalar(t, db, `SELECT state FROM vulnerability WHERE id = 'CVE-2024-5678'`); got != "not_affected" {
		t.Fatalf("state = %q; the fixture has to be a SUPPRESSING statement to be interesting", got)
	}

	if !sourcesFor(t, db, "CVE-2024-5678")["2.0.0/fixture.msi.cdx.json"] {
		t.Error("a build was subtracted by a statement msis never evaluated; a conclusion " +
			"whose conditions nobody checked is not one to act on")
	}
}

// And the assessments themselves are readable, with msis's verdict beside each, so the work of
// reassessing starts from what was claimed and why it lapsed.
func TestTheAssessmentsQueryShowsTheVerdict(t *testing.T) {
	db, _, _ := buildIndex(t)

	rows := queryRows(t, db, "assessments", map[string]string{"cve": ""})
	if len(rows) != 6 {
		t.Fatalf("%d assessments listed, want 6", len(rows))
	}
	var sawApplies, sawReview bool
	for _, r := range rows {
		joined := strings.Join(r, " | ")
		switch {
		case strings.Contains(joined, "CVE-2024-4321"):
			sawApplies = strings.Contains(joined, "applies")
		case strings.Contains(joined, "CVE-2024-9999"):
			sawReview = strings.Contains(joined, "needs-review") &&
				strings.Contains(joined, "0.9.0")
		}
	}
	if !sawApplies {
		t.Error("the applicable assessment is not reported as applying")
	}
	if !sawReview {
		t.Error("the lapsed assessment does not say it needs review, or does not say which " +
			"release it was made for")
	}
}
