package generator

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Feature ids (#87, decisions D28).
//
// The Feature table key is what Windows Installer remembers about a feature: MajorUpgrade's
// MigrateFeatures hands each installed feature's state to the feature with the SAME key in the
// new package. msis numbers features FEATURE_%05d in the order they are written, so inserting
// one renumbers every feature after it, and an upgrade hands the customer's choices to the
// wrong features (T85 reproduced it).
//
// A feature may carry an explicit id. Positional numbers count only the features that have
// none, so a feature inserted WITH an id moves no other feature's number, and a package that
// uses no ids at all gets exactly the ids it always got. checkFeatureIDs validates the whole
// tree once, before any component is assigned to a feature.

// featurePlace is one authored feature, as assignFeatureIDs placed it.
type featurePlace struct {
	id       string
	explicit bool
	where    string // the titles from the root down, for messages: "Main > Tools"
	depth    int    // 1 for a top-level feature
}

// featureIDPattern is a Windows Installer Identifier; the Feature table's key holds 38 characters.
var featureIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

const maxFeatureIDLength = 38

// maxFeatureDepth is the Feature table's nesting limit.
const maxFeatureDepth = 16

// reservedFeatureIDs are keys an explicit id must not take: one msis generates itself, and the
// ones the shipped templates declare. ALL is checked separately, in any case.
var reservedFeatureIDs = []string{packageItemsFeatureID, "VCRedist"}

// checkFeatureIDs validates every feature id, explicit and positional, and under Strict requires
// an explicit one on every authored feature.
func (c *Context) checkFeatureIDs() error {
	var problems []string
	byID := map[string][]featurePlace{}
	var missing []featurePlace
	for _, p := range c.featurePlaces {
		byID[p.id] = append(byID[p.id], p)
		if p.depth > maxFeatureDepth {
			problems = append(problems, fmt.Sprintf("feature %q is nested %d deep; Windows Installer allows %d", p.where, p.depth, maxFeatureDepth))
		}
		if !p.explicit {
			missing = append(missing, p)
			continue
		}
		switch {
		case strings.Contains(p.id, "{{"):
			problems = append(problems, fmt.Sprintf(`feature %q: id %q holds a {{...}} reference; an id is written literally, so that /SET cannot change a feature's identity`, p.where, p.id))
		case !featureIDPattern.MatchString(p.id):
			problems = append(problems, fmt.Sprintf("feature %q: id %q is not an identifier (a letter or underscore, then letters, digits, underscores and periods)", p.where, p.id))
		case len(p.id) > maxFeatureIDLength:
			problems = append(problems, fmt.Sprintf("feature %q: id %q is %d characters; the Feature table holds %d", p.where, p.id, len(p.id), maxFeatureIDLength))
		case strings.EqualFold(p.id, "ALL"):
			problems = append(problems, fmt.Sprintf("feature %q: id %q is reserved: ADDLOCAL=ALL and REMOVE=ALL mean every feature", p.where, p.id))
		case slices.Contains(reservedFeatureIDs, p.id):
			problems = append(problems, fmt.Sprintf("feature %q: id %q is reserved for a feature msis or its templates declare", p.where, p.id))
		}
	}
	for _, id := range sortedKeysOf(byID) {
		if places := byID[id]; len(places) > 1 {
			var names []string
			for _, p := range places {
				kind := "positional"
				if p.explicit {
					kind = "explicit"
				}
				names = append(names, fmt.Sprintf("%q (%s)", p.where, kind))
			}
			problems = append(problems, fmt.Sprintf("features %s all have the id %s. "+
				"A positional id counts only the features without an id, so an explicit id equal to one of them collides. "+
				"Before giving any existing feature an id, give every existing feature the id it shipped with",
				strings.Join(names, ", "), id))
		}
	}
	if c.Strict && len(missing) > 0 {
		var lines []string
		for _, p := range missing {
			lines = append(lines, fmt.Sprintf("  %s: id=%q", p.where, p.id))
		}
		problems = append(problems, "/STRICT requires an id on every feature. Give each existing feature the id its LAST SHIPPED package has. "+
			"The ids below are what THIS script gives each feature, a diagnostic. They are the shipped ones only if the features have not changed "+
			"since the last release and it was built by msis 3, not msis-2.x (#91). "+
			"Read the ids from the shipped MSI or its WXS, never invent new ones, or upgrades hand the features' states to the wrong features:\n"+
			strings.Join(lines, "\n"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("feature ids (#87, decisions D28):\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}
