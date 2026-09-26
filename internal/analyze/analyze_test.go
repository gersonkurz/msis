package analyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A syft-json report shaped like syft 1.52's, one artifact per kind of identification msis has
// to decide about (#82, D25). Paths are as syft writes them on Windows, relative to the scanned
// folder with a leading separator.
const report52 = `{
 "descriptor": {"name": "syft", "version": "1.52.0"},
 "artifacts": [
  {"name": "pillow", "version": "10.4.0", "foundBy": "python-installed-package-cataloger",
   "purl": "pkg:pypi/pillow@10.4.0", "metadataType": "python-package",
   "locations": [{"path": "\\lib\\pillow-10.4.0.dist-info\\RECORD", "annotations": {"evidence": "supporting"}},
                 {"path": "\\lib\\pillow-10.4.0.dist-info\\METADATA", "annotations": {"evidence": "primary"}}]},
  {"name": "legacy", "version": "1.0", "foundBy": "python-installed-package-cataloger",
   "purl": "pkg:pypi/legacy@1.0", "metadataType": "python-package",
   "locations": [{"path": "\\lib\\legacy.egg-info\\PKG-INFO", "annotations": {"evidence": "primary"}}]},
  {"name": "logback-core", "version": "1.3.14", "foundBy": "java-archive-cataloger",
   "purl": "pkg:maven/ch.qos.logback/logback-core@1.3.14", "metadataType": "java-archive",
   "locations": [{"path": "\\CINEO\\logback-core.jar", "annotations": {"evidence": "primary"}}],
   "metadata": {"virtualPath": "\\CINEO\\logback-core.jar",
     "pomProperties": {"groupId": "ch.qos.logback", "artifactId": "logback-core", "version": "1.3.14"}}},
  {"name": "jetty-http", "version": "9.4.53", "foundBy": "java-archive-cataloger",
   "purl": "pkg:maven/org.eclipse.jetty/jetty-http@9.4.53", "metadataType": "java-archive",
   "locations": [{"path": "\\CINEO\\plugins\\pax-web.jar", "annotations": {"evidence": "primary"}}],
   "metadata": {"virtualPath": "\\CINEO\\plugins\\pax-web.jar:lib/jetty-http.jar",
     "pomProperties": {"groupId": "org.eclipse.jetty", "artifactId": "jetty-http", "version": "9.4.53"}}},
  {"name": "bcprov-ext-jdk15on", "version": "1.69.00.0", "foundBy": "java-archive-cataloger",
   "purl": "pkg:maven/org.bouncycastle/bcprov-ext-jdk15on@1.69.00.0", "metadataType": "java-archive",
   "locations": [{"path": "\\CINEO\\bcprov-ext-jdk15on.jar", "annotations": {"evidence": "primary"}}],
   "metadata": {"virtualPath": "\\CINEO\\bcprov-ext-jdk15on.jar"}},
  {"name": "renamed", "version": "2.0", "foundBy": "java-archive-cataloger",
   "purl": "pkg:maven/g/renamed@2.0", "metadataType": "java-archive",
   "locations": [{"path": "\\CINEO\\renamed.jar", "annotations": {"evidence": "primary"}}],
   "metadata": {"pomProperties": {"groupId": "g", "artifactId": "original", "version": "2.0"}}},
  {"name": "Newtonsoft.Json", "version": "13.0.1", "foundBy": "dotnet-deps-binary-cataloger",
   "purl": "pkg:nuget/Newtonsoft.Json@13.0.1", "metadataType": "dotnet-deps-entry",
   "locations": [{"path": "\\app\\App.deps.json", "annotations": {"evidence": "primary"}}],
   "metadata": {"name": "Newtonsoft.Json", "version": "13.0.1", "type": "package"}},
  {"name": "App", "version": "1.0.0", "foundBy": "dotnet-deps-binary-cataloger",
   "purl": "pkg:nuget/App@1.0.0", "metadataType": "dotnet-deps-entry",
   "locations": [{"path": "\\app\\App.deps.json", "annotations": {"evidence": "primary"}}],
   "metadata": {"name": "App", "version": "1.0.0", "type": "project"}},
  {"name": "log4net", "version": "2.0.8.0-.NET 2.0", "foundBy": "dotnet-deps-binary-cataloger",
   "purl": "pkg:nuget/log4net@2.0.8.0-.NET%202.0", "metadataType": "dotnet-portable-executable-entry",
   "locations": [{"path": "\\app\\log4net.dll", "annotations": {"evidence": "primary"}}]},
  {"name": "Python", "version": "3.10.15", "foundBy": "pe-binary-package-cataloger", "metadataType": "pe-binary",
   "locations": [{"path": "\\python310.dll", "annotations": {"evidence": "primary"}}]},
  {"name": "python", "version": "3.10.15", "foundBy": "binary-classifier-cataloger",
   "purl": "pkg:generic/python@3.10.15",
   "locations": [{"path": "\\python310.dll", "annotations": {"evidence": "primary"}}]},
  {"name": "nopurl", "version": "1.0", "foundBy": "python-installed-package-cataloger", "metadataType": "python-package",
   "locations": [{"path": "\\lib\\nopurl-1.0.dist-info\\METADATA", "annotations": {"evidence": "primary"}}]},
  {"name": "elsewhere", "version": "1.0", "foundBy": "python-installed-package-cataloger",
   "purl": "pkg:pypi/elsewhere@1.0", "metadataType": "python-package",
   "locations": [{"path": "\\not\\in\\the\\package.dist-info\\METADATA", "annotations": {"evidence": "primary"}}]}
 ]
}`

