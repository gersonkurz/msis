package bundle

import (
	"slices"
	"strings"
	"testing"
)

// #73: the lists in these error messages come from maps; they are sorted, so the message is the
// same on every run.
func TestPrerequisiteErrorsListInADefinedOrder(t *testing.T) {
	listed := func(msg, after string) []string {
		_, rest, _ := strings.Cut(msg, after)
		return strings.Split(rest, ", ")
	}
	err := ValidatePrerequisite("nosuchtype", "1")
	if err == nil {
		t.Fatal("an unknown type was accepted")
	}
	if types := listed(err.Error(), "available types: "); len(types) < 2 || !slices.IsSorted(types) {
		t.Errorf("available types %v are not sorted", types)
	}
	err = ValidatePrerequisite("vcredist", "1999")
	if err == nil {
		t.Fatal("an unknown version was accepted")
	}
	if versions := listed(err.Error(), "available versions: "); len(versions) < 2 || !slices.IsSorted(versions) {
		t.Errorf("available versions %v are not sorted", versions)
	}
}
