package main

import (
	"debug/buildinfo"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Licences of what msis links, read from the licence TEXT that ships with each module (#62).
//
// msis's own binaries are the one place where msis knows its components' licences: the module
// cache holds each linked module's source, licence file included, and the standard library's is
// in GOROOT. A hand-kept table would drift silently, which is exactly what an SBOM exists to
// prevent. The NuGet libraries are the exception: their licence is DECLARED by the package, and
// sits next to their pinned version in nativeComponents.
//
// The ids are CONCLUDED, not declared: the module author wrote a licence text, and msis
// identified it. Identification is a comparison of the WHOLE text against a reviewed canonical
// text (licences/*.txt), not a search for phrases: a phrase search accepts BSD-4-Clause as
// BSD-3-Clause, and an MIT text with an added restriction as MIT. Only the parts named in the
// templates may vary - the notice above the terms (title, copyright lines), the organisation in
// BSD clause 3.
// Any other difference, and any text matching no template, is an error, never a guess.
//
// ponytail: three reviewed texts (the licences msis links today). A dependency under any other
// licence, or with a variant text, stops the release until its text has been reviewed and added
// here. If the graph outgrows a handful of licences, a real classifier (google/licensecheck) in
// its own module, as tools/sbom-index is, is the upgrade.

// license is one CycloneDX licence choice: an SPDX id, and who stated it.
type license struct {
	ID              string `json:"id"`
	Acknowledgement string `json:"acknowledgement"` // "declared" or "concluded"
}

type licenseChoice struct {
	License license `json:"license"`
}

func concluded(id string) []licenseChoice {
	return []licenseChoice{{License: license{ID: id, Acknowledgement: "concluded"}}}
}

func declared(id string) []licenseChoice {
	return []licenseChoice{{License: license{ID: id, Acknowledgement: "declared"}}}
}

//go:embed licences/MIT.txt
var mitText string

//go:embed licences/BSD-3-Clause.txt
var bsd3Text string

//go:embed licences/Apache-2.0.txt
var apacheText string

//go:embed licences/Apache-2.0-appendix.txt
var apacheAppendix string

var (
	listMarker = regexp.MustCompile(`^\s*(?:\d+[.)]|[*-])\s+`)
	nonWord    = regexp.MustCompile(`[^a-z0-9]+`)
)

// normalize reduces a licence text to its words: lower case, punctuation and layout gone, list
// markers gone. The one part that legitimately differs between copies - the NOTICE at the top:
// a title, copyright lines, "all rights reserved", blank lines - is dropped, and only there.
// From the first line of the terms onward every line is kept, including ones that begin with
// "(c)" or "copyright": Apache section 4(c) and a wrapped BSD line do, and dropping them would
// let a changed term through (#62's review).
func normalize(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	i := 0
	for ; i < len(lines); i++ {
		if !isNotice(strings.ToLower(strings.TrimSpace(lines[i]))) {
			break
		}
	}
	var kept []string
	for _, line := range lines[i:] {
		kept = append(kept, listMarker.ReplaceAllString(strings.ToLower(strings.TrimSpace(line)), ""))
	}
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.Join(kept, " "), " "))
}

// isNotice reports whether a line, lower-cased and trimmed, belongs to the notice above the
// terms. MIT's title comes in three spellings; the other licences' titles are part of their terms.
func isNotice(l string) bool {
	switch l {
	case "", "all rights reserved", "all rights reserved.",
		"mit license", "the mit license", "the mit license (mit)":
		return true
	}
	return strings.HasPrefix(l, "copyright ") || strings.HasPrefix(l, "(c) ") ||
		strings.HasPrefix(l, "\u00a9")
}

// template turns a canonical text into an anchored pattern over normalized text. VARNAME stands
// for the organisation BSD clause 3 names (one to twelve words), VAROWNER for "owner" or
// "holder", the two wordings of the BSD disclaimer.
func template(canonical, prefix, suffix string) *regexp.Regexp {
	body := regexp.QuoteMeta(normalize(canonical))
	body = strings.ReplaceAll(body, "varname", `(?:[a-z0-9]+ ){0,11}[a-z0-9]+`)
	body = strings.ReplaceAll(body, "varowner", `(?:owner|holder)`)
	return regexp.MustCompile("^" + prefix + body + suffix + "$")
}

var templates = []struct {
	id string
	re *regexp.Regexp
}{
	{"MIT", template(mitText, "", "")},
	{"BSD-3-Clause", template(bsd3Text, "", "")},
	// The terms end at section 9; a copy may add "END OF TERMS AND CONDITIONS", and after it the
	// standard appendix exactly - including its "Copyright [yyyy] [name of copyright owner]"
	// line, which is part of the appendix's text, not a notice. An appendix filled in with a
	// real year and name is therefore refused until reviewed.
	{"Apache-2.0", template(apacheText, "",
		`(?: end of terms and conditions(?: `+regexp.QuoteMeta(normalize(apacheAppendix))+`)?)?`)},
}

