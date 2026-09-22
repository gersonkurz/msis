package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWxs creates the target's directory, however deep, before writing (#42).
func TestWriteWxsCreatesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "probe.wxs")

	if err := writeWxs(path, "<Wix/>"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "<Wix/>" {
		t.Fatalf("content %q, err %v", got, err)
	}
}

// A bare file name has no directory to create; it is written into the working directory as
// before, and MkdirAll is not asked to create ".".
func TestWriteWxsBareNameGoesToTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := writeWxs("probe.wxs", "<Wix/>"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "probe.wxs")); err != nil {
		t.Fatal(err)
	}
}

// When the directory cannot be created - here because a regular file already has its name -
// the error names the directory, so the reader knows which path to look at rather than which
// file msis failed to open inside it.
func TestWriteWxsNamesTheDirectoryItCouldNotCreate(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "dist")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := writeWxs(filepath.Join(blocker, "probe.wxs"), "<Wix/>")
	if err == nil {
		t.Fatal("writing under a path that is a file succeeded")
	}
	if !strings.Contains(err.Error(), "creating output directory "+blocker) {
		t.Errorf("error does not name the directory: %v", err)
	}
}
