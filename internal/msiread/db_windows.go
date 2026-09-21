//go:build windows

package msiread

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This is the whole platform-dependent surface: opening an installer database read-only, running
// a query, and reading fields and streams out of the result. Everything else in the package is
// ordinary Go on top of it.
//
// msi.dll is called through syscall rather than cgo deliberately: the shipped binaries build
// with CGO_ENABLED=0 and cross-compile to windows/{amd64,386,arm64}, and cgo would break that.
//
// NewLazySystemDLL, not NewLazyDLL: the plain loader searches the executable's own directory
// first, and msi.dll is neither on Go's protected list nor in KnownDLLs. A msi.dll dropped beside
// a portable msis.exe would then be loaded and run - during a command whose whole promise is that
// it does not execute anything. The system loader restricts the search to System32.
var (
	msiDLL = windows.NewLazySystemDLL("msi.dll")

	procOpenDatabase     = msiDLL.NewProc("MsiOpenDatabaseW")
	procDatabaseOpenView = msiDLL.NewProc("MsiDatabaseOpenViewW")
	procViewExecute      = msiDLL.NewProc("MsiViewExecute")
	procViewFetch        = msiDLL.NewProc("MsiViewFetch")
	procRecordGetString  = msiDLL.NewProc("MsiRecordGetStringW")
	procRecordGetInteger = msiDLL.NewProc("MsiRecordGetInteger")
	procRecordReadStream = msiDLL.NewProc("MsiRecordReadStream")
	procCloseHandle      = msiDLL.NewProc("MsiCloseHandle")
)

const (
	errSuccess     = 0
	errMoreData    = 234
	errNoMoreItems = 259
)

// msiNullInteger is what MsiRecordGetInteger returns for a NULL column.
const msiNullInteger = -2147483648

type handle uintptr

// close releases an MSI handle. The error is returned rather than dropped: a handle that will
// not close is a leak in a process that may inspect many packages, and silence would hide it.
//
// Every caller must already be on the thread that created the handle - see lockThread.
func (h handle) close() error {
	if h == 0 {
		return nil
	}
	r, _, _ := procCloseHandle.Call(uintptr(h))
	if r != errSuccess {
		return fmt.Errorf("MsiCloseHandle returned %d", r)
	}
	return nil
}

// database is an open, read-only installer database.
type database struct {
	h    handle
	path string

	// tables is the set of table names the database actually contains, read once from
	// _Tables. It exists because MSI reports a missing table and a missing column with the
	// same code, so the only way to tell them apart is to ask what is there.
	tables map[string]bool
}

// openDatabase opens path read-only. It never writes, and never executes anything in the
// package.
func openDatabase(path string) (*database, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var h uintptr
	// MSIDBOPEN_READONLY is (LPCWSTR)0 - a small integer cast to a pointer rather than a
	// string, which is why this argument is a bare 0 and not a UTF-16 pointer.
	r, _, _ := procOpenDatabase.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&h)))
	if r != errSuccess {
		return nil, fmt.Errorf("opening %s as an installer database: MsiOpenDatabase returned %d "+
			"(is it an .msi?)", path, r)
	}

	db := &database{h: handle(h), path: path, tables: map[string]bool{}}
	if err := db.query("SELECT `Name` FROM `_Tables`", func(r *row) error {
		db.tables[r.text(1)] = true
		return nil
	}); err != nil {
		db.close()
		return nil, fmt.Errorf("listing the tables of %s: %w", path, err)
	}
	return db, nil
}

func (d *database) close() error {
	err := d.h.close()
	d.h = 0
	return err
}

// hasTable reports whether the database contains a table, read from _Tables rather than inferred
// from a failed query.
func (d *database) hasTable(name string) bool { return d.tables[name] }

// row is one fetched record. Its handle is valid only until the next fetch.
//
// A field read that fails records the first error on the row; the query loop returns it. Without
// that, an unreadable column would read as an empty string and the inventory would come out
// plausible and wrong - the failure mode this package exists to avoid.
type row struct {
	h   handle
	err error
}

func (r *row) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// text reads a string column. MSI reports the required length rather than truncating, so the
// call is made twice: once to size the buffer, once to fill it. A NULL or empty column reads as
// "" with no error; anything else is recorded.
func (r *row) text(field int) string {
	var n uint32
	buf := make([]uint16, 1)
	ret, _, _ := procRecordGetString.Call(uintptr(r.h), uintptr(field),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if ret == errMoreData {
		buf = make([]uint16, n+1)
		n++
		ret, _, _ = procRecordGetString.Call(uintptr(r.h), uintptr(field),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	}
	if ret != errSuccess {
		r.fail(fmt.Errorf("reading field %d as text: MsiRecordGetString returned %d", field, ret))
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// number reads an integer column. A NULL column reads as 0 with ok false, so a caller can tell
// "absent" from "zero".
func (r *row) number(field int) (int, bool) {
	v, _, _ := procRecordGetInteger.Call(uintptr(r.h), uintptr(field))
	n := int(int32(v))
	if n == msiNullInteger {
		return 0, false
	}
	return n, true
}

// stream reads a binary column whole.
func (r *row) stream(field int, hint int) ([]byte, error) {
	if hint <= 0 {
		hint = 64 * 1024
	}
	var out []byte
	buf := make([]byte, hint)
	for {
		n := uint32(len(buf))
		ret, _, _ := procRecordReadStream.Call(uintptr(r.h), uintptr(field),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
		if ret != errSuccess {
			return nil, fmt.Errorf("MsiRecordReadStream returned %d", ret)
		}
		if n == 0 {
			return out, nil
		}
		out = append(out, buf[:n]...)
	}
}

// query runs an MSI SQL statement and calls fn for each row. fn must not retain the row: its
// handle is closed before the next fetch.
//
// Every failure is reported. MSI answers 1615 (bad query syntax) for a missing table AND for a
// missing column, so this layer does not try to interpret it - callers ask hasTable instead.
func (d *database) query(sql string, fn func(*row) error) error {
	q, err := syscall.UTF16PtrFromString(sql)
	if err != nil {
		return err
	}

	var view uintptr
	ret, _, _ := procDatabaseOpenView.Call(uintptr(d.h), uintptr(unsafe.Pointer(q)),
		uintptr(unsafe.Pointer(&view)))
	if ret != errSuccess {
		return fmt.Errorf("MsiDatabaseOpenView returned %d for %s", ret, sql)
	}
	viewHandle := handle(view)
	defer viewHandle.close()

	if ret, _, _ := procViewExecute.Call(view, 0); ret != errSuccess {
		return fmt.Errorf("MsiViewExecute returned %d for %s", ret, sql)
	}

	for {
		var rec uintptr
		ret, _, _ := procViewFetch.Call(view, uintptr(unsafe.Pointer(&rec)))
		if ret == errNoMoreItems {
			return nil
		}
		if ret != errSuccess {
			return fmt.Errorf("MsiViewFetch returned %d for %s", ret, sql)
		}

		r := &row{h: handle(rec)}
		callErr := fn(r)
		closeErr := handle(rec).close()

		switch {
		case callErr != nil:
			return callErr
		case r.err != nil:
			return fmt.Errorf("%s: %w", sql, r.err)
		case closeErr != nil:
			return fmt.Errorf("%s: %w", sql, closeErr)
		}
	}
}
