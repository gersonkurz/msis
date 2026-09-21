//go:build windows

package msiread

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// Regressions for the six defects found in the second review round.

// Finding 1: FDINOTIFICATION's layout. Hand-written padding hard-codes the amd64 answer; on 386
// a pointer aligns to 4, so psz1 belongs at offset 4 and an explicit pad displaces every field
// after it - including hf, which decides which extracted file the bytes belong to.
//
// Checked by arithmetic rather than by eye, so it holds on whichever architecture runs it.
func TestNotificationLayoutMatchesThePlatform(t *testing.T) {
	var n fdiNotification
	ptr := unsafe.Sizeof(uintptr(0))

	if got, want := unsafe.Offsetof(n.psz1), ptr; got != want {
		t.Errorf("psz1 at offset %d, want %d (one pointer in, after the 4-byte cb)", got, want)
	}
	// The three char* then void* then INT_PTR follow consecutively.
	for i, f := range []struct {
		name string
		off  uintptr
	}{
		{"psz2", unsafe.Offsetof(n.psz2)},
		{"psz3", unsafe.Offsetof(n.psz3)},
		{"pv", unsafe.Offsetof(n.pv)},
		{"hf", unsafe.Offsetof(n.hf)},
	} {
		if want := ptr * uintptr(i+2); f.off != want {
			t.Errorf("%s at offset %d, want %d", f.name, f.off, want)
		}
	}
}

// Finding 2: a spanned cabinet must abort, not spin. Returning 0 from fdintNEXT_CABINET tells
// FDI to retry, and the open callback would hand back the same bytes for ever - holding the
// extraction lock with it.
//
// The notification logic is reachable directly, so this does not need a spanned package built.
func TestSpannedCabinetAborts(t *testing.T) {
	cabExtractMu.Lock()
	defer cabExtractMu.Unlock()
	cabSpanned = false
	defer func() { cabSpanned = false }()

	got := handleNotify(fdintNEXT_CABINET, &fdiNotification{})

	if got != ^uintptr(0) {
		t.Errorf("fdintNEXT_CABINET returned %d, want -1 so FDI stops retrying", int(got))
	}
	if !cabSpanned {
		t.Error("the spanned case was not recorded, so the error would not say why")
	}

	// And an unrecognised notification still means "carry on" rather than aborting.
	if got := handleNotify(fdintCABINET_INFO, &fdiNotification{}); got != 0 {
		t.Errorf("fdintCABINET_INFO returned %d, want 0", int(got))
	}
}

// Finding 3: extraction must leave the handle registry as it found it. Cabinet handles carry the
// whole compressed buffer, so retaining them grows memory with every package inspected.
func TestExtractionLeavesNoHandlesBehind(t *testing.T) {
	before := cabRegistrySize()

	if _, err := Read(fixturePath()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after := cabRegistrySize(); after != before {
		t.Errorf("after a successful read the registry holds %d handles, was %d", after, before)
	}

	// A failed extraction must clean up too.
	if _, err := extractCabinet([]byte("MSCFnot really a cabinet at all")); err == nil {
		t.Fatal("garbage must not extract successfully")
	}
	if after := cabRegistrySize(); after != before {
		t.Errorf("after a failed extraction the registry holds %d handles, was %d", after, before)
	}
}

// Finding 4: a missing digest is excusable only when the media that carries THAT file is
// unavailable. Accepting any missing file whenever any media row was unavailable let an external
// disk 2 excuse a file genuinely absent from the embedded disk 1.
func TestOnlyTheOwningMediaExcusesAMissingFile(t *testing.T) {
	media := []Media{
		{DiskID: 1, Cabinet: "#embedded.cab", LastSequence: 10},
		{DiskID: 2, Cabinet: "external.cab", LastSequence: 20, Unavailable: "not present"},
	}

	cases := []struct {
		sequence int
		wantDisk int
	}{
		{1, 1}, {10, 1}, {11, 2}, {20, 2},
	}
	for _, c := range cases {
		m := mediaFor(media, c.sequence)
		if m == nil {
			t.Errorf("sequence %d: no media found", c.sequence)
			continue
		}
		if m.DiskID != c.wantDisk {
			t.Errorf("sequence %d attributed to disk %d, want %d", c.sequence, m.DiskID, c.wantDisk)
		}
	}

	// A sequence beyond every media row belongs to none, and so is not excused by any of them.
	if m := mediaFor(media, 999); m != nil {
		t.Errorf("sequence 999 attributed to disk %d, want no media", m.DiskID)
	}

	// Order in the table must not matter.
	reversed := []Media{media[1], media[0]}
	if m := mediaFor(reversed, 5); m == nil || m.DiskID != 1 {
		t.Errorf("attribution depends on table order: got %+v", m)
	}
}

// Finding 5: the admin custom action must not run, and the detector must be able to tell.
//
// A type-34 action runs with its working directory set to the resolved INSTALLDIR, so watching
// the test's own directory proved nothing. The fixture now writes to a fixed path under %TEMP%.
//
// The control is an actual execution. Planting the file from Go would only prove that os.Stat
// works; what has to be established is that IF the package's command ran, this detector would
// see its output. So the test runs the fixture's own command line - read out of the package,
// not copied into the test - confirms the sentinel appears, clears it, and only then reads the
// package and requires that nothing reappears.
func TestInspectionDoesNotExecuteTheAdminAction(t *testing.T) {
	sentinel := filepath.Join(os.TempDir(), "msiread-fixture-sentinel.txt")
	_ = os.Remove(sentinel)
	defer os.Remove(sentinel)

	// The action has to still be in the package, and still aimed at the full path the test
	// watches - not merely at a file of that name somewhere.
	command := adminActionTarget(t)
	if command == "" {
		t.Fatal("the fixture no longer schedules AdminSentinel; this test proves nothing")
	}
	if !strings.Contains(command, `%TEMP%\msiread-fixture-sentinel.txt`) {
		t.Fatalf("the fixture's action writes to %q, which is not the path this test watches",
			command)
	}

	// Positive control: run the package's own command and require the detector to catch it.
	runFixtureCommand(t, command)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("the control executed the action's command but the detector saw nothing at %s: %v",
			sentinel, err)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatalf("clearing the control: %v", err)
	}

	// The real check.
	if _, err := Read(fixturePath()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("%s exists: inspection executed the package's admin custom action", sentinel)
	}
}

