// Command marker is the T90 probe's stand-in installer: run by Burn as an <exe>, it writes the
// file named by its one argument, so the probe can tell that the package ran. It does nothing
// else.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// id is stamped per package at build time (-ldflags -X main.id=E32), so each package's file is
// different: WiX derives a package's cache id from its file, and identical copies collide.
var id = "unset"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: marker <file>")
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Dir(os.Args[1]), 0o755); err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], []byte("ran "+id+"\n"), 0o644); err != nil {
		os.Exit(1)
	}
}
