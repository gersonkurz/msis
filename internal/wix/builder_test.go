package wix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
)

func TestNewBuilder(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["PLATFORM"] = "x64"
	vars["LANGUAGE"] = "en-us"

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", false)

	if b.WxsFile != "test.wxs" {
		t.Errorf("WxsFile = %q, want %q", b.WxsFile, "test.wxs")
	}
	if b.OutputFile != "output.msi" {
		t.Errorf("OutputFile = %q, want %q", b.OutputFile, "output.msi")
	}
	if b.Platform != "x64" {
		t.Errorf("Platform = %q, want %q", b.Platform, "x64")
	}
	if b.Language != "en-us" {
		t.Errorf("Language = %q, want %q", b.Language, "en-us")
	}
	if b.RetainWxs != false {
		t.Error("RetainWxs should be false")
	}
}

func TestNewBuilderWithRetainWxs(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", true)

	if b.RetainWxs != true {
		t.Error("RetainWxs should be true")
	}
}

// TestNewBundleBuilderSourceDir verifies the bundle builder records the .msis source dir, so it can
// be added to the WiX bind paths (the fix for source-relative LOGO_BOOTSTRAP not being bindable).
func TestNewBundleBuilderSourceDir(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "setup.exe"

	b := NewBundleBuilder(vars, "setup-bundle.wxs", "/templates", "/custom", "/project/src", false)
	if b.SourceDir != "/project/src" {
		t.Errorf("SourceDir = %q, want %q", b.SourceDir, "/project/src")
	}
}

// TestBindPathArgs covers the shared bind-path ordering used by both builders: workDir first, then
// the source dir (deduped against workDir), then custom templates, then template folder; empties
// are skipped. Uses real temp dirs so the absolute paths are platform-correct.
func TestBindPathArgs(t *testing.T) {
	work := t.TempDir()
	src := t.TempDir()
	custom := t.TempDir()
	tmpl := t.TempDir()

	// All four distinct -> all present in order.
	got := bindPathArgs(work, src, custom, tmpl)
	want := []string{"-b", work, "-b", src, "-b", custom, "-b", tmpl}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("bindPathArgs(all distinct) = %v, want %v", got, want)
	}

	// Source dir equal to workDir is not duplicated.
	got = bindPathArgs(work, work, "", "")
	want = []string{"-b", work}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("bindPathArgs(src==work) = %v, want %v", got, want)
	}

	// The source dir is included even when custom/template are empty (the bundle regression case).
	got = bindPathArgs(work, src, "", "")
	want = []string{"-b", work, "-b", src}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("bindPathArgs(src only) = %v, want %v", got, want)
	}
}

