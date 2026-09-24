package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// TestEveryComponentBelongsToAFeature asserts the invariant WiX itself enforces:
// every <Component> must be referenced by some <Feature>, or the build fails with
// "error WIX0267: Found orphaned Component".
//
// Nothing in the suite checked this before, and two separate defects hid behind that
// gap — #15 (items written directly under <setup> were never referenced) and #18 (a
// feature holding no <files> left INSTALLDIR owned by no feature, so its permission
// component was orphaned). Both were found by building real packages. One table-driven
// test over a range of package shapes would have caught them together, which is what
// this is.
//
// The shapes matter more than the count: the defects appeared only in particular
// combinations, and each row below is a combination that was broken or is adjacent to
// one that was.
func TestEveryComponentBelongsToAFeature(t *testing.T) {
	regFile := func(t *testing.T, dir string) string {
		t.Helper()
		const content = "Windows Registry Editor Version 5.00\n" +
			"\n" +
			"[HKEY_LOCAL_MACHINE\\SOFTWARE\\Probe]\n" +
			"\"V\"=\"1\"\n"
		path := filepath.Join(dir, "probe.reg")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return "probe.reg"
	}

	cases := []struct {
		name  string
		build func(t *testing.T, workDir string) *ir.Setup
	}{
		{
			// #18: the feature has no <files>, so nothing used to mark INSTALLDIR.
			name: "feature with only set-env",
			build: func(t *testing.T, _ string) *ir.Setup {
				return &ir.Setup{Features: []ir.Feature{{
					Name:  "Main",
					Items: []ir.Item{ir.SetEnv{Name: "V", Value: "x"}},
				}}}
			},
		},
		{
			name: "feature with only registry",
			build: func(t *testing.T, workDir string) *ir.Setup {
				return &ir.Setup{Features: []ir.Feature{{
					Name:  "Main",
					Items: []ir.Item{ir.Registry{File: regFile(t, workDir)}},
				}}}
			},
		},
		{
			name: "feature with only a cleanup item",
			build: func(t *testing.T, _ string) *ir.Setup {
				return &ir.Setup{Features: []ir.Feature{{
					Name:  "Main",
					Items: []ir.Item{ir.RemoveOnUninstall{Folder: `[APPDATADIR]Vendor\logs`}},
				}}}
			},
		},
		{
			// #15: a top-level item alongside a declared feature.
			name: "top-level set-env plus a feature",
			build: func(t *testing.T, _ string) *ir.Setup {
				return &ir.Setup{
					Items: []ir.Item{ir.SetEnv{Name: "TOP", Value: "x"}},
					Features: []ir.Feature{{
						Name:  "Main",
						Items: []ir.Item{ir.SetEnv{Name: "V", Value: "y"}},
					}},
				}
			},
		},
		{
			name: "top-level registry plus a feature",
			build: func(t *testing.T, workDir string) *ir.Setup {
				return &ir.Setup{
					Items: []ir.Item{ir.Registry{File: regFile(t, workDir)}},
					Features: []ir.Feature{{
						Name:  "Main",
						Items: []ir.Item{ir.SetEnv{Name: "V", Value: "y"}},
					}},
				}
			},
		},
		{
			name: "top-level cleanup plus a feature",
			build: func(t *testing.T, _ string) *ir.Setup {
				return &ir.Setup{
					Items: []ir.Item{
						ir.RemoveOnUninstall{Folder: `[APPDATADIR]Vendor\logs`},
						ir.RemoveOnUninstall{Registry: `HKLM\Software\Vendor`},
					},
					Features: []ir.Feature{{
						Name:  "Main",
						Items: []ir.Item{ir.SetEnv{Name: "V", Value: "y"}},
					}},
				}
			},
		},
		{
			name: "two features",
			build: func(t *testing.T, _ string) *ir.Setup {
				return &ir.Setup{Features: []ir.Feature{
					{Name: "A", Items: []ir.Item{ir.SetEnv{Name: "A", Value: "1"}}},
					{Name: "B", Items: []ir.Item{ir.SetEnv{Name: "B", Value: "2"}}},
				}}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			ctx := NewContext(tc.build(t, workDir), variables.New(), workDir)
			output, err := ctx.Generate()
			if err != nil {
				t.Fatalf("Generate failed: %v", err)
			}

			declared := allComponentIDs(output)
			if len(declared) == 0 {
				t.Fatal("expected at least one component to be declared")
			}

			referenced := make(map[string]bool)
			for _, id := range refsFor(t, output) {
				referenced[id] = true
			}
			for _, id := range declared {
				if !referenced[id] {
					t.Errorf("component %s is declared but no feature references it — WiX rejects this as WIX0267", id)
				}
			}
		})
	}
}

