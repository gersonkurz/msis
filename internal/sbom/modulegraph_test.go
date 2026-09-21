package sbom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The SBOM index (#35) needs SQLite; msis must not.
//
// #29 D11 is precise about what that means: not importing modernc.org/sqlite into cmd/msis
// keeps it out of that BINARY, but not out of the root module's requirement graph - a module
// required by the root go.mod is fetched, verified and vendored by anyone building msis at all,
// whatever they build. The nested module under tools/sbom-index is what actually isolates it.
//
// The check is DIRECT, as the ticket asks, and it lives here rather than in the tools module
// for a mundane reason: `go test ./...` is module-scoped, so a test inside tools/sbom-index
// would never run during `just check` and the property would go unverified exactly when it
// broke. Reading go.mod and go.sum also keeps it hermetic - `go list -m all` would need the
// module cache or the network to answer.
func TestTheRootModuleDoesNotRequireSQLite(t *testing.T) {
	root := repoRoot(t)

	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// go.sum lists the whole module graph, not just direct requirements, so a module
		// pulled in transitively would show up here even if go.mod stayed clean.
		for _, forbidden := range []string{"modernc.org/sqlite", "modernc.org/libc"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("the root %s requires %s; it belongs to tools/sbom-index alone, "+
					"which is its own module so that building msis never fetches it",
					name, forbidden)
			}
		}
	}
}

// ...and the isolation has to be structural, not a coincidence of nobody having imported it
// yet. A nested go.mod is the only thing keeping tools/sbom-index out of the root module: if
// it were deleted, the directory would silently rejoin the root module and the test above
// would start failing for a reason nobody would connect to this.
func TestTheIndexToolIsItsOwnModule(t *testing.T) {
	root := repoRoot(t)

	data, err := os.ReadFile(filepath.Join(root, "tools", "sbom-index", "go.mod"))
	if err != nil {
		t.Fatalf("tools/sbom-index has no go.mod of its own, so it is part of the root "+
			"module and its dependencies are the root module's: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "module github.com/gersonkurz/msis/tools/sbom-index") {
		t.Errorf("tools/sbom-index/go.mod does not declare the expected module path:\n%s", text)
	}
	// It does require SQLite - so the test above is checking a real separation and not
	// merely that nothing anywhere uses it.
	if !strings.Contains(text, "modernc.org/sqlite") {
		t.Error("tools/sbom-index does not require modernc.org/sqlite, so the root module " +
			"being clean of it proves nothing")
	}
}

// repoRoot walks up from the package directory to the directory holding the root go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/gersonkurz/msis\n") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("cannot find the repository root: no go.mod declaring the msis module above " +
		"this package")
	return ""
}
