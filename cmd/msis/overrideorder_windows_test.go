//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
)

// #86 (D33): two <files> installing one target in one feature is the override idiom (D24) - the
// copy written LAST is meant to win. WiX numbers the File table by File id, so the later-written
// copy is installed later only because it has the later id, which the counter suffix of a shared
// destination (CID_<hash>_1) gives it. With unversioned text, the copy installed later was the one
// left on disk (T79); for versioned files and a repair over a modified file, Windows Installer's
// replacement rules decide, and this test does not claim otherwise.
// It builds the shape both ways round with the real wix and checks the later-written copy has the
// higher File.Sequence: the guard against making those ids order-independent (#86's proposal),
// which would sequence whichever copy's hash sorts later last.
func TestTheLaterWrittenOverrideIsSequencedLast(t *testing.T) {
	requireWix(t)
	for name, order := range map[string][2]string{"core, then ng": {"core", "ng"}, "ng, then core": {"ng", "core"}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "core", "CONFIG", "CURRENCY.TXT"), "core\n")
			write(t, filepath.Join(dir, "ng", "CONFIG", "CURRENCY.TXT"), "ng copy, the override\n")
			script := scriptFor(t, dir, "override.msi", `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="OverrideOrder"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{6E2A9C41-3B7D-4F85-A0C2-8D1F5E3B7A86}"/>
  <set name="INSTALLDIR" value="OverrideOrder"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="`+order[0]+`\CONFIG" target="[INSTALLDIR]CONFIG"/>
    <files source="`+order[1]+`\CONFIG" target="[INSTALLDIR]CONFIG"/>
  </feature>
</setup>`)
			if err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
				t.Fatalf("building: %v", err)
			}
			pkg, err := msiread.Read(filepath.Join(dir, "override.msi"))
			if err != nil {
				t.Fatal(err)
			}
			seq := map[string]int{} // copy -> File.Sequence, told apart by size
			for _, f := range pkg.Files {
				if strings.EqualFold(f.Name, "CURRENCY.TXT") {
					copy := "core"
					if f.Size == len("ng copy, the override\n") {
						copy = "ng"
					}
					seq[copy] = f.Sequence
				}
			}
			if len(seq) != 2 {
				t.Fatalf("want both copies in the File table, got %v", seq)
			}
			if first, last := order[0], order[1]; seq[last] <= seq[first] {
				t.Errorf("%s was written last but is sequenced at %d, before %s at %d: it would not be installed last", last, seq[last], first, seq[first])
			}
		})
	}
}
