package sbom

import "testing"

// #74: the one component count - the subject, every component, and every nested one.
func TestCountComponents(t *testing.T) {
	for doc, want := range map[string]int{
		`{"metadata": {"component": {"bom-ref": "p", "name": "P"}},
		  "components": [{"name": "a"}, {"name": "b", "components": [{"name": "b1"}, {"name": "b2", "components": [{"name": "b21"}]}]}]}`: 6,
		`{"metadata": {"component": {}}, "components": [{"name": "a"}]}`:               1, // a subject naming nothing is not one
		`{"metadata": {"component": {"bom-ref": "p", "components": [{"name": "c"}]}}}`: 2,
		`{"components": []}`: 0,
	} {
		got, err := CountComponents([]byte(doc))
		if err != nil || got != want {
			t.Errorf("CountComponents = %d (%v), want %d, for %s", got, err, want, doc)
		}
	}
}
