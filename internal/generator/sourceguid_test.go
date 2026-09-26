package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// TestFileGUIDIsProductAndDestination pins the identity scheme (#81, decisions D23): a file
// component's GUID is the product's UpgradeCode plus where it installs, and its id is where it
// installs. The values are pinned deliberately - an assertion against GenerateGUID(...) would
// pass however the scheme changed, and a change moves every component of every package.
func TestFileGUIDIsProductAndDestination(t *testing.T) {
	const (
		componentID = "CID_6e8a60f5c40b3b03"
		guid        = "9418f85d-a1d5-17da-9288-2e55a366b723"
	)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := variables.New()
	vars["UPGRADE_CODE"] = "{11111111-2222-3333-4444-555555555555}"
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Items: []ir.Item{ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"}}}}}
	output, err := NewContext(setup, vars, dir).Generate()
	if err != nil {
		t.Fatal(err)
	}
	if got := componentGUIDs(output); got[componentID] != guid {
		t.Errorf("component %s should have GUID %s; the identity scheme changed, which moves every "+
			"component of every package (have: %v)", componentID, guid, got)
	}
}

// buildIn generates a package from absolute <files> sources in dir - the NG1 CI shape (#81) -
// under the given UpgradeCode, and returns its GUIDs by destination plus its component ids.
func buildIn(t *testing.T, dir, upgradeCode string, features ...ir.Feature) (map[string]string, []string) {
	t.Helper()
	for i := range features {
		for j, item := range features[i].Items {
			f := item.(ir.Files)
			path := filepath.Join(dir, f.Source)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(f.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			f.Source = path
			features[i].Items[j] = f
		}
	}
	vars := variables.New()
	vars["UPGRADE_CODE"] = upgradeCode
	ctx := NewContext(&ir.Setup{Features: features}, vars, dir)
	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	ids := sortedKeysOf(componentGUIDs(output))
	return guidsByDestination(ctx), ids
}

func features(items ...[]ir.Item) []ir.Feature {
	out := make([]ir.Feature, len(items))
	for i, it := range items {
		out[i] = ir.Feature{Name: fmt.Sprintf("F%d", i), Items: it}
	}
	return out
}

// TestFileGUIDsDoNotDependOnTheBuildFolder is #81: the same script built from two folders, with
// absolute sources, gave every component a new GUID - NG1's reference and a local build differed
// in all 10,565 - because the GUID hashed the absolute source path.
func TestFileGUIDsDoNotDependOnTheBuildFolder(t *testing.T) {
	const code = "{11111111-2222-3333-4444-555555555555}"
	shape := func() []ir.Feature {
		return features([]ir.Item{
			ir.Files{Source: `out\conf\fastcgi.conf`, Target: `[INSTALLDIR]conf`},
			ir.Files{Source: `out\app.exe`, Target: "[INSTALLDIR]"},
		})
	}
	ciGUIDs, ciIDs := buildIn(t, filepath.Join(t.TempDir(), "ng1-2.4.0-banking"), code, shape()...)
	localGUIDs, localIDs := buildIn(t, filepath.Join(t.TempDir(), "Downloads", "ng1"), code, shape()...)
	if len(ciGUIDs) != 2 || !sameMap(ciGUIDs, localGUIDs) {
		t.Errorf("the build folder changed the GUIDs:\n%v\nvs\n%v", ciGUIDs, localGUIDs)
	}
	if !slices.Equal(ciIDs, localIDs) {
		t.Errorf("the build folder changed the component ids: %v vs %v", ciIDs, localIDs)
	}
}

// TestFileGUIDsAreScopedToTheProduct: two products installing the same destination (each into
// its own INSTALLDIR) must not share a component - Windows Installer would refcount one
// component across both, at two different paths.
func TestFileGUIDsAreScopedToTheProduct(t *testing.T) {
	shape := func() []ir.Feature {
		return features([]ir.Item{ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"}})
	}
	a, _ := buildIn(t, t.TempDir(), "{11111111-1111-1111-1111-111111111111}", shape()...)
	b, _ := buildIn(t, t.TempDir(), "{22222222-2222-2222-2222-222222222222}", shape()...)
	if a[`installdir\app.txt`] == "" || a[`installdir\app.txt`] == b[`installdir\app.txt`] {
		t.Errorf("two products share the GUID of \x60installdir\\app.txt: %v / %v", a, b)
	}
}

// TestSharedDestinationGetsDistinctStableGUIDs is the #79 shape: two features install different
// sources to ONE destination. Each component needs its own GUID (WIX0369 otherwise), and which
// GUID belongs to which source must not depend on the order the features are written in.
func TestSharedDestinationGetsDistinctStableGUIDs(t *testing.T) {
	const code = "{11111111-2222-3333-4444-555555555555}"
	standard := []ir.Item{ir.Files{Source: `a\config.json`, Target: "[INSTALLDIR]"}}
	variant := []ir.Item{ir.Files{Source: `b\config.json`, Target: "[INSTALLDIR]"}}
	dir := t.TempDir()
	guidsOf := func(fs ...ir.Feature) map[string]string {
		vars := variables.New()
		vars["UPGRADE_CODE"] = code
		for i := range fs {
			for j, item := range fs[i].Items {
				f := item.(ir.Files)
				path := filepath.Join(dir, f.Source)
				_ = os.MkdirAll(filepath.Dir(path), 0o755)
				if err := os.WriteFile(path, []byte(f.Source), 0o644); err != nil {
					t.Fatal(err)
				}
				fs[i].Items[j] = f
			}
		}
		ctx := NewContext(&ir.Setup{Features: fs}, vars, dir)
		if _, err := ctx.Generate(); err != nil {
			t.Fatal(err)
		}
		bySource := map[string]string{}
		for _, placed := range ctx.fileComponentsBySource {
			for _, p := range placed {
				bySource[strings.ToLower(p.comp.Files[0].SourcePath)] = p.comp.GUID
			}
		}
		return bySource
	}
	forward := guidsOf(features(standard, variant)...)
	reversed := guidsOf(features(variant, standard)...)
	if len(forward) != 2 || forward[`a\config.json`] == forward[`b\config.json`] {
		t.Errorf("the two components at one destination should have distinct GUIDs: %v", forward)
	}
	if !sameMap(forward, reversed) {
		t.Errorf("reordering the features moved the GUIDs between sources:\n%v\nvs\n%v", forward, reversed)
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
