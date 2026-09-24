package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
func matchTarget(targets map[string]installed, want string) ([]string, error) {
	at, ok := targets[canonicalTarget(generator.ParseTarget(want))]
	ids := at.ids
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

// installed is what one install target holds: the WiX File ids at it, and the target as the
// script would write it - canonical keys are folded to lower case for matching, and a file's own
// name keeps its case (#65).
type installed struct {
	ids     []string
	spelled string
}

// installTargets maps every install target this build produces to the WiX File ids at it.
//
// Built from the generator's own tree, in the script's vocabulary, so the comparison is between
// two things written the same way.
func installTargets(ctx *generator.Context) map[string]installed {
	out := map[string]installed{}
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
					spelled := "[" + key + "]" + joinRel(rel, f.Name)
					t := canonicalTarget(key, joinRel(rel, f.Name))
					out[t] = installed{ids: append(out[t].ids, f.ID), spelled: spelled}
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

// resolveDeclaredComponents turns each <component> element into per-file declarations (#64,
// #65): one for a single target, one for every file under a folder target. Each is joined to
// exactly one file by its target; its facts go onto that file's own component (D16). Like
// <sbom>, it runs on every build, so a `for` that names nothing is reported now rather than at
// the next release.
func resolveDeclaredComponents(decls []ir.DeclaredComponent, ctx *generator.Context, script string) ([]sbom.Declaration, error) {
	if len(decls) == 0 {
		return nil, nil
	}
	targets := installTargets(ctx)
	var out []sbom.Declaration
	for _, d := range decls {
		element := fmt.Sprintf("%s <component for=%q>", filepath.Base(script), d.For)
		if d.IsFolder() {
			files, err := expandFolder(targets, d)
			if err != nil {
				return nil, fmt.Errorf("<component for=%q>: %w", d.For, err)
			}
			for _, f := range files {
				out = append(out, declarationFor(d, element, f.spelled, f.ids[0]))
			}
			continue
		}
		ids, err := matchTarget(targets, d.For)
		if err != nil {
			return nil, fmt.Errorf("<component for=%q>: %w", d.For, err)
		}
		out = append(out, declarationFor(d, element, d.For, ids[0]))
	}
	return out, nil
}

func declarationFor(d ir.DeclaredComponent, element, target, fileID string) sbom.Declaration {
	return sbom.Declaration{
		Source: element, Target: target, FileID: fileID,
		Name: d.Name, Version: d.Version, Creator: d.Creator, License: d.License, PURL: d.PURL, CPE: d.CPE,
		SourceCode: d.SourceCode,
	}
}

// expandFolder is every file a folder declaration covers (#65): installed under the folder, and
// in subfolders unless the declaration says recursive="no". Each must be exactly one file, as
// for a single target, and a folder that covers nothing is an error, as a target that names
// nothing is.
func expandFolder(targets map[string]installed, d ir.DeclaredComponent) ([]installed, error) {
	root, sub := generator.ParseTarget(d.For)
	prefix := canonicalTarget(root, sub)
	keys := make([]string, 0, len(targets))
	for t := range targets {
		keys = append(keys, t)
	}
	sort.Strings(keys)
	var out []installed
	for _, t := range keys {
		rest, ok := strings.CutPrefix(t, prefix)
		if !ok || rest == "" {
			continue
		}
		if sub != "" {
			// "[root]templates" must not match "[root]templates-old\x".
			if rest, ok = strings.CutPrefix(rest, `\`); !ok {
				continue
			}
		}
		if !d.Recursive && strings.Contains(rest, `\`) {
			continue
		}
		at := targets[t]
		if len(at.ids) > 1 {
			return nil, fmt.Errorf("%d files are installed at %s, so which one a declaration "+
				"describes cannot be decided; give them distinct targets", len(at.ids), at.spelled)
		}
		out = append(out, at)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no file is installed under that folder")
	}
	return out, nil
}