func extracted() map[string]string {
	n := filepath.FromSlash
	return map[string]string{
		n("lib/pillow-10.4.0.dist-info/METADATA"): "FILE_METADATA",
		n("lib/pillow-10.4.0.dist-info/RECORD"):   "FILE_RECORD",
		n("lib/legacy.egg-info/PKG-INFO"):         "FILE_PKGINFO",
		n("CINEO/logback-core.jar"):               "FILE_LOGBACK",
		n("CINEO/plugins/pax-web.jar"):            "FILE_PAX",
		n("CINEO/bcprov-ext-jdk15on.jar"):         "FILE_BC",
		n("CINEO/renamed.jar"):                    "FILE_RENAMED",
		n("app/App.deps.json"):                    "FILE_DEPS",
		n("app/log4net.dll"):                      "FILE_LOG4NET",
		n("python310.dll"):                        "FILE_PYDLL",
		n("lib/nopurl-1.0.dist-info/METADATA"):    "FILE_NOPURL",
	}
}

// Only a package's own declaration becomes an identity: dist-info METADATA, pom.properties
// whose artifact syft reports under the pom's own name and version, a .deps.json entry. The
// rest - a PE version resource, a jar named from its file, a binary classifier - is counted.
func TestParseKeepsOnlyDeclaredIdentities(t *testing.T) {
	a, err := Parse([]byte(report52), extracted())
	if err != nil {
		t.Fatal(err)
	}
	if a.Tool.Name != "syft" || a.Tool.Version != "1.52.0" {
		t.Errorf("tool = %s %s", a.Tool.Name, a.Tool.Version)
	}
	var got []string
	for _, p := range a.Packages {
		got = append(got, strings.Join([]string{p.FileID, p.PURL, p.Basis, p.Within}, " | "))
	}
	want := []string{
		"FILE_DEPS | pkg:nuget/Newtonsoft.Json@13.0.1 | .deps.json | ",
		"FILE_LOGBACK | pkg:maven/ch.qos.logback/logback-core@1.3.14 | pom.properties | ",
		"FILE_METADATA | pkg:pypi/pillow@10.4.0 | dist-info METADATA | ",
		"FILE_PAX | pkg:maven/org.eclipse.jetty/jetty-http@9.4.53 | pom.properties | lib/jetty-http.jar",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("packages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantSkipped := map[string]int{
		skipPython:   1, // legacy, from an egg-info
		skipJarNoPOM: 2, // bcprov, named from its file; renamed, whose name is not the pom's
		skipPE:       2, // log4net's assembly version resource, Python's PE
		skipOther:    1, // the binary classifier
		skipNoPURL:   1,
		skipOutside:  1,
		skipProject:  1, // the application's own project in the .deps.json
	}
	for reason, n := range wantSkipped {
		if a.Skipped[reason] != n {
			t.Errorf("skipped %q = %d, want %d (all: %v)", reason, a.Skipped[reason], n, a.Skipped)
		}
	}
	if len(a.Skipped) != len(wantSkipped) {
		t.Errorf("skipped reasons %v, want exactly %v", a.Skipped, wantSkipped)
	}
}

// #82 review: the digest in metadata.tools is the program's, not a launcher's. A Scoop shim is
// followed to the program its .shim names; one that names nothing gives no digest at all.
func TestLaunchedFollowsAScoopShim(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "syft.exe")
	if err := os.WriteFile(exe, []byte("launcher"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := launched(exe); got != exe {
		t.Errorf("a plain executable resolved to %q", got)
	}
	real := filepath.Join(dir, "apps", "syft", "current", "syft.exe")
	if err := os.WriteFile(filepath.Join(dir, "syft.shim"), []byte("path = \""+real+"\"\r\nargs = \r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := launched(exe); got != real {
		t.Errorf("the shim resolved to %q, want %q", got, real)
	}
	if err := os.WriteFile(filepath.Join(dir, "syft.shim"), []byte("args = x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := launched(exe); got != "" {
		t.Errorf("a shim naming no program resolved to %q, want none", got)
	}
}

// A Chocolatey shim (an exe in %ChocolateyInstall%\bin) names no program msis can read: no
// digest, rather than the launcher's. Outside that folder the same executable is hashed as is.
func TestLaunchedGivesNoProgramForAChocolateyShim(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ChocolateyInstall", root)
	shim := filepath.Join(root, "bin", "syft.exe")
	if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("launcher"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := launched(shim); got != "" {
		t.Errorf("a Chocolatey shim resolved to %q, want none", got)
	}
	elsewhere := filepath.Join(root, "tools", "syft.exe")
	if got := launched(elsewhere); got != elsewhere {
		t.Errorf("an executable outside Chocolatey's bin resolved to %q", got)
	}
}

// A report from anything but syft is not read as one.
func TestParseRefusesAForeignReport(t *testing.T) {
	if _, err := Parse([]byte(`{"descriptor":{"name":"other","version":"1"},"artifacts":[]}`), nil); err == nil {
		t.Error("a report that is not syft's was accepted")
	}
	if _, err := Parse([]byte(`not json`), nil); err == nil {
		t.Error("a report that is not JSON was accepted")
	}
}
