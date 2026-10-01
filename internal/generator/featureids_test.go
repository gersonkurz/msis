package generator

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// frozen returns features with every feature given, explicitly, the positional id it would get
// - what an author does before reorganizing a tree (#87, D28) - so tests of other /STRICT rules
// are not refused for missing ids.
func frozen(features []ir.Feature) []ir.Feature {
	n := 0
	var freeze func([]ir.Feature) []ir.Feature
	freeze = func(fs []ir.Feature) []ir.Feature {
		out := make([]ir.Feature, len(fs))
		for i, f := range fs {
			f.ID = fmt.Sprintf("FEATURE_%05d", n)
			n++
			f.SubFeatures = freeze(f.SubFeatures)
			out[i] = f
		}
		return out
	}
	return freeze(features)
}

// featureIDsOf generates features and returns each one's id by title.
func featureIDsOf(t *testing.T, strict bool, features ...ir.Feature) (map[string]string, error) {
	t.Helper()
	ctx := NewContext(&ir.Setup{Features: features}, variables.New(), t.TempDir())
	ctx.Strict = strict
	if _, err := ctx.Generate(); err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, id := range ctx.featureIDs {
		ids[ctx.featureNames[id]] = id
	}
	return ids, nil
}

func feat(name, id string, children ...ir.Feature) ir.Feature {
	return ir.Feature{Name: name, ID: id, Enabled: true, Allowed: true, SubFeatures: children}
}

// TestFeaturesWithoutIDsKeepTodaysNumbers: a package with no ids gets exactly what it always got,
// FEATURE_%05d from zero, in preorder - so no existing installer changes (#87, D28).
func TestFeaturesWithoutIDsKeepTodaysNumbers(t *testing.T) {
	ids, err := featureIDsOf(t, false, feat("A", "", feat("A1", ""), feat("A2", "")), feat("B", ""))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "FEATURE_00000", "A1": "FEATURE_00001", "A2": "FEATURE_00002", "B": "FEATURE_00003"}
	for name, id := range want {
		if ids[name] != id {
			t.Errorf("%s = %s, want %s", name, ids[name], id)
		}
	}
}

// TestAFeatureInsertedWithAnIDMovesNoOtherNumber: the property #87 is about. Inserted first, in
// the middle, as a sub-feature, and as a whole subtree with ids, the existing features keep
// their numbers.
func TestAFeatureInsertedWithAnIDMovesNoOtherNumber(t *testing.T) {
	before, err := featureIDsOf(t, false, feat("Main", "", feat("Docs", "")), feat("Debug", ""))
	if err != nil {
		t.Fatal(err)
	}
	for name, features := range map[string][]ir.Feature{
		"first":       {feat("New", "NewTool"), feat("Main", "", feat("Docs", "")), feat("Debug", "")},
		"middle":      {feat("Main", "", feat("Docs", "")), feat("New", "NewTool"), feat("Debug", "")},
		"sub-feature": {feat("Main", "", feat("New", "NewTool"), feat("Docs", "")), feat("Debug", "")},
		"subtree":     {feat("New", "NewTool", feat("Part", "NewPart")), feat("Main", "", feat("Docs", "")), feat("Debug", "")},
	} {
		after, err := featureIDsOf(t, false, features...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for title, id := range before {
			if after[title] != id {
				t.Errorf("%s: %s moved from %s to %s", name, title, id, after[title])
			}
		}
		if after["New"] != "NewTool" {
			t.Errorf("%s: New is %s, want its explicit id", name, after["New"])
		}
	}
}

// TestAnIDLessChildOfANewFeatureStillTakesANumber pins the rule the documentation states: a
// new feature's sub-features need ids too, or they advance the counter and move what follows.
func TestAnIDLessChildOfANewFeatureStillTakesANumber(t *testing.T) {
	ids, err := featureIDsOf(t, false, feat("New", "NewTool", feat("Part", "")), feat("Main", ""))
	if err != nil {
		t.Fatal(err)
	}
	if ids["Part"] != "FEATURE_00000" || ids["Main"] != "FEATURE_00001" {
		t.Errorf("Part %s, Main %s; want FEATURE_00000 and FEATURE_00001", ids["Part"], ids["Main"])
	}
}

// TestFeatureIDCollisionsFailTheBuild: two explicit ids alike, and the partial freeze the
// documentation warns about - giving only B its shipped id hands C the same number.
func TestFeatureIDCollisionsFailTheBuild(t *testing.T) {
	for name, features := range map[string][]ir.Feature{
		"two explicit":   {feat("A", "Same"), feat("B", "Same")},
		"partial freeze": {feat("A", ""), feat("B", "FEATURE_00001"), feat("C", "")},
		"nested":         {feat("A", "Same", feat("A1", "Same"))},
	} {
		_, err := featureIDsOf(t, false, features...)
		if err == nil || !strings.Contains(err.Error(), "all have the id") {
			t.Errorf("%s: want a collision error, got %v", name, err)
		}
	}
	_, err := featureIDsOf(t, false, feat("A", ""), feat("B", "FEATURE_00001"), feat("C", ""))
	if err == nil || !strings.Contains(err.Error(), `"B" (explicit)`) || !strings.Contains(err.Error(), `"C" (positional)`) {
		t.Errorf("the collision must name both features and say which is which: %v", err)
	}
}

// TestInvalidFeatureIDsFailTheBuild covers every rule an explicit id answers to.
func TestInvalidFeatureIDsFailTheBuild(t *testing.T) {
	ok38 := "A" + strings.Repeat("b", 37)
	if _, err := featureIDsOf(t, false, feat("A", ok38), feat("B", "_x.y_9")); err != nil {
		t.Errorf("valid ids refused: %v", err)
	}
	for id, want := range map[string]string{
		"{{FEATURE}}":         "written literally",
		"1abc":                "not an identifier",
		"a-b":                 "not an identifier",
		ok38 + "c":            "39 characters",
		"ALL":                 "ADDLOCAL=ALL",
		"all":                 "ADDLOCAL=ALL",
		packageItemsFeatureID: "reserved",
		"VCRedist":            "reserved",
	} {
		_, err := featureIDsOf(t, false, feat("A", id))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("id %q: want an error saying %q, got %v", id, want, err)
		}
	}
}

