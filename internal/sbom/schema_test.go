package sbom

import (
	"testing"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// TestTheValidatorActuallyValidates: a validator that accepts everything would make every
// schema assertion in this package meaningless, so it is checked against documents that must
// pass and documents that must not before anything relies on it.
//
// This used to compile the vendored schema a second time, here, with its own copy of the
// resource-registration dance. It now goes through the one validator - the schema moved into
// the conformance package and is embedded there (#35 needs it path-free), and two compilations
// of one schema could only ever drift.
func TestTheValidatorActuallyValidates(t *testing.T) {
	valid := []struct{ name, doc string }{
		{"minimal", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`},
		{"with a component", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,
			"components":[{"type":"file","name":"a.dll"}]}`},
	}
	for _, c := range valid {
		if err := conformance.ValidateSchema([]byte(c.doc)); err != nil {
			t.Errorf("%s: a valid document was rejected: %v", c.name, err)
		}
	}

	invalid := []struct{ name, doc string }{
		{"wrong bomFormat", `{"bomFormat":"NotCycloneDX","specVersion":"1.6","version":1}`},
		{"missing specVersion", `{"bomFormat":"CycloneDX","version":1}`},
		{"unknown component type", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,
			"components":[{"type":"not-a-type","name":"a"}]}`},
		{"bad hash algorithm", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,
			"components":[{"type":"file","name":"a","hashes":[{"alg":"SHA-999","content":"ab"}]}]}`},
		{"serialNumber not a urn:uuid", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,
			"serialNumber":"not-a-uuid"}`},
	}
	for _, c := range invalid {
		if err := conformance.ValidateSchema([]byte(c.doc)); err == nil {
			t.Errorf("%s: an invalid document was accepted; the validator is not working", c.name)
		}
	}
}