// allComponentIDs collects every declared Component id across the emitted fragments.
// Deliberately broader than any single test needs: a component added to a fragment that
// is not scanned here would slip past the invariant unnoticed.
func allComponentIDs(output *GeneratedOutput) []string {
	fragments := []string{
		output.DirectoryXML,
		output.AppDataDirXML,
		output.RoamingAppDataDirXML,
		output.LocalAppDataDirXML,
		output.CommonFilesDirXML,
		output.WindowsDirXML,
		output.SystemDirXML,
		output.RegistryXML,
		output.DesktopXML,
		output.StartMenuXML,
		output.RemoveOnUninstallXML,
	}
	var ids []string
	for _, frag := range fragments {
		for _, line := range strings.Split(frag, "\n") {
			const marker = "<Component Id='"
			i := strings.Index(line, marker)
			if i < 0 {
				continue
			}
			rest := line[i+len(marker):]
			if j := strings.Index(rest, "'"); j >= 0 {
				ids = append(ids, rest[:j])
			}
		}
	}
	return ids
}

// refsByFeature maps each emitted <Feature Id> to the component ids it references.
// ComponentRefs are written immediately under their own feature's open tag and before
// any sub-feature opens, so the most recent <Feature Id> owns the refs that follow.
func refsByFeature(t *testing.T, output *GeneratedOutput) map[string][]string {
	t.Helper()
	refs := make(map[string][]string)
	current := ""
	for _, line := range strings.Split(output.FeatureXML, "\n") {
		if id, ok := attrAfter(line, "<Feature Id='"); ok {
			current = id
			continue
		}
		if id, ok := attrAfter(line, "<ComponentRef Id='"); ok && current != "" {
			refs[current] = append(refs[current], id)
		}
	}
	return refs
}

// permissionComponentID returns the id of the component carrying util:PermissionEx for
// INSTALLDIR — the component that #18 left orphaned.
func permissionComponentID(t *testing.T, output *GeneratedOutput) string {
	t.Helper()
	current := ""
	for _, line := range strings.Split(output.DirectoryXML, "\n") {
		if id, ok := attrAfter(line, "<Component Id='"); ok {
			current = id
			continue
		}
		if strings.Contains(line, "util:PermissionEx") {
			return current
		}
	}
	t.Fatal("no component with util:PermissionEx found in DirectoryXML")
	return ""
}

func attrAfter(line, marker string) (string, bool) {
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

// TestSharedDirectoryIsReferencedByEveryOwningFeature covers what the flattened
// invariant above cannot see. TestEveryComponentBelongsToAFeature pools the refs of all
// features, so INSTALLDIR's permission component satisfies it as soon as ONE feature
// references it — while a user installing only the other feature would get the directory
// without its permissions. generatePermissionComponent adds a ref per owning feature, so
// both features must appear, and that is what is asserted here.
func TestSharedDirectoryIsReferencedByEveryOwningFeature(t *testing.T) {
	setup := &ir.Setup{Features: []ir.Feature{
		{Name: "A", Items: []ir.Item{ir.SetEnv{Name: "A", Value: "1"}}},
		{Name: "B", Items: []ir.Item{ir.SetEnv{Name: "B", Value: "2"}}},
	}}

	// A named INSTALLDIR: an unnamed one is the Program Files folder itself, which gets no
	// permission component (#55).
	ctx := NewContext(setup, variables.Dictionary{"INSTALLDIR": "Shared"}, t.TempDir())
	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	permID := permissionComponentID(t, output)
	refs := refsByFeature(t, output)
	if len(refs) != 2 {
		t.Fatalf("expected 2 features in FeatureXML, got %d: %v", len(refs), refs)
	}

	for featureID, compIDs := range refs {
		found := false
		for _, id := range compIDs {
			if id == permID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("feature %s owns INSTALLDIR but does not reference its permission component %s — "+
				"installing that feature alone would create the directory without permissions",
				featureID, permID)
		}
	}
}