// TestFeaturesNestedTooDeepFailTheBuild: the Feature table allows 16 levels.
func TestFeaturesNestedTooDeepFailTheBuild(t *testing.T) {
	deep := feat("L17", "")
	for i := 16; i >= 1; i-- {
		deep = feat(fmt.Sprintf("L%d", i), "", deep)
	}
	if _, err := featureIDsOf(t, false, deep); err == nil || !strings.Contains(err.Error(), "nested 17 deep") {
		t.Errorf("want a nesting error, got %v", err)
	}
	if _, err := featureIDsOf(t, false, deep.SubFeatures[0]); err != nil {
		t.Errorf("16 levels refused: %v", err)
	}
}

// TestStrictRequiresAnIDOnEveryFeature: recursively, disabled ones included, and the error gives
// each feature's current id to freeze - with the warning not to invent new ones.
func TestStrictRequiresAnIDOnEveryFeature(t *testing.T) {
	off := feat("Off", "")
	off.Enabled = false
	_, err := featureIDsOf(t, true, feat("Main", "MainId", feat("Child", "")), off)
	if err == nil {
		t.Fatal("/STRICT built features without ids")
	}
	for _, want := range []string{`Main > Child: id="FEATURE_00000"`, `Off: id="FEATURE_00001"`, "LAST SHIPPED", "#91"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the /STRICT error lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `Main: id=`) {
		t.Errorf("a feature with an id was listed as missing one:\n%v", err)
	}
	if _, err := featureIDsOf(t, true, frozen([]ir.Feature{feat("Main", "", feat("Child", "")), off})...); err != nil {
		t.Errorf("/STRICT refused fully identified features: %v", err)
	}
}

// TestAnExplicitIDReachesEveryConsumer: the Feature element, its components, and the D27
// guard's reference from a top-level feature all use the explicit id; nothing still names the
// positional one.
func TestAnExplicitIDReachesEveryConsumer(t *testing.T) {
	out, _, err := generateGuardedWithIDs(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.FeatureXML, "<Feature Id='MainId' Title='Main'") {
		t.Errorf("no Feature with the explicit id:\n%s", out.FeatureXML)
	}
	if strings.Contains(out.FeatureXML, "FEATURE_00000") {
		t.Errorf("the positional id is still emitted:\n%s", out.FeatureXML)
	}
	main := out.FeatureXML[strings.Index(out.FeatureXML, "<Feature Id='MainId'"):]
	main = main[:strings.Index(main, "</Feature>")]
	if strings.Count(main, "<ComponentRef ") < 2 {
		t.Errorf("Main should reference its file's component and the guard's:\n%s", main)
	}
}

func generateGuardedWithIDs(t *testing.T) (*GeneratedOutput, *Context, error) {
	t.Helper()
	work := t.TempDir()
	writeTree(t, work, `main.txt`)
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", ID: "MainId", Enabled: true, Allowed: true,
		Items: []ir.Item{ir.Files{Source: "main.txt", Target: "[INSTALLDIR]"}}}}}
	vars := variables.New()
	vars["PRODUCT_VERSION"] = "1.0.0"
	ctx := NewContext(setup, vars, work)
	out, err := ctx.Generate()
	return out, ctx, err
}
