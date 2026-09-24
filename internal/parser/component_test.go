package parser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
)

func setupWith(element string) []byte {
	return []byte(`<setup><set name="PRODUCT_NAME" value="P"/>` + element + `</setup>`)
}

// #64: a <component> declares facts about one payload file; every attribute is carried.
func TestParseDeclaredComponent(t *testing.T) {
	s, err := ParseBytes(setupWith(`<component for="[INSTALLDIR]libfoo.dll" name="libfoo" version="2.3.1"
		creator="https://foo.example" license="Apache-2.0 OR MIT" purl="pkg:nuget/Foo@2.3.1"
		cpe="cpe:2.3:a:foo:libfoo:2.3.1:*:*:*:*:*:*:*"/>`))
	if err != nil {
		t.Fatal(err)
	}
	want := []ir.DeclaredComponent{{
		For: "[INSTALLDIR]libfoo.dll", Name: "libfoo", Version: "2.3.1", Creator: "https://foo.example",
		License: "Apache-2.0 OR MIT", PURL: "pkg:nuget/Foo@2.3.1", CPE: "cpe:2.3:a:foo:libfoo:2.3.1:*:*:*:*:*:*:*",
	}}
	if !reflect.DeepEqual(s.Components, want) {
		t.Errorf("components %+v, want %+v", s.Components, want)
	}
}

// A declaration is published as a fact about the file, so a malformed one is refused at parse
// time, naming the element.
func TestAMalformedDeclarationIsRefused(t *testing.T) {
	for element, mustSay := range map[string]string{
		`<component name="x"/>`:                                            "requires a for attribute",
		`<component for="[INSTALLDIR]a.dll"/>`:                             "declares nothing",
		`<component for="[INSTALLDIR]a.dll" nmae="x"/>`:                    "unknown attribute 'nmae'",
		`<component for="[INSTALLDIR]a.dll" creator="the vendor"/>`:        "creator",
		`<component for="[INSTALLDIR]a.dll" license="MIT and some more"/>`: "license",
		`<component for="[INSTALLDIR]a.dll" purl="nuget:Foo"/>`:            "purl",
		`<component for="[INSTALLDIR]a.dll" cpe="foo:libfoo"/>`:            "cpe",
	} {
		_, err := ParseBytes(setupWith(element))
		if err == nil || !strings.Contains(err.Error(), mustSay) {
			t.Errorf("%s: want an error saying %q, got %v", element, mustSay, err)
		}
	}
}

// The licence check follows the SPDX grammar (#64's review), not a copy of the SPDX list.
func TestSPDXExpressionSyntax(t *testing.T) {
	for _, ok := range []string{
		"MIT", "GPL-2.0+", "LicenseRef-acme-proprietary", "DocumentRef-spdx-tool-1.2:LicenseRef-MIT-Style-2",
		"GPL-3.0-only WITH Classpath-exception-2.0",
		"(MIT OR Apache-2.0) AND BSD-3-Clause",
		"(MIT)OR(Apache-2.0)", // parentheses delimit tokens
		"MIT AND (LGPL-2.1-or-later OR BSD-3-Clause) AND ((Apache-2.0))",
		"(GPL-2.0-only WITH Classpath-exception-2.0) OR MIT", // a WITH inside a group, combined by OR
	} {
		if err := validSPDX(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "(MIT", "MIT)", "MIT++", "()", "MIT OR", "OR MIT", "MIT AND AND Apache-2.0",
		"MIT and Apache-2.0", // operators are upper case
		"MIT WITH", "MIT WITH OR", "(MIT OR Apache-2.0", "MIT Apache-2.0", "MIT,Apache-2.0",
		"LicenseRef-", "GPL-2.0+ WITH exception+",
		// WITH's left operand is a single licence (Annex D's simple expression), never a group.
		"(MIT OR Apache-2.0) WITH Classpath-exception-2.0",
		"(GPL-2.0-only WITH Classpath-exception-2.0) WITH LLVM-exception",
		"(MIT) WITH Classpath-exception-2.0",
		"MIT WITH Classpath-exception-2.0 WITH LLVM-exception",
	} {
		if err := validSPDX(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
