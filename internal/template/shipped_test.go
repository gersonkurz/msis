package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupShipsEveryTemplateFolder guards msis's own installer (bootstrap/setup.msis): every
// folder under templates/ must be packaged, or an installed msis lacks a template the repo has.
// minimal-x86 was missing - the release built its own x86 MSI with it, yet never shipped it (#61).
// The folder list is read from disk, so a new template folder fails here until it is packaged.
func TestSetupShipsEveryTemplateFolder(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "bootstrap", "setup.msis"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if want := `source="..\templates\` + e.Name() + `"`; !strings.Contains(string(script), want) {
			t.Errorf("bootstrap/setup.msis does not package templates/%s: no %s", e.Name(), want)
		}
	}
}
