package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// processWithVars writes content as a .reg file, processes it with the given dictionary
// attached, and returns the processor (for warnings) and the generated registry XML.
func processWithVars(t *testing.T, content string, vars variables.Dictionary, preserve bool) (*Processor, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "v.reg"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor(dir, "")
	proc.Variables = vars
	comps, err := proc.Process(ir.Registry{File: "v.reg", Preserve: preserve})
	if err != nil {
		t.Fatal(err)
	}
	ids := proc.BuildAllPreservedIDs(comps)
	return proc, proc.GenerateXMLWithPreservedIDs(comps, false, ids), proc.GeneratePreservationXML(comps, ids)
}

func dict(kv ...string) variables.Dictionary {
	d := variables.New()
	for i := 0; i+1 < len(kv); i += 2 {
		d[kv[i]] = kv[i+1]
	}
	return d
}

// {{VAR}} in a REG_SZ value expands at build time, as in msis-2.x (#46). A backslash before
// the reference is a path separator and survives (#13's rule, which Resolve applies).
func TestVariablesExpandInStringValues(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Version"="{{PRODUCT_VERSION}}"
"Dir"="C:\\Apps\\{{PRODUCT_NAME}}\\bin"
"Mixed"="{{PRODUCT_NAME}} {{PRODUCT_VERSION}}"
"Exe"="[INSTALLDIR]{{PRODUCT_NAME}}.exe"
"Plain"="nothing to expand"
`
	proc, xml, _ := processWithVars(t, content, dict("PRODUCT_NAME", "MyApp", "PRODUCT_VERSION", "1.2.3"), false)

	for _, want := range []string{
		`Name='Version' Value='1.2.3' Type='string'`,
		`Name='Dir' Value='C:\Apps\MyApp\bin' Type='string'`,
		`Name='Mixed' Value='MyApp 1.2.3' Type='string'`,
		// Build-time {{VAR}} and install-time [PROPERTY] are different mechanisms and combine:
		// the variable expands now, the leading property reference is left for Windows
		// Installer (D4) - and, leading, it draws no Formatted warning.
		`Name='Exe' Value='[INSTALLDIR]MyApp.exe' Type='string'`,
		`Name='Plain' Value='nothing to expand' Type='string'`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("want %s in:\n%s", want, xml)
		}
	}
	if w := proc.Warnings(); len(w) != 0 {
		t.Errorf("fully resolved values must not warn, got: %v", w)
	}
}

// An undefined name is not rendered as empty, which is what Handlebars (and msis-2.x) did:
// the value is written as authored and the build says so.
func TestUndefinedVariableWarnsAndKeepsTheValueAsAuthored(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Oops"="{{PRODUCT_VERSOIN}}"
`
	proc, xml, _ := processWithVars(t, content, dict("PRODUCT_VERSION", "1.2.3"), false)

	if !strings.Contains(xml, `Name='Oops' Value='{{PRODUCT_VERSOIN}}' Type='string'`) {
		t.Errorf("an undefined reference must be written as authored, got:\n%s", xml)
	}
	joined := strings.Join(proc.Warnings(), "\n")
	for _, want := range []string{`"Oops"`, "{{PRODUCT_VERSOIN}}", "not a defined variable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning lacks %q: %s", want, joined)
		}
	}
}

// Text the template engine cannot parse - the "{{VAR, DEFAULT}}" form seen in the wild (#14),
// or a stray "{{" - is warned about and written as authored, never half-substituted.
func TestMalformedReferenceWarnsAndKeepsTheValueAsAuthored(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Default"="{{PRODUCT_VERSION, 0.0.0}}"
"Stray"="a {{ b"
`
	proc, xml, _ := processWithVars(t, content, dict("PRODUCT_VERSION", "1.2.3"), false)

	for _, want := range []string{
		`Name='Default' Value='{{PRODUCT_VERSION, 0.0.0}}' Type='string'`,
		`Name='Stray' Value='a {{ b' Type='string'`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("want %s in:\n%s", want, xml)
		}
	}
	joined := strings.Join(proc.Warnings(), "\n")
	if !strings.Contains(joined, `"Default"`) || !strings.Contains(joined, `"Stray"`) {
		t.Errorf("both malformed values must warn, got: %s", joined)
	}
	if !strings.Contains(joined, "written as authored") {
		t.Errorf("the warning must say the value is used as written, got: %s", joined)
	}
}

// Value names and key paths are not expanded - msis-2.x did not either - and neither are
// non-string value types. The scope is REG_SZ values, exactly.
func TestNamesKeysAndOtherTypesAreNotExpanded(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\{{PRODUCT_NAME}}]
"{{PRODUCT_NAME}}"="named after a variable"
"Multi"=hex(7):7b,00,7b,00,58,00,7d,00,7d,00,00,00,00,00
`
	_, xml, _ := processWithVars(t, content, dict("PRODUCT_NAME", "MyApp", "X", "expanded"), false)

	// Key paths are emitted as nested RegistryKey elements, one per segment.
	if !strings.Contains(xml, `<RegistryKey Key='{{PRODUCT_NAME}}'`) {
		t.Errorf("a key path must not be expanded, got:\n%s", xml)
	}
	if !strings.Contains(xml, `Name='{{PRODUCT_NAME}}' Value='named after a variable'`) {
		t.Errorf("a value name must not be expanded, got:\n%s", xml)
	}
	if !strings.Contains(xml, `<MultiStringValue>{{X}}</MultiStringValue>`) {
		t.Errorf("a multi-string element must not be expanded, got:\n%s", xml)
	}
}

