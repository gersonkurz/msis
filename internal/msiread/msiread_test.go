package msiread

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The parsing and path logic is pure, so it is tested directly: these are the parts that decide
// what an inventory says, and they need no installer database to exercise.

func TestSplitFileName(t *testing.T) {
	cases := []struct{ in, short, long string }{
		{"qpnybzec.dll|msi-simplica.dll", "qpnybzec.dll", "msi-simplica.dll"},
		{"template.wxs", "", "template.wxs"},
		{"s4rnqck_.git|.gitkeep", "s4rnqck_.git", ".gitkeep"},
		{"", "", ""},
	}
	for _, c := range cases {
		short, long := splitFileName(c.in)
		if short != c.short || long != c.long {
			t.Errorf("splitFileName(%q) = %q,%q want %q,%q", c.in, short, long, c.short, c.long)
		}
	}
}

// targetName must take the target side of a DefaultDir and the long name within it. Taking the
// source side would describe the layout the package was built from rather than where files land.
func TestTargetName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"MSIS", "MSIS"},
		{"nlezqamx|templates", "templates"},
		{"targetdir:sourcedir", "targetdir"},
		{"tshort|tlong:sshort|slong", "tlong"},
		{".", "."},
		{"SourceDir", "SourceDir"},
	}
	for _, c := range cases {
		if got := targetName(c.in); got != c.want {
			t.Errorf("targetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDirectoryPath covers the shape a real package has: TARGETDIR at the top, a standard folder
// below it that names a property rather than a folder, then the package's own directories.
func TestDirectoryPath(t *testing.T) {
	p := &Package{Directories: map[string]Directory{
		"TARGETDIR":         {ID: "TARGETDIR", Parent: "", Name: "SourceDir"},
		"ProgramFiles64Fol": {ID: "ProgramFiles64Fol", Parent: "TARGETDIR", Name: "."},
		"INSTALLDIR":        {ID: "INSTALLDIR", Parent: "ProgramFiles64Fol", Name: "MSIS"},
		"BIN":               {ID: "BIN", Parent: "INSTALLDIR", Name: "bin"},
		"LOCALAPPDATA":      {ID: "LOCALAPPDATA", Parent: "TARGETDIR", Name: "."},
		"TPL":               {ID: "TPL", Parent: "LOCALAPPDATA", Name: "templates"},
		"ORPHAN":            {ID: "ORPHAN", Parent: "NOSUCHDIR", Name: "stray"},
	}}

	cases := []struct{ id, want string }{
		{"INSTALLDIR", `[ProgramFiles64Fol]MSIS`},
		{"BIN", `[ProgramFiles64Fol]MSIS\bin`},
		{"TPL", `[LOCALAPPDATA]templates`},
		{"TARGETDIR", "[TARGETDIR]"},
		// A parent that is not in the table truncates rather than failing: a partial path is
		// more use than none, and the alternative is looping.
		{"ORPHAN", `[ORPHAN]`},
		{"NOSUCHDIR", ""},
	}
	for _, c := range cases {
		if got := p.DirectoryPath(c.id); got != c.want {
			t.Errorf("DirectoryPath(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// A cyclic Directory_Parent must terminate. A malformed third-party package should produce a
// bad path, not a hung inspection.
func TestDirectoryPathTerminatesOnACycle(t *testing.T) {
	p := &Package{Directories: map[string]Directory{
		"A": {ID: "A", Parent: "B", Name: "a"},
		"B": {ID: "B", Parent: "A", Name: "b"},
	}}
	done := make(chan string, 1)
	go func() { done <- p.DirectoryPath("A") }()
	select {
	case got := <-done:
		if got == "" {
			t.Error("want some path, got empty")
		}
	case <-time.After(time.Second):
		t.Fatal("DirectoryPath did not terminate on a directory cycle")
	}
}

func TestResolveTargets(t *testing.T) {
	p := &Package{
		Directories: map[string]Directory{
			"TARGETDIR": {ID: "TARGETDIR", Name: "SourceDir"},
			"PF":        {ID: "PF", Parent: "TARGETDIR", Name: "."},
			"APP":       {ID: "APP", Parent: "PF", Name: "MyApp"},
		},
		Components: []Component{{ID: "C1", Directory: "APP"}},
		Files: []File{
			{ID: "F1", Component: "C1", Name: "app.exe"},
			{ID: "F2", Component: "C_MISSING", Name: "orphan.txt"},
		},
	}
	resolveTargets(p)

	if want := `[PF]MyApp\app.exe`; p.Files[0].Target != want {
		t.Errorf("target = %q, want %q", p.Files[0].Target, want)
	}
	// A file whose component is not in the table still gets a name rather than vanishing.
	if p.Files[1].Target != "orphan.txt" {
		t.Errorf("orphan target = %q, want the bare name", p.Files[1].Target)
	}
}

func TestMediaEmbedded(t *testing.T) {
	embedded := Media{Cabinet: "#setup.cab"}
	if !embedded.Embedded() || embedded.StreamName() != "setup.cab" {
		t.Errorf("embedded cabinet misread: %+v", embedded)
	}
	external := Media{Cabinet: "media1.cab"}
	if external.Embedded() || external.StreamName() != "media1.cab" {
		t.Errorf("external cabinet misread: %+v", external)
	}
}

// TestReadRealPackage is the one check that exercises msi.dll. The pure logic above cannot tell
// us that the syscall layer works, and reading the code cannot either.
//
// It uses a released package when one is present. `bootstrap/dist` is gitignored, so this skips
// in a clean checkout — stated plainly rather than papered over. Producing a committed fixture
// is worth doing and is not free: see the notes on issue #31.
func TestReadRealPackage(t *testing.T) {
	msi := findReleasedPackage(t)
	if msi == "" {
		t.Skip("no released .msi in bootstrap/dist; run `just release-all` to produce one")
	}

	p, err := Read(msi)
	if err != nil {
		t.Fatalf("Read(%s): %v", msi, err)
	}

	if got := p.Properties["Manufacturer"]; got == "" {
		t.Error("no Manufacturer property; the Property table did not read")
	}
	if len(p.Files) == 0 {
		t.Fatal("no files read")
	}

	// Every file must resolve to a symbolic target rooted in a directory property.
	for _, f := range p.Files {
		if f.Name == "" {
			t.Errorf("file %s has no name", f.ID)
		}
		if !strings.HasPrefix(f.Target, "[") {
			t.Errorf("file %s target %q is not rooted in a directory property", f.ID, f.Target)
		}
	}

	// The payload msis itself ships: an ordinary file, not a Binary stream.
	var hook *File
	for i := range p.Files {
		if strings.EqualFold(p.Files[i].Name, "msi-simplica.dll") {
			hook = &p.Files[i]
		}
	}
	if hook == nil {
		t.Error("msi-simplica.dll not found among the payload files")
	} else if !strings.Contains(hook.Target, `templates\`) {
		t.Errorf("hook DLL target = %q, want it under the templates tree", hook.Target)
	}

	// Binary-table streams are the payload a build-time view never sees: WiX injects its own
	// custom action and UI resources whatever the template. Reading them is the point.
	if len(p.Binaries) == 0 {
		t.Error("no Binary-table streams read; WiX injects its own even for a minimal template")
	}
	for _, b := range p.Binaries {
		if b.Size == 0 {
			t.Errorf("Binary %q read as empty", b.Name)
		}
		if len(b.SHA256) != 64 {
			t.Errorf("Binary %q has no SHA-256", b.Name)
		}
	}

	if len(p.Media) == 0 {
		t.Error("no Media rows; the payload cabinet is unaccounted for")
	}

	// Determinism: the inventory feeds an SBOM that has to be diffable.
	again, err := Read(msi)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if len(again.Files) != len(p.Files) {
		t.Fatalf("file count differs between reads: %d vs %d", len(p.Files), len(again.Files))
	}
	for i := range p.Files {
		if p.Files[i] != again.Files[i] {
			t.Errorf("file %d differs between reads:\n  %+v\n  %+v", i, p.Files[i], again.Files[i])
			break
		}
	}
	for i := range p.Binaries {
		if p.Binaries[i] != again.Binaries[i] {
			t.Errorf("binary %d differs between reads", i)
			break
		}
	}

	t.Logf("%s: %d files, %d binaries, %d components, %d media",
		filepath.Base(msi), len(p.Files), len(p.Binaries), len(p.Components), len(p.Media))
}

func findReleasedPackage(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "bootstrap", "dist", "*.msi"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	if _, err := os.Stat(matches[0]); err != nil {
		return ""
	}
	return matches[0]
}

// The tests below are regressions for the six defects found in review. Each names the defect it
// pins, because a test whose point is invisible gets "simplified" away later.

// Finding 4: "SourceDir" is the conventional DefaultDir of the installation ROOT. Lower down it
// is an ordinary folder name, and skipping it wherever it appeared silently dropped a real
// directory out of every target beneath it.
func TestSourceDirIsOnlySpecialAtTheRoot(t *testing.T) {
	p := &Package{Directories: map[string]Directory{
		"TARGETDIR": {ID: "TARGETDIR", Parent: "", Name: "SourceDir"},
		"PF":        {ID: "PF", Parent: "TARGETDIR", Name: "."},
		"APP":       {ID: "APP", Parent: "PF", Name: "App"},
		// A subdirectory a package is perfectly entitled to call SourceDir.
		"SRC": {ID: "SRC", Parent: "APP", Name: "SourceDir"},
		"SUB": {ID: "SUB", Parent: "SRC", Name: "deep"},
	}}

	if got, want := p.DirectoryPath("SRC"), `[PF]App\SourceDir`; got != want {
		t.Errorf("DirectoryPath(SRC) = %q, want %q", got, want)
	}
	if got, want := p.DirectoryPath("SUB"), `[PF]App\SourceDir\deep`; got != want {
		t.Errorf("DirectoryPath(SUB) = %q, want %q", got, want)
	}

	// A root identified by its DefaultDir rather than by the TARGETDIR key is still dropped.
	q := &Package{Directories: map[string]Directory{
		"ROOT": {ID: "ROOT", Parent: "", Name: "SourceDir"},
		"PF":   {ID: "PF", Parent: "ROOT", Name: "."},
		"APP":  {ID: "APP", Parent: "PF", Name: "App"},
	}}
	if got, want := q.DirectoryPath("APP"), `[PF]App`; got != want {
		t.Errorf("non-TARGETDIR root: DirectoryPath(APP) = %q, want %q", got, want)
	}
}

// Finding 5: deciding whether a separator is needed by looking for a trailing "]" breaks on a
// directory legitimately named "data]", which would swallow the separator before the filename.
func TestTargetSeparatorSurvivesABracketInADirectoryName(t *testing.T) {
	p := &Package{
		Directories: map[string]Directory{
			"TARGETDIR": {ID: "TARGETDIR", Name: "SourceDir"},
			"PF":        {ID: "PF", Parent: "TARGETDIR", Name: "."},
			"APP":       {ID: "APP", Parent: "PF", Name: "App"},
			"ODD":       {ID: "ODD", Parent: "APP", Name: "data]"},
		},
		Components: []Component{
			{ID: "C_ODD", Directory: "ODD"},
			{ID: "C_ROOT", Directory: "PF"},
		},
		Files: []File{
			{ID: "F1", Component: "C_ODD", Name: "file.dll"},
			{ID: "F2", Component: "C_ROOT", Name: "top.dll"},
		},
	}
	resolveTargets(p)

	if got, want := p.Files[0].Target, `[PF]App\data]\file.dll`; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	// And a file directly under the root property still gets no stray separator.
	if got, want := p.Files[1].Target, `[PF]top.dll`; got != want {
		t.Errorf("root-level target = %q, want %q", got, want)
	}
}

// Finding 6: ServiceInstall's unique key is the ServiceInstall column, not Name. Two rows may
// carry the same service name - conditionally selected service components do - and sorting on a
// non-unique key leaves their relative order down to database enumeration.
func TestServicesSortByTheirUniqueKey(t *testing.T) {
	forward := &Package{Services: []Service{
		{ID: "S_B", Name: "same"},
		{ID: "S_A", Name: "same"},
	}}
	reversed := &Package{Services: []Service{
		{ID: "S_A", Name: "same"},
		{ID: "S_B", Name: "same"},
	}}
	sortAll(forward)
	sortAll(reversed)

	for i := range forward.Services {
		if forward.Services[i] != reversed.Services[i] {
			t.Fatalf("same rows in opposite order sorted differently at %d: %+v vs %+v",
				i, forward.Services[i], reversed.Services[i])
		}
	}
	if forward.Services[0].ID != "S_A" {
		t.Errorf("sorted by %q first, want the lowest ServiceInstall key", forward.Services[0].ID)
	}
}