// runFixtureCommand executes the command line the fixture's custom action carries, the way the
// installer would. It is the fixture's own string, so the control cannot drift from the thing
// it is controlling for.
func runFixtureCommand(t *testing.T, command string) {
	t.Helper()
	const prefix = "cmd.exe /c "
	if !strings.HasPrefix(command, prefix) {
		t.Fatalf("the fixture's action is %q; this control only knows how to run a cmd.exe one",
			command)
	}
	// The raw command line, not re-quoted arguments: Go's exec quoting mangles the redirection,
	// and handing the string over verbatim is also what the installer does with the Target
	// column - so the control runs what the package actually carries.
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: command}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running the control command %q: %v\n%s", command, err, out)
	}
}

// adminActionTarget returns the command AdminSentinel would run, or "" if it is gone.
func adminActionTarget(t *testing.T) string {
	t.Helper()

	// Same ownership rule as the production path: MSI handles belong to the thread that
	// created them, and that has to include the deferred close.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	db, err := openDatabase(fixturePath())
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer func() {
		if cerr := db.close(); cerr != nil {
			t.Errorf("closing the fixture: %v", cerr)
		}
	}()

	if !db.hasTable("CustomAction") || !db.hasTable("AdminExecuteSequence") {
		return ""
	}
	scheduled := false
	if err := db.query("SELECT `Action` FROM `AdminExecuteSequence`", func(r *row) error {
		if r.text(1) == "AdminSentinel" {
			scheduled = true
		}
		return nil
	}); err != nil {
		t.Fatalf("reading AdminExecuteSequence: %v", err)
	}
	if !scheduled {
		return ""
	}

	var target string
	if err := db.query("SELECT `Action`,`Target` FROM `CustomAction`", func(r *row) error {
		if r.text(1) == "AdminSentinel" {
			target = r.text(2)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading CustomAction: %v", err)
	}
	return target
}

// The decision itself, not just the attribution helper it calls. A regression restoring the old
// blanket exemption - any unavailable media excusing any missing file - would leave
// TestOnlyTheOwningMediaExcusesAMissingFile passing, because that one only exercises mediaFor.
func TestOnlyFilesOnUnavailableMediaAreExcused(t *testing.T) {
	media := []Media{
		{DiskID: 1, Cabinet: "#embedded.cab", LastSequence: 10},
		{DiskID: 2, Cabinet: "external.cab", LastSequence: 20, Unavailable: "not present"},
	}
	files := []File{
		{ID: "F_EMBEDDED", Name: "onDisk1.txt", Sequence: 5},
		{ID: "F_EXTERNAL", Name: "onDisk2.txt", Sequence: 15},
	}

	// Neither produced bytes. Only the one on the unavailable media is excused.
	missing := unexplainedFiles(files, media, map[string][]byte{})
	if len(missing) != 1 || !strings.Contains(missing[0], "F_EMBEDDED") {
		t.Errorf("unexplained = %v, want only the file on the embedded media", missing)
	}

	// With the embedded file accounted for, nothing is unexplained.
	if got := unexplainedFiles(files, media, map[string][]byte{"F_EMBEDDED": nil}); len(got) != 0 {
		t.Errorf("unexplained = %v, want none", got)
	}

	// With no media unavailable at all, both are unexplained.
	available := []Media{
		{DiskID: 1, Cabinet: "#embedded.cab", LastSequence: 10},
		{DiskID: 2, Cabinet: "#second.cab", LastSequence: 20},
	}
	if got := unexplainedFiles(files, available, map[string][]byte{}); len(got) != 2 {
		t.Errorf("unexplained = %v, want both files", got)
	}
}
