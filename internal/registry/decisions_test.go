package registry

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docs/decisions.md records the questions that were settled deliberately - REG_QWORD truncation,
// what preserve="yes" skips - so that a reader who meets one of them does not investigate it a
// second time. #40 exists because exactly that happened: #10 settled QWORD handling with an
// install probe, and the question came back a month later from a note that outlived the fix.
//
// A record that quietly stops being true is worse than none, because the next reader believes
// it. So each entry names the code that implements it, and this test requires those anchors to
// still be there.
//
// That is an ANCHOR-PRESENCE check and nothing more. It catches an entry whose code was deleted,
// moved or renamed - the moment to rewrite the entry rather than leave it pointing at nothing.
// It does NOT catch a decision reversed in place: flipping a `return false` to `return true`
// leaves every anchor exactly where it was. Keeping the prose true is a job for review, and
// saying so here matters, because a guard described as stronger than it is buys false comfort.
func TestEverySettledDecisionIsStillImplemented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "decisions.md"))
	if err != nil {
		t.Fatal(err)
	}

	// **Implemented by:** `<file>` — `<anchor>`[, `<anchor>`…]
	entries := regexp.MustCompile(`\*\*Implemented by:\*\* `+"`([^`]+)`"+` — (.+)`).
		FindAllStringSubmatch(string(doc), -1)
	if len(entries) == 0 {
		t.Fatal("docs/decisions.md names no implementing code; either the format changed or " +
			"the entries stopped being checkable, and an unchecked record is one that rots")
	}

	anchors := regexp.MustCompile("`([^`]+)`")
	for _, entry := range entries {
		file := filepath.Join("..", "..", filepath.FromSlash(entry[1]))
		code, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("a decision names %s, which is not there: %v", entry[1], err)
			continue
		}
		for _, m := range anchors.FindAllStringSubmatch(entry[2], -1) {
			if !strings.Contains(string(code), m[1]) {
				t.Errorf("%s no longer contains %q.\n"+
					"A settled decision in docs/decisions.md says it does. Either the decision "+
					"was reversed - in which case rewrite the entry, and say what changed - or "+
					"the anchor moved and the entry needs a new one.", entry[1], m[1])
			}
		}
	}
}
