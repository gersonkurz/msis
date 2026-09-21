package main

import (
	"fmt"
	"strings"

	"github.com/gersonkurz/msis/internal/burnread"
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
	if strings.EqualFold(pathExt(path), ".exe") {
		return inspectBundle(path)
	}
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
	ext := pathExt(path)
	if strings.EqualFold(ext, ".msi") || strings.EqualFold(ext, ".exe") {
		return nil
	}
	return fmt.Errorf("/INSPECT reads a built .msi or a Burn bundle .exe; %s is neither"+
		"\n  hint: build it first (msis /BUILD ...), then inspect what it produced", path)
}

// inspectBundle prints what is inside a Burn bundle: the bootstrapper application it runs and
// the installers it chains. A chained installer's own contents are not expanded here - point
// /INSPECT at the .msi for that - because the bundle's inventory is the chain, not the payload
// of every package in it.
func inspectBundle(path string) error {
	b, err := burnread.Read(path)
	if err != nil {
		return err
	}

	fmt.Printf("%s %s\n", cli.Bold("Bundle:"), cli.Filename(path))
	for _, row := range [][2]string{
		{"Name", b.Name}, {"Version", b.Version}, {"Publisher", b.Publisher},
		{"BundleCode", b.Code}, {"UpgradeCode", b.UpgradeCode},
		{"Scope", b.Scope}, {"Engine", b.EngineVersion},
	} {
		if row[1] != "" {
			fmt.Printf("  %-16s %s\n", row[0], row[1])
		}
	}

	fmt.Printf("\n%s (%s) %s\n", cli.Bold("Bootstrapper payloads"),
		cli.Number(fmt.Sprintf("%d", len(b.UX))),
		cli.Info("- run the install, never installed by it"))
	for _, p := range b.UX {
		fmt.Printf("  %9s  %s  %s\n", humanSize(p.Size), digest(p.SHA256), p.Name)
	}

	// Payloads that belong to no chain package. They ship inside the bundle (or beside it)
	// all the same, so leaving them out of the listing would be the same hole the reader was
	// fixed for - just one layer further out.
	if len(b.Loose) > 0 {
		fmt.Printf("\n%s (%s) %s\n", cli.Bold("Bundle payloads"),
			cli.Number(fmt.Sprintf("%d", len(b.Loose))),
			cli.Info("- carried by the bundle, referenced by no chain package"))
		for _, p := range b.Loose {
			note := ""
			if p.LayoutOnly {
				note = "  " + cli.Info("layout only")
			}
			fmt.Printf("  %9s  %s  %s%s\n", humanSize(p.Size), digest(p.SHA256), p.Name, note)
			if p.Unavailable != "" {
				fmt.Printf("    %s\n", cli.Warning("Warning: "+p.Unavailable))
			}
		}
	}

	fmt.Printf("\n%s (%s)\n", cli.Bold("Chain"), cli.Number(fmt.Sprintf("%d", len(b.Packages))))
	for _, pkg := range b.Packages {
		name := pkg.DisplayName
		if name == "" {
			name = pkg.ID
		}
		version := ""
		if pkg.Version != "" {
			version = "  v" + pkg.Version
		}
		fmt.Printf("  %s  %s%s\n", cli.Bold(name), cli.Info(pkg.Kind), version)
		if pkg.InstallCondition != "" {
			// What is in the bundle and what a given machine installs are different things.
			fmt.Printf("    %s %s\n", cli.Info("installs when"), pkg.InstallCondition)
		}
		for _, pay := range pkg.Payloads {
			fmt.Printf("    %9s  %s  %-28s %s\n",
				humanSize(pay.Size), digest(pay.SHA256), pay.Name, cli.Info(string(pay.Role)))
			if pay.Unavailable != "" {
				fmt.Printf("      %s\n", cli.Warning("Warning: "+pay.Unavailable))
			}
		}
	}

	fmt.Printf("\n%s\n", cli.Bold("Not covered"))
	fmt.Println("  - a chained installer's own payload; run /INSPECT against that .msi")
	fmt.Println("  - what a payload binary was itself built from is opaque; the digest identifies")
	fmt.Println("    the bytes, it says nothing about their provenance")
	fmt.Println("  - install conditions decide which chained packages a machine actually installs")
	fmt.Println("  - a payload the engine downloads at install time is not in this file; any such")
	fmt.Println("    is named above")

	return nil
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
