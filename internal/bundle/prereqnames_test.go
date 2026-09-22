package bundle

import (
	"testing"

	"github.com/gersonkurz/msis/internal/prereqcache"
)

// The chain names a prerequisite by the Source in Prerequisites, and the cache stores the
// download under prereqcache's FileName. When the two are the same file they must have the
// same name, or the PrerequisitesFolder fallback would look for a file the cache never
// writes. #30 re-pinned the .NET entries to the OFFLINE installers, whose names the chain
// already used — this keeps the two tables from drifting apart again.
func TestPinnedDownloadNamesMatchTheChainSources(t *testing.T) {
	for typ, versions := range prereqcache.DownloadURLs {
		for version, arches := range versions {
			def, ok := Prerequisites[typ][version]
			if !ok {
				t.Errorf("%s %s can be downloaded but the chain has no definition for it", typ, version)
				continue
			}
			for arch, e := range arches {
				want := def.Source
				if arch != "" {
					want = ExpandArchName(def.Source, arch)
				}
				if e.FileName != want {
					t.Errorf("%s %s %q: cache writes %q, chain expects %q", typ, version, arch, e.FileName, want)
				}
			}
		}
	}
}
