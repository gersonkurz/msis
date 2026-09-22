package variables

import (
	"reflect"
	"testing"
)

func checked(t *testing.T, d Dictionary, s string) (string, []string) {
	t.Helper()
	out, undefined, err := d.ResolveChecked(s)
	if err != nil {
		t.Fatalf("ResolveChecked(%q): %v", s, err)
	}
	return out, undefined
}

// Every reference form the engine accepts resolves when defined, and is reported when not -
// the two review findings on #46: a regexp guard rejected `{{else}}` as an undefined variable
// and let `{{~X~}}` and hyphenated names through to render as empty.
func TestResolveCheckedCoversTheEnginesReferenceForms(t *testing.T) {
	d := Dictionary{"NAME": "msis", "VER": "1.2.3", "MY-VAR": "hyphenated", "EMPTY": ""}

	for _, tc := range []struct{ in, want string }{
		{"{{NAME}}", "msis"},
		{"{{~VER~}}", "1.2.3"},
		{"{{ VER }}", "1.2.3"},
		{"{{MY-VAR}}", "hyphenated"},
		{"{{#if NAME}}yes{{else}}no{{/if}}", "yes"},
		{"{{#if EMPTY}}yes{{else}}no{{/if}}", "no"},
		{"{{#unless EMPTY}}set{{/unless}}", "set"},
		{`C:\{{NAME}}\bin`, `C:\msis\bin`}, // #13: the backslash is a separator
		{"no reference at all", "no reference at all"},
	} {
		out, undefined := checked(t, d, tc.in)
		if len(undefined) != 0 {
			t.Errorf("%q: reported %v as undefined; every name here is defined", tc.in, undefined)
		}
		if out != tc.want {
			t.Errorf("%q = %q, want %q", tc.in, out, tc.want)
		}
	}
}

func TestResolveCheckedReportsUndefinedReferencesInEveryForm(t *testing.T) {
	d := Dictionary{"NAME": "msis", "EMPTY": ""}

	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"{{NAME}}-{{VERSOIN}}", []string{"VERSOIN"}},
		{"{{~VERSOIN~}}", []string{"VERSOIN"}},
		{"{{NAME-VERSOIN}}", []string{"NAME-VERSOIN"}},
		{"{{VERSOIN}}", []string{"VERSOIN"}},
		// A name used as a CONDITION must be defined too: undefined is not "false" here.
		{"{{#if MISSING}}a{{else}}b{{/if}}", []string{"MISSING"}},
		// Two undefined names, reported sorted.
		{"{{ZED}} {{ALPHA}}", []string{"ALPHA", "ZED"}},
	} {
		out, undefined := checked(t, d, tc.in)
		if !reflect.DeepEqual(undefined, tc.want) {
			t.Errorf("%q: undefined = %v, want %v", tc.in, undefined, tc.want)
		}
		if out != "" {
			t.Errorf("%q: out = %q, want nothing when a reference is undefined", tc.in, out)
		}
	}
}

// Only what the render reaches counts: a reference inside a branch the engine does not take is
// not reported, which is the same rule ResolveAll uses to avoid inventing dependencies.
func TestResolveCheckedIgnoresReferencesInUntakenBranches(t *testing.T) {
	d := Dictionary{"EMPTY": "", "NAME": "msis"}
	out, undefined := checked(t, d, "{{#if EMPTY}}{{MISSING}}{{else}}{{NAME}}{{/if}}")
	if len(undefined) != 0 || out != "msis" {
		t.Errorf("out=%q undefined=%v; the untaken branch's MISSING must not be reported", out, undefined)
	}
}

// Helper names and block syntax are never mistaken for variables.
func TestResolveCheckedDoesNotReportHelpersOrSyntax(t *testing.T) {
	d := Dictionary{"NAME": "msis", "LIST": ""}
	for _, in := range []string{
		"{{#if NAME}}{{NAME}}{{else}}-{{/if}}",
		"{{#unless NAME}}-{{else}}{{NAME}}{{/unless}}",
		"{{#with NAME}}{{this}}{{/with}}",
	} {
		_, undefined := checked(t, d, in)
		if len(undefined) != 0 {
			t.Errorf("%q: %v reported as undefined variables", in, undefined)
		}
	}
}

// Round-2 review of #46: a literal segment `{{[NAME]}}` parses with its brackets but is looked
// up without them, so a candidate keyed on the raw part never matched the render's lookup and
// an undefined bracketed reference rendered as "" unreported. The candidate key is now the
// evaluator's key. Brackets are the only way to reference a name with a space in it.
func TestResolveCheckedNormalisesBracketedSegmentsLikeTheEvaluator(t *testing.T) {
	d := Dictionary{"NAME": "msis", "MY VAR": "with a space"}

	for _, tc := range []struct{ in, want string }{
		{"{{[NAME]}}", "msis"},
		{"{{[MY VAR]}}", "with a space"},
		{"{{#if [MY VAR]}}yes{{else}}no{{/if}}", "yes"},
	} {
		out, undefined := checked(t, d, tc.in)
		if len(undefined) != 0 || out != tc.want {
			t.Errorf("%q = %q (undefined %v), want %q with nothing undefined", tc.in, out, undefined, tc.want)
		}
	}
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"{{[VERSOIN]}}", []string{"VERSOIN"}},
		{"{{[NO SUCH VAR]}}", []string{"NO SUCH VAR"}},
		{"{{NAME}}-{{[VERSOIN]}}", []string{"VERSOIN"}},
		{"{{#if [MISSING]}}a{{/if}}", []string{"MISSING"}},
	} {
		out, undefined := checked(t, d, tc.in)
		if !reflect.DeepEqual(undefined, tc.want) || out != "" {
			t.Errorf("%q: out=%q undefined=%v, want nothing rendered and %v reported", tc.in, out, undefined, tc.want)
		}
	}
}

// A parse error is an error, as from Resolve - not an undefined-name report.
func TestResolveCheckedReportsParseErrors(t *testing.T) {
	d := Dictionary{"NAME": "msis"}
	for _, in := range []string{"{{NAME, default}}", "a {{ b", "{{#if NAME}}unclosed"} {
		if _, _, err := d.ResolveChecked(in); err == nil {
			t.Errorf("%q: expected a parse error", in)
		}
	}
}
