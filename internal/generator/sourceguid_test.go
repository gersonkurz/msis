package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// componentGUIDs maps component id -> GUID across every directory fragment.
func componentGUIDs(output *GeneratedOutput) map[string]string {
	fragments := []string{
		output.DirectoryXML, output.AppDataDirXML, output.RoamingAppDataDirXML,
		output.LocalAppDataDirXML, output.CommonFilesDirXML, output.WindowsDirXML,
		output.SystemDirXML,
	}
	guids := make(map[string]string)
	for _, frag := range fragments {
		for _, line := range strings.Split(frag, "\n") {
			id, ok := attrValue(line, "<Component Id='")
			if !ok {
				continue
			}
			if guid, ok := attrValue(line, "Guid='"); ok {
				guids[id] = guid
			}
		}
	}
	return guids
}

func attrValue(line, marker string) (string, bool) {
	i := strings.Index(line, marker)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// generateFilesCtx builds a one-feature package from the given <files> items, with every
// distinct source existing on disk, and returns the context so tests can read the directory
// trees rather than scrape XML.
func generateFilesCtx(t *testing.T, items ...ir.Files) *Context {
	t.Helper()

	workDir := t.TempDir()
	generic := make([]ir.Item, 0, len(items))
	written := make(map[string]bool)
	for _, f := range items {
		if !written[f.Source] {
			if err := os.WriteFile(filepath.Join(workDir, f.Source), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			written[f.Source] = true
		}
		generic = append(generic, f)
	}

	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Items: generic}}}
	ctx := NewContext(setup, variables.New(), workDir)
	if _, err := ctx.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	return ctx
}

func generateFiles(t *testing.T, items ...ir.Files) *GeneratedOutput {
	t.Helper()

	workDir := t.TempDir()
	generic := make([]ir.Item, 0, len(items))
	written := make(map[string]bool)
	for _, f := range items {
		if !written[f.Source] {
			if err := os.WriteFile(filepath.Join(workDir, f.Source), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			written[f.Source] = true
		}
		generic = append(generic, f)
	}

	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Items: generic}}}
	output, err := NewContext(setup, variables.New(), workDir).Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	return output
}

