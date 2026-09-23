package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/ir"
)

// The record is told what the generator VERIFIED, not what the script CARRIES (#50, round-1
// review): a supplied source whose sha256= was never checked - the check did not run, or a
// caller recorded without ensuring - is "unverified" in the SBOM, whatever the attribute says.
func TestRecordedVerificationFollowsWhatWasVerifiedNotTheAttribute(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"checked.exe", "unchecked.exe", "bare.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	digest := "0000000000000000000000000000000000000000000000000000000000000000"
	prereqs := []ir.Prerequisite{
		{Type: "vcredist", Version: "2015", Source: "checked.exe", SHA256: digest},
		{Type: "vcredist", Version: "2017", Source: "unchecked.exe", SHA256: digest},
		{Type: "vcredist", Version: "2019", Source: "bare.exe"},
	}

	rec := buildrecord.New(buildrecord.PathBundle, script, nil)
	recordPrerequisites(rec, prereqs, nil, map[string]bool{"checked.exe": true})

	want := map[string]string{
		"2015": buildrecord.VerifiedByScript, // digest present and verified
		"2017": buildrecord.Unverified,       // digest present, never verified
		"2019": buildrecord.Unverified,       // no digest
	}
	if len(rec.Prereqs) != len(want) {
		t.Fatalf("%d recorded, want %d: %+v", len(rec.Prereqs), len(want), rec.Prereqs)
	}
	for _, p := range rec.Prereqs {
		if p.Verification != want[p.Version] {
			t.Errorf("vcredist %s (%s): Verification = %q, want %q", p.Version, p.Source, p.Verification, want[p.Version])
		}
	}
}
