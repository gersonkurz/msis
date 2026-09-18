package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// generateWithSource builds a one-feature package whose single <files> element has the given
// source, against a work directory holding `present.txt` and a `dist` directory.
func generateWithSource(t *testing.T, source string) error {
	t.Helper()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "present.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "dist", "inner.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Items: []ir.Item{
		ir.Files{Source: source, Target: "[INSTALLDIR]"},
	}}}}
	_, err := NewContext(setup, variables.New(), workDir).Generate()
	return err
}

// TestMissingSourceFailsTheBuild is issue #24. A <files source=> that does not exist used to be
// skipped in silence: the directory was created, the payload was not, and both msis and
// `wix build` reported success - so a mistyped path shipped a package without the file and
// nothing said so.
//
// msis-2.x reports "ERROR, '%s' is not a valid directory" and then fails, so this is also what
// the reference implementation does.
func TestMissingSourceFailsTheBuild(t *testing.T) {
	err := generateWithSource(t, "nope.txt")
	if err == nil {
		t.Fatal("a source that does not exist produced no error - the package would ship " +
			"without the file and say nothing")
	}
	if !strings.Contains(err.Error(), "nope.txt") {
		t.Errorf("the error does not name the source: %v", err)
	}
}

// TestMissingSourceDirectoryFailsTheBuild covers the directory form of the same mistake.
func TestMissingSourceDirectoryFailsTheBuild(t *testing.T) {
	if err := generateWithSource(t, "nosuchdir"); err == nil {
		t.Fatal("a source directory that does not exist produced no error")
	}
}

// TestWildcardSourceIsRejectedWithAnExplanation covers what the silence was hiding. The
// tutorial used to present `source="dist\*"` as the way to copy a tree; it never worked, in
// this version or in msis-2.x, and it landed in the same skipped-silently branch - so the
// documented example built a package containing nothing.
//
// The error names wildcards specifically, because "no such file or directory (looked in
// ...\dist\*)" would read like a filesystem problem rather than an unsupported syntax.
func TestWildcardSourceIsRejectedWithAnExplanation(t *testing.T) {
	err := generateWithSource(t, `dist\*`)
	if err == nil {
		t.Fatal("a wildcard source produced no error, so it would silently install nothing")
	}
	if !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("the error does not mention wildcards, so the user is left guessing: %v", err)
	}
}

// TestPresentSourcesStillBuild is the other half: the check must not fire on the forms that
// work, including the directory form the wildcard error points people at.
//
// The nested case is built with filepath.Join rather than a literal `dist\inner.txt`: on Unix
// a backslash is an ordinary filename character, so the literal names a file the fixture never
// created and the test would fail for a reason that has nothing to do with what it checks.
func TestPresentSourcesStillBuild(t *testing.T) {
	for _, source := range []string{"present.txt", "dist", filepath.Join("dist", "inner.txt")} {
		if err := generateWithSource(t, source); err != nil {
			t.Errorf("source %q exists but was rejected: %v", source, err)
		}
	}
}
