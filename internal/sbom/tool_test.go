package sbom

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/burnread"
)

// #29 D7: the document names the msis that produced it by the SHA-256 of the running binary.
// Every test used to pass no binary at all, so nothing ever checked the hash was there.
func TestTheToolIsTheRunningBinaryByDigest(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	tools := doc.Metadata.Tools.Components
	if len(tools) != 1 || tools[0].Name != "msis" || tools[0].Version != "1.0" {
		t.Fatalf("metadata.tools: %+v", tools)
	}
	if got := sha256Of(tools[0].Hashes); got != want {
		t.Errorf("msis tool SHA-256 %q, want the running binary's %s", got, want)
	}
}

// #58: when the running binary cannot be hashed, no document is written - the hash used to be
// dropped and the document written anyway, no longer saying which msis produced it.
func TestNoDocumentWhenMsisCannotHashItself(t *testing.T) {
	saved := selfDigest
	defer func() { selfDigest = saved }()
	selfDigest = func() (string, error) { return "", errors.New("the executable is gone") }

	if doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "1.0"}); doc != nil || err == nil ||
		!strings.Contains(err.Error(), "the executable is gone") {
		t.Errorf("FromPackage: want no document and the hashing error, got doc=%v err=%v", doc != nil, err)
	}
	b := &burnread.Bundle{Path: "testdata/artifact.bin"}
	if doc, err := FromBundle(b, Options{MsisVersion: "1.0"}); doc != nil || err == nil ||
		!strings.Contains(err.Error(), "the executable is gone") {
		t.Errorf("FromBundle: want no document and the hashing error, got doc=%v err=%v", doc != nil, err)
	}
	if _, err := MsisTool("1.0"); err == nil {
		t.Error("MsisTool: want an error, got none")
	}
}
