package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/bundle"
	"github.com/gersonkurz/msis/internal/burnread"
	"github.com/gersonkurz/msis/internal/cli"
	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/prereqcache"
	"github.com/gersonkurz/msis/internal/requirements"
	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/variables"
	"github.com/gersonkurz/msis/internal/vex"
	"github.com/gersonkurz/msis/internal/wix"
)

// Build-time enrichment (#34): what each of the four build paths knows, collected as it is
// resolved and handed to the SBOM emitter at the end of the run.
//
// There is no single place to read this from. The four paths resolve different things at
// different times - an MSI build resolves payload while generating, an auto-bundle resolves
// prerequisites only after the MSI is already built, an explicit <bundle> resolves chained
// packages instead of payload, and /STANDALONE resolves no chain at all - so each contributes
// what it learned rather than one traversal pretending to cover them all.

// buildBindPaths mirrors wix.bindPathArgs: the ordered -b directories WiX resolves source files
// against, with a symbolic name for each so the record can say WHICH one matched without
// publishing a machine path.
//
// The order is load-bearing, not decorative. On a machine carrying a /CUSTOMTEMPLATES overlay
// there can be several copies of the installer-hook DLL, and WiX takes the first match; naming
// the obvious one instead records a digest for bytes that never ran.
func buildBindPaths(wxsDir, workDir, customTemplates, templateFolder string) []buildrecord.BindPath {
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return p
	}
	return []buildrecord.BindPath{
		{Name: "wxs", Dir: abs(wxsDir)},
		{Name: "script", Dir: abs(workDir)},
		{Name: "custom-templates", Dir: abs(customTemplates)},
		{Name: "templates", Dir: abs(templateFolder)},
	}
}

// newBuildRecord starts a record and fills in what is known before any path-specific work.
func newBuildRecord(p buildrecord.Path, script string, bind []buildrecord.BindPath) *buildrecord.Record {
	rec := buildrecord.New(p, script, bind)
	rec.AddTool("msis", Version)
	if v := wix.GetWixVersion(); v != "" {
		rec.AddTool("wix", v)
	}
	return rec
}

// recordGeneratedFiles is the MSI path's contribution: every payload file the generator
// resolved, keyed by the WiX File id so the artifact's own components can be joined to it
// exactly rather than by name.
func recordGeneratedFiles(rec *buildrecord.Record, ctx *generator.Context) {
	var walk func(*generator.Directory)
	walk = func(d *generator.Directory) {
		for _, c := range d.Components {
			for _, f := range c.Files {
				rec.AddFile(f.ID, f.SourcePath)
			}
		}
		// Children are a map, so a defined order is imposed here rather than inherited from
		// Go's map iteration - two builds of one script must produce one record.
		for _, name := range sortedChildNames(d) {
			walk(d.Children[name])
		}
	}
	for _, key := range sortedTreeKeys(ctx.DirectoryTrees) {
		walk(ctx.DirectoryTrees[key])
	}
}

// recordTemplateBinaries is the other half of the MSI path: files the TEMPLATE names rather
// than the script. msis supplies exactly one, the installer-hook DLL, and it is the one that
// executes during installation - so it is resolved through the real bind paths.
//
// HookDllDir names a directory, not a file, which is why the two are joined here.
func recordTemplateBinaries(rec *buildrecord.Record, vars variables.Dictionary) {
	// The same two conditions validateInstallerHooks gates on, so the record describes the
	// build that actually happened rather than one it might have.
	if !vars.GetBool("USE_INSTALLER_HOOKS") {
		return
	}
	entry := vars["DLL_ENTRY"]
	if entry == "" {
		return
	}
	rec.AddTemplateBinary(vars.HookDllDir() + "/" + entry)
}

// recordExtensionFiles is the MSI path's third contribution (#67): the files the WiX extensions
// the build LOADED carry, so a Binary-table stream WiX embedded can be attributed to the package
// that shipped it. It reads the extensions the builder handed to wix, the resolved DLLs, so the
// record describes those very files (decisions D18). Only a package whose .nuspec facts msis
// pins is recorded.
func recordExtensionFiles(rec *buildrecord.Record, loaded []wix.ResolvedExtension) {
	for _, e := range loaded {
		if e.Path == "" {
			continue // wix found it by id; msis does not know which file, and claims nothing
		}
		facts, ok := wix.ExtensionPackageFacts(e.ID, e.Version)
		if !ok {
			continue
		}
		payloads, err := wix.ExtensionPayloads(e.Path)
		if err != nil {
			rec.Unresolved = append(rec.Unresolved, fmt.Sprintf("%s %s: reading its files: %v", e.ID, e.Version, err))
			continue
		}
		for sum, entry := range payloads {
			rec.Extensions = append(rec.Extensions, buildrecord.ExtensionFile{
				Package: e.ID, Version: e.Version, Entry: entry, SHA256: sum,
				Authors: facts.Authors, Repository: facts.Repository, License: facts.License,
			})
		}
	}
}

