//go:build windows

package msiread

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// #82: ExtractTo writes every payload file Read hashed at its install target below the folder,
// with the bytes the package carries, and maps each written path back to its File id - the join
// an analyzer's findings come back on.
func TestExtractToWritesThePayloadAtItsTargets(t *testing.T) {
	dir := t.TempDir()
	pkg, written, err := ExtractTo(fixturePath(), dir)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for rel, id := range written {
		byID[id] = rel
	}
	hashed := 0
	for _, f := range pkg.Files {
		if f.SHA256 == "" {
			continue
		}
		hashed++
		rel, ok := byID[f.ID]
		if !ok {
			t.Errorf("%s (%s) was not written", f.ID, f.Target)
			continue
		}
		if want := targetPath(f.Target); rel != want && filepath.Base(filepath.Dir(filepath.Dir(rel))) != dupDir {
			t.Errorf("%s written at %s, want %s", f.ID, rel, want)
		}
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Errorf("%s: the extracted bytes are not the package's", f.Target)
		}
	}
	if hashed == 0 || len(written) != hashed {
		t.Errorf("wrote %d file(s) for %d hashed payload file(s)", len(written), hashed)
	}
}

// #82 review: a package's Directory and File tables are not validated, so a target climbing out
// of the extraction folder must be refused before anything is written - executed against a
// sentinel beside the folder, which must come through unchanged.
func TestWritePayloadStaysInsideTheFolder(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "extract")
	sentinel := filepath.Join(base, "victim.txt")
	if err := os.WriteFile(sentinel, []byte("customer data"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{`[ROOT]..\..\victim.txt`, `[ROOT]a\..\..\..\victim.txt`, `[ROOT]C:\victim.txt`} {
		files := []File{{ID: "F_OK", Target: `[ROOT]ok.txt`}, {ID: "F_BAD", Target: target}}
		payload := map[string][]byte{"F_OK": []byte("fine"), "F_BAD": []byte("overwritten")}
		if _, err := writePayload(dir, files, payload); err == nil {
			t.Errorf("%s: a target outside the folder was accepted", target)
		}
		if data, err := os.ReadFile(sentinel); err != nil || string(data) != "customer data" {
			t.Fatalf("%s: the file outside the folder was changed: %q, %v", target, data, err)
		}
	}
	// A duplicate target goes below the File id; the id is table data too.
	files := []File{{ID: "F1", Target: `[ROOT]a.txt`}, {ID: `..\..\..`, Target: `[ROOT]a.txt`}}
	if _, err := writePayload(dir, files, map[string][]byte{"F1": []byte("1"), `..\..\..`: []byte("2")}); err == nil {
		t.Error("a duplicate whose File id climbs out of the folder was accepted")
	}
}

func TestTargetPath(t *testing.T) {
	for target, want := range map[string]string{
		`[ProgramFiles64Folder]MSIS\templates\x64\a.dll`: filepath.Join("ProgramFiles64Folder", "MSIS", "templates", "x64", "a.dll"),
		`[INSTALLDIR]a.txt`: filepath.Join("INSTALLDIR", "a.txt"),
		`a.txt`:             "a.txt",
	} {
		if got := targetPath(target); got != want {
			t.Errorf("targetPath(%q) = %q, want %q", target, got, want)
		}
	}
}