func TestGetLocalizationFile(t *testing.T) {
	// Create temp directory with mock localization files
	tmpDir, err := os.MkdirTemp("", "wix-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create wixlib directory
	wixlibDir := filepath.Join(tmpDir, "wixlib")
	if err := os.MkdirAll(wixlibDir, 0755); err != nil {
		t.Fatalf("failed to create wixlib dir: %v", err)
	}

	// Create mock localization file
	locFile := filepath.Join(wixlibDir, "en-us.wxl")
	if err := os.WriteFile(locFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write loc file: %v", err)
	}

	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["LANGUAGE"] = "en-us"

	b := NewBuilder(vars, "test.wxs", tmpDir, "", "", false)

	result := b.getLocalizationFile()
	if result != locFile {
		t.Errorf("getLocalizationFile() = %q, want %q", result, locFile)
	}
}

func TestGetLocalizationFileNotFound(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["LANGUAGE"] = "nonexistent"

	b := NewBuilder(vars, "test.wxs", "/nonexistent/path", "", "", false)

	result := b.getLocalizationFile()
	if result != "" {
		t.Errorf("getLocalizationFile() = %q, want empty string", result)
	}
}

func TestGetLocalizationFileNoLanguage(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", false)

	result := b.getLocalizationFile()
	if result != "" {
		t.Errorf("getLocalizationFile() = %q, want empty string", result)
	}
}

func TestCleanupWithRetainWxs(t *testing.T) {
	// Create temp directory with mock files
	tmpDir, err := os.MkdirTemp("", "wix-cleanup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wxsFile := filepath.Join(tmpDir, "test.wxs")
	msiFile := filepath.Join(tmpDir, "test.msi")
	pdbFile := filepath.Join(tmpDir, "test.wixpdb")

	// Create mock files
	os.WriteFile(wxsFile, []byte("test"), 0644)
	os.WriteFile(pdbFile, []byte("test"), 0644)

	vars := variables.New()
	vars["BUILD_TARGET"] = msiFile

	b := NewBuilder(vars, wxsFile, tmpDir, "", "", true) // retain wxs
	b.cleanup()

	// WXS should still exist (retained)
	if _, err := os.Stat(wxsFile); os.IsNotExist(err) {
		t.Error("wxs file should be retained")
	}

	// PDB should be deleted
	if _, err := os.Stat(pdbFile); !os.IsNotExist(err) {
		t.Error("wixpdb file should be deleted")
	}
}

func TestCleanupWithoutRetainWxs(t *testing.T) {
	// Create temp directory with mock files
	tmpDir, err := os.MkdirTemp("", "wix-cleanup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wxsFile := filepath.Join(tmpDir, "test.wxs")
	msiFile := filepath.Join(tmpDir, "test.msi")
	pdbFile := filepath.Join(tmpDir, "test.wixpdb")

	// Create mock files
	os.WriteFile(wxsFile, []byte("test"), 0644)
	os.WriteFile(pdbFile, []byte("test"), 0644)

	vars := variables.New()
	vars["BUILD_TARGET"] = msiFile

	b := NewBuilder(vars, wxsFile, tmpDir, "", "", false) // don't retain wxs
	b.cleanup()

	// WXS should be deleted
	if _, err := os.Stat(wxsFile); !os.IsNotExist(err) {
		t.Error("wxs file should be deleted")
	}

	// PDB should be deleted
	if _, err := os.Stat(pdbFile); !os.IsNotExist(err) {
		t.Error("wixpdb file should be deleted")
	}
}

func TestParseMajorVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"6.0.2+b3f3403", 6},
		{"7.0.0", 7},
		{"7.0.0-rc.1", 7},
		{"  6.0.2  ", 6},
		{"5.0.2+aa65968c", 5},
		{"10.1.0", 10},
		{"(unavailable)", 0},
		{"", 0},
		{"vNext", 0},
	}
	for _, c := range cases {
		if got := parseMajorVersion(c.in); got != c.want {
			t.Errorf("parseMajorVersion(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEulaAcceptArgs(t *testing.T) {
	// WiX 6 and earlier: no EULA gate, no flag.
	for _, major := range []int{0, 5, 6} {
		if got := eulaAcceptArgs(major); got != nil {
			t.Errorf("eulaAcceptArgs(%d) = %v, want nil", major, got)
		}
	}

	// WiX 7+: --acceptEula wix<major> (the build flag requires the EULA id value).
	cases := map[int][]string{
		7: {"--acceptEula", "wix7"},
		8: {"--acceptEula", "wix8"},
	}
	for major, want := range cases {
		got := eulaAcceptArgs(major)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("eulaAcceptArgs(%d) = %v, want %v", major, got, want)
		}
	}
}

func TestIsWixAvailable(t *testing.T) {
	// This test just verifies the function doesn't panic
	// The result depends on whether WiX is installed
	result := IsWixAvailable()
	t.Logf("IsWixAvailable() = %v", result)
}

func TestPlatformLowercase(t *testing.T) {
	// Verify platform is normalized to lowercase for wix CLI
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["PLATFORM"] = "X64" // uppercase

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", false)

	// Platform should be stored as-is
	if b.Platform != "X64" {
		t.Errorf("Platform = %q, want %q", b.Platform, "X64")
	}

	// The runWixBuild will convert to lowercase when building args
	// We can't test that directly without mocking exec, but we can
	// verify the builder stores the value correctly
	if !strings.EqualFold(b.Platform, "x64") {
		t.Errorf("Platform should be case-insensitively equal to x64")
	}
}

// TestTrimPackageSuffix covers the trap that truncated versions (issue #27): filepath.Ext reads
// the last dot-segment of a version as an extension, so the old
// TrimSuffix(name, filepath.Ext(name)) + ".exe" turned "MyApp-1.0.0" into "MyApp-1.0.exe". The
// ".msi" cases are the naming an auto-bundling package with BUILD_TARGET has always had, and
// must survive: the MSI is "App-1.0.0.msi", the bundle beside it "App-1.0.0.exe".
func TestTrimPackageSuffix(t *testing.T) {
	tests := []struct{ in, wantTrim, wantEnsure string }{
		{"MyApp-1.0.0", "MyApp-1.0.0", "MyApp-1.0.0.exe"},
		{"X86 Bundle Probe-1.0.0", "X86 Bundle Probe-1.0.0", "X86 Bundle Probe-1.0.0.exe"},
		{"setup.exe", "setup", "setup.exe"},
		{"SETUP.EXE", "SETUP", "SETUP.exe"},
		{"App-1.0.0.msi", "App-1.0.0", "App-1.0.0.exe"},
		{`dist\App-1.0.0.MSI`, `dist\App-1.0.0`, `dist\App-1.0.0.exe`},
		{"setup", "setup", "setup.exe"},
		{"dist/msis-3.0.5-setup.exe", "dist/msis-3.0.5-setup", "dist/msis-3.0.5-setup.exe"},
		{"", "", ".exe"},
	}
	for _, tt := range tests {
		if got := TrimPackageSuffix(tt.in); got != tt.wantTrim {
			t.Errorf("TrimPackageSuffix(%q) = %q, want %q", tt.in, got, tt.wantTrim)
		}
		if got := TargetPath(tt.in, "exe"); got != tt.wantEnsure {
			t.Errorf("TargetPath(%q, exe) = %q, want %q", tt.in, got, tt.wantEnsure)
		}
	}
}

// TestNewBundleBuilderOutputFile: without BUILD_TARGET the bundle is named from the .wxs path, so
// it lands beside the source. It used to be PRODUCT_NAME + "-" + PRODUCT_VERSION - relative, so
// filepath.Abs in runWixBuild resolved it against the process's working directory (issue #27).
func TestNewBundleBuilderOutputFile(t *testing.T) {
	dir := t.TempDir()
	vars := variables.New()
	vars["PRODUCT_NAME"] = "X86 Bundle Probe"
	vars["PRODUCT_VERSION"] = "1.0.0"

	b := NewBundleBuilder(vars, filepath.Join(dir, "probe-bundle.wxs"), "", "", dir, false)
	if want := filepath.Join(dir, "probe.exe"); b.OutputFile != want {
		t.Errorf("default OutputFile = %q, want %q", b.OutputFile, want)
	}

	// BUILD_TARGET wins, and keeps its full version even without an extension.
	vars["BUILD_TARGET"] = "out/Probe-1.0.0"
	b = NewBundleBuilder(vars, filepath.Join(dir, "probe-bundle.wxs"), "", "", dir, false)
	if want := "out/Probe-1.0.0.exe"; b.OutputFile != want {
		t.Errorf("BUILD_TARGET OutputFile = %q, want %q", b.OutputFile, want)
	}

	// A package that auto-bundles shares its variables with the MSI, so BUILD_TARGET normally
	// names a .msi; the bundle beside it must keep being "<same name>.exe", version and all.
	vars["BUILD_TARGET"] = "out/Probe-1.0.0.msi"
	b = NewBundleBuilder(vars, filepath.Join(dir, "probe-bundle.wxs"), "", "", dir, false)
	if want := "out/Probe-1.0.0.exe"; b.OutputFile != want {
		t.Errorf("BUILD_TARGET .msi OutputFile = %q, want %q", b.OutputFile, want)
	}
}

// TestTargetPathRenamesExtension covers issue #28: BUILD_TARGET is a name pattern, so every
// artifact takes its directory and stem and supplies its own extension. Handing "probe.exe" to
// the MSI build made `wix build` infer a Bundle output type from the extension and fail with
// WIX0341; an extensionless target failed with WIX7014 ("output type: .0").
func TestTargetPathRenamesExtension(t *testing.T) {
	cases := []struct{ target, ext, want string }{
		{"probe.exe", "msi", "probe.msi"},
		{"probe.msi", "exe", "probe.exe"},
		{"probe.exe", "wxs", "probe.wxs"},
		{"probe-1.0.0", "msi", "probe-1.0.0.msi"},
		{"probe-1.0.0", "wxs", "probe-1.0.0.wxs"},
		{`dist\msis-3.0.5-setup.exe`, "msi", `dist\msis-3.0.5-setup.msi`},
		{"probe.MSI", "exe", "probe.exe"},
	}
	for _, c := range cases {
		if got := TargetPath(c.target, c.ext); got != c.want {
			t.Errorf("TargetPath(%q, %q) = %q, want %q", c.target, c.ext, got, c.want)
		}
	}
}

// TestNewBuilderMsiOutputFromBuildTarget: the MSI is always named ".msi", whatever extension
// BUILD_TARGET carries. Before issue #28 the value was taken verbatim, so a package that
// auto-bundles - where BUILD_TARGET describes the bundle and the MSI is an intermediate the
// user never names - asked WiX to write an MSI to a .exe path and the build failed.
func TestNewBuilderMsiOutputFromBuildTarget(t *testing.T) {
	cases := []struct{ target, want string }{
		{"probe.exe", "probe.msi"},
		{"probe.msi", "probe.msi"},
		{"probe-1.0.0", "probe-1.0.0.msi"},
		{`dist\app-2.1.0.exe`, `dist\app-2.1.0.msi`},
	}
	for _, c := range cases {
		vars := variables.New()
		vars["BUILD_TARGET"] = c.target
		if got := NewBuilder(vars, "probe.wxs", "", "", "", false).OutputFile; got != c.want {
			t.Errorf("BUILD_TARGET %q -> OutputFile %q, want %q", c.target, got, c.want)
		}
	}

	// No BUILD_TARGET: named from the .wxs, beside the source.
	dir := t.TempDir()
	b := NewBuilder(variables.New(), filepath.Join(dir, "probe.wxs"), "", "", "", false)
	if want := filepath.Join(dir, "probe.msi"); b.OutputFile != want {
		t.Errorf("default OutputFile = %q, want %q", b.OutputFile, want)
	}
}

// TestDefaultOutputKeepsWholeSourceStem: with no BUILD_TARGET the name comes from the .wxs, and
// that stem is the source's own name - it must not be normalized a second time. A source called
// "probe.exe.msis" builds "probe.exe.msi"; trimming again would build "probe.msi" and overwrite
// what the neighbouring "probe.msis" builds. Found in review of issue #28.
func TestDefaultOutputKeepsWholeSourceStem(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ wxs, wantMsi string }{
		{"probe.wxs", "probe.msi"},
		{"probe.exe.wxs", "probe.exe.msi"},
		{"probe.msi.wxs", "probe.msi.msi"},
		{"App-1.0.0.wxs", "App-1.0.0.msi"},
	}
	for _, c := range cases {
		got := NewBuilder(variables.New(), filepath.Join(dir, c.wxs), "", "", "", false).OutputFile
		if want := filepath.Join(dir, c.wantMsi); got != want {
			t.Errorf("NewBuilder(%q).OutputFile = %q, want %q", c.wxs, got, want)
		}
	}

	// The bundle default is derived the same way, after dropping the "-bundle" infix.
	bundles := []struct{ wxs, wantExe string }{
		{"probe-bundle.wxs", "probe.exe"},
		{"probe.exe-bundle.wxs", "probe.exe.exe"},
		{"probe.msi-bundle.wxs", "probe.msi.exe"},
	}
	for _, c := range bundles {
		got := NewBundleBuilder(variables.New(), filepath.Join(dir, c.wxs), "", "", dir, false).OutputFile
		if want := filepath.Join(dir, c.wantExe); got != want {
			t.Errorf("NewBundleBuilder(%q).OutputFile = %q, want %q", c.wxs, got, want)
		}
	}
}