// recordStandaloneRuntimes is the /STANDALONE contribution: no chain at all. The prerequisites
// become launch conditions, so what the build arranged is DETECTION, not distribution, and the
// record says so - a document that listed them like bundled payload would claim the installer
// ships something it does not.
func recordStandaloneRuntimes(rec *buildrecord.Record, reqs []ir.Requirement, arch string) {
	for _, req := range reqs {
		// One at a time: GenerateLaunchConditions drops a requirement it has no rule for,
		// so a positional join against the whole slice would attribute one requirement's
		// condition to another. Asking per requirement cannot mismatch.
		condition := ""
		if got := requirements.GenerateLaunchConditions([]ir.Requirement{req}, arch); len(got) == 1 {
			condition = got[0].Condition
		}
		rec.AddRuntime(req.Type, req.Version, condition)
	}
}

// recordPrerequisites is the auto-bundle and <bundle> contribution: the runtimes the build
// arranged to SHIP, with where each came from. The artifact records that a prerequisite is
// chained and nothing else - not the URL it was fetched from, nor which entry of the cache it
// landed in - and that is the provenance #34 moved here from B.
//
// cached is the generator's OWN map of what it resolved, keyed "type/version/arch". Reading it
// rather than recomputing one matters twice: a bundle caches every architecture it can carry,
// so guessing one would miss the others and mislabel the one it found; and a prerequisite the
// script supplied itself has no cache entry at all, which is exactly the signal that it was
// not downloaded.
//
// verified is the generator's account of which supplied sources it actually verified against
// their sha256= (#50). The record is told THAT, not whether the attribute exists: a digest the
// script carries but msis never checked - because the build was interrupted, or a future
// caller recorded without ensuring - must not be published as "script-digest". Round-1 review
// caught the attribute-presence shortcut.
func recordPrerequisites(rec *buildrecord.Record, prereqs []ir.Prerequisite,
	cached map[string]string, verified map[string]bool) {

	for _, p := range prereqs {
		// A source the script named: no download happened, so no download is claimed.
		if p.Source != "" {
			digest := ""
			if verified[p.Source] {
				digest = p.SHA256
			}
			rec.AddPrerequisiteFromSource(p.Type, p.Version, p.Source, digest)
			continue
		}

		archs := cachedArchs(cached, p.Type, p.Version)
		if len(archs) == 0 {
			rec.AddPrerequisiteUnresolved(p.Type, p.Version,
				"the build arranged it but resolved no file for it, so msis cannot say where "+
					"its bytes came from")
			continue
		}
		for _, arch := range archs {
			rec.AddPrerequisiteFromCache(p.Type, p.Version, arch,
				cached[cacheKey(p.Type, p.Version, arch)],
				prereqcacheURL(p.Type, p.Version, arch))
		}
	}
}

func cacheKey(typ, version, arch string) string { return typ + "/" + version + "/" + arch }

