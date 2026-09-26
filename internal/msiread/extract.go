package msiread

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dupDir holds the second and later files installed to one target (two components owning one
// file, #79), each below its File id, so an analyzer finds every copy under its real name.
const dupDir = ".msis-dup"

// ExtractTo reads the package as Read does and writes its payload below dir, each file at its
// install target with the root's brackets dropped: [ProgramFiles64Folder]App\a.dll becomes
// dir\ProgramFiles64Folder\App\a.dll. It returns the package and, for every file written, its
// path relative to dir mapped to its File id - the join an analyzer's findings come back on.
//
// It writes only what Read already extracted to hash, so a file Read could not read (an
// external cabinet, #31) is not written either. It never executes anything.
func ExtractTo(path, dir string) (*Package, map[string]string, error) {
	pkg, payload, err := read(path)
	if err != nil {
		return nil, nil, err
	}
	written, err := writePayload(dir, pkg.Files, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("extracting %s: %w", path, err)
	}
	return pkg, written, nil
}

// writePayload writes each file's bytes below dir at its target, in the order given (Read sorts
// by File id, so which copy of a shared target is the duplicate is stable).
//
// The names come from the package's Directory and File tables, which nothing validates: a
// package naming a directory ".." would otherwise reach outside dir and overwrite whatever is
// there. So every path is checked to stay inside dir before anything is written, and one that
// would not stops the extraction.
func writePayload(dir string, files []File, payload map[string][]byte) (map[string]string, error) {
	written := map[string]string{}
	used := map[string]bool{}
	for _, f := range files {
		data, ok := payload[f.ID]
		if !ok {
			continue
		}
		rel := targetPath(f.Target)
		if used[strings.ToLower(rel)] {
			rel = filepath.Join(dupDir, f.ID, rel)
		}
		if !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("file %s installs to %s, which would leave the extraction folder; "+
				"nothing of it is written", f.ID, f.Target)
		}
		used[strings.ToLower(rel)] = true
		out := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return nil, fmt.Errorf("extracting %s: %w", f.Target, err)
		}
		written[rel] = f.ID
	}
	return written, nil
}

// targetPath turns "[Root]a\b\name" into a relative host path Root\a\b\name.
func targetPath(target string) string {
	t := strings.Replace(strings.TrimPrefix(target, "["), "]", `\`, 1)
	return filepath.Join(strings.Split(t, `\`)...)
}
