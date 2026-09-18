package generator

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// cleanupComponentIDs returns the component ids emitted for <remove-on-uninstall>, in order.
func cleanupComponentIDs(t *testing.T, items ...ir.RemoveOnUninstall) []string {
	t.Helper()

	generic := make([]ir.Item, 0, len(items))
	for _, item := range items {
		generic = append(generic, item)
	}
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Items: generic}}}
	output, err := NewContext(setup, variables.New(), t.TempDir()).Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	var ids []string
	for _, line := range strings.Split(output.RemoveOnUninstallXML, "\n") {
		if id, ok := attrValue(line, "<Component Id='"); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestCleanupComponentIDsAreUnchangedForSingleUseItems is the compatibility half of issue #23.
// These three shapes all build today, so their component ids - and the Guid='*' identities WiX
// derives from them - must not move. The values are pinned as literals, read out of the build
// before the change: asserting against the id-building expression would pass however the
// scheme changed, which is what this test exists to prevent.
//
// The third case matters most and is the one an "if both attributes are set" fix would have
// broken: an unrecognized registry root is skipped silently, so an item carrying BOTH
// attributes still emits only the folder component, and that package builds today.
func TestCleanupComponentIDsAreUnchangedForSingleUseItems(t *testing.T) {
	cases := []struct {
		name string
		item ir.RemoveOnUninstall
		want []string
	}{
		{
			name: "folder only",
			item: ir.RemoveOnUninstall{Folder: `[APPDATADIR]MyApp`},
			want: []string{"C_RemoveOnUninstall_0000"},
		},
		{
			name: "registry only",
			item: ir.RemoveOnUninstall{Registry: `HKLM\Software\MyApp`},
			want: []string{"C_RemoveOnUninstall_0000"},
		},
		{
			name: "both set, but the registry root is not recognized",
			item: ir.RemoveOnUninstall{Folder: `[APPDATADIR]MyApp`, Registry: `HKEY_TYPO\Software\MyApp`},
			want: []string{"C_RemoveOnUninstall_0000"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanupComponentIDs(t, tc.item)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d component(s) %v, got %d: %v", len(tc.want), tc.want, len(got), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("component id changed from %s to %s - every installed package "+
						"would see this component change identity on upgrade", tc.want[i], got[i])
				}
			}
		})
	}
}

// TestCleanupItemWithBothTargetsGetsDistinctComponentIDs is the defect half. Before the fix
// both branches named their component C_<item id>, so one element carrying folder= and
// registry= emitted the same id twice and wix build rejected the package:
//
//	error WIX0091: Duplicate Component with identifier 'C_RemoveOnUninstall_0000'
func TestCleanupItemWithBothTargetsGetsDistinctComponentIDs(t *testing.T) {
	ids := cleanupComponentIDs(t, ir.RemoveOnUninstall{
		Folder:   `[APPDATADIR]MyApp`,
		Registry: `HKLM\Software\MyApp`,
	})

	if len(ids) != 2 {
		t.Fatalf("expected a component for each target, got %v", ids)
	}
	if ids[0] == ids[1] {
		t.Errorf("both cleanup components share id %s - wix build rejects this with WIX0091", ids[0])
	}
}

// TestAddingASecondTargetDoesNotMoveOtherItems covers the realistic upgrade: an existing
// element gains a second target while the elements around it stay put. A dual-target element
// consumes exactly one item number, so the split produces two components without renumbering
// anything else — had it consumed two, every later cleanup component in an installed package
// would change identity on the next release.
func TestAddingASecondTargetDoesNotMoveOtherItems(t *testing.T) {
	first := ir.RemoveOnUninstall{Folder: `[APPDATADIR]First`}
	last := ir.RemoveOnUninstall{Registry: `HKLM\Software\Last`}

	before := cleanupComponentIDs(t, first, ir.RemoveOnUninstall{Folder: `[APPDATADIR]Middle`}, last)
	want := []string{"C_RemoveOnUninstall_0000", "C_RemoveOnUninstall_0001", "C_RemoveOnUninstall_0002"}
	for i := range want {
		if before[i] != want[i] {
			t.Fatalf("baseline ids are not what this test assumes: got %v, want %v", before, want)
		}
	}

	// The middle element gains a registry target; the sequence of elements is unchanged.
	after := cleanupComponentIDs(t, first,
		ir.RemoveOnUninstall{Folder: `[APPDATADIR]Middle`, Registry: `HKLM\Software\Middle`},
		last)

	expected := []string{
		"C_RemoveOnUninstall_0000",
		"C_RemoveOnUninstall_0001_reg",
		"C_RemoveOnUninstall_0001_dir",
		"C_RemoveOnUninstall_0002",
	}
	if len(after) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, after)
	}
	for i := range expected {
		if after[i] != expected[i] {
			t.Errorf("component %d is %s, want %s - the neighbouring items must keep their "+
				"numbers when one element gains a second target (got %v)", i, after[i], expected[i], after)
		}
	}
}

// TestCleanupComponentsAreAllReferencedByTheFeature guards the invariant WiX enforces and that
// #18 was about: the split into two components must register both, or the build fails with
// WIX0267 instead.
func TestCleanupComponentsAreAllReferencedByTheFeature(t *testing.T) {
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Items: []ir.Item{
		ir.RemoveOnUninstall{Folder: `[APPDATADIR]MyApp`, Registry: `HKLM\Software\MyApp`},
	}}}}
	output, err := NewContext(setup, variables.New(), t.TempDir()).Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	referenced := make(map[string]bool)
	for _, line := range strings.Split(output.FeatureXML, "\n") {
		if id, ok := attrValue(line, "<ComponentRef Id='"); ok {
			referenced[id] = true
		}
	}
	for _, line := range strings.Split(output.RemoveOnUninstallXML, "\n") {
		id, ok := attrValue(line, "<Component Id='")
		if !ok {
			continue
		}
		if !referenced[id] {
			t.Errorf("cleanup component %s is declared but no feature references it - "+
				"WiX rejects this as WIX0267", id)
		}
	}
}