// cachedArchs lists, in a defined order, every architecture the generator resolved for one
// prerequisite. A bundle carries x64, x86 and arm64 of the VC++ runtime, and each is its own
// file with its own digest.
func cachedArchs(cached map[string]string, typ, version string) []string {
	prefix := typ + "/" + version + "/"
	var out []string
	for k := range cached {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	sort.Strings(out)
	return out
}

// emitBuildSBOM writes the document(s) for what a build just produced.
//
// An auto-bundle produces two artifacts, and the ORDER matters: the MSI's document is written
// first so that the bundle's can link to it. #33 verifies a BOM-Link against the child's
// subject digest before making it, so a bundle written first would simply record that no
// document was there - correct, but less useful than doing it the other way round.
func emitBuildSBOM(rec *buildrecord.Record, artifacts []string, supplied []sbom.Supplied, declared []sbom.Declaration,
	statements *vex.Source) error {

	// The VEX annotates ONE document: the inventory of what is installed. Where a build
	// produces both an MSI and the bundle wrapping it, that is the MSI - its components are
	// the files a statement is about, and the wrapper links to it rather than repeating it.
	inventory, inventoryDoc := "", (*sbom.Document)(nil)

	for _, artifact := range artifacts {
		var doc *sbom.Document
		var err error
		switch {
		case isBundleArtifact(artifact):
			// The bundle carries the prerequisites and the chained installers; the MSI it
			// wraps carries neither. Handing the whole record to both made the MSI claim
			// the wrapper's payload, and made it depend on itself.
			opts := sbom.Options{MsisVersion: Version,
				Build: rec.For(buildrecord.ScopeArtifactBundle)}
			var b *burnread.Bundle
			if b, err = burnread.Read(artifact); err == nil {
				doc, err = sbom.FromBundle(b, opts)
			}
		default:
			// Supplied documents describe installed FILES, which only the MSI has. The
			// wrapper carries the MSI itself and links to its document (#33); repeating the
			// contents of a file inside that MSI would say the bundle contains them directly.
			opts := sbom.Options{MsisVersion: Version,
				Build: rec.For(buildrecord.ScopeArtifactMSI), Supplied: supplied, Declared: declared}
			var pkg *msiread.Package
			if pkg, err = msiread.Read(artifact); err == nil {
				doc, err = sbom.FromPackage(pkg, opts)
			}
		}
		if err != nil {
			return fmt.Errorf("writing the SBOM for %s: %w", filepath.Base(artifact), err)
		}

		out, preserved, err := sbom.Write(artifact, doc)
		if err != nil {
			return err
		}
		fmt.Printf("  %s %s (%s components, enriched from the %s build)\n",
			cli.Success("SBOM:"), cli.Filename(out),
			cli.Number(fmt.Sprintf("%d", len(doc.Components))), rec.Path)
		if preserved != "" {
			fmt.Printf("  %s\n", cli.Info("Kept the previous document as "+preserved))
		}
		warnNTIAUnknown(doc)
		printBOMLinks(doc)

		if inventoryDoc == nil || !isBundleArtifact(artifact) {
			inventory, inventoryDoc = artifact, doc
		}
	}

	if inventoryDoc != nil {
		return emitVEX(inventory, inventoryDoc, statements)
	}
	return nil
}

func isBundleArtifact(path string) bool {
	return len(path) > 4 && filepath.Ext(path) == ".exe"
}

// prereqcacheURL reports the URL a well-known prerequisite is fetched from, or "" when msis has
// no record of one - a custom <prerequisite source=> has no download.
func prereqcacheURL(typ, version, arch string) string {
	if u := prereqcache.LookupDownloadURL(typ, version, arch); u != nil {
		return u.URL
	}
	return ""
}

func sortedChildNames(d *generator.Directory) []string {
	names := make([]string, 0, len(d.Children))
	for n := range d.Children {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedTreeKeys(m map[string]*generator.Directory) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// autoBundlePrereqs converts <requires> the way processAutoBundle does, so the record and the
// chain cannot disagree about what was arranged.
func autoBundlePrereqs(reqs []ir.Requirement) ([]ir.Prerequisite, error) {
	return bundle.RequirementsToPrerequisites(reqs)
}

// recordBundleSources is the explicit <bundle> contribution: the installers it chains, by the
// source each was built from. A bundle artifact holds the packaged bytes and nothing about
// where they came from, so this is the only place the association exists.
func recordBundleSources(rec *buildrecord.Record, b *ir.Bundle, cached map[string]string, verified map[string]bool) {
	if b == nil {
		return
	}
	if b.MSI != nil {
		// A bundle may name one MSI or one per architecture. Every source it names is
		// recorded, not just the one matching this build's platform: the script asked for
		// all of them, and which the engine installs is an install-time decision.
		for _, src := range []string{b.MSI.Source, b.MSI.Source64bit, b.MSI.Source32bit, b.MSI.SourceArm64} {
			if src != "" {
				rec.AddChained("MsiPackage", src)
			}
		}
	}
	for _, exe := range b.ExePackages {
		rec.AddChained("ExePackage", exe.Source)
	}
	// Its prerequisites ship inside the bundle, so they are carried - the same category as
	// an auto-bundle's, and a different one from a /STANDALONE launch condition.
	recordPrerequisites(rec, b.Prerequisites, cached, verified)
}
