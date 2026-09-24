package sbom

import (
	"reflect"
	"testing"
)

// #62: NTIA's generation context. A document read from an artifact alone is post-build; one
// written during the build also says build, because the build record contributed to it.
func TestTheGenerationContextSaysWhenTheInformationWasObtained(t *testing.T) {
	phases := func(d *Document) []string {
		var out []string
		for _, l := range d.Metadata.Lifecycles {
			out = append(out, l.Phase)
		}
		return out
	}

	artifactOnly, err := FromPackage(syntheticPackage(), Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := phases(artifactOnly); !reflect.DeepEqual(got, []string{LifecyclePostBuild}) {
		t.Errorf("artifact only: %q, want [post-build]", got)
	}

	enriched, err := docWith(t, recordFor(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := phases(enriched); !reflect.DeepEqual(got, []string{LifecycleBuild, LifecyclePostBuild}) {
		t.Errorf("enriched by the build: %q, want [build post-build]", got)
	}

	bundle := bundleDoc(t, syntheticBundle("testdata/artifact.bin"))
	if got := phases(bundle); !reflect.DeepEqual(got, []string{LifecyclePostBuild}) {
		t.Errorf("bundle: %q, want [post-build]", got)
	}
}
