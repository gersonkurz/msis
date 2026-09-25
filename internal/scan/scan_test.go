package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scannedDoc = `{
  "serialNumber": "urn:uuid:11111111-2222-4333-8444-555555555555", "version": 1,
  "metadata": {"component": {"bom-ref": "product", "name": "Product"}},
  "components": [
    {"bom-ref": "text", "name": "golang.org/x/text", "purl": "pkg:golang/golang.org/x/text@v0.20.0"},
    {"bom-ref": "sys", "name": "golang.org/x/sys", "purl": "pkg:golang/golang.org/x/sys@v0.40.0"},
    {"bom-ref": "notes.txt", "name": "notes.txt",
     "externalReferences": [{"type": "bom", "url": "urn:cdx:99999999-2222-4333-8444-555555555555/1"}]}
  ]
}`

const grypeOut = `{"matches": [
  {"vulnerability": {"id": "GO-2026-5970", "severity": "High", "fix": {"versions": ["0.39.0"]}},
   "relatedVulnerabilities": [{"id": "CVE-2026-56852"}],
   "artifact": {"id": "text", "name": "golang.org/x/text", "version": "v0.20.0"}},
  {"vulnerability": {"id": "GO-2026-5024", "severity": "Low", "fix": {"versions": ["0.44.0"]}},
   "artifact": {"id": "sys", "name": "golang.org/x/sys", "version": "v0.40.0"}}
], "descriptor": {"timestamp": "2026-09-24T18:00:00.1234567+02:00"}}`

// vexFor is a VEX sidecar as msis writes it: its subject links the inventory it was evaluated
// against, and each statement has the state that evaluation left it in.
func vexFor(link, textState, sysState string) []byte {
	return []byte(`{
  "metadata": {"component": {"externalReferences": [{"type": "bom", "url": "` + link + `"}]}},
  "vulnerabilities": [
    {"id": "CVE-2026-56852", "affects": [{"ref": "text"}], "analysis": {"state": "` + textState + `", "justification": "code_not_reachable"}},
    {"id": "GO-2026-5024", "affects": [{"ref": "sys"}], "analysis": {"state": "` + sysState + `"}}
  ]
}`)
}

const thisDocument = "urn:cdx:11111111-2222-4333-8444-555555555555/1"

