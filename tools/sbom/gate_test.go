package main

import (
	"strings"
	"testing"
)

// The gate counts gaps across nested components, fails a count above its maximum or a score
// below its minimum, and asks for the baseline to follow an improvement.
func TestGateMeasuresAndCompares(t *testing.T) {
	doc := `{"components": [
		{"name": "a.txt", "hashes": [{"alg": "SHA-256", "content": "00"}],
		 "properties": [{"name": "msis:msi.fileKey", "value": "F1"}]},
		{"name": "msis", "version": "1", "licenses": [{"license": {"id": "MIT"}}],
		 "manufacturer": {"url": ["https://x.example"]},
		 "hashes": [{"alg": "SHA-256", "content": "00"}, {"alg": "SHA-512", "content": "00"}],
		 "externalReferences": [{"type": "source-distribution", "url": "https://x.example/src"}],
		 "components": [{"name": "lib", "version": "2", "externalReferences": [{"type": "vcs", "url": "https://x.example/src"}]}]}
	]}`
	got, err := measure([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	// lib's vcs reference is not BSI's source-distribution (Table 11): it counts as no source.
	want := coverage{NoLicence: 2, NoCreator: 2, NoVersion: 1, NoSHA512: 1, NoFilename: 1, NoSource: 2}
	if got != want {
		t.Fatalf("measure = %+v, want %+v", got, want)
	}

	if f, n := compare(got, 7, baseline{want, 7}); len(f) != 0 || len(n) != 0 {
		t.Errorf("at the baseline: failures %v, notes %v", f, n)
	}
	worse := want
	worse.NoLicence--
	f, _ := compare(got, 6.5, baseline{worse, 7})
	if len(f) != 2 || !strings.Contains(f[0], "licence") || !strings.Contains(f[1], "score 6.50") {
		t.Errorf("a regressed count and score: failures %v", f)
	}
	better := want
	better.NoCreator++
	if f, n := compare(got, 7, baseline{better, 7}); len(f) != 0 || len(n) != 1 || !strings.Contains(n[0], "lower the baseline") {
		t.Errorf("an improvement: failures %v, notes %v", f, n)
	}
}

// The artifact itself is measured: a subject that loses its SHA-512 (D10) while keeping its
// SHA-256 is a regression, and nothing else in the document would show it.
func TestGateMeasuresTheArtifact(t *testing.T) {
	doc := func(hashes string) string {
		return `{"metadata": {"component": {"name": "a.msi", "version": "1",
			"licenses": [{"license": {"id": "MIT"}}], "manufacturer": {"url": ["https://x.example"]},
			"externalReferences": [{"type": "source-distribution", "url": "https://x.example/src"}],
			"hashes": [` + hashes + `]}}, "components": []}`
	}
	full, err := measure([]byte(doc(`{"alg": "SHA-256", "content": "00"}, {"alg": "SHA-512", "content": "00"}`)))
	if err != nil || full != (coverage{}) {
		t.Fatalf("a complete subject: %+v, %v", full, err)
	}
	lost, err := measure([]byte(doc(`{"alg": "SHA-256", "content": "00"}`)))
	if err != nil || lost != (coverage{NoSHA512: 1}) {
		t.Fatalf("a subject without its SHA-512: %+v, %v", lost, err)
	}
	if f, _ := compare(lost, 7, baseline{full, 7}); len(f) != 1 || !strings.Contains(f[0], "SHA-512") {
		t.Errorf("the gate let the subject's SHA-512 go: %v", f)
	}
}

// A creator or licence is counted only when it says something: null, {} and all-empty fields
// name no one.
func TestGateCountsOnlyAStatedCreatorOrLicence(t *testing.T) {
	for creator, want := range map[string]bool{
		`"manufacturer": null`:                                    false,
		`"manufacturer": {}`:                                      false,
		`"manufacturer": {"url": [""], "contact": [{}]}`:          false,
		`"supplier": {"name": ""}`:                                false,
		`"authors": [{}]`:                                         false,
		`"manufacturer": {"url": ["https://x.example"]}`:          true,
		`"manufacturer": {"contact": [{"email": "a@x.example"}]}`: true,
		`"supplier": {"name": "Foo"}`:                             true,
		`"authors": [{"name": "Jane"}]`:                           true,
	} {
		got, err := measure([]byte(`{"components": [{"name": "c", "version": "1", "licenses": [{"license": {"id": "MIT"}}], ` + creator + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if has := got.NoCreator == 0; has != want {
			t.Errorf("%s: counted as a creator %v, want %v", creator, has, want)
		}
	}
	for licences, want := range map[string]bool{
		`[{}]`:                                  false,
		`[{"license": {}}]`:                     false,
		`[{"license": {"id": "MIT"}}]`:          true,
		`[{"license": {"name": "Acme EULA"}}]`:  true,
		`[{"expression": "MIT OR Apache-2.0"}]`: true,
	} {
		got, err := measure([]byte(`{"components": [{"name": "c", "version": "1", "authors": [{"name": "J"}], "licenses": ` + licences + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if has := got.NoLicence == 0; has != want {
			t.Errorf("%s: counted as a licence %v, want %v", licences, has, want)
		}
	}
}
