package main

import (
	"fmt"
	"strings"

	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/msiread"
)

// runInspect prints what is actually inside a built installer.
//
// This reads the artifact, not the .msis that produced it, which is the point: the script
// describes what was asked for, the package describes what shipped. They differ — the WiX
// template and toolchain contribute payload the generator never saw — and for an old release the
// script may not even reproduce.
//
// Inspection is passive. It opens the database read-only and never runs the package.
func runInspect(path string) error {
	pkg, err := msiread.Read(path)
	if err != nil {
		return err
	}

	fmt.Printf("%s %s\n", cli.Bold("Package:"), cli.Filename(path))
	for _, key := range []string{"ProductName", "ProductVersion", "Manufacturer", "ProductCode", "UpgradeCode", "ProductLanguage"} {
		if v := pkg.Properties[key]; v != "" {
			fmt.Printf("  %-16s %s\n", key, v)
		}
	}

	fmt.Printf("\n%s (%s)\n", cli.Bold("Payload files"), cli.Number(fmt.Sprintf("%d", len(pkg.Files))))
	for _, f := range pkg.Files {
		version := ""
		if f.Version != "" {
			version = "  v" + f.Version
		}
		fmt.Printf("  %9s  %s  %s%s\n", humanSize(f.Size), digest(f.SHA256), cli.Filename(f.Target), version)
	}

	// Binary-table streams are custom actions and UI resources held in the database rather than
	// in the cabinet. They are named separately because they are not installed anywhere: they
	// run during installation, which makes them worth an inventory line of their own.
	if len(pkg.Binaries) > 0 {
		fmt.Printf("\n%s (%s) %s\n", cli.Bold("Binary streams"),
			cli.Number(fmt.Sprintf("%d", len(pkg.Binaries))),
			cli.Info("- custom actions and UI resources; executed, not installed"))
		for _, b := range pkg.Binaries {
			fmt.Printf("  %9s  %s  %s\n", humanSize(b.Size), digest(b.SHA256), b.Name)
		}
	}

	if len(pkg.Media) > 0 {
		fmt.Printf("\n%s\n", cli.Bold("Media"))
		for _, m := range pkg.Media {
			where := "beside the package"
			if m.Embedded() {
				where = "embedded stream"
			}
			fmt.Printf("  disk %d  %-20s %s, through sequence %d\n",
				m.DiskID, m.StreamName(), where, m.LastSequence)
			if m.Unavailable != "" {
				fmt.Printf("    %s\n", cli.Warning("Warning: "+m.Unavailable+
					"; the files it carries have no digest below"))
			}
		}
	}

	if len(pkg.Registry) > 0 {
		fmt.Printf("\n%s (%s)\n", cli.Bold("Registry"), cli.Number(fmt.Sprintf("%d", len(pkg.Registry))))
		for _, r := range pkg.Registry {
			name := r.Name
			if name == "" {
				name = "(default)"
			}
			fmt.Printf("  %s\\%s  %s = %s\n", registryRootName(r.Root), r.Key, name, r.Value)
		}
	}

	if len(pkg.Services) > 0 {
		fmt.Printf("\n%s (%s)\n", cli.Bold("Services"), cli.Number(fmt.Sprintf("%d", len(pkg.Services))))
		for _, s := range pkg.Services {
			fmt.Printf("  %s (%s)\n", s.Name, s.DisplayName)
		}
	}

	if len(pkg.Shortcuts) > 0 {
		fmt.Printf("\n%s (%s)\n", cli.Bold("Shortcuts"), cli.Number(fmt.Sprintf("%d", len(pkg.Shortcuts))))
		for _, s := range pkg.Shortcuts {
			fmt.Printf("  %s -> %s\n", s.Name, s.Target)
		}
	}

	// What this inventory does not tell you. Stated in the output rather than only in the docs,
	// because an inventory that looks complete is worse than one that says where it stops.
	fmt.Printf("\n%s\n", cli.Bold("Not covered"))
	fmt.Println("  - what a payload binary was itself built from is opaque; the digest identifies")
	fmt.Println("    the bytes, it says nothing about their provenance")
	fmt.Println("  - install-time conditions decide what actually lands on a machine")
	fmt.Println("  - a cabinet that did not travel with the package cannot be read; any such is")
	fmt.Println("    named under Media above")

	return nil
}

// registryRootName renders the MSI Registry.Root column. -1 means "HKCU or HKLM depending on
// ALLUSERS", which is a real answer rather than an unknown one.
func registryRootName(root int) string {
	switch root {
	case -1:
		return "HKCU-or-HKLM"
	case 0:
		return "HKCR"
	case 1:
		return "HKCU"
	case 2:
		return "HKLM"
	case 3:
		return "HKU"
	default:
		return fmt.Sprintf("root%d", root)
	}
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// inspectablePath reports whether a file looks like something /INSPECT can read, so the error
// for `msis /INSPECT setup.msis` names the mistake instead of MSI's return code.
func inspectablePath(path string) error {
	if strings.EqualFold(pathExt(path), ".msi") {
		return nil
	}
	return fmt.Errorf("/INSPECT reads a built .msi; %s is not one"+
		"\n  hint: build it first (msis /BUILD ...), then inspect the .msi it produced", path)
}

func pathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

// digest renders a SHA-256 short enough to scan down a column, or says plainly that there is
// none. "-" would read as a dash in a table; the absence of a hash is worth a word.
func digest(sum string) string {
	if sum == "" {
		return "  (no digest)   "
	}
	return sum[:16]
}
