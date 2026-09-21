package sbom

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The serial in an existing sidecar is untrusted input: the file may be corrupt, or written by
// something else entirely. Interpolating it into a filename let a crafted value steer the write
// out of the artifact's directory and truncate whatever it landed on.
func TestArchiveRefusesAnUnsafeSerial(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "App.msi")
	if err := os.WriteFile(artifact, []byte("installer"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A sentinel outside the artifact's directory, which must survive untouched.
	outside := filepath.Join(filepath.Dir(dir), "victim.cdx.json")
	if err := os.WriteFile(outside, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	// An existing sidecar whose serial tries to escape.
	//
	// Built with json.Marshal rather than written by hand. A raw string with single
	// backslashes is not valid JSON — `\.` is not an escape — so the first version of this
	// test fed the parser something it rejected: the serial came back empty and what was
	// actually exercised was the empty-serial path, not traversal at all.
	const hostileSerial = `urn:uuid:x\..\..\victim`
	hostile, err := json.Marshal(map[string]string{
		"bomFormat":    "CycloneDX",
		"serialNumber": hostileSerial,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SidecarPath(artifact), hostile, 0o644); err != nil {
		t.Fatal(err)
	}

	// Prove the fixture is what it claims to be before relying on it: the payload must
	// survive a round trip, or this test is quietly about something else.
	var readBack struct {
		SerialNumber string `json:"serialNumber"`
	}
	if err := json.Unmarshal(hostile, &readBack); err != nil {
		t.Fatalf("the fixture is not valid JSON, so the traversal payload never reaches the "+
			"code under test: %v", err)
	}
	if readBack.SerialNumber != hostileSerial {
		t.Fatalf("the fixture decodes to %q, not the traversal payload %q",
			readBack.SerialNumber, hostileSerial)
	}

	pkg := syntheticPackage()
	pkg.Path = artifact
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = Write(artifact, doc)
	if err == nil {
		t.Fatal("a sidecar carrying an unusable serial must not be silently replaced")
	}
	if !strings.Contains(err.Error(), "urn:uuid") {
		t.Errorf("error = %v, want it to name the malformed serial", err)
	}

	// Nothing outside the directory was touched, and the existing document is still there.
	if got, _ := os.ReadFile(outside); string(got) != "do not touch" {
		t.Errorf("the sentinel outside the artifact's directory was modified: %q", got)
	}
	if got, _ := os.ReadFile(SidecarPath(artifact)); string(got) != string(hostile) {
		t.Error("the existing document was replaced despite the failure")
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "victim") && e.Name() != "victim.cdx.json" {
			t.Errorf("a file was created outside the artifact's directory: %s", e.Name())
		}
	}
}

// Only "not there" means absent. A document that cannot be read cannot be preserved, so it must
// not be replaced either - treating a permission error as absence would destroy it.
func TestUnreadableExistingDocument(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "App.msi")
	if err := os.WriteFile(artifact, []byte("installer"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory where the sidecar should be: reading it fails with something that is not
	// "not found", which is the shape of the problem without needing an ACL dance.
	if err := os.Mkdir(SidecarPath(artifact), 0o755); err != nil {
		t.Fatal(err)
	}

	pkg := syntheticPackage()
	pkg.Path = artifact
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Write(artifact, doc)
	if err == nil {
		t.Fatal("an existing document that cannot be read must not be replaced")
	}
	// The failure has to be the REFUSAL, not an incidental one further down. Without the
	// check, Write carried on and failed later trying to rename over the same path - an
	// error either way, which made this test pass while the defect was present.
	if !strings.Contains(err.Error(), "cannot be preserved") {
		t.Errorf("error = %v; want the refusal to replace an unreadable document, "+
			"not a later failure that happens to look like one", err)
	}
}

// An archive is another document, not a slot to reuse: it may be the one a parent references.
func TestArchiveNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "App.msi")
	if err := os.WriteFile(artifact, []byte("installer"), 0o644); err != nil {
		t.Fatal(err)
	}

	const oldSerial = "urn:uuid:aaaaaaaa-1111-4111-8111-111111111111"
	if err := os.WriteFile(SidecarPath(artifact),
		[]byte(`{"serialNumber":"`+oldSerial+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// An archive for that serial already exists, holding something that matters.
	archived := filepath.Join(dir, "App.msi.aaaaaaaa-1111-4111-8111-111111111111.cdx.json")
	if err := os.WriteFile(archived, []byte("the document a parent references"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg := syntheticPackage()
	pkg.Path = artifact
	doc, err := FromPackage(pkg, Options{
		MsisVersion: "test",
		NewSerial:   func() (string, error) { return "urn:uuid:bbbbbbbb-2222-4222-8222-222222222222", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := Write(artifact, doc); err == nil {
		t.Fatal("writing over an existing archive must fail")
	}
	if got, _ := os.ReadFile(archived); string(got) != "the document a parent references" {
		t.Errorf("the existing archive was overwritten: %q", got)
	}
}