// #69: findings join their component; the coverage the scan could not reach is counted; a
// BOM-Link is listed for the caller to follow.
func TestAnalyzeJoinsFindingsAndStatesCoverage(t *testing.T) {
	r, err := Analyze([]byte(scannedDoc), []byte(grypeOut), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 2 {
		t.Fatalf("findings %+v", r.Findings)
	}
	f := r.Findings[1] // sorted by id: GO-2026-5024, then GO-2026-5970
	if f.ID != "GO-2026-5970" || f.Component != "text" || len(f.Aliases) != 1 || f.Aliases[0] != "CVE-2026-56852" ||
		f.FixedIn[0] != "0.39.0" || f.SuppressedBy != "" {
		t.Errorf("finding %+v", f)
	}
	if r.Components != 4 || r.Unidentified != 2 {
		t.Errorf("coverage %d/%d, want 2 of 4 unidentified (the product and notes.txt)", r.Unidentified, r.Components)
	}
	if len(r.Links) != 1 || !strings.HasPrefix(r.Links[0], "urn:cdx:99999999") {
		t.Errorf("links %v", r.Links)
	}
	if r.VEX != "no VEX document beside it" {
		t.Errorf("VEX %q", r.VEX)
	}
}

// A statement answers a finding only if msis evaluated it against THIS document and left it in
// a suppressing state - by the finding's id or an alias of it, for that component. A lapsed
// one (moved to in_triage by #37's evaluation) answers nothing.
func TestVEXAnswersOnlyWhatStillHoldsForThisDocument(t *testing.T) {
	r, err := Analyze([]byte(scannedDoc), []byte(grypeOut), vexFor(thisDocument, "not_affected", "in_triage"))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Finding{}
	for _, f := range r.Findings {
		byID[f.ID] = f
	}
	if got := byID["GO-2026-5970"].SuppressedBy; got != "CVE-2026-56852: not_affected (code_not_reachable)" {
		t.Errorf("the alias statement: suppressed by %q", got)
	}
	if got := byID["GO-2026-5024"].SuppressedBy; got != "" {
		t.Errorf("a lapsed (in_triage) statement suppressed %q", got)
	}

	r, err = Analyze([]byte(scannedDoc), []byte(grypeOut),
		vexFor("urn:cdx:22222222-2222-4333-8444-555555555555/1", "not_affected", "not_affected"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		if f.SuppressedBy != "" {
			t.Errorf("a VEX evaluated against another document suppressed %s", f.ID)
		}
	}
	if !strings.Contains(r.VEX, "another document") {
		t.Errorf("VEX %q, want it to say why nothing was applied", r.VEX)
	}
}

// A report is never overwritten: the previous one is kept under its own timestamp, and every
// uncertainty refuses.
func TestWriteKeepsThePreviousReport(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "app.msi.cdx.json")
	out := ReportPath(doc, "")
	preserved, err := Write(out, []byte(grypeOut))
	if err != nil || out != filepath.Join(dir, "app.msi.grype.json") || preserved != "" {
		t.Fatalf("first write: %q %q %v", out, preserved, err)
	}
	preserved, err = Write(out, []byte(`{"descriptor": {"timestamp": "later"}}`))
	want := filepath.Join(dir, "app.msi.2026-09-24T18-00-00-1234567-02-00.grype.json")
	if err != nil || preserved != want {
		t.Fatalf("second write kept %q (%v), want %q", preserved, err, want)
	}
	if kept, _ := os.ReadFile(want); string(kept) != grypeOut {
		t.Error("the kept report is not the previous one")
	}

	// An existing report msis cannot date is not replaced.
	if err := os.WriteFile(out, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(out, []byte(grypeOut)); err == nil {
		t.Error("an undatable report was replaced")
	}
}

// The document suffix is matched in any case, as Windows matches it and /SCAN accepts it.
func TestTheSuffixIsMatchedInAnyCase(t *testing.T) {
	for doc, want := range map[string]string{
		`d\app.msi.cdx.json`: `d\app.msi`, `d\app.msi.CDX.JSON`: `d\app.msi`, `d\app.msi.Cdx.Json`: `d\app.msi`,
	} {
		if got := Artifact(doc); got != want {
			t.Errorf("Artifact(%q) = %q, want %q", doc, got, want)
		}
		if got := ReportPath(doc, ""); got != want+".grype.json" {
			t.Errorf("ReportPath(%q) = %q", doc, got)
		}
	}
}

// #70: with /SCAN-DIR the report is written in that directory, under the document's own
// artifact name, and Write creates the directory - so the path printed is the path it has.
func TestAReportDirectoryHoldsTheReport(t *testing.T) {
	dist, reports := t.TempDir(), filepath.Join(t.TempDir(), "scan")
	doc := filepath.Join(dist, "app.msi.CDX.JSON")
	out := ReportPath(doc, reports)
	if want := filepath.Join(reports, "app.msi.grype.json"); out != want {
		t.Fatalf("ReportPath = %q, want %q", out, want)
	}
	if _, err := Write(out, []byte(grypeOut)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("the report is not where ReportPath says: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dist, "*.grype.json")); len(left) != 0 {
		t.Errorf("a report was written beside the document too: %v", left)
	}
}

// #74's review: the scan's own coverage walk visits exactly what sbom.CountComponents counts, so
// "N of M components" cannot disagree with itself - an absent or empty subject is neither
// counted nor walked, and a subject naming only itself is both, with its nested components.
func TestTheCoverageWalkAgreesWithTheCount(t *testing.T) {
	for _, tc := range []struct {
		doc                      string
		components, unidentified int
	}{
		{`{"components": [{"name": "a"}, {"name": "b", "purl": "pkg:generic/b@1"}]}`, 2, 1},
		{`{"metadata": {"component": {}}, "components": [{"name": "a"}]}`, 1, 1},
		{`{"metadata": {"component": {"name": "P", "components": [{"name": "c", "cpe": "cpe:/a:x:c:1"}, {"name": "d"}]}},
		   "components": [{"name": "a", "purl": "pkg:generic/a@1"}]}`, 4, 2},
	} {
		r, err := Analyze([]byte(tc.doc), []byte(`{"matches": []}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Components != tc.components || r.Unidentified != tc.unidentified {
			t.Errorf("%s: %d of %d unidentified, want %d of %d", tc.doc, r.Unidentified, r.Components, tc.unidentified, tc.components)
		}
	}
}
