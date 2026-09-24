package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// realistic copies of the reviewed texts, as they appear in the wild: with titles, copyright
// lines, and the organisation BSD clause 3 names.
var (
	mitCopy  = "The MIT License (MIT)\n\nCopyright (c) 2015 Aymerick JEHANNE\n\n" + mitText
	bsd3Copy = "Copyright 2009 The Go Authors.\n\n" +
		strings.NewReplacer("VARNAME", "Google LLC", "VAROWNER", "OWNER").Replace(bsd3Text)
)

// BSD-4-Clause is BSD-3-Clause plus an advertising clause: a phrase search accepts it as
// BSD-3-Clause, which is exactly the misattribution #62's review found.
const bsd4Copy = `Copyright (c) 1998 The Regents.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:
1. Redistributions of source code must retain the above copyright
   notice, this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright
   notice, this list of conditions and the following disclaimer in the
   documentation and/or other materials provided with the distribution.
3. All advertising materials mentioning features or use of this software
   must display the following acknowledgement:
   This product includes software developed by the Regents.
4. Neither the name of the Regents nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
`

// #62: a licence is concluded only when the WHOLE text is a reviewed licence text, varying only
// where the templates allow. An added, removed or changed term is refused, never rounded off.
func TestClassifyConcludesOnlyAWholeReviewedText(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"MIT with title and copyright":          {mitCopy, "MIT"},
		"MIT, bare":                             {mitText, "MIT"},
		"MIT, CRLF":                             {strings.ReplaceAll(mitCopy, "\n", "\r\n"), "MIT"},
		"BSD-3-Clause, the Go text":             {bsd3Copy, "BSD-3-Clause"},
		"BSD-3-Clause, numbered, HOLDER":        {strings.NewReplacer("   * ", "1. ", "VARNAME", "the Regents", "VAROWNER", "HOLDER").Replace(bsd3Text), "BSD-3-Clause"},
		"Apache-2.0, terms only":                {apacheText, "Apache-2.0"},
		"Apache-2.0, with END and the appendix": {apacheText + "\n   END OF TERMS AND CONDITIONS\n\n   " + apacheAppendix, "Apache-2.0"},
	} {
		if got, err := classify(tc.text); err != nil || got != tc.want {
			t.Errorf("%s: classify = %q, %v; want %q", name, got, err, tc.want)
		}
	}

	for name, text := range map[string]string{
		"BSD-4-Clause (BSD-3 plus an advertising clause)": bsd4Copy,
		"BSD-2-Clause (BSD-3 without its third clause)": changed(t, bsd3Copy,
			regexp.MustCompile(`(?s)   \* Neither.*?permission\.\n`).ReplaceAllString(bsd3Copy, "")),
		"MIT with an added restriction": changed(t, mitCopy, strings.Replace(mitCopy, "portions of the Software.",
			"portions of the Software.\n\nThe Software shall be used for Good, not Evil.", 1)),
		"MIT with a term removed": changed(t, mitCopy,
			strings.Replace(mitCopy, "sublicense, and/or sell", "and/or", 1)),
		"Apache-2.0 with a word changed": changed(t, apacheText,
			strings.Replace(apacheText, "royalty-free, irrevocable", "royalty-free, revocable", 1)),
		"Apache-2.0 with a clause appended": apacheText + "\n10. You shall not use the Work for evil.\n",
		// Lines of the TERMS that begin like a notice: only the notice at the top may vary.
		"Apache-2.0 with section 4(c) weakened": changed(t, apacheText,
			strings.Replace(apacheText, "(c) You must retain", "(c) You need not retain", 1)),
		"BSD-3-Clause with a wrapped 'copyright notice' line changed": changed(t, bsd3Copy,
			strings.Replace(bsd3Copy, "copyright notice, this list of conditions and the following disclaimer\n",
				"copyright notice alone\n", 1)),
		"a dual MIT and Apache text": mitCopy + "\n\n" + apacheText,
		"an unknown licence":         "All rights reserved. No permission is granted.",
	} {
		if got, err := classify(text); err == nil {
			t.Errorf("%s was concluded as %q", name, got)
		}
	}
}

// changed guards a negative fixture: an edit that matched nothing leaves the reviewed text, which
// is accepted, and the refusal the fixture exists to test would never run.
func changed(t *testing.T, original, edited string) string {
	t.Helper()
	if edited == original {
		t.Fatal("fixture: the edit matched nothing, so the text is still the reviewed one")
	}
	return edited
}

// A module directory must hold exactly one licence file: none, or two, would mean choosing.
func TestLicenseInNeedsExactlyOneLicenceFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := licenseIn(dir); err == nil {
		t.Error("a directory with no licence file was accepted")
	}
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("LICENSE", mitCopy)
	if got, err := licenseIn(dir); err != nil || got != "MIT" {
		t.Errorf("licenseIn = %q, %v; want MIT", got, err)
	}
	write("COPYING", bsd3Copy)
	if _, err := licenseIn(dir); err == nil {
		t.Error("a directory with two licence files was accepted")
	}
}

func TestEscapePathMatchesTheModuleCache(t *testing.T) {
	if got := escapePath("github.com/BurntSushi/toml"); got != "github.com/!burnt!sushi/toml" {
		t.Errorf("escapePath = %q", got)
	}
}

// The NuGet libraries' licence is the one each package declares. It is written next to the
// pinned version; this checks it against the .nuspec wherever the NuGet cache holds the package
// (a machine that has run `just build-hooks`), and says so when it cannot.
func TestNugetLicencesMatchTheNuspec(t *testing.T) {
	root := os.Getenv("NUGET_PACKAGES")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory to find the NuGet cache in")
		}
		root = filepath.Join(home, ".nuget", "packages")
	}
	re := regexp.MustCompile(`<license type="expression">([^<]+)</license>`)
	checked := 0
	for _, c := range nativeComponents {
		id := strings.ToLower(c.Name)
		data, err := os.ReadFile(filepath.Join(root, id, c.Version, id+".nuspec"))
		if err != nil {
			continue
		}
		m := re.FindSubmatch(data)
		if m == nil {
			t.Errorf("%s %s: the .nuspec declares no licence expression", c.Name, c.Version)
			continue
		}
		// The declared entry is the package's own declaration (decisions D12).
		if len(c.Licenses) == 0 || c.Licenses[0].License.ID != string(m[1]) ||
			c.Licenses[0].License.Acknowledgement != "declared" {
			t.Errorf("%s: licence %+v, but the .nuspec declares %s", c.Name, c.Licenses, m[1])
		}
		checked++
	}
	if checked == 0 {
		t.Skip("the WiX NuGet packages are not in the NuGet cache; run `just build-hooks` to check their licences")
	}
}
