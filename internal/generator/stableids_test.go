package generator

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// wxsIdentities reads every directory fragment and names each Directory, Component and File by
// where it sits - the Name path of its directories, plus what the element is - mapped to its Id
// (and a component's Guid). Two builds whose maps agree on a key agree on that element's
// identity, whatever else was added around it.
func wxsIdentities(t *testing.T, out *GeneratedOutput) map[string]string {
	t.Helper()
	ids := make(map[string]string)
	for root, frag := range map[string]string{
		"installdir": out.DirectoryXML, "appdatadir": out.AppDataDirXML,
		"roamingappdatadir": out.RoamingAppDataDirXML, "localappdatadir": out.LocalAppDataDirXML,
		"commonfilesdir": out.CommonFilesDirXML, "windowsdir": out.WindowsDirXML,
		"systemdir": out.SystemDirXML,
	} {
		dec := xml.NewDecoder(strings.NewReader("<r>" + frag + "</r>"))
		path := []string{root}     // the fragment's root, since two roots can share folder names
		var comp map[string]string // attributes of the open Component
		var what string            // what the open Component holds
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("parsing fragment: %v\n%s", err, frag)
			}
			switch el := tok.(type) {
			case xml.StartElement:
				attrs := make(map[string]string)
				for _, a := range el.Attr {
					attrs[a.Name.Local] = a.Value
				}
				where := strings.Join(path, `\`)
				switch el.Name.Local {
				case "Directory":
					path = append(path, strings.ToLower(cmpOr(attrs["Name"], attrs["Id"])))
					ids["dir "+strings.Join(path, `\`)] = attrs["Id"]
				case "Component":
					comp, what = attrs, ""
				case "File":
					what = "file " + strings.ToLower(attrs["Name"])
					ids["file "+where+`\`+strings.ToLower(attrs["Name"])] = attrs["Id"]
				case "PermissionEx":
					what = "permission"
				case "CreateFolder":
					if what == "" {
						what = "create-folder"
					}
				}
			case xml.EndElement:
				switch el.Name.Local {
				case "Directory":
					path = path[:len(path)-1]
				case "Component":
					key := "component " + strings.Join(path, `\`) + " " + what
					if _, dup := ids[key]; dup {
						t.Fatalf("two components are %s", key)
					}
					ids[key] = comp["Id"] + " " + comp["Guid"]
				}
			}
		}
	}
	return ids
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// writeTree creates each named file (relative, backslashed) under root with content x.
func writeTree(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(f, `\`, "/")))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// generateStable builds the #85 fixture from workDir: a nested INSTALLDIR, a directory walk into
// it and into APPDATADIR, and a <create-folder> - so directories, files, permission components
// and create-folder components are all present.
func generateStable(t *testing.T, workDir string, extra ...ir.Item) (*GeneratedOutput, *Context) {
	t.Helper()
	vars := variables.New()
	vars["INSTALLDIR"] = `NGBT\chimera`
	vars["UPGRADE_CODE"] = "{11111111-2222-3333-4444-555555555555}"
	items := append([]ir.Item{
		ir.Files{Source: "app", Target: "[INSTALLDIR]"},
		ir.Files{Source: "data", Target: "[APPDATADIR]data"},
		ir.CreateFolder{Target: "[APPDATADIR]logs"},
	}, extra...)
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Enabled: true, Allowed: true, Items: items}}}
	ctx := NewContext(setup, vars, workDir)
	out, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return out, ctx
}

// TestAnAddedFolderOrFileRenumbersNothing is #85's defect: an empty folder, a folder with files
// and a file added early in the walk, and another <create-folder>, must leave the Id (and Guid)
// of every element that was already there unchanged. With sequence numbers every directory
// and file after the insertion was renumbered, and so was every permission component.
func TestAnAddedFolderOrFileRenumbersNothing(t *testing.T) {
	work := t.TempDir()
	writeTree(t, work, `app\z.exe`, `app\conf\a.ini`, `app\lib\b.dll`, `app\lib\sub\c.dll`,
		`data\seed.db`, `data\cache\d.bin`)
	before, _ := generateStable(t, work)
	was := wxsIdentities(t, before)

	// Sorted first, so everything the walk meets afterwards used to shift.
	writeTree(t, work, `app\__pycache__\m.pyc`, `app\aaa.txt`, `data\__pycache__\n.pyc`)
	if err := os.MkdirAll(filepath.Join(work, "app", "_empty"), 0755); err != nil {
		t.Fatal(err)
	}
	after, _ := generateStable(t, work, ir.CreateFolder{Target: "[APPDATADIR]archive"})
	is := wxsIdentities(t, after)

	for key, id := range was {
		if is[key] != id {
			t.Errorf("%s: was %s, is %q", key, id, is[key])
		}
	}
	for key := range is {
		if _, old := was[key]; old {
			continue
		}
		if !strings.Contains(key, "__pycache__") && !strings.Contains(key, "aaa.txt") &&
			!strings.Contains(key, "_empty") && !strings.Contains(key, "archive") {
			t.Errorf("unexpected new element %s", key)
		}
	}
	if len(is) <= len(was) {
		t.Fatalf("the additions added nothing: %d elements before, %d after", len(was), len(is))
	}
}

// TestDirectoryAndFileIDsArePinned pins the scheme (D26): a root's directory keeps the root key,
// one below it is DIR_ plus the hash of the root key and the case-folded path, one above a nested
// root's is DIR_ plus the hash of the root key and its distance, and a File is its component's id
// with FILE_ for CID_. Pinned as literals, because an assertion computed with the same helpers
// would pass however the scheme changed, and a change renames every row of every package.
func TestDirectoryAndFileIDsArePinned(t *testing.T) {
	work := t.TempDir()
	writeTree(t, work, `app\z.exe`, `app\Conf\a.ini`, `data\seed.db`)
	out, _ := generateStable(t, work)
	ids := wxsIdentities(t, out)

	want := map[string]string{
		`dir installdir\ngbt`:                     "DIR_5aa8e900734c96d3", // shortHash("INSTALLDIR|up1")
		`dir installdir\ngbt\chimera`:             "INSTALLDIR",
		`dir installdir\ngbt\chimera\conf`:        "DIR_713ff75a59fe1961", // shortHash(`INSTALLDIR\conf`)
		`file installdir\ngbt\chimera\conf\a.ini`: "FILE_f29a2be0ba08a44b",
		`dir appdatadir\ngbt`:                     "DIR_f6a20bbb03a3968e", // shortHash("APPDATADIR|up1")
	}
	for key, id := range want {
		if ids[key] != id {
			t.Errorf("%s = %q, want %q", key, ids[key], id)
		}
	}
	if comp := ids[`component installdir\ngbt\chimera\conf file a.ini`]; !strings.HasPrefix(comp, "CID_f29a2be0ba08a44b ") {
		t.Errorf("a.ini's component is %q; its File id must be the component id with FILE_ for CID_", comp)
	}
}

// TestNestedRootNameIsNotDirectoryIdentity: the directories above a nested root are named by
// distance, so renaming the install folder changes Name attributes and no id - D23 keeps the
// install folder's name out of component identity, and D26 out of directory identity.
func TestNestedRootNameIsNotDirectoryIdentity(t *testing.T) {
	work := t.TempDir()
	writeTree(t, work, `app\conf\a.ini`, `data\seed.db`)
	_, ctx := generateStable(t, work)

	vars := variables.New()
	vars["INSTALLDIR"] = `Other\product`
	vars["UPGRADE_CODE"] = "{11111111-2222-3333-4444-555555555555}"
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Enabled: true, Allowed: true, Items: []ir.Item{
		ir.Files{Source: "app", Target: "[INSTALLDIR]"},
		ir.Files{Source: "data", Target: "[APPDATADIR]data"},
		ir.CreateFolder{Target: "[APPDATADIR]logs"},
	}}}}
	renamed := NewContext(setup, vars, work)
	if _, err := renamed.Generate(); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"INSTALLDIR", "APPDATADIR"} {
		a, b := ctx.DirectoryTrees[root], renamed.DirectoryTrees[root]
		if a.ID != b.ID || a.Name == b.Name {
			t.Errorf("%s's top directory: %s %q vs %s %q; want one id, two names", root, a.ID, a.Name, b.ID, b.Name)
		}
	}
}

// TestDirectoryIDIgnoresNameCase: the tree holds [INSTALLDIR]Conf and [INSTALLDIR]conf as one
// directory, so they have one id whichever spelling the build met first.
func TestDirectoryIDIgnoresNameCase(t *testing.T) {
	ctx := NewContext(&ir.Setup{}, variables.New(), ".")
	upper := ctx.GetOrCreateDirectory("INSTALLDIR", `Conf\SSL`, false)
	other := NewContext(&ir.Setup{}, variables.New(), ".")
	lower := other.GetOrCreateDirectory("INSTALLDIR", `conf\ssl`, false)
	if upper.ID != lower.ID {
		t.Errorf("Conf\\SSL is %s, conf\\ssl is %s", upper.ID, lower.ID)
	}
	if upper.Name != "SSL" {
		t.Errorf("the directory's Name keeps its spelling: got %q", upper.Name)
	}
}

// TestEveryFileIDIsItsComponentsAndUnique covers the shapes where a component's id is not the
// plain destination hash: two <files> installing one target (D24, CID_<hash>_1) and a service's
// own copy of its executable (keyed on the service name). Every File must still be its
// component's id with FILE_ for CID_, and no two Files may share an id.
func TestEveryFileIDIsItsComponentsAndUnique(t *testing.T) {
	work := t.TempDir()
	writeTree(t, work, `a\config.json`, `b\config.json`, `svc.exe`)
	setup := &ir.Setup{Features: []ir.Feature{
		{Name: "Main", Enabled: true, Allowed: true, Items: []ir.Item{
			ir.Files{Source: `a\config.json`, Target: "[INSTALLDIR]"},
			ir.Files{Source: `b\config.json`, Target: "[INSTALLDIR]"},
			ir.Files{Source: "svc.exe", Target: "[INSTALLDIR]"},
		}},
		{Name: "Service", Enabled: true, Allowed: true, Items: []ir.Item{
			ir.Service{ServiceName: "probe", FileName: "svc.exe"},
		}},
	}}
	ctx := NewContext(setup, variables.New(), work)
	if _, err := ctx.Generate(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func(*Directory)
	walk = func(d *Directory) {
		for _, comp := range d.Components {
			for _, f := range comp.Files {
				if f.ID != fileIDFor(comp.ID) {
					t.Errorf("%s in %s: File %s, component %s", f.Name, comp.ID, f.ID, comp.ID)
				}
				if seen[f.ID] {
					t.Errorf("File id %s is used twice", f.ID)
				}
				seen[f.ID] = true
			}
		}
		for _, child := range d.Children {
			walk(child)
		}
	}
	for _, root := range ctx.DirectoryTrees {
		walk(root)
	}
	if len(seen) != 4 {
		t.Errorf("want 4 files (two config.json, svc.exe and the service's copy), got %d", len(seen))
	}

	// msiread extracts a shared target's copies in File-id order and puts the first at the
	// target (writePayload), so the copy declared first must still sort first, as it did when
	// ids were sequence numbers.
	var configs []string
	for _, comp := range ctx.DirectoryTrees["INSTALLDIR"].Components {
		if comp.Files[0].Name == "config.json" {
			configs = append(configs, comp.Files[0].SourcePath+" "+comp.Files[0].ID)
		}
	}
	if len(configs) != 2 || !strings.HasPrefix(configs[0], `a\`) ||
		strings.Fields(configs[0])[1] >= strings.Fields(configs[1])[1] {
		t.Errorf("the first-declared config.json must have the smaller File id: %v", configs)
	}
}

// TestADirectoryIDCollisionFailsTheBuild: two keys with one id can only be a hash collision
// (the key space is the tree's), and it fails the build rather than taking a counter. The
// collision is injected, since a real one needs two paths sharing 64 bits of SHA-256.
func TestADirectoryIDCollisionFailsTheBuild(t *testing.T) {
	work := t.TempDir()
	writeTree(t, work, `app\conf\a.ini`)
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Enabled: true, Allowed: true, Items: []ir.Item{
		ir.Files{Source: "app", Target: "[INSTALLDIR]"},
	}}}}
	ctx := NewContext(setup, variables.New(), work)
	ctx.directoryKeys["DIR_"+shortHash(`INSTALLDIR\conf`)] = `INSTALLDIR\elsewhere`
	_, err := ctx.Generate()
	if err == nil || !strings.Contains(err.Error(), "hash to the same id") {
		t.Fatalf("want the collision reported, got %v", err)
	}
}
