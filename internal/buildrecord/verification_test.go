package buildrecord

import (
	"path/filepath"
	"testing"
)

// The record says what a prerequisite's bytes were checked against before being chained
// (#50): a pin msis carries, the script's own sha256=, or nothing. Three different claims,
// and the SBOM must not present the third as if it were one of the first two.
func TestPrerequisiteVerificationIsRecorded(t *testing.T) {
	dir, script := scratch(t)
	rec := New(PathBundle, script, nil)

	supplied := filepath.Join(dir, "vc.exe")
	put(t, supplied, "the supplied redistributable\n")
	rec.AddPrerequisiteFromSource("vcredist", "2015", "vc.exe", "")
	rec.AddPrerequisiteFromSource("vcredist", "2017", "vc.exe", "0000000000000000000000000000000000000000000000000000000000000000")

	cached := filepath.Join(dir, "msis", "prerequisites", "vcredist", "2022", "vc_redist.x64.exe")
	put(t, cached, "the pinned redistributable\n")
	rec.AddPrerequisiteFromCache("vcredist", "2022", "x64", cached, "https://example.invalid/pinned")

	want := map[string]string{"2015": Unverified, "2017": VerifiedByScript, "2022": VerifiedByPin}
	if len(rec.Prereqs) != len(want) {
		t.Fatalf("%d prerequisites recorded, want %d: %+v", len(rec.Prereqs), len(want), rec.Prereqs)
	}
	for _, p := range rec.Prereqs {
		if p.Verification != want[p.Version] {
			t.Errorf("vcredist %s: Verification = %q, want %q", p.Version, p.Verification, want[p.Version])
		}
		if p.SHA256 == "" {
			t.Errorf("vcredist %s: the digest of what is on disk must be recorded whatever it was verified against", p.Version)
		}
	}
}

// Locate answers "which file will WiX bind" for a supplied source: the bind paths in order,
// so a copy in the WXS directory wins over one beside the script - exactly what resolve hashes
// for the record. The bundle generators verify a sha256= against this path.
func TestLocateFollowsTheBindPathOrder(t *testing.T) {
	dir, script := scratch(t)
	wxs := filepath.Join(dir, "out")
	put(t, filepath.Join(dir, "vc.exe"), "beside the script\n")
	put(t, filepath.Join(wxs, "vc.exe"), "in the WXS directory\n")
	put(t, filepath.Join(dir, "only-here.exe"), "only beside the script\n")

	rec := New(PathBundle, script, []BindPath{{Name: "wxs", Dir: wxs}, {Name: "script", Dir: dir}})

	if got, ok := rec.Locate("vc.exe"); !ok || got != filepath.Join(wxs, "vc.exe") {
		t.Errorf("Locate(vc.exe) = %q, %v; want the WXS directory's copy", got, ok)
	}
	if got, ok := rec.Locate("only-here.exe"); !ok || got != filepath.Join(dir, "only-here.exe") {
		t.Errorf("Locate(only-here.exe) = %q, %v; want the script directory's copy", got, ok)
	}
	if _, ok := rec.Locate("missing.exe"); ok {
		t.Error("Locate found a file that is nowhere")
	}
	if _, ok := rec.Locate("out"); ok {
		t.Error("Locate returned a directory as if it were the file")
	}
	abs := filepath.Join(dir, "vc.exe")
	if got, ok := rec.Locate(abs); !ok || got != abs {
		t.Errorf("Locate(absolute) = %q, %v; want it back as it is", got, ok)
	}
}
