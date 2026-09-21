//go:build ignore

// gen builds the test corpus: real CycloneDX documents, produced by the real emitters from the
// repository's own committed fixtures, so the index is tested against msis output rather than
// against JSON someone wrote to match the index.
//
// Windows only, because reading an MSI needs msi.dll. The result is committed, so the index's
// own tests need neither Windows nor a WiX toolchain.
//
// It regenerates the WHOLE corpus: the documents it produces, and the two hand-written
// fixtures under static/ that it copies in. It used to delete the corpus and rewrite only what
// it generated, while the README told the reader to leave a hand-written file alone - an
// instruction that could not be followed, because the file was already gone.
//
// Usage, from this directory:  go run gen.go
package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gersonkurz/msis/internal/burnread"
	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom"
)

const corpus = "corpus"

func main() {
	if err := os.RemoveAll(corpus); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		log.Fatal(err)
	}

	// One directory per release, which is how a real corpus is laid out and what makes the
	// bundle's BOM-Link resolve: the engine's chained payload is fixture.msi, so the
	// document it links to is the one sitting beside it under the same name.
	if err := os.MkdirAll(filepath.Join(corpus, "1.0.0"), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(corpus, "2.0.0"), 0o755); err != nil {
		log.Fatal(err)
	}

	// The artifacts are copied in so that each document's subject digest is the digest of a
	// file that really sat beside it.
	msi := copyIn("1.0.0/fixture.msi", filepath.Join("..", "..", "..", "internal", "msiread", "testdata", "fixture.msi"))
	exe := copyIn("1.0.0/fixture.exe", filepath.Join("..", "..", "..", "internal", "burnread", "testdata", "fixture.exe"))
	msi2 := copyIn("2.0.0/fixture.msi", filepath.Join("..", "..", "..", "internal", "msiread", "testdata", "fixture.msi"))

	// Release 1 of the product.
	pkg, err := msiread.Read(msi)
	if err != nil {
		log.Fatal(err)
	}
	write(msi, pkg, "1.0.0", at(1), "11111111-1111-4111-8111-111111111111")

	// Release 2 of the SAME product: same UpgradeCode, later version, one payload's bytes
	// changed and one file gone. The corpus has only one fixture MSI, so this second release
	// is synthesised - by the real emitter, from a modified reading of the real package, so
	// the DOCUMENT is genuine even though the package it describes is hypothetical. It is
	// what makes the release-diff query testable at all.
	pkg2, err := msiread.Read(msi2)
	if err != nil {
		log.Fatal(err)
	}
	pkg2.Properties["ProductVersion"] = "2.0.0"
	pkg2.Properties["ProductCode"] = "{9F1E7A62-0C4D-4B33-9A77-1E5B0D2C8A44}" // regenerated per build, as MSI does
	if len(pkg2.Files) > 1 {
		// One payload's bytes change, one file goes, one arrives - so the release-diff
		// query is exercised on all three of its answers rather than two.
		pkg2.Files[0].SHA256 = "00000000000000000000000000000000000000000000000000000000000000ff"
		pkg2.Files = pkg2.Files[:len(pkg2.Files)-1]
		added := pkg2.Files[0]
		added.ID = "F_Added"
		added.Name = "added.txt"
		added.Component = "C_Added"
		added.Target = `[ProgramFiles64Folder]MsiReadFixturedded.txt`
		added.SHA256 = "00000000000000000000000000000000000000000000000000000000000000aa"
		pkg2.Files = append(pkg2.Files, added)
		pkg2.Components = append(pkg2.Components, msiread.Component{
			ID: "C_Added", GUID: "{7C4A1E55-3B21-4D0F-9E88-5A6C2B9F1D30}", Directory: "INSTALLDIR",
		})
	}
	write(msi2, pkg2, "2.0.0", at(2), "22222222-2222-4222-8222-222222222222")

	// A SECOND document for release 2.0.0: the same product, the same version, a different
	// artifact - which is what msis's own releases look like (3.0.5 ships an x64, an x86 and
	// an arm64 MSI under one UpgradeCode and one ProductVersion). It is here because a
	// release naming several documents is the case that made a release-level diff
	// cross-match every document on one side against every document on the other.
	// Built from the SAME package as the other 2.0.0 document, so the two describe the same
	// content and differ only in ProductCode and artifact name. That is the point: diffing
	// two documents of one release must find nothing, and it is what a release-level diff
	// got wrong by joining every document on one side to every document on the other.
	pkg3 := pkg2
	pkg3.Properties["ProductCode"] = "{2B8D5F30-77A1-4C62-9E14-3D0A5B7C9E28}"
	variant := copyIn("2.0.0/fixture-x86.msi", filepath.Join("..", "..", "..", "internal", "msiread", "testdata", "fixture.msi"))
	write(variant, pkg3, "2.0.0", at(4), "44444444-4444-4444-8444-444444444444")

	// The bundle, whose document links to release 1's - the digests agree because the bundle
	// really does carry that MSI.
	b, err := burnread.Read(exe)
	if err != nil {
		log.Fatal(err)
	}
	doc, err := sbom.FromBundle(b, options(at(3), "33333333-3333-4333-8333-333333333333"))
	if err != nil {
		log.Fatal(err)
	}
	save(exe, doc)

	// The artifacts themselves are not part of the corpus; only the documents are.
	for _, p := range []string{msi, exe, msi2, variant} {
		os.Remove(p)
	}

	// The hand-written fixtures, copied in rather than left to survive a RemoveAll they
	// cannot survive. One is msis's own release document (no serialNumber, no bom-refs); the
	// other is not valid CycloneDX at all, which is its entire job.
	copyTree("static", corpus)

	fmt.Println("wrote the corpus to", corpus)
}

func write(artifact string, pkg *msiread.Package, version, ts, serial string) {
	pkg.Properties["ProductVersion"] = version
	doc, err := sbom.FromPackage(pkg, options(ts, serial))
	if err != nil {
		log.Fatal(err)
	}
	save(artifact, doc)
}

func save(artifact string, doc *sbom.Document) {
	data, err := sbom.Marshal(doc)
	if err != nil {
		log.Fatal(err)
	}
	out := artifact + ".cdx.json"
	if err := os.WriteFile(out, data, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("  ", out)
}

// options pins the two fields that legitimately vary between runs, so regenerating the corpus
// produces the same documents and the committed fixtures do not churn.
func options(ts, serial string) sbom.Options {
	return sbom.Options{
		MsisVersion: "test",
		Now:         func() time.Time { t, _ := time.Parse(time.RFC3339, ts); return t },
		NewSerial:   func() (string, error) { return "urn:uuid:" + serial, nil },
	}
}

func at(n int) string {
	return time.Date(2026, 9, 21, 12, n, 0, 0, time.UTC).Format(time.RFC3339)
}

func copyIn(name, from string) string {
	data, err := os.ReadFile(from)
	if err != nil {
		log.Fatal(err)
	}
	to := filepath.Join(corpus, name)
	if err := os.WriteFile(to, data, 0o644); err != nil {
		log.Fatal(err)
	}
	return to
}

// copyTree copies every file under src into dst, preserving relative paths.
func copyTree(src, dst string) {
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fmt.Println("  ", out)
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		log.Fatal(err)
	}
}
