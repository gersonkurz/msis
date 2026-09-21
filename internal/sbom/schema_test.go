package sbom

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The official CycloneDX 1.6 schema, vendored under testdata/cyclonedx together with the two
// schemas it references (spdx and jsf). Vendored rather than fetched so no test depends on the
// network, and complete rather than partial because a $ref that cannot resolve makes the
// compile fail rather than silently skipping a constraint.
//
// Each is registered under its canonical $id, not its filename: the refs inside the bom schema
// are absolute URLs, so registering by filename leaves them unresolvable.
const schemaDir = "testdata/cyclonedx"

var (
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
)

func cycloneDXSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		for _, name := range []string{"spdx.schema.json", "jsf-0.82.schema.json", "bom-1.6.schema.json"} {
			f, err := os.Open(filepath.Join(schemaDir, name))
			if err != nil {
				schemaErr = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(f)
			f.Close()
			if err != nil {
				schemaErr = err
				return
			}
			c.AddResource("http://cyclonedx.org/schema/"+name, doc)
		}
		schema, schemaErr = c.Compile("http://cyclonedx.org/schema/bom-1.6.schema.json")
	})
	if schemaErr != nil {
		t.Fatalf("compiling the vendored CycloneDX schema: %v", schemaErr)
	}
	return schema
}

// validateAgainstSchema checks one document. Exported through the conformance package for B, C
// and E; here it is the primitive the rest of the checks build on.
func validateAgainstSchema(t *testing.T, doc []byte) error {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return err
	}
	return cycloneDXSchema(t).Validate(v)
}

// TestTheValidatorActuallyValidates: a validator that accepts everything would make every other
// schema assertion in this package meaningless, so it is checked against documents that must
// pass and documents that must not before anything relies on it.
func TestTheValidatorActuallyValidates(t *testing.T) {
	valid := []struct{ name, doc string }{
		{"minimal", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}`},
		{"with a component", `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,
			"components":[{"type":"file","name":"a.dll"}]}`},
	}
	for _, c := range valid {
		if err := validateAgainstSchema(t, []byte(c.doc)); err != nil {
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
		if err := validateAgainstSchema(t, []byte(c.doc)); err == nil {
			t.Errorf("%s: an invalid document was accepted; the validator is not working", c.name)
		}
	}
}
