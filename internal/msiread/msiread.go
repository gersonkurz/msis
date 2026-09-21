// Package msiread reads a built MSI: its tables, its resolved install targets, and the payload
// held in the database itself.
//
// It exists because the .msis script is not the best description of what shipped. The generator
// knows the payload it generated; the package knows everything that ended up in it, including
// what the WiX template and toolchain contributed — a released msis package carries seven
// Binary-table streams (WixUI resources and WiX's own Util custom-action DLL) that appear
// nowhere in the generator's file tree. For a question like "what is in the version at customer
// X", the artifact is also the only thing that still exists: re-resolving an old tag may not
// reproduce.
//
// Reading is strictly passive. It never runs the package. An administrative install
// (`msiexec /a`) would lay the payload out using the platform's own engine and would be far less
// code, but `/qn` suppresses only the UI: AdminExecuteSequence still runs, so an arbitrary
// third-party package would execute code during what is advertised as inspection. It also omits
// features at level zero, so its output is not even complete.
package msiread

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// Package is everything read out of one installer database.
//
// Slices are sorted by a defined key so that two reads of one package produce identical output;
// the SBOM built on top of this has to be diffable, and MSI returns rows in no guaranteed order.
type Package struct {
	Path       string
	Properties map[string]string

	Files       []File
	Binaries    []Binary
	Media       []Media
	Registry    []RegistryEntry
	Services    []Service
	Shortcuts   []Shortcut
	Components  []Component
	Directories map[string]Directory
}

// File is one row of the File table, with its install target resolved.
type File struct {
	ID        string // the File key, e.g. FILE_ID00006
	Name      string // long name
	ShortName string // 8.3 name, when the package carries one
	Component string
	Size      int
	Version   string // the file's own version resource, when MSI recorded one
	Language  string
	Sequence  int
	Target    string // symbolic, e.g. [ProgramFiles64Folder]MSIS\templates\x64\msi-simplica.dll
}

// Binary is a row of the Binary table: a stream held in the database rather than in a cabinet.
// Custom-action DLLs and UI resources live here, which is why they are invisible to a
// build-time view of the payload.
type Binary struct {
	Name   string
	Size   int
	SHA256 string
}

// Media describes one cabinet or media entry. A Cabinet beginning with '#' is embedded in the
// database as a stream of that name; anything else is a file expected beside the package.
type Media struct {
	DiskID       int
	Cabinet      string
	LastSequence int
}

// Embedded reports whether the cabinet is held inside the package.
func (m Media) Embedded() bool { return strings.HasPrefix(m.Cabinet, "#") }

// StreamName is the _Streams name of an embedded cabinet.
func (m Media) StreamName() string { return strings.TrimPrefix(m.Cabinet, "#") }

type Component struct {
	ID        string
	Directory string
	KeyPath   string
}

type Directory struct {
	ID     string
	Parent string
	Name   string // the target name, long form where the package carries both
}

type RegistryEntry struct {
	ID        string
	Root      int
	Key       string
	Name      string
	Value     string
	Component string
}

type Service struct {
	ID          string // the ServiceInstall key: the table's unique key, and the sort key
	Name        string
	DisplayName string
	Component   string
	StartType   int
}

type Shortcut struct {
	ID        string
	Directory string
	Name      string
	Target    string
	Component string
}

// Read opens an installer database and reads everything this package understands.
func Read(path string) (pkg *Package, err error) {
	// Windows Installer handles belong to the thread that created them, and Go may move a
	// goroutine to a different OS thread at almost any call - so the open, every fetch and
	// every close have to happen on one thread. The lock covers the deferred close too.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	db, oerr := openDatabase(path)
	if oerr != nil {
		return nil, oerr
	}
	defer func() {
		if cerr := db.close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing %s: %w", path, cerr)
		}
	}()

	p := &Package{Path: path, Properties: map[string]string{}, Directories: map[string]Directory{}}

	for _, step := range []struct {
		name string
		fn   func(*database, *Package) error
	}{
		{"Property", readProperties},
		{"Directory", readDirectories},
		{"Component", readComponents},
		{"File", readFiles},
		{"Binary", readBinaries},
		{"Media", readMedia},
		{"Registry", readRegistry},
		{"ServiceInstall", readServices},
		{"Shortcut", readShortcuts},
	} {
		if err := step.fn(db, p); err != nil {
			return nil, fmt.Errorf("reading the %s table of %s: %w", step.name, path, err)
		}
	}

	resolveTargets(p)
	sortAll(p)
	return p, nil
}

