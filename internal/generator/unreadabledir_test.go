package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// TestUnreadableSourceDirectoryFailsTheBuild is issue #25. addDirectoryContents used to answer
// every os.ReadDir failure with `return nil // Skip if can't read`, so a source directory that
// exists but cannot be enumerated contributed nothing and the build still reported success.
//
// The two cases below are the ReadDir failures reachable without elevation. The one that
// motivated the ticket - a directory whose permissions deny enumeration - is not reproducible
// here: on Windows os.Chmod only toggles the read-only bit, which does not block a directory
// listing. What is exercised is the behaviour that changed, namely that a ReadDir error is
// returned instead of discarded; the error path is identical whatever made ReadDir fail.
func TestUnreadableSourceDirectoryFailsTheBuild(t *testing.T) {
	workDir := t.TempDir()
	notADirectory := filepath.Join(workDir, "file.txt")
	if err := os.WriteFile(notADirectory, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
	}{
		// Present, but enumerating it fails - the shape of the reported defect.
		{"a file where a directory is expected", notADirectory},
		// The directory went away between being seen and being walked.
		{"a directory that is not there", filepath.Join(workDir, "gone")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext(&ir.Setup{}, variables.New(), workDir)
			dir := ctx.GetOrCreateDirectory("INSTALLDIR", "", false)

			err := ctx.addDirectoryContents(dir, workDir, tc.path, "FEATURE_00000", false)
			if err == nil {
				t.Fatal("enumerating this source failed, but the build was allowed to continue - " +
					"the package would ship without those files and say nothing")
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Errorf("the error does not name the directory: %v", err)
			}
		})
	}
}

// TestExcludedDirectoryIsStillSkippedQuietly guards the deliberate skip sitting immediately
// above the one that was removed. <exclude> asks for a folder to be left out, so that one must
// keep returning nil - turning it into an error would break a documented feature.
func TestExcludedDirectoryIsStillSkippedQuietly(t *testing.T) {
	workDir := t.TempDir()
	excluded := filepath.Join(workDir, "skipme")
	if err := os.MkdirAll(excluded, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(excluded, "inner.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := NewContext(&ir.Setup{}, variables.New(), workDir)
	ctx.ExcludedFolders[strings.ToLower(excluded)] = true
	dir := ctx.GetOrCreateDirectory("INSTALLDIR", "", false)

	if err := ctx.addDirectoryContents(dir, workDir, excluded, "FEATURE_00000", false); err != nil {
		t.Errorf("an excluded folder must be skipped without error, got %v", err)
	}
}