// classify names the licence whose reviewed text this is.
func classify(text string) (string, error) {
	norm := normalize(text)
	var matched []string
	for _, t := range templates {
		if t.re.MatchString(norm) {
			matched = append(matched, t.id)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return "", fmt.Errorf("the licence text is not one of the reviewed texts in " +
			"tools/sbom/licences (MIT, BSD-3-Clause, Apache-2.0), or differs from it in its " +
			"terms; review it and add it there rather than let it be guessed")
	default:
		return "", fmt.Errorf("the licence text matches %s; that cannot be resolved by picking one",
			strings.Join(matched, " and "))
	}
}

// licenseIn identifies the licence file in dir. Exactly one LICENSE/LICENCE/COPYING file is
// expected; none or several is an error, since picking one would be a guess.
func licenseIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var files []string
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !e.IsDir() && (strings.HasPrefix(n, "license") || strings.HasPrefix(n, "licence") ||
			strings.HasPrefix(n, "copying")) {
			files = append(files, e.Name())
		}
	}
	if len(files) != 1 {
		return "", fmt.Errorf("%s holds %d licence files (%s); exactly one is needed",
			dir, len(files), strings.Join(files, ", "))
	}
	return classifyFile(filepath.Join(dir, files[0]))
}

func classifyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id, err := classify(string(data))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return id, nil
}

// goEnv is where the toolchain keeps what a binary was built from.
type goEnv struct {
	ModCache  string `json:"GOMODCACHE"`
	GOROOT    string `json:"GOROOT"`
	GoVersion string `json:"GOVERSION"`
	GoMod     string `json:"GOMOD"` // the main module's go.mod: its directory holds msis's LICENSE
}

func readGoEnv() (goEnv, error) {
	out, err := exec.Command("go", "env", "-json", "GOMODCACHE", "GOROOT", "GOVERSION", "GOMOD").Output()
	if err != nil {
		return goEnv{}, fmt.Errorf("go env: %w", err)
	}
	var env goEnv
	if err := json.Unmarshal(out, &env); err != nil {
		return goEnv{}, err
	}
	return env, nil
}

// escapePath is the module cache's case encoding: an upper-case letter becomes '!' and its lower
// case, so that module paths differing only in case do not collide on case-insensitive disks.
func escapePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// moduleLicenses returns the licence of each linked module, keyed by module path, and of the
// standard library, read from the toolchain that built the binary - which must be the one on
// PATH, or the stdlib licence read would be another release's.
func moduleLicenses(info *buildinfo.BuildInfo, env goEnv) (map[string]string, string, error) {
	out := map[string]string{}
	for _, dep := range info.Deps {
		if dep.Replace != nil {
			dep = dep.Replace
		}
		if dep.Version == "" {
			return nil, "", fmt.Errorf("module %s is replaced by a local directory; its licence "+
				"is not in the module cache, and msis does not guess it", dep.Path)
		}
		id, err := licenseIn(filepath.Join(env.ModCache, escapePath(dep.Path)+"@"+escapePath(dep.Version)))
		if err != nil {
			return nil, "", fmt.Errorf("the licence of %s@%s: %w", dep.Path, dep.Version, err)
		}
		out[dep.Path] = id
	}
	if env.GoVersion != info.GoVersion {
		return nil, "", fmt.Errorf("the binary was built by %s but the toolchain on PATH is %s; "+
			"the standard library's licence would be read from the wrong release", info.GoVersion, env.GoVersion)
	}
	std, err := licenseIn(env.GOROOT)
	if err != nil {
		return nil, "", fmt.Errorf("the Go standard library's licence: %w", err)
	}
	return out, std, nil
}

// ownLicense is msis's own licence, read from the repository the release was built from. By
// name: the root also holds LICENSE.rtf, the same text formatted for the installer's dialog.
func ownLicense(env goEnv) (string, error) {
	if env.GoMod == "" || env.GoMod == os.DevNull {
		return "", fmt.Errorf("not inside the msis module, so its LICENSE cannot be found")
	}
	return classifyFile(filepath.Join(filepath.Dir(env.GoMod), "LICENSE"))
}

// licensed attaches the concluded licence to each module component by its name (the module path).
func licensed(comps []component, byPath map[string]string) {
	for i := range comps {
		if id, ok := byPath[comps[i].Name]; ok {
			comps[i].Licenses = concluded(id)
		}
	}
}
