package buildrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The record is what gets published, so the rules about what may leave it are tested here
// rather than only observed end to end: nothing absolute, every hash of the file that was
// actually read, and a defined order.

func scratch(t *testing.T) (dir, script string) {
	t.Helper()
	dir = t.TempDir()
	script = filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, script
}

func put(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// A source path is published; an absolute one names the build machine and tells the reader
// nothing they can act on.
func TestSourcesAreRelativeToTheScript(t *testing.T) {
	dir, script := scratch(t)
	want := put(t, filepath.Join(dir, "lib", "deep", "a.txt"), "payload\n")

	rec := New(PathMSI, script, nil)
	rec.AddFile("FILE_ID00000", filepath.Join("lib", "deep", "a.txt"))

	if len(rec.Files) != 1 {
		t.Fatalf("%d files recorded: %+v", len(rec.Files), rec.Files)
	}
	f := rec.Files[0]
	if f.Source != "lib/deep/a.txt" {
		t.Errorf("source = %q, want lib/deep/a.txt - relative and slash-separated", f.Source)
	}
	if f.SHA256 != want {
		t.Errorf("digest = %s, want the file's own %s", f.SHA256, want)
	}
	if filepath.IsAbs(f.Source) || strings.Contains(f.Source, dir) {
		t.Errorf("an absolute path reached the record: %q", f.Source)
	}
}

// An ABSOLUTE source in the script is still published relative. A path above the script's own
// directory is legitimate - msis's own setup.msis uses "..\templates\x64" - so "relative" means
// relative, not "under".
func TestAnAbsoluteSourceIsRelativised(t *testing.T) {
	dir, script := scratch(t)
	nested := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(nested, "setup.msis")
	if err := os.WriteFile(inner, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "shared", "b.txt"), "shared payload\n")

	rec := New(PathMSI, inner, nil)
	rec.AddFile("FILE_ID00000", filepath.Join(dir, "shared", "b.txt")) // absolute

	if len(rec.Files) != 1 {
		t.Fatalf("%+v", rec.Files)
	}
	got := rec.Files[0].Source
	if got != "../shared/b.txt" {
		t.Errorf("source = %q, want ../shared/b.txt", got)
	}
	if strings.Contains(got, ":") {
		t.Errorf("a drive letter reached the record: %q", got)
	}
	_ = script
}

// A source the build cannot read is RECORDED as unresolved, not skipped. A document that
// silently omits an input it could not account for looks complete and is not.
func TestAnUnreadableSourceIsRecordedNotDropped(t *testing.T) {
	_, script := scratch(t)
	rec := New(PathMSI, script, nil)
	rec.AddFile("FILE_ID00000", "missing.txt")

	if len(rec.Files) != 0 {
		t.Errorf("a file the build could not read was recorded as if it had: %+v", rec.Files)
	}
	if len(rec.Unresolved) != 1 {
		t.Fatalf("%d unresolved entries, want 1: %v", len(rec.Unresolved), rec.Unresolved)
	}
	for _, want := range []string{"FILE_ID00000", "missing.txt"} {
		if !strings.Contains(rec.Unresolved[0], want) {
			t.Errorf("%q does not mention %q", rec.Unresolved[0], want)
		}
	}
}

// The bind path ORDER is the whole point: WiX takes the first match, and on a machine carrying
// a /CUSTOMTEMPLATES overlay that is not the template folder's copy. Recording the wrong one
// puts a confidently wrong digest against code that executes during installation.
func TestATemplateBinaryResolvesThroughBindPathsInOrder(t *testing.T) {
	dir, script := scratch(t)

	overlay := filepath.Join(dir, "overlay")
	templates := filepath.Join(dir, "templates")
	overlayDigest := put(t, filepath.Join(overlay, "x64", "hook.dll"), "the overlay's copy\n")
	put(t, filepath.Join(templates, "x64", "hook.dll"), "the template folder's copy\n")

	rec := New(PathMSI, script, []BindPath{
		{Name: "wxs", Dir: dir},
		{Name: "script", Dir: dir},
		{Name: "custom-templates", Dir: overlay},
		{Name: "templates", Dir: templates},
	})
	rec.AddTemplateBinary("x64/hook.dll")

	if len(rec.Binaries) != 1 {
		t.Fatalf("%d binaries recorded: %+v", len(rec.Binaries), rec.Binaries)
	}
	b := rec.Binaries[0]
	if b.Root != "custom-templates" {
		t.Errorf("root = %q, want custom-templates: it comes first of the two that have the "+
			"file, and WiX takes the first match", b.Root)
	}
	if b.SHA256 != overlayDigest {
		t.Errorf("digest = %s, want the overlay's %s", b.SHA256[:16], overlayDigest[:16])
	}
	if b.Source != "x64/hook.dll" || b.Name != "hook.dll" {
		t.Errorf("source = %q, name = %q", b.Source, b.Name)
	}
	// The bind path DIRECTORY is a machine path and must not be published; only its name is.
	if strings.Contains(b.Source, dir) || strings.Contains(b.Root, dir) {
		t.Error("a bind path directory reached the record")
	}
}

