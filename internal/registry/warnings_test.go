package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
)

// warningsFor writes a .reg file and returns the warnings Process produced for it.
func warningsFor(t *testing.T, content string, preserve bool) string {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "w.reg"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor(tmpDir, "")
	if _, err := proc.Process(ir.Registry{File: "w.reg", Preserve: preserve}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(proc.Warnings(), "\n")
}

// TestWarnFormattedExpandableIsNotProtectedByPreserve guards a gap found in review of
// #14. Component-level preserve="yes" does NOT mean every value is preserved:
// shouldPreserveValue additionally excludes expandable strings (see #10), so a
// REG_EXPAND_SZ still reaches the formatted Registry column directly and is still at
// risk. Gating the warning on the component flag hid exactly that case.
func TestWarnFormattedExpandableIsNotProtectedByPreserve(t *testing.T) {
	// hex(2) is REG_EXPAND_SZ; these bytes are "a[Foo]b" in UTF-16LE.
	const content = "Windows Registry Editor Version 5.00\n" +
		"\n" +
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\MyApp]\n" +
		"\"Expand\"=hex(2):61,00,5b,00,46,00,6f,00,6f,00,5d,00,62,00,00,00\n"

	for _, preserve := range []bool{false, true} {
		got := warningsFor(t, content, preserve)
		if !strings.Contains(got, "Expand") {
			t.Errorf("preserve=%v: an expandable value is never preserved, so it must warn; got: %s", preserve, got)
		}
		// Recommending preserve="yes" would be useless advice for a value that
		// preservation excludes by design.
		if strings.Contains(got, `preserve="yes"`) {
			t.Errorf("preserve=%v: must not recommend preserve for a value that cannot be preserved; got: %s", preserve, got)
		}
	}
}

// TestWarnFormattedMultiSepBehindLeadingBracket: "[~]" is a type marker, not a property
// reference, and it is significant at the start of a value too. The leading-"[" exemption
// must not swallow it — "[~]a" would otherwise install as a multi-string in silence.
func TestWarnFormattedMultiSepBehindLeadingBracket(t *testing.T) {
	const content = "Windows Registry Editor Version 5.00\n" +
		"\n" +
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\MyApp]\n" +
		"\"LeadingSep\"=\"[~]a\"\n" +
		"\"RefThenSep\"=\"[INSTALLDIR]a[~]b\"\n" +
		"\"OrdinaryRef\"=\"[INSTALLDIR]app.exe\"\n"

	for _, preserve := range []bool{false, true} {
		got := warningsFor(t, content, preserve)
		for _, want := range []string{"LeadingSep", "RefThenSep"} {
			if !strings.Contains(got, want) {
				t.Errorf("preserve=%v: %s contains [~] and must warn; got: %s", preserve, want, got)
			}
		}
		// A leading [PROPERTY] with no type marker is the documented, intentional
		// form; warning on it would fire on a large share of real packages.
		if strings.Contains(got, "OrdinaryRef") {
			t.Errorf("preserve=%v: an ordinary [PROPERTY] reference must stay quiet; got: %s", preserve, got)
		}
	}
}

// TestWarnFormattedPreservableStringIsProtected is the other half of the contract: a
// plain REG_SZ with brackets IS preservable, so preserve="yes" genuinely protects it
// and the warning must disappear — and when preservation is off, the remedy may
// legitimately suggest it.
func TestWarnFormattedPreservableStringIsProtected(t *testing.T) {
	const content = "Windows Registry Editor Version 5.00\n" +
		"\n" +
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\MyApp]\n" +
		"\"Brackets\"=\"a[Foo]b\"\n"

	if got := warningsFor(t, content, false); !strings.Contains(got, `preserve="yes"`) {
		t.Errorf("unpreserved: remedy should offer preserve for a preservable value; got: %s", got)
	}
	if got := warningsFor(t, content, true); got != "" {
		t.Errorf("preserved: brackets survive, so there is nothing to warn about; got: %s", got)
	}
}