// optional runs a query over a table that a package need not contain at all - a package with no
// services has no ServiceInstall table.
//
// Absence is established by asking the database which tables it has, not by running the query
// and interpreting the failure: MSI answers 1615 for a missing table AND for a missing column,
// so treating 1615 as absence would silently swallow a query naming a column that is not there
// and report a complete-looking, empty result. "No services" and "could not read services" have
// to stay distinguishable.
func optional(db *database, table, sql string, fn func(*row) error) error {
	if !db.hasTable(table) {
		return nil
	}
	return db.query(sql, fn)
}

func readProperties(db *database, p *Package) error {
	return optional(db, "Property", "SELECT `Property`,`Value` FROM `Property`", func(r *row) error {
		p.Properties[r.text(1)] = r.text(2)
		return nil
	})
}

func readDirectories(db *database, p *Package) error {
	return optional(db, "Directory", "SELECT `Directory`,`Directory_Parent`,`DefaultDir` FROM `Directory`",
		func(r *row) error {
			id := r.text(1)
			p.Directories[id] = Directory{
				ID:     id,
				Parent: r.text(2),
				Name:   targetName(r.text(3)),
			}
			return nil
		})
}

func readComponents(db *database, p *Package) error {
	return optional(db, "Component", "SELECT `Component`,`Directory_`,`KeyPath` FROM `Component`",
		func(r *row) error {
			p.Components = append(p.Components, Component{
				ID:        r.text(1),
				Directory: r.text(2),
				KeyPath:   r.text(3),
			})
			return nil
		})
}

func readFiles(db *database, p *Package) error {
	return optional(db, "File",
		"SELECT `File`,`Component_`,`FileName`,`FileSize`,`Version`,`Language`,`Sequence` FROM `File`",
		func(r *row) error {
			short, long := splitFileName(r.text(3))
			size, _ := r.number(4)
			seq, _ := r.number(7)
			p.Files = append(p.Files, File{
				ID:        r.text(1),
				Component: r.text(2),
				ShortName: short,
				Name:      long,
				Size:      size,
				Version:   r.text(5),
				Language:  r.text(6),
				Sequence:  seq,
			})
			return nil
		})
}

func readBinaries(db *database, p *Package) error {
	return optional(db, "Binary", "SELECT `Name`,`Data` FROM `Binary`", func(r *row) error {
		name := r.text(1)
		data, err := r.stream(2, 0)
		if err != nil {
			// A Binary row whose stream cannot be read is a hole in the inventory, not a
			// detail to skip: these are executable custom actions.
			return fmt.Errorf("reading the stream for Binary %q: %w", name, err)
		}
		sum := sha256.Sum256(data)
		p.Binaries = append(p.Binaries, Binary{
			Name:   name,
			Size:   len(data),
			SHA256: hex.EncodeToString(sum[:]),
		})
		return nil
	})
}

func readMedia(db *database, p *Package) error {
	return optional(db, "Media", "SELECT `DiskId`,`Cabinet`,`LastSequence` FROM `Media`", func(r *row) error {
		disk, _ := r.number(1)
		last, _ := r.number(3)
		p.Media = append(p.Media, Media{DiskID: disk, Cabinet: r.text(2), LastSequence: last})
		return nil
	})
}

func readRegistry(db *database, p *Package) error {
	return optional(db, "Registry", "SELECT `Registry`,`Root`,`Key`,`Name`,`Value`,`Component_` FROM `Registry`",
		func(r *row) error {
			root, _ := r.number(2)
			p.Registry = append(p.Registry, RegistryEntry{
				ID:        r.text(1),
				Root:      root,
				Key:       r.text(3),
				Name:      r.text(4),
				Value:     r.text(5),
				Component: r.text(6),
			})
			return nil
		})
}

func readServices(db *database, p *Package) error {
	return optional(db, "ServiceInstall",
		"SELECT `ServiceInstall`,`Name`,`DisplayName`,`StartType`,`Component_` FROM `ServiceInstall`",
		func(r *row) error {
			start, _ := r.number(4)
			p.Services = append(p.Services, Service{
				ID:          r.text(1),
				Name:        r.text(2),
				DisplayName: r.text(3),
				StartType:   start,
				Component:   r.text(5),
			})
			return nil
		})
}

func readShortcuts(db *database, p *Package) error {
	return optional(db, "Shortcut", "SELECT `Shortcut`,`Directory_`,`Name`,`Component_`,`Target` FROM `Shortcut`",
		func(r *row) error {
			_, name := splitFileName(r.text(3))
			p.Shortcuts = append(p.Shortcuts, Shortcut{
				ID:        r.text(1),
				Directory: r.text(2),
				Name:      name,
				Component: r.text(4),
				Target:    r.text(5),
			})
			return nil
		})
}