// guidsByDestination maps every file component's destination - directory plus installed file
// name, case-folded the way the identity key is - to its GUID.
//
// It reads the directory trees rather than the rendered XML so there is exactly one entry per
// component. An earlier XML-scraping helper kept only one entry per root, which would have
// hidden both defects this round fixes.
func guidsByDestination(ctx *Context) map[string]string {
	out := make(map[string]string)
	var walk func(dir *Directory)
	walk = func(dir *Directory) {
		for _, comp := range dir.Components {
			for _, f := range comp.Files {
				out[strings.ToLower(targetPathOf(dir)+`\`+f.Name)] = comp.GUID
			}
		}
		for _, child := range dir.Children {
			walk(child)
		}
	}
	for _, root := range ctx.DirectoryTrees {
		walk(root)
	}
	return out
}

// TestSingleUseSourceKeepsItsHistoricGUID is the compatibility half of issue #21, and the
// reason the fix is safe to ship: a source file installed to one place must keep exactly the
// GUID msis has always given it, or every installed product sees its components change
// identity on upgrade.
//
// The value below was read out of the build before the fix and is pinned deliberately. An
// assertion against GenerateGUID(source) would pass no matter how the scheme changed, which is
// the one thing this test exists to prevent.
func TestSingleUseSourceKeepsItsHistoricGUID(t *testing.T) {
	const (
		componentID  = "CID_62d0a0ae93133a7a"
		historicGUID = "62d0a0ae-9313-3a7a-ad1a-08cff7ba0fcd"
	)

	output := generateFiles(t, ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"})

	guids := componentGUIDs(output)
	got, ok := guids[componentID]
	if !ok {
		t.Fatalf("component %s is gone; the id scheme changed, which moves component identity "+
			"for every installed product (have: %v)", componentID, guids)
	}
	if got != historicGUID {
		t.Errorf("GUID for a single-use source changed from %s to %s - every package in the "+
			"field would see this component change identity on upgrade", historicGUID, got)
	}
}

// TestDuplicateSourceGetsDistinctGUIDs is the defect half. Before the fix both components were
// given GenerateGUID(source) and wix build rejected the package:
//
//	error WIX0369: ... has a @Guid value '{...}' that duplicates another component
//
// Note the component *ids* were already distinct (NextComponentID disambiguates), so the GUID
// was the only collision.
func TestDuplicateSourceGetsDistinctGUIDs(t *testing.T) {
	ctx := generateFilesCtx(t,
		ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"},
		ir.Files{Source: "app.txt", Target: `[APPDATADIR]MyApp\data`},
	)

	byDest := guidsByDestination(ctx)
	installed, appData := byDest[`installdir\app.txt`], byDest[`appdatadir\myapp\data\app.txt`]
	if installed == "" || appData == "" {
		t.Fatalf("expected app.txt in both destinations, got %v", byDest)
	}
	if installed == appData {
		t.Errorf("both components built from app.txt share GUID %s - wix build rejects this "+
			"with WIX0369", installed)
	}
}

// TestRenamedCopiesInOneDirectoryGetDistinctGUIDs covers the shape the first version of this
// fix missed: <files target="[INSTALLDIR]name.txt"> renames on install, so one source can land
// twice in ONE directory. Keying identity on the directory alone gave both components the same
// GUID, leaving exactly the WIX0369 failure issue #21 is about.
func TestRenamedCopiesInOneDirectoryGetDistinctGUIDs(t *testing.T) {
	ctx := generateFilesCtx(t,
		ir.Files{Source: "app.txt", Target: "[INSTALLDIR]first.txt"},
		ir.Files{Source: "app.txt", Target: "[INSTALLDIR]second.txt"},
	)

	byDest := guidsByDestination(ctx)
	first, second := byDest[`installdir\first.txt`], byDest[`installdir\second.txt`]
	if first == "" || second == "" {
		t.Fatalf("expected both renamed copies, got %v", byDest)
	}
	if first == second {
		t.Errorf("both renamed copies share GUID %s - wix build rejects this with WIX0369", first)
	}
}

// TestDuplicateSourceGUIDsAreStableAcrossRuns guards the review focus on deterministic output:
// the re-keying walks a map, so the result must not depend on iteration order.
func TestDuplicateSourceGUIDsAreStableAcrossRuns(t *testing.T) {
	items := []ir.Files{
		{Source: "app.txt", Target: "[INSTALLDIR]"},
		{Source: "app.txt", Target: `[APPDATADIR]MyApp\data`},
		{Source: "app.txt", Target: "[LOCALAPPDATADIR]cache"},
	}

	first := guidsByDestination(generateFilesCtx(t, items...))
	for range 10 {
		if got := guidsByDestination(generateFilesCtx(t, items...)); !sameMap(first, got) {
			t.Fatalf("GUIDs vary between runs:\n%v\nvs\n%v", first, got)
		}
	}
}

// TestDuplicateSourceGUIDsSurviveReordering is why every component of a duplicated source is
// re-keyed rather than only the second one. If the first kept the legacy source-only GUID,
// swapping the two <files> lines would swap the two identities - an upgrade would see both
// components move, from a change that installs exactly the same files to the same places.
func TestDuplicateSourceGUIDsSurviveReordering(t *testing.T) {
	installDir := ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"}
	appData := ir.Files{Source: "app.txt", Target: `[APPDATADIR]MyApp\data`}

	forward := guidsByDestination(generateFilesCtx(t, installDir, appData))
	reversed := guidsByDestination(generateFilesCtx(t, appData, installDir))

	if !sameMap(forward, reversed) {
		t.Errorf("reordering the <files> elements changed which GUID belongs to which destination:\n%v\nvs\n%v",
			forward, reversed)
	}
}

// TestDuplicateSourceGUIDsIgnoreDirectoryCasing guards a subtler order dependence. Directory
// lookup is case-insensitive and keeps whichever spelling it saw first, so declaring
// [INSTALLDIR]Data\one before [INSTALLDIR]data\two makes the shared parent "Data", and the
// reverse order makes it "data". Hashing the raw name made identical destinations produce
// different GUIDs depending only on the order the elements were written in.
func TestDuplicateSourceGUIDsIgnoreDirectoryCasing(t *testing.T) {
	upperFirst := ir.Files{Source: "app.txt", Target: `[INSTALLDIR]Data\one`}
	lowerSecond := ir.Files{Source: "app.txt", Target: `[INSTALLDIR]data\two`}

	forward := guidsByDestination(generateFilesCtx(t, upperFirst, lowerSecond))
	reversed := guidsByDestination(generateFilesCtx(t, lowerSecond, upperFirst))

	if !sameMap(forward, reversed) {
		t.Errorf("directory casing changed the GUIDs for identical destinations:\n%v\nvs\n%v",
			forward, reversed)
	}
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestTargetPathOf(t *testing.T) {
	root := &Directory{ID: "DIR_ID00001", Name: "MyApp", CustomID: "INSTALLDIR"}
	child := &Directory{ID: "DIR_ID00002", Name: "data", Parent: root}
	grandchild := &Directory{ID: "DIR_ID00003", Name: "cache", Parent: child}

	// A nested INSTALLDIR value ("NGBT\chimera") puts the tree root above the CustomID node.
	// The walk must stop at the CustomID, or renaming the install folder would move every
	// duplicated component's identity.
	nestedRoot := &Directory{ID: "DIR_ID00010", Name: "NGBT"}
	nestedCustom := &Directory{ID: "DIR_ID00011", Name: "chimera", CustomID: "INSTALLDIR", Parent: nestedRoot}
	nestedChild := &Directory{ID: "DIR_ID00012", Name: "logs", Parent: nestedCustom}

	cases := []struct {
		dir  *Directory
		want string
	}{
		{root, "INSTALLDIR"},
		{child, `INSTALLDIR\data`},
		{grandchild, `INSTALLDIR\data\cache`},
		{nestedCustom, "INSTALLDIR"},
		{nestedChild, `INSTALLDIR\logs`},
	}
	for _, tc := range cases {
		if got := targetPathOf(tc.dir); got != tc.want {
			t.Errorf("targetPathOf(%s) = %q, want %q", tc.dir.ID, got, tc.want)
		}
	}
}
