package parser

import (
	"strings"
	"testing"
)

// sha256= on <requires source=> and <prerequisite source=> (#50): accepted in either case and
// normalised to lowercase, refused when malformed, and refused without a source - a download
// msis performs is pinned by msis itself, so a digest there has nothing to apply to, and an
// attribute that were silently dropped is one the author believes is protecting them.

const digestUpper = "CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B"

func TestRequiresSHA256IsAcceptedAndNormalised(t *testing.T) {
	setup, err := ParseBytes([]byte(`<setup>
  <requires type="vcredist" version="2015" source=".\redist\vc_redist.x64.exe" sha256="` + digestUpper + `"/>
</setup>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := setup.Requires[0].SHA256; got != strings.ToLower(digestUpper) {
		t.Errorf("SHA256 = %q, want the lowercase digest", got)
	}
}

func TestPrerequisiteSHA256IsAcceptedAndNormalised(t *testing.T) {
	setup, err := ParseBytes([]byte(`<setup>
  <bundle>
    <prerequisite type="vcredist" version="2022" source="vcstub.exe" sha256=" ` + digestUpper + ` "/>
    <msi source="inner.msi"/>
  </bundle>
</setup>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := setup.Bundle.Prerequisites[0].SHA256; got != strings.ToLower(digestUpper) {
		t.Errorf("SHA256 = %q, want the trimmed lowercase digest", got)
	}
}

func TestSHA256WithoutASourceIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, xml, element string }{
		{"requires", `<setup><requires type="vcredist" version="2022" sha256="` + digestUpper + `"/></setup>`, "<requires>"},
		{"prerequisite", `<setup><bundle><prerequisite type="vcredist" version="2022" sha256="` + digestUpper + `"/><msi source="x.msi"/></bundle></setup>`, "<prerequisite>"},
	} {
		_, err := ParseBytes([]byte(tc.xml))
		if err == nil {
			t.Errorf("%s: a digest with no source to apply to was accepted", tc.name)
			continue
		}
		for _, want := range []string{tc.element, "sha256", "pinned by msis"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", tc.name, err, want)
			}
		}
	}
}

func TestMalformedSHA256IsRefused(t *testing.T) {
	for _, bad := range []string{"abc", strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
		_, err := ParseBytes([]byte(`<setup><requires type="vcredist" version="2022" source="x.exe" sha256="` + bad + `"/></setup>`))
		if err == nil || !strings.Contains(err.Error(), "64 hexadecimal") {
			t.Errorf("sha256=%q: err = %v, want a refusal naming the required form", bad, err)
		}
	}
}

// The attribute is optional: a supplied source without it still parses (and is warned about
// at build time, not here - the parser has no warning channel and the file is not read yet).
func TestSourceWithoutSHA256StillParses(t *testing.T) {
	setup, err := ParseBytes([]byte(`<setup><requires type="vcredist" version="2022" source="x.exe"/></setup>`))
	if err != nil {
		t.Fatal(err)
	}
	if setup.Requires[0].SHA256 != "" {
		t.Errorf("SHA256 = %q, want empty", setup.Requires[0].SHA256)
	}
}
