//go:build windows

package msiread

import (
	"runtime"
	"testing"
)

// Finding 3: MSI answers 1615 for a missing TABLE and for a missing COLUMN alike, so reading
// that code as "absent table" swallowed real query errors and produced a complete-looking, empty
// inventory. Absence is now established from _Tables.
//
// Windows-only: it needs a real database to ask.
func TestAbsentTableAndAbsentColumnAreDistinguished(t *testing.T) {
	msi := findReleasedPackage(t)
	if msi == "" {
		t.Skip("no released .msi in bootstrap/dist; run `just release-all` to produce one")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	db, err := openDatabase(msi)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer db.close()

	// The release installs no services, so this table really is absent.
	if db.hasTable("ServiceInstall") {
		t.Skip("this package has a ServiceInstall table; the absent-table case needs one without")
	}
	if err := optional(db, "ServiceInstall",
		"SELECT `ServiceInstall`,`Name` FROM `ServiceInstall`", func(*row) error {
			t.Error("a row was returned from a table that does not exist")
			return nil
		}); err != nil {
		t.Errorf("an absent table must read as no rows, got %v", err)
	}

	// A table that IS present, queried for a column that is not: the old code reported this as
	// an absent table and returned success.
	if !db.hasTable("File") {
		t.Fatal("the package has no File table")
	}
	err = optional(db, "File", "SELECT `NoSuchColumn` FROM `File`", func(*row) error { return nil })
	if err == nil {
		t.Error("a query naming a column that does not exist must fail, not read as absent")
	}
}

// Finding 1: MSI handles belong to the thread that created them, and Go may move a goroutine
// between OS threads at almost any call. Read pins the thread for the whole database lifetime,
// including the deferred close.
//
// This drives concurrent reads with scheduling pressure: without the pin, closes land on
// whatever thread the goroutine drifted to, which is exactly what MsiCloseHandle forbids.
func TestConcurrentReadsUnderSchedulingPressure(t *testing.T) {
	msi := findReleasedPackage(t)
	if msi == "" {
		t.Skip("no released .msi in bootstrap/dist; run `just release-all` to produce one")
	}

	// Plenty of runnable goroutines so the scheduler has every reason to migrate.
	stop := make(chan struct{})
	for i := 0; i < runtime.NumCPU()*2; i++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					runtime.Gosched()
				}
			}
		}()
	}
	defer close(stop)

	const readers, rounds = 4, 10
	errs := make(chan error, readers*rounds)
	done := make(chan struct{})
	for i := 0; i < readers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for r := 0; r < rounds; r++ {
				if _, err := Read(msi); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for i := 0; i < readers; i++ {
		<-done
	}
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Read failed: %v", err)
	}
}
