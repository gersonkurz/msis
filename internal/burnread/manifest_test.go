package burnread

import (
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
)

// The chain is decidable from the manifest alone - which payload is the installer, which
// container holds it, what happens when it is not carried - so these drive readChain with a
// stand-in container opener rather than a PE and a cabinet. Reaching the opener at all is
// itself the defect in two of these cases, which a fixture-based test could not observe.

// --- the signed-engine layout ---------------------------------------------------------------

// sectionSigned builds a header for a bundle whose engine was signed before its containers were
// attached: WiX's reattach step records where that signature was, and the attached containers
// begin after it rather than straight after the bootstrapper's container.
func sectionSigned(stub, sigOffset, sigSize uint32, sizes ...uint32) []byte {
	b := section(stub, sizes...)
	binary.LittleEndian.PutUint32(b[offOriginalSigOffset:], sigOffset)
	binary.LittleEndian.PutUint32(b[offOriginalSigSize:], sigSize)
	return b
}

// The bootstrapper's container sits after the stub; the ATTACHED containers begin at the end of
// the engine, which is a different place as soon as the engine carried a signature of its own.
// Assuming one layout for both reads a signed bundle's chain at the wrong offset - and then,
// because the container digest will not match, reports a perfectly good file as corrupt.
func TestASignedEngineMovesTheAttachedContainers(t *testing.T) {
	raw := make([]byte, 200)
	for i := range raw {
		raw[i] = byte(i)
	}

	// Stub 10, bootstrapper container 20 (ending at 30), a 15-byte engine signature at 30,
	// so the attached container begins at 45.
	h, err := parseSection(sectionSigned(10, 30, 15, 20, 25), len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if h.EngineSize != 45 {
		t.Fatalf("EngineSize = %d, want 45 - the signature's offset plus its size", h.EngineSize)
	}
	if got := h.offset(0); got != 10 {
		t.Errorf("container 0 at %d, want 10: the signature is after it, not before", got)
	}
	if got := h.offset(1); got != 45 {
		t.Errorf("container 1 at %d, want 45; 30 is where the engine's own signature starts", got)
	}

	data, err := h.container(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 25 || data[0] != 45 {
		t.Errorf("container 1 = %d bytes starting %d, want 25 starting 45", len(data), data[0])
	}

	// An unsigned engine keeps the simple layout, or this would break every bundle shipping
	// today - including the fixture and the release.
	plain, err := parseSection(section(10, 20, 25), len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.offset(1); got != 30 {
		t.Errorf("unsigned: container 1 at %d, want 30", got)
	}
}

// --- detached containers ----------------------------------------------------------------------

const detachedManifest = `<?xml version="1.0" encoding="utf-8"?>
<BurnManifest EngineVersion="7.0.0.0" xmlns="http://wixtoolset.org/schemas/v4/2008/Burn">
  <Container Id="Attached" FileSize="10" Attached="yes" Primary="yes" AttachedIndex="1" />
  <Container Id="Detached" FileSize="99" FilePath="detached.cab"
             DownloadUrl="https://example.invalid/detached.cab" />
  <Payload Id="Main" FilePath="Main.msi" FileSize="99" Packaging="embedded"
           SourcePath="a0" Container="Detached" />
  <Chain>
    <MsiPackage Id="Main" ProductCode="{33333333-3333-4333-8333-333333333333}" Version="1.0">
      <PayloadRef Id="Main" />
    </MsiPackage>
  </Chain>
</BurnManifest>`

// A DETACHED container is a separate file beside the bundle, and the manifest omits its
// AttachedIndex entirely - which unmarshals to 0, the bootstrapper's own container. Using the
// index without first asking whether the container is attached extracts the UX cabinet and
// checks it against the detached container's digest, so a bundle that merely HAS a detached
// container fails to read at all.
func TestADetachedContainerIsNotReadAsAttached(t *testing.T) {
	var m manifest
	if err := xml.Unmarshal([]byte(detachedManifest), &m); err != nil {
		t.Fatal(err)
	}

	// The trap, stated outright: the absent attribute really does come through as index 0.
	d := m.container("Detached")
	if d == nil {
		t.Fatal("the detached container was not parsed")
	}
	if d.AttachedIndex != 0 || d.attached() {
		t.Fatalf("AttachedIndex = %d, attached = %v; the point is that the missing attribute "+
			"reads as 0 and must therefore never be used", d.AttachedIndex, d.attached())
	}
	if a := m.container("Attached"); a == nil || !a.attached() || a.AttachedIndex != 1 {
		t.Fatalf("the attached container was misread: %+v", a)
	}

	// Opening any container at all is itself the defect, so the stand-in refuses and counts.
	opened := 0
	b := &Bundle{}
	err := b.readChain(&m, func(id string) (map[string][]byte, error) {
		opened++
		return nil, fmt.Errorf("container %q must not be opened", id)
	})
	if err != nil {
		t.Fatalf("a bundle with a detached container must still read: %v", err)
	}
	if opened != 0 {
		t.Errorf("%d container(s) were opened for a payload that is not in this file", opened)
	}

	inst := b.Packages[0].Installer()
	switch {
	case inst == nil:
		t.Fatal("the package lost its installer payload")
	case inst.Carried:
		t.Error("a payload in a detached container is not carried in the bundle")
	case inst.SHA256 != "":
		t.Errorf("msis reported a digest for bytes it does not have: %q", inst.SHA256)
	case inst.Unavailable == "":
		t.Error("a payload with no digest must say why")
	}
	for _, want := range []string{"detached container detached.cab", "example.invalid"} {
		if !strings.Contains(inst.Unavailable, want) {
			t.Errorf("Unavailable = %q, want it to mention %q", inst.Unavailable, want)
		}
	}
}

// --- which payload is the installer -------------------------------------------------------

const orderManifest = `<?xml version="1.0" encoding="utf-8"?>
<BurnManifest EngineVersion="7.0.0.0" xmlns="http://wixtoolset.org/schemas/v4/2008/Burn">
  <Container Id="C" FileSize="10" Attached="yes" Primary="yes" AttachedIndex="1" />
  <Payload Id="ActualInstaller" FilePath="Installer.msi" SourcePath="a0" Container="C" />
  <Payload Id="Main" FilePath="helper.dll" SourcePath="a1" Container="C" />
  <Chain>
    <MsiPackage Id="Main" ProductCode="{44444444-4444-4444-8444-444444444444}" Version="1.0">
      <PayloadRef Id="ActualInstaller" />
      <PayloadRef Id="Main" />
    </MsiPackage>
  </Chain>
</BurnManifest>`

// The installer is the FIRST PayloadRef: WiX writes the package payload's reference first and
// Burn takes payload[0]. Guessing "the payload whose id matches the package id" agrees almost
// always, and when it does not it hands a supplementary file the installer's role - and with
// it the digest that lands on the package component and the child document that gets linked.
func TestTheInstallerIsTheFirstPayloadRefNotTheIDMatch(t *testing.T) {
	var m manifest
	if err := xml.Unmarshal([]byte(orderManifest), &m); err != nil {
		t.Fatal(err)
	}

	b := &Bundle{}
	err := b.readChain(&m, func(string) (map[string][]byte, error) {
		return map[string][]byte{"a0": []byte("installer"), "a1": []byte("helper")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	inst := b.Packages[0].Installer()
	if inst == nil {
		t.Fatal("no installer payload")
	}
	if inst.Name != "Installer.msi" {
		t.Errorf("installer = %q, want Installer.msi; helper.dll merely shares the package's id",
			inst.Name)
	}
	payloads := b.Packages[0].Payloads
	if len(payloads) != 2 || payloads[1].Role != RoleSupplementary ||
		payloads[1].Name != "helper.dll" {
		t.Errorf("payload roles are wrong: %+v", payloads)
	}
}

// --- payloads no package references ---------------------------------------------------------

const looseManifest = `<?xml version="1.0" encoding="utf-8"?>
<BurnManifest EngineVersion="7.0.0.0" xmlns="http://wixtoolset.org/schemas/v4/2008/Burn">
  <Container Id="C" FileSize="10" Attached="yes" Primary="yes" AttachedIndex="1" />
  <Payload Id="Main" FilePath="Main.msi" SourcePath="a0" Container="C" />
  <Payload Id="Readme" FilePath="readme.txt" SourcePath="a1" Container="C" LayoutOnly="yes" />
  <Chain>
    <MsiPackage Id="Main" ProductCode="{55555555-5555-4555-8555-555555555555}" Version="1.0">
      <PayloadRef Id="Main" />
    </MsiPackage>
  </Chain>
</BurnManifest>`

// Burn writes EVERY non-UX payload at bundle level, including ones no chain package references
// - a LayoutOnly file is the usual case. Walking only the chain's PayloadRefs dropped those
// without a word: a file that ships inside the bundle and appears in no inventory.
func TestPayloadsNoPackageReferencesAreStillInventoried(t *testing.T) {
	var m manifest
	if err := xml.Unmarshal([]byte(looseManifest), &m); err != nil {
		t.Fatal(err)
	}

	b := &Bundle{}
	err := b.readChain(&m, func(string) (map[string][]byte, error) {
		return map[string][]byte{"a0": []byte("installer"), "a1": []byte("readme")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(b.Loose) != 1 {
		t.Fatalf("%d bundle-level payloads, want 1: %+v", len(b.Loose), b.Loose)
	}
	loose := b.Loose[0]
	switch {
	case loose.Name != "readme.txt":
		t.Errorf("bundle-level payload = %q, want readme.txt", loose.Name)
	case loose.Role != RoleBundle:
		t.Errorf("role = %q, want %q", loose.Role, RoleBundle)
	case !loose.LayoutOnly:
		t.Error("LayoutOnly was not recorded, so the document cannot say it is never installed")
	case !loose.Carried || loose.SHA256 == "":
		t.Errorf("it is in the container, so it must be carried and hashed: %+v", loose)
	}

	// A payload a package DOES reference must not also appear as a bundle-level one.
	for _, p := range b.Loose {
		if p.ID == "Main" {
			t.Error("a payload a package references was counted twice")
		}
	}
}
