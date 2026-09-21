//go:build !windows

package msiread

import "fmt"

// Reading an installer database means calling msi.dll, which exists only on Windows. The package
// still compiles elsewhere - `go vet ./...` and the unix build recipes cross-compile for Windows
// from a Linux box - but fails with a message that says why rather than reporting an empty
// package, which would read as "this installer contains nothing".

type handle uintptr

type database struct {
	h      handle
	path   string
	tables map[string]bool
}

type row struct {
	h   handle
	err error
}

func openDatabase(path string) (*database, error) {
	return nil, fmt.Errorf("reading %s needs the Windows Installer library (msi.dll); "+
		"msis can only inspect packages on Windows", path)
}

func (d *database) close() error                         { return nil }
func (d *database) hasTable(string) bool                 { return false }
func (d *database) query(string, func(*row) error) error { return nil }
func (r *row) text(int) string                           { return "" }
func (r *row) number(int) (int, bool)                    { return 0, false }
func (r *row) stream(int, int) ([]byte, error)           { return nil, nil }
