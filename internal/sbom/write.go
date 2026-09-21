package sbom

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SidecarPath is where a document for artifact lands.
//
// Named after the FULL artifact filename, not its stem: since issue #28 a BUILD_TARGET is a name
// pattern, so an auto-bundle's MSI and EXE deliberately share a stem. A stem-based sidecar would
// give both artifacts one path and the second would overwrite the first.
func SidecarPath(artifact string) string {
	return artifact + ".cdx.json"
}

// Marshal renders a document as the bytes that get written.
func Marshal(doc *Document) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// serialPattern is the RFC 4122 form CycloneDX requires. It is enforced before a serial ever
// reaches a filename: the serial in an existing sidecar is untrusted input - the file may be
// corrupt, or written by something else entirely - and a value like
// "urn:uuid:x\..\..\victim" would otherwise steer a write outside the artifact's directory and
// truncate whatever it landed on.
var serialPattern = regexp.MustCompile(
	`^urn:uuid:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Write puts the document beside its artifact, preserving any document already there.
//
// Retention, not overwrite. A BOM-Link addresses a particular serial number, so a parent
// document already in a customer's hands still references the serial that was there before -
// and reissuing the parent cannot repair a parent that has already shipped. The earlier document
// is therefore kept under its own serial and stays resolvable.
//
// Writing is atomic: a temporary file renamed on success, so a failed run cannot leave a
// half-written sidecar looking current.
//
// Every uncertainty fails rather than proceeds. If an existing document cannot be read, or
// cannot be archived safely, nothing is replaced - losing a referenced document is the outcome
// this function exists to prevent, and doing it silently would be worse than refusing.
func Write(artifact string, doc *Document) (path string, preserved string, err error) {
	path = SidecarPath(artifact)

	existing, serial, err := readExisting(path)
	if err != nil {
		return "", "", err
	}
	if existing != nil && serial != doc.SerialNumber {
		preserved, err = archive(artifact, serial, existing)
		if err != nil {
			return "", "", err
		}
	}

	data, err := Marshal(doc)
	if err != nil {
		return "", "", err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", "", err
	}
	return path, preserved, nil
}

// archive keeps the previous document under a name derived from its serial.
//
// The serial must be a well-formed UUID urn before it is allowed anywhere near a path, and the
// result must land in the artifact's own directory - checked after joining, so no amount of
// traversal in the input escapes. An archive that already exists is never overwritten: it may
// itself be the document some parent references.
func archive(artifact, serial string, data []byte) (string, error) {
	if !serialPattern.MatchString(serial) {
		return "", fmt.Errorf(
			"the document already at %s carries serial %q, which is not a well-formed "+
				"urn:uuid; refusing to replace it, because it cannot be archived under a name "+
				"derived from it and a parent document may reference it",
			SidecarPath(artifact), serial)
	}

	dir, base := filepath.Split(artifact)
	name := base + "." + strings.TrimPrefix(serial, "urn:uuid:") + ".cdx.json"
	target := filepath.Join(dir, name)

	// Belt and braces: the pattern above already excludes separators, and this proves it.
	if filepath.Dir(target) != filepath.Dir(filepath.Join(dir, base)) {
		return "", fmt.Errorf("refusing to archive outside the artifact's directory: %s", target)
	}

	// O_EXCL: an existing archive is another document, not a slot to reuse.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf(
				"a document is already archived at %s; refusing to overwrite it, since it may "+
					"be the one a parent references", target)
		}
		return "", fmt.Errorf("archiving the previous document: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("archiving the previous document: %w", err)
	}
	return target, nil
}

// readExisting reads a sidecar already present.
//
// Only "not there" counts as absent. Any other failure - a permission problem, an I/O error - is
// returned, because treating it as absence would replace a document that could not be read and
// therefore could not be preserved.
func readExisting(path string) (data []byte, serial string, err error) {
	data, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf(
			"a document already exists at %s but cannot be read, so it cannot be preserved: %w",
			path, err)
	}

	var partial struct {
		SerialNumber string `json:"serialNumber"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		// Unparseable, so its serial is unknown. archive() will refuse, which is the point:
		// something is there, and it is not this tool's place to discard it.
		return data, "", nil
	}
	return data, partial.SerialNumber, nil
}

// CanonicalForDiff renders a document with the two fields that legitimately vary between runs
// removed, so "diff two releases" is a supported operation rather than a convention each
// consumer reinvents.
//
// Exactly two fields vary: metadata.timestamp, which NTIA's minimum elements require, and
// serialNumber, which identifies the document rather than its subject.
func CanonicalForDiff(data []byte) ([]byte, error) {
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	delete(generic, "serialNumber")
	if md, ok := generic["metadata"].(map[string]any); ok {
		delete(md, "timestamp")
	}
	out, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
