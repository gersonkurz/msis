//go:build windows

package msiread

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// The sticky field-read path is text() -> r.err -> query. It is the fix that stops an unreadable
// column reading as an empty string and producing a plausible, wrong inventory, so it is worth
// covering directly rather than only through a failing query.
//
// It is covered in two halves because MSI will not produce the whole thing on demand: an
// out-of-range field index returns ERROR_SUCCESS with an empty value (checked — it is a legal
// read of nothing, not a failure), so there is no way to make a real column read fail inside a
// real query. An invalid handle does fail, which exercises the recording half; the propagation
// half is driven by setting the error the way a failed read would.

// TestFieldReadFailureIsRecorded: text() on a handle MSI rejects must record, not return "".
func TestFieldReadFailureIsRecorded(t *testing.T) {
	r := &row{h: 0} // never a valid record handle
	got := r.text(1)

	if got != "" {
		t.Errorf("text() on an invalid handle = %q, want the empty string", got)
	}
	if r.err == nil {
		t.Fatal("a failed field read must be recorded on the row, not silently discarded")
	}
	if !strings.Contains(r.err.Error(), "MsiRecordGetString") {
		t.Errorf("recorded error = %v, want it to name the failing call", r.err)
	}

	// Only the first failure is kept, so the reported error is the one that started it.
	first := r.err
	r.text(2)
	if r.err != first {
		t.Errorf("a later failure replaced the first: %v", r.err)
	}
}

// TestRecordedFieldErrorFailsTheQuery: a row error must surface from query, so a read failure
// mid-table aborts the inventory instead of truncating it.
func TestRecordedFieldErrorFailsTheQuery(t *testing.T) {
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

	sentinel := errors.New("field read failed")
	rows := 0
	err = db.query("SELECT `Name` FROM `_Tables`", func(r *row) error {
		rows++
		r.fail(sentinel) // what text() does when MsiRecordGetString fails
		return nil
	})

	if err == nil {
		t.Fatal("a recorded field-read error must fail the query")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("query error = %v, want it to wrap the field error", err)
	}
	if !strings.Contains(err.Error(), "_Tables") {
		t.Errorf("query error = %v, want it to name the query", err)
	}
	if rows != 1 {
		t.Errorf("query continued past a failed row: %d rows visited", rows)
	}
}
