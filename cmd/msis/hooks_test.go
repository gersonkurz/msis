package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
)

func TestHookArchFolder(t *testing.T) {
	cases := map[string]string{"x86": "x86", "x64": "x64", "arm64": "x64", "X64": "x64", "": "x64"}
	for in, want := range cases {
		if got := hookArchFolder(in); got != want {
			t.Errorf("hookArchFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateInstallerHooks(t *testing.T) {
	// helper: create <dir>/<arch>/msi-simplica.dll and return dir
	withDLL := func(arch string) string {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, arch), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, arch, "msi-simplica.dll"), []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	x64Vars := variables.Dictionary{"USE_INSTALLER_HOOKS": "True", "PLATFORM": "x64", "DLL_ENTRY": "msi-simplica.dll"}

	// Hooks disabled: always OK regardless of anything else.
	if err := validateInstallerHooks(variables.Dictionary{}, []string{"/no/such"}); err != nil {
		t.Errorf("hooks disabled should pass, got %v", err)
	}

	// arm64 + hooks is rejected (no native arm64 DLL; must not silently use x64).
	armVars := variables.Dictionary{"USE_INSTALLER_HOOKS": "True", "PLATFORM": "arm64", "DLL_ENTRY": "msi-simplica.dll"}
	if err := validateInstallerHooks(armVars, []string{"/no/such"}); err == nil || !strings.Contains(err.Error(), "arm64") {
		t.Errorf("arm64+hooks should be rejected, got %v", err)
	}

	// hooks on, but DLL_ENTRY missing.
	noEntry := variables.Dictionary{"USE_INSTALLER_HOOKS": "True", "PLATFORM": "x64"}
	if err := validateInstallerHooks(noEntry, []string{"/no/such"}); err == nil || !strings.Contains(err.Error(), "DLL_ENTRY") {
		t.Errorf("missing DLL_ENTRY should fail, got %v", err)
	}

	// hooks on, DLL named, but absent on every bind path -> clear failure.
	if err := validateInstallerHooks(x64Vars, []string{t.TempDir(), t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing DLL file should fail clearly, got %v", err)
	}

	// DLL present in the template folder -> OK.
	if err := validateInstallerHooks(x64Vars, []string{t.TempDir(), withDLL("x64")}); err != nil {
		t.Errorf("DLL in template folder should pass, got %v", err)
	}

	// DLL beside the .msis / WXS (a non-template bind path) -> OK (the legacy case WiX accepts).
	if err := validateInstallerHooks(x64Vars, []string{withDLL("x64"), t.TempDir()}); err != nil {
		t.Errorf("DLL on a non-template bind path should pass, got %v", err)
	}

	// x86 DLL present in the custom-templates overlay -> OK.
	x86 := variables.Dictionary{"USE_INSTALLER_HOOKS": "True", "PLATFORM": "x86", "DLL_ENTRY": "msi-simplica.dll"}
	if err := validateInstallerHooks(x86, []string{t.TempDir(), withDLL("x86")}); err != nil {
		t.Errorf("DLL in overlay should pass, got %v", err)
	}
}