// splitFileName splits an MSI filename column, which is "short|long" where the package carries
// both and just the one name where it does not. The long name is what a user recognises.
func splitFileName(v string) (short, long string) {
	if i := strings.IndexByte(v, '|'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return "", v
}

// targetName extracts the install-time name from a DefaultDir column, which is
// "target[:source]" with each side optionally "short|long". Only the target side describes where
// files land; the source side describes the layout the package was built from.
func targetName(defaultDir string) string {
	target := defaultDir
	if i := strings.IndexByte(target, ':'); i >= 0 {
		target = target[:i]
	}
	_, long := splitFileName(target)
	return long
}

// DirectoryPath renders a directory as "[RootProperty]sub\path".
//
// The root is left as the package's own key rather than being mapped to an msis root such as
// INSTALLDIR. A package msis did not build has no such key, and reporting the key the package
// actually uses is the honest answer for an inventory.
func (p *Package) DirectoryPath(id string) string {
	root, segments := p.directoryParts(id)
	if root == "" {
		return ""
	}
	return "[" + root + "]" + strings.Join(segments, `\`)
}

// directoryParts splits a directory into its bracketed root property and the literal path
// segments below it. Callers need the two apart: whether a separator belongs before a filename
// depends on there being segments, and that cannot be recovered from the joined string - a
// directory legitimately named "data]" would otherwise be mistaken for a bare property
// reference and swallow the separator.
func (p *Package) directoryParts(id string) (root string, segments []string) {
	var chain []Directory
	seen := map[string]bool{}
	for cur := id; cur != ""; {
		d, ok := p.Directories[cur]
		if !ok || seen[cur] {
			// An unknown or cyclic parent: stop rather than loop, and let the caller see a
			// partial path instead of hanging.
			break
		}
		seen[cur] = true
		chain = append(chain, d)
		cur = d.Parent
	}
	if len(chain) == 0 {
		return "", nil
	}

	// chain is bottom-up; walk it top-down.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	// Drop the installation root. "SourceDir" is its conventional DefaultDir and TARGETDIR its
	// conventional key; either identifies it. The test is applied ONLY to the top of the chain:
	// "SourceDir" lower down is an ordinary folder name, and skipping it wherever it appeared
	// dropped a real directory out of the path.
	if chain[0].ID == "TARGETDIR" || chain[0].Name == "SourceDir" {
		chain = chain[1:]
	}
	if len(chain) == 0 {
		return "TARGETDIR", nil
	}

	root = chain[0].ID
	for _, d := range chain[1:] {
		// A standard folder carries "." - it names a property, not a folder - and an empty
		// name contributes nothing either.
		if d.Name == "." || d.Name == "" {
			continue
		}
		segments = append(segments, d.Name)
	}
	return root, segments
}

// resolveTargets turns each file's component-directory into a symbolic path.
func resolveTargets(p *Package) {
	dirOf := map[string]string{}
	for _, c := range p.Components {
		dirOf[c.ID] = c.Directory
	}
	type parts struct {
		root     string
		segments []string
	}
	cache := map[string]parts{}
	for i := range p.Files {
		dir := dirOf[p.Files[i].Component]
		got, ok := cache[dir]
		if !ok {
			root, segs := p.directoryParts(dir)
			got = parts{root, segs}
			cache[dir] = got
		}
		p.Files[i].Target = joinTarget(got.root, got.segments, p.Files[i].Name)
	}
}

// joinTarget builds "[Root]a\b\name" from the parts. Working from the parts rather than from a
// rendered path is what keeps a directory called "data]" from losing its separator.
func joinTarget(root string, segments []string, name string) string {
	if root == "" {
		return name
	}
	out := "[" + root + "]"
	if len(segments) > 0 {
		out += strings.Join(segments, `\`) + `\`
	}
	return out + name
}

func sortAll(p *Package) {
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].ID < p.Files[j].ID })
	sort.Slice(p.Binaries, func(i, j int) bool { return p.Binaries[i].Name < p.Binaries[j].Name })
	sort.Slice(p.Media, func(i, j int) bool { return p.Media[i].DiskID < p.Media[j].DiskID })
	sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].ID < p.Components[j].ID })
	sort.Slice(p.Registry, func(i, j int) bool { return p.Registry[i].ID < p.Registry[j].ID })
	// By the table's unique key, not by Name: two ServiceInstall rows may carry the same
	// service name - conditionally selected service components do exactly that - and sorting
	// on a non-unique key leaves their order down to database enumeration.
	sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].ID < p.Services[j].ID })
	sort.Slice(p.Shortcuts, func(i, j int) bool { return p.Shortcuts[i].ID < p.Shortcuts[j].ID })
}