// With no dictionary attached - the processor used on its own - nothing changes.
func TestNoDictionaryLeavesValuesAsWritten(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Version"="{{PRODUCT_VERSION}}"
`
	proc, xml, _ := processWithVars(t, content, nil, false)
	if !strings.Contains(xml, `Value='{{PRODUCT_VERSION}}'`) {
		t.Errorf("without a dictionary the value must pass through, got:\n%s", xml)
	}
	if w := proc.Warnings(); len(w) != 0 {
		t.Errorf("no dictionary, no warning, got: %v", w)
	}
}

// The expansion happens where the value is read, so every later consumer sees the expanded
// text: the preserved default (msis-2.x expanded on that path too) and the Formatted-field
// warning, which must judge what will actually land in the Registry table.
func TestExpansionReachesThePreservedDefaultAndTheFormattedWarning(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Version"="{{PRODUCT_VERSION}}"
`
	_, xml, props := processWithVars(t, content, dict("PRODUCT_VERSION", "1.2.3"), true)
	if !strings.Contains(props, `<Property Id='PS_RV_00000' Value='1.2.3' Secure='yes'>`) {
		t.Errorf("the preserved default must be the expanded value, got:\n%s", props)
	}
	if !strings.Contains(xml, `Name='Version' Value='[PS_RV_00000]'`) {
		t.Errorf("the value must still be written through its property, got:\n%s", xml)
	}

	// A variable whose VALUE carries a Formatted hazard: the warning fires on what lands.
	hazard := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Sep"="{{SEPARATED}}"
`
	proc, _, _ := processWithVars(t, hazard, dict("SEPARATED", "a[~]b"), false)
	joined := strings.Join(proc.Warnings(), "\n")
	if !strings.Contains(joined, `"Sep"`) || !strings.Contains(joined, "REG_MULTI_SZ") || !strings.Contains(joined, `"a[~]b"`) {
		t.Errorf("the Formatted warning must see the expanded value, got: %s", joined)
	}
}

// Round-1 review of #46: the undefined-name guard was a regexp, which rejected `{{else}}` as
// an undefined variable and let `{{~X~}}` and hyphenated names through to render as "". The
// guard is now the engine's own evaluation (variables.ResolveChecked); this pins the forms the
// review named, from the registry's side.
func TestReferenceFormsAndConditionalsResolveLikeTheEngine(t *testing.T) {
	content := `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SOFTWARE\MyApp]
"Cond"="{{#if PRODUCT_NAME}}yes{{else}}no{{/if}}"
"Trim"="{{~PRODUCT_VERSION~}}"
"Hyphen"="{{MY-VAR}}"
"Untaken"="{{#if EMPTY}}{{NOT_DEFINED}}{{else}}taken{{/if}}"
"UndefTrim"="{{~PRODUCT_VERSOIN~}}"
"UndefHyphen"="{{PRODUCT-VERSOIN}}"
"UndefCond"="{{#if NOT_DEFINED}}a{{else}}b{{/if}}"
"Partial"="{{PRODUCT_VERSION}}-{{PRODUCT_VERSOIN}}"
"Bracket"="{{[MY VAR]}}"
"PartialBracket"="{{PRODUCT_VERSION}}-{{[PRODUCT_VERSOIN]}}"
`
	proc, xml, _ := processWithVars(t, content,
		dict("PRODUCT_NAME", "MyApp", "PRODUCT_VERSION", "1.2.3", "MY-VAR", "hyphenated", "MY VAR", "spaced", "EMPTY", ""), false)

	for _, want := range []string{
		`Name='Cond' Value='yes'`,
		`Name='Trim' Value='1.2.3'`,
		`Name='Hyphen' Value='hyphenated'`,
		`Name='Untaken' Value='taken'`,
		`Name='Bracket' Value='spaced'`,
		// Undefined in any form: the WHOLE value as authored, never half-substituted.
		`Name='UndefTrim' Value='{{~PRODUCT_VERSOIN~}}'`,
		`Name='UndefHyphen' Value='{{PRODUCT-VERSOIN}}'`,
		`Name='UndefCond' Value='{{#if NOT_DEFINED}}a{{else}}b{{/if}}'`,
		`Name='Partial' Value='{{PRODUCT_VERSION}}-{{PRODUCT_VERSOIN}}'`,
		// Round-2 review: a bracketed literal segment is looked up without its brackets.
		`Name='PartialBracket' Value='{{PRODUCT_VERSION}}-{{[PRODUCT_VERSOIN]}}'`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("want %s in:\n%s", want, xml)
		}
	}

	joined := strings.Join(proc.Warnings(), "\n")
	for _, name := range []string{`"UndefTrim"`, `"UndefHyphen"`, `"UndefCond"`, `"Partial"`, `"PartialBracket"`} {
		if !strings.Contains(joined, name) {
			t.Errorf("%s must warn, got: %s", name, joined)
		}
	}
	for _, name := range []string{`"Cond"`, `"Trim"`, `"Hyphen"`, `"Untaken"`, `"Bracket"`} {
		if strings.Contains(joined, name) {
			t.Errorf("%s resolved fully and must not warn, got: %s", name, joined)
		}
	}
	if !strings.Contains(joined, "{{NOT_DEFINED}}") || !strings.Contains(joined, "{{PRODUCT-VERSOIN}}") {
		t.Errorf("warnings must name the undefined references, got: %s", joined)
	}
	if strings.Count(joined, "{{PRODUCT_VERSOIN}}") < 2 {
		t.Errorf("Partial and PartialBracket must both name PRODUCT_VERSOIN (the bracketed form under its lookup key), got: %s", joined)
	}
}