// Found in none of them is a stated gap, not silence.
func TestATemplateBinaryFoundNowhereIsRecorded(t *testing.T) {
	dir, script := scratch(t)
	rec := New(PathMSI, script, []BindPath{{Name: "templates", Dir: dir}})
	rec.AddTemplateBinary("x64/absent.dll")

	if len(rec.Binaries) != 0 {
		t.Errorf("%+v", rec.Binaries)
	}
	if len(rec.Unresolved) != 1 || !strings.Contains(rec.Unresolved[0], "x64/absent.dll") {
		t.Errorf("unresolved = %v, want it to name the file", rec.Unresolved)
	}
}

// A cache path names a user account, so it is published symbolically - the reader needs which
// ENTRY of the cache it was, not where that user's profile lives.
func TestACachePathIsSymbolicNotAbsolute(t *testing.T) {
	dir, script := scratch(t)
	cached := filepath.Join(dir, "msis", "prerequisites", "vcredist", "2022", "vc_redist.x64.exe")
	want := put(t, cached, "stands in for the redistributable\n")

	rec := New(PathAutoBundle, script, nil)
	rec.AddPrerequisiteFromCache("vcredist", "2022", "x64", cached,
		"https://aka.ms/vs/17/release/vc_redist.x64.exe")

	if len(rec.Prereqs) != 1 {
		t.Fatalf("%+v", rec.Prereqs)
	}
	p := rec.Prereqs[0]
	if p.CachePath != "prerequisite-cache:vcredist/2022/vc_redist.x64.exe" {
		t.Errorf("cache path = %q, want the entry rather than the machine path", p.CachePath)
	}
	if strings.Contains(p.CachePath, dir) || filepath.IsAbs(p.CachePath) {
		t.Errorf("an absolute cache path reached the record: %q", p.CachePath)
	}
	// It is hashed while the record still knows where it is - the symbolic form cannot be
	// opened later.
	if p.SHA256 != want {
		t.Errorf("digest = %q, want the cached file's %q", p.SHA256, want)
	}
	if p.DownloadURL == "" {
		t.Error("the URL the build fetched from is the provenance #34 asks for")
	}
}

// Two builds of one input must produce one record, or the document is not diffable (#29 D6).
func TestTheRecordHasATotalOrder(t *testing.T) {
	dir, script := scratch(t)
	for _, n := range []string{"c.txt", "a.txt", "b.txt"} {
		put(t, filepath.Join(dir, n), n)
	}

	build := func(order []string) *Record {
		rec := New(PathMSI, script, nil)
		for i, n := range order {
			rec.AddFile("FILE_ID0000"+string(rune('0'+i)), n)
		}
		rec.AddRuntime("vcredist", "2022", "cond")
		rec.AddRuntime("netfx", "4.8", "cond")
		rec.AddTool("wix", "7.0.0")
		rec.AddTool("msis", "3.0.5")
		rec.Sort()
		return rec
	}

	// Same files, recorded in different orders with the SAME ids, so only the sort can make
	// the two agree.
	a := build([]string{"a.txt", "b.txt", "c.txt"})
	b := build([]string{"a.txt", "b.txt", "c.txt"})
	if len(a.Files) != 3 {
		t.Fatalf("%+v", a.Files)
	}
	for i := range a.Files {
		if a.Files[i] != b.Files[i] {
			t.Errorf("file %d differs: %+v vs %+v", i, a.Files[i], b.Files[i])
		}
	}
	for i := 1; i < len(a.Files); i++ {
		if a.Files[i-1].FileID > a.Files[i].FileID {
			t.Errorf("files are not ordered: %s before %s", a.Files[i-1].FileID, a.Files[i].FileID)
		}
	}
	if a.Toolchain[0].Name != "msis" || a.Toolchain[1].Name != "wix" {
		t.Errorf("toolchain is not ordered: %+v", a.Toolchain)
	}
	if a.Runtimes[0].Type != "netfx" {
		t.Errorf("runtimes are not ordered: %+v", a.Runtimes)
	}
}

// A directory is not a file. HookDllDir names a directory, and joining it to DLL_ENTRY is the
// caller's job - handing a directory here must not produce a digest of nothing.
func TestADirectoryIsNotHashedAsAFile(t *testing.T) {
	dir, script := scratch(t)
	if err := os.MkdirAll(filepath.Join(dir, "x64"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := New(PathMSI, script, []BindPath{{Name: "script", Dir: dir}})
	rec.AddTemplateBinary("x64")

	if len(rec.Binaries) != 0 {
		t.Errorf("a directory was recorded as a binary: %+v", rec.Binaries)
	}
	if len(rec.Unresolved) != 1 {
		t.Errorf("unresolved = %v", rec.Unresolved)
	}
}

func TestScriptIsRecordedByNameOnly(t *testing.T) {
	_, script := scratch(t)
	rec := New(PathMSI, script, nil)
	if rec.Script != "setup.msis" {
		t.Errorf("script = %q, want just its name - the directory is the reader's own context",
			rec.Script)
	}
	if runtime.GOOS == "windows" && strings.Contains(rec.Script, `\`) {
		t.Errorf("a path separator reached the script name: %q", rec.Script)
	}
}
