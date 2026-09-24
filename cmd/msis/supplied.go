package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/contact"
	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/sbom"
)

// Supplied component SBOMs (#36): `<sbom source="app.cdx.json" for="[INSTALLDIR]app.exe"/>`.
//
// The two halves are resolved here, at build time, because this is the only place both are
// known: the file lives beside the .msis, and which payload `for` names is a question about the
// tree the generator just built.
//
// The join handed to the emitter is the WiX File id, not the target string. The artifact's
// install targets are in Windows Installer's own vocabulary - [ProgramFiles64Folder]Company\App
// - while the script writes msis's ([INSTALLDIR]App), and the mapping between them lives in the
// .wxs TEMPLATES rather than in Go. Re-deriving it here would mean reproducing the templates'
// directory layout and the INSTALLDIR variable's value, and a re-derivation that drifts attaches
// a dependency graph to the wrong file. The File id is already the exact join #34 uses.

// resolveSuppliedSBOMs turns each <sbom> element into what the emitter needs, or explains why it
// cannot. It runs on every build, not only when /SBOM was asked for: a `for` that names nothing
// is a mistake in the script, and finding it at the next release rather than now is no help.
func resolveSuppliedSBOMs(decls []ir.SuppliedSBOM, ctx *generator.Context, script string) ([]sbom.Supplied, error) {
	if len(decls) == 0 {
		return nil, nil
	}
	targets := installTargets(ctx)

	out := make([]sbom.Supplied, 0, len(decls))
	for _, d := range decls {
		ids, err := matchTarget(targets, d.For)
		if err != nil {
			return nil, fmt.Errorf("<sbom source=%q for=%q>: %w", d.Source, d.For, err)
		}

		// Relative to the .msis, not through WiX's bind paths: this file is read by msis and
		// is never packaged, so WiX never resolves it and its search order does not apply.
		path := d.Source
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(script), d.Source)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("<sbom source=%q for=%q>: %w", d.Source, d.For, err)
		}

		out = append(out, sbom.Supplied{
			Source: filepath.ToSlash(d.Source),
			Target: d.For,
			FileID: ids[0],
			Data:   data,
		})
	}
	return out, nil
}

// matchTarget requires exactly one file. A target matching several is refused rather than
// resolved: msis already packages two different files to one destination under short names
// (that is what TestDuplicateTargetFilesGetShortName covers), and attaching someone's dependency
// graph to whichever of them came first would be a coin toss presented as a fact.
func matchTarget(targets map[string][]string, want string) ([]string, error) {
	ids, ok := targets[canonicalTarget(generator.ParseTarget(want))]
	switch {
	case !ok:
		known := make([]string, 0, len(targets))
		for t := range targets {
			known = append(known, t)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("no file is installed there; this build installs to %s",
			strings.Join(known, ", "))
	case len(ids) > 1:
		sort.Strings(ids)
		return nil, fmt.Errorf("%d files are installed there (%s), so which one the document "+
			"describes cannot be decided; give them distinct targets",
			len(ids), strings.Join(ids, ", "))
	}
	return ids, nil
}

// installTargets maps every install target this build produces to the WiX File ids at it.
//
// Built from the generator's own tree, in the script's vocabulary, so the comparison is between
// two things written the same way.
func installTargets(ctx *generator.Context) map[string][]string {
	out := map[string][]string{}
	for _, key := range sortedTreeKeys(ctx.DirectoryTrees) {
		root := ctx.DirectoryTrees[key]
		// A root key with a nested value - INSTALLDIR="Company\App" - becomes a chain of
		// directories with the key on the LAST one, and that is what [INSTALLDIR] addresses.
		anchor := anchorOf(root, key)
		if anchor == nil {
			anchor = root
		}
		var walk func(d *generator.Directory, rel string)
		walk = func(d *generator.Directory, rel string) {
			for _, c := range d.Components {
				for _, f := range c.Files {
					t := canonicalTarget(key, joinRel(rel, f.Name))
					out[t] = append(out[t], f.ID)
				}
			}
			for _, name := range sortedChildNames(d) {
				child := d.Children[name]
				walk(child, joinRel(rel, child.Name))
			}
		}
		walk(anchor, "")
	}
	return out
}

// anchorOf finds the directory the root key addresses.
func anchorOf(d *generator.Directory, key string) *generator.Directory {
	if d == nil {
		return nil
	}
	if d.CustomID == key {
		return d
	}
	for _, name := range sortedChildNames(d) {
		if got := anchorOf(d.Children[name], key); got != nil {
			return got
		}
	}
	return nil
}

func joinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + `\` + name
}

// canonicalTarget gives one spelling for one destination: Windows paths are case-insensitive and
// the script may write either separator, so both sides are folded before they are compared.
func canonicalTarget(rootKey, sub string) string {
	sub = strings.ReplaceAll(sub, "/", `\`)
	sub = strings.Trim(sub, `\`)
	return strings.ToLower("[" + rootKey + "]" + sub)
}

// resolveDeclaredComponents turns each <component> element into a supplied document (#64), so
// a declaration is merged by exactly the rules a supplied SBOM is: joined to one file by its
// target, namespaced, marked as supplied - here, by the script - and never taken for something
// msis observed. Like <sbom>, it runs on every build, so a `for` that names nothing is reported
// now rather than at the next release.
func resolveDeclaredComponents(decls []ir.DeclaredComponent, ctx *generator.Context, script string) ([]sbom.Supplied, error) {
	if len(decls) == 0 {
		return nil, nil
	}
	targets := installTargets(ctx)
	out := make([]sbom.Supplied, 0, len(decls))
	for _, d := range decls {
		ids, err := matchTarget(targets, d.For)
		if err != nil {
			return nil, fmt.Errorf("<component for=%q>: %w", d.For, err)
		}
		data, err := declaredDocument(d)
		if err != nil {
			return nil, fmt.Errorf("<component for=%q>: %w", d.For, err)
		}
		out = append(out, sbom.Supplied{
			// The source names the element, so the document's provenance says where the facts
			// came from: this script, this declaration.
			Source:   fmt.Sprintf("%s <component for=%q>", filepath.Base(script), d.For),
			Target:   d.For,
			FileID:   ids[0],
			Data:     data,
			Declared: true,
		})
	}
	return out, nil
}

// declaredDocument is the CycloneDX document a declaration stands for: one subject, carrying
// exactly the fields the author declared and nothing else. It says nothing about what the file
// contains or depends on, so both stay unknown.
func declaredDocument(d ir.DeclaredComponent) ([]byte, error) {
	name := d.Name
	if name == "" {
		// The file's name: the last segment of its install target.
		name = d.For[strings.LastIndexAny(d.For, `]/\`)+1:]
	}
	subject := map[string]any{"type": "library", "bom-ref": "declared", "name": name}
	if d.Version != "" {
		subject["version"] = d.Version
	}
	if d.PURL != "" {
		subject["purl"] = d.PURL
	}
	if d.CPE != "" {
		subject["cpe"] = d.CPE
	}
	switch {
	case contact.IsEmail(d.Creator):
		subject["manufacturer"] = map[string]any{"contact": []any{map[string]any{"email": d.Creator}}}
	case d.Creator != "":
		subject["manufacturer"] = map[string]any{"url": []any{d.Creator}}
	}
	if d.License != "" {
		// The author declares the licence the component's creator assigned: its original licence.
		subject["licenses"] = []any{map[string]any{"expression": d.License, "acknowledgement": "declared"}}
	}
	return json.Marshal(map[string]any{
		"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
		"metadata": map[string]any{"component": subject},
	})
}
