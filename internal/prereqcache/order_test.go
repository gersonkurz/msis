package prereqcache

import (
	"slices"
	"strings"
	"testing"
)

// #73: the hint lists come from maps; they are sorted, so the error is the same on every run.
func TestVersionHintsListInADefinedOrder(t *testing.T) {
	for typ, after := range map[string]string{
		"nosuchtype": "available types: ",
		"vcredist":   "versions with auto-download: ",
	} {
		_, rest, _ := strings.Cut(getAvailableVersionsHint(typ), after)
		if list := strings.Split(rest, ", "); len(list) < 2 || !slices.IsSorted(list) {
			t.Errorf("%s: %v is not sorted", typ, list)
		}
	}
}
