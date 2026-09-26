//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
)

// #80: since WiX 6, BrowseDlg's OK button carries no events of its own - WiX's stock dialog sets
// publish them - so every template with its own UI set has to. Without them OK does nothing and
// only Cancel closes the dialog. Checked in the built MSI's ControlEvent table, where the issue
// found the rows missing, for every template that reaches BrowseDlg.
func TestBrowseDlgOKSetsTheFolderAndCloses(t *testing.T) {
	requireWix(t)
	for _, tc := range []struct {
		template, platform, extra string
	}{
		{"x64", "x64", ""},
		{"x64", "x64", `<set name="INSTALL_DIR_DIALOG" value="True"/>`}, // InstallDirDlg's Change reaches it too
		{"x86", "x86", ""},
		{"minimal", "x64", ""},
		{"minimal-x86", "x86", ""},
	} {
		name := tc.template
		if tc.extra != "" {
			name += "+INSTALL_DIR_DIALOG"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "app.txt"), "x")
			script := scriptFor(t, dir, "browse.msi", `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Browse80"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{3D8A6C21-7E4B-4F90-A1C5-9B2D4E6F8A07}"/>
  <set name="PLATFORM" value="`+tc.platform+`"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  `+tc.extra+`
  <feature name="Main"><files source="app.txt" target="[INSTALLDIR]"/></feature>
</setup>`)
			templates := repoTemplates(t)
			if err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{},
				templateFolder: templates, template: filepath.Join(templates, tc.template, "template.wxs")}); err != nil {
				t.Fatalf("must build: %v", err)
			}
			rows, err := msiread.Rows(filepath.Join(dir, "browse.msi"),
				"SELECT `Control_`, `Event`, `Argument`, `Condition`, `Ordering` FROM `ControlEvent` WHERE `Dialog_` = 'BrowseDlg'", 5)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int{}
			for _, r := range rows {
				got[strings.Join(r, " ")]++
			}
			// Unconditional (WiX writes an omitted condition as "1"; a false one would disable the
			// event as surely as a missing row) and in the stock order: set the folder, then close.
			// Cancel's row comes from the dialog itself; it shows the table was read at all.
			for _, want := range []string{"OK SetTargetPath [_BrowseProperty] 1 3", "OK EndDialog Return 1 4", "Cancel EndDialog Return 1 2"} {
				if got[want] != 1 {
					t.Errorf("BrowseDlg rows %q: %d, want exactly 1 (all rows: %v)", want, got[want], rows)
				}
			}
		})
	}
}
