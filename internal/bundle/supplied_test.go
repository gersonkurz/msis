package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// A script-supplied prerequisite (#50): verified against its sha256= where there is one, warned
// about where there is not, and located the way WiX will bind it.

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// suppliedGen builds an explicit-bundle generator around one supplied prerequisite.
func suppliedGen(t *testing.T, workDir string, prereq ir.Prerequisite) *Generator {
	t.Helper()
	setup := &ir.Setup{Bundle: &ir.Bundle{
		Prerequisites: []ir.Prerequisite{prereq},
		MSI:           &ir.BundleMSI{Source: "app.msi"},
	}}
	return NewGenerator(setup, variables.New(), workDir)
}

func TestASuppliedPrerequisiteWithAMatchingDigestIsVerified(t *testing.T) {
	dir := t.TempDir()
	body := []byte("the redistributable the author supplied\n")
	if err := os.WriteFile(filepath.Join(dir, "vc.exe"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	gen := suppliedGen(t, dir, ir.Prerequisite{Type: "vcredist", Version: "2015", Source: "vc.exe", SHA256: digestOf(body)})

	var progress []string
	if err := gen.EnsurePrerequisites(func(m string) { progress = append(progress, m) }); err != nil {
		t.Fatalf("a matching digest must pass: %v", err)
	}
	if len(gen.Warnings) != 0 {
		t.Errorf("a verified prerequisite must not warn: %v", gen.Warnings)
	}
	if joined := strings.Join(progress, "\n"); !strings.Contains(joined, "Verified: vc.exe") {
		t.Errorf("progress should say the file was verified, got %q", joined)
	}
}

func TestASuppliedPrerequisiteWithAWrongDigestRefusesTheBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vc.exe"), []byte("not what the script expects\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected := digestOf([]byte("what the script expects\n"))
	gen := suppliedGen(t, dir, ir.Prerequisite{Type: "vcredist", Version: "2015", Source: "vc.exe", SHA256: expected})

	err := gen.EnsurePrerequisites(nil)
	if err == nil {
		t.Fatal("a mismatching digest was accepted")
	}
	for _, want := range []string{"vcredist 2015", "does not match the sha256=", "expected " + expected, "got ", "does not chain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestASuppliedPrerequisiteWithoutADigestWarnsAndProceeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vc.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := suppliedGen(t, dir, ir.Prerequisite{Type: "vcredist", Version: "2015", Source: "vc.exe"})

	if err := gen.EnsurePrerequisites(nil); err != nil {
		t.Fatalf("no digest is not an error - the file is used as before: %v", err)
	}
	if len(gen.Warnings) != 1 {
		t.Fatalf("want exactly one warning, got %v", gen.Warnings)
	}
	for _, want := range []string{"vcredist 2015", `"vc.exe"`, "without a sha256=", "unverified", `sha256="<digest>"`} {
		if !strings.Contains(gen.Warnings[0], want) {
			t.Errorf("warning lacks %q: %s", want, gen.Warnings[0])
		}
	}
}

func TestASuppliedPrerequisiteThatIsMissingCannotBeVerified(t *testing.T) {
	gen := suppliedGen(t, t.TempDir(), ir.Prerequisite{Type: "custom", Source: "nowhere.exe", SHA256: strings.Repeat("a", 64)})
	err := gen.EnsurePrerequisites(nil)
	if err == nil || !strings.Contains(err.Error(), "was not found") || !strings.Contains(err.Error(), "prerequisite custom:") {
		t.Errorf("err = %v, want a not-found refusal naming the prerequisite (version-less)", err)
	}
}

// The file verified is the file WiX will bind: with a resolver (main hands in the build
// record's bind-path lookup) the resolver's answer wins over the .msis directory.
func TestASuppliedPrerequisiteIsVerifiedWhereWiXWillBindIt(t *testing.T) {
	scriptDir := t.TempDir()
	wxsDir := t.TempDir()
	scriptCopy := []byte("the copy beside the script\n")
	wxsCopy := []byte("the copy in the WXS directory, which WiX binds first\n")
	if err := os.WriteFile(filepath.Join(scriptDir, "vc.exe"), scriptCopy, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wxsDir, "vc.exe"), wxsCopy, 0o644); err != nil {
		t.Fatal(err)
	}
	// The script's digest is the WXS copy's: that is the file that ships.
	gen := suppliedGen(t, scriptDir, ir.Prerequisite{Type: "vcredist", Version: "2015", Source: "vc.exe", SHA256: digestOf(wxsCopy)})
	gen.ResolveSource = func(source string) (string, bool) {
		p := filepath.Join(wxsDir, source)
		_, err := os.Stat(p)
		return p, err == nil
	}
	if err := gen.EnsurePrerequisites(nil); err != nil {
		t.Fatalf("the resolver's file must be the one verified: %v", err)
	}

	// Without a resolver the .msis directory is the fallback, and there the digest differs.
	gen.ResolveSource = nil
	if err := gen.EnsurePrerequisites(nil); err == nil {
		t.Fatal("without a resolver the script-directory copy is verified, and it does not match")
	}
}

// The auto-bundle path goes through the same verification.
func TestAutoBundleVerifiesASuppliedRequirement(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vc.exe"), []byte("supplied\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prereqs, err := RequirementsToPrerequisites([]ir.Requirement{
		{Type: "vcredist", Version: "2015", Source: "vc.exe", SHA256: strings.Repeat("0", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prereqs[0].SHA256 != strings.Repeat("0", 64) {
		t.Fatalf("RequirementsToPrerequisites dropped the digest: %+v", prereqs[0])
	}
	gen := NewAutoBundleGenerator(variables.New(), dir, filepath.Join(dir, "app.msi"), prereqs)
	err = gen.EnsurePrerequisites(nil)
	if err == nil || !strings.Contains(err.Error(), "does not match the sha256=") {
		t.Errorf("err = %v, want the mismatch refused on the auto-bundle path too", err)
	}
}
