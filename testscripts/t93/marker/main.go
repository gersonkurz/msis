// Command marker is the T93 probe's stand-in installer: run by Burn, it records that it ran and
// whether its process was elevated, in %LOCALAPPDATA%\MsisProbe93\markers\<id>.txt. Burn's
// elevated engine runs as the same user, so both engines write to the same folder. It ignores its
// arguments (a prerequisite's are fixed by msis) and does nothing else.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// id is stamped per package at build time (-ldflags -X main.id=EPM): it names the marker, and it
// makes each package's file different, since WiX derives a package's cache id from its content.
var id = "unset"

func main() {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "MsisProbe93", "markers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		os.Exit(1)
	}
	elevated := exec.Command("net", "session").Run() == nil // succeeds only elevated
	if err := os.WriteFile(filepath.Join(dir, id+".txt"), []byte(fmt.Sprintf("elevated=%v\n", elevated)), 0o644); err != nil {
		os.Exit(1)
	}
}
