//go:build windows

package main

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
)

// #97 (D35) in the built MSI: Registry rows that create or delete a key (Name "+", "-" or
// "*") and MsiLockPermissionsEx rows exist for the keys a .reg file declares, and for none of
// the keys on the path to them - HKLM\SYSTEM, HKLM\SOFTWARE and the rest. The generator test
// checks the WiX authoring; this checks what WiX turned it into, which is what Windows
// Installer acts on.
func TestRegistryPathStepsGetNoRowsInTheMSI(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "declared.reg"), "Windows Registry Editor Version 5.00\r\n\r\n"+
		"[HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Services\\EventLog\\Application\\Msis97]\r\n"+
		"\"TypesSupported\"=dword:00000007\r\n\r\n"+
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\Msis97Vendor]\r\n\r\n"+
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\Msis97Vendor\\Product\\Settings]\r\n\"A\"=\"1\"\r\n\r\n"+
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\Msis97Vendor\\Empty]\r\n")
	write(t, filepath.Join(dir, "deleteonly.reg"), "Windows Registry Editor Version 5.00\r\n\r\n"+
		"[-HKEY_LOCAL_MACHINE\\SOFTWARE\\Msis97Vendor\\Old]\r\n")
	script := scriptFor(t, dir, "steps.msi", `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="PathSteps97"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{5C1E7A93-2B4D-4E8F-9A06-3D7F1B5C9E97}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <registry file="declared.reg"/>
    <registry file="deleteonly.reg"/>
  </feature>
</setup>`)
	if err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("must build: %v", err)
	}
	msi := filepath.Join(dir, "steps.msi")

	regRows, err := msiread.Rows(msi, "SELECT `Registry`, `Root`, `Key`, `Name` FROM `Registry`", 4)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{} // Registry id -> Key
	var keyRows []string        // "<Name> <Key>" for rows that create or delete the key itself
	for _, r := range regRows {
		byID[r[0]] = r[2]
		if r[3] == "+" || r[3] == "-" || r[3] == "*" {
			keyRows = append(keyRows, r[3]+" "+r[2])
		}
	}
	sort.Strings(keyRows)
	// "*" is ForceCreateOnInstall + ForceDeleteOnUninstall: only the empty declared key.
	wantKeyRows := []string{
		`* SOFTWARE\Msis97Vendor\Empty`,
		`+ SOFTWARE\Msis97Vendor`,
		`+ SOFTWARE\Msis97Vendor\Product\Settings`,
		`+ SYSTEM\CurrentControlSet\Services\EventLog\Application\Msis97`,
	}
	if strings.Join(keyRows, "\n") != strings.Join(wantKeyRows, "\n") {
		t.Errorf("key create/delete rows:\n  %s\nwant:\n  %s", strings.Join(keyRows, "\n  "), strings.Join(wantKeyRows, "\n  "))
	}

	lockRows, err := msiread.Rows(msi, "SELECT `LockObject`, `Table` FROM `MsiLockPermissionsEx`", 2)
	if err != nil {
		t.Fatal(err)
	}
	var locked []string
	for _, r := range lockRows {
		if r[1] == "Registry" {
			locked = append(locked, byID[r[0]])
		}
	}
	sort.Strings(locked)
	wantLocked := []string{
		`SOFTWARE\Msis97Vendor`,
		`SOFTWARE\Msis97Vendor\Empty`,
		`SOFTWARE\Msis97Vendor\Product\Settings`,
		`SYSTEM\CurrentControlSet\Services\EventLog\Application\Msis97`,
	}
	if strings.Join(locked, "\n") != strings.Join(wantLocked, "\n") {
		t.Errorf("registry keys with permissions:\n  %s\nwant:\n  %s", strings.Join(locked, "\n  "), strings.Join(wantLocked, "\n  "))
	}

	// The delete-only component's keypath is in the package's own key, not in SOFTWARE and not
	// in the key it deletes.
	for _, r := range regRows {
		if strings.HasPrefix(r[3], "_msis_keypath") {
			if r[2] != `Software\msis\Packages\5C1E7A93-2B4D-4E8F-9A06-3D7F1B5C9E97` && r[2] != `SOFTWARE\Msis97Vendor` {
				t.Errorf("dummy keypath %s in %s", r[3], r[2])
			}
		}
	}
}
