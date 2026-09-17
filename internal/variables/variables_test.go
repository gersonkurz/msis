package variables

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
)

func TestCheckInstallerHookUsage(t *testing.T) {
	has := func(ws []string, substr string) bool {
		for _, w := range ws {
			if strings.Contains(w, substr) {
				return true
			}
		}
		return false
	}
	const danger = "recursively deletes"
	const foldersNoEffect = "REMOVE_FOLDERS_ON_UNINSTALL has no effect unless USE_INSTALLER_HOOKS"
	const retainNoEffect = "RETAIN_FILES_ON_UNINSTALL has no effect"

	t.Run("nothing set", func(t *testing.T) {
		w := Dictionary{}.CheckInstallerHookUsage()
		if len(w) != 0 {
			t.Errorf("expected no warnings, got %v", w)
		}
	})

	t.Run("folders+hooks: danger only", func(t *testing.T) {
		w := Dictionary{"REMOVE_FOLDERS_ON_UNINSTALL": "True", "USE_INSTALLER_HOOKS": "True"}.CheckInstallerHookUsage()
		if !has(w, danger) || has(w, foldersNoEffect) {
			t.Errorf("expected danger warning only, got %v", w)
		}
	})

	t.Run("folders without hooks: danger + no-effect", func(t *testing.T) {
		w := Dictionary{"REMOVE_FOLDERS_ON_UNINSTALL": "True"}.CheckInstallerHookUsage()
		if !has(w, danger) || !has(w, foldersNoEffect) {
			t.Errorf("expected danger + folders-no-effect, got %v", w)
		}
	})

	t.Run("retain without active cleanup: retain no-effect", func(t *testing.T) {
		w := Dictionary{"RETAIN_FILES_ON_UNINSTALL": "[APPDATADIR]DATABASE\\proakt.db"}.CheckInstallerHookUsage()
		if !has(w, retainNoEffect) {
			t.Errorf("expected retain-no-effect, got %v", w)
		}
	})

	t.Run("retain with active cleanup: no retain warning", func(t *testing.T) {
		w := Dictionary{
			"REMOVE_FOLDERS_ON_UNINSTALL": "True",
			"USE_INSTALLER_HOOKS":         "True",
			"RETAIN_FILES_ON_UNINSTALL":   "[APPDATADIR]DATABASE\\proakt.db",
		}.CheckInstallerHookUsage()
		if has(w, retainNoEffect) {
			t.Errorf("did not expect retain-no-effect when cleanup active, got %v", w)
		}
	})
}

func TestHookDllDir(t *testing.T) {
	cases := map[string]string{"x86": "x86", "X86": "x86", "x64": "x64", "arm64": "arm64", "ARM64": "arm64", "": "x64"}
	for plat, want := range cases {
		if got := (Dictionary{"PLATFORM": plat}).HookDllDir(); got != want {
			t.Errorf("HookDllDir(PLATFORM=%q) = %q, want %q", plat, got, want)
		}
	}
}

func TestRegistryTreeActive(t *testing.T) {
	active := []string{`HKLM\Software\X`, `HKEY_CLASSES_ROOT\Y`, `  HKCU\Z  `, "anything-nonempty"}
	for _, v := range active {
		if !(Dictionary{"REMOVE_REGISTRY_TREE": v}).RegistryTreeActive() {
			t.Errorf("expected active for %q", v)
		}
	}
	inactive := []string{"", "   ", "False", "false", "No", "OFF", "off", "0"}
	for _, v := range inactive {
		if (Dictionary{"REMOVE_REGISTRY_TREE": v}).RegistryTreeActive() {
			t.Errorf("expected inactive for %q", v)
		}
	}
	if (Dictionary{}).RegistryTreeActive() {
		t.Error("missing REMOVE_REGISTRY_TREE should be inactive")
	}
}

func TestCheckInstallerHookUsageRegistry(t *testing.T) {
	has := func(ws []string, substr string) bool {
		for _, w := range ws {
			if strings.Contains(w, substr) {
				return true
			}
		}
		return false
	}
	const danger = "REMOVE_REGISTRY_TREE recursively deletes"
	const noEffect = "REMOVE_REGISTRY_TREE has no effect"

	t.Run("active + hooks: danger only", func(t *testing.T) {
		w := Dictionary{"REMOVE_REGISTRY_TREE": `HKLM\Software\X`, "USE_INSTALLER_HOOKS": "True"}.CheckInstallerHookUsage()
		if !has(w, danger) || has(w, noEffect) {
			t.Errorf("expected registry danger only, got %v", w)
		}
	})
	t.Run("active without hooks: danger + no-effect", func(t *testing.T) {
		w := Dictionary{"REMOVE_REGISTRY_TREE": `HKLM\Software\X`}.CheckInstallerHookUsage()
		if !has(w, danger) || !has(w, noEffect) {
			t.Errorf("expected registry danger + no-effect, got %v", w)
		}
	})
	t.Run("false-value: no registry warnings", func(t *testing.T) {
		w := Dictionary{"REMOVE_REGISTRY_TREE": "False", "USE_INSTALLER_HOOKS": "True"}.CheckInstallerHookUsage()
		if has(w, danger) || has(w, noEffect) {
			t.Errorf("false-value REMOVE_REGISTRY_TREE must not warn, got %v", w)
		}
	})
}

func TestNewDictionaryHasDefaults(t *testing.T) {
	d := New()

	if d.Get("PLATFORM") != "x64" {
		t.Errorf("expected PLATFORM=x64, got %s", d.Get("PLATFORM"))
	}

	if d.Get("ADD_TO_PATH") != "False" {
		t.Errorf("expected ADD_TO_PATH=False, got %s", d.Get("ADD_TO_PATH"))
	}
}

func TestLoadFromSetup(t *testing.T) {
	d := New()

	setup := &ir.Setup{
		Sets: []ir.Set{
			{Name: "PRODUCT_NAME", Value: "Test Product"},
			{Name: "PRODUCT_VERSION", Value: "1.0.0"},
			{Name: "PLATFORM", Value: "x86"}, // Override default
		},
	}

	d.LoadFromSetup(setup)

	if d.Get("PRODUCT_NAME") != "Test Product" {
		t.Errorf("expected 'Test Product', got %s", d.Get("PRODUCT_NAME"))
	}

	if d.Get("PLATFORM") != "x86" {
		t.Errorf("expected PLATFORM=x86 (overridden), got %s", d.Get("PLATFORM"))
	}
}

func TestResolve(t *testing.T) {
	d := New()
	d.Set("PRODUCT_NAME", "Test Product")
	d.Set("PRODUCT_VERSION", "1.0.0")

	result, err := d.Resolve("{{PRODUCT_NAME}} v{{PRODUCT_VERSION}}")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expected := "Test Product v1.0.0"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestResolveAll(t *testing.T) {
	d := New()
	d.Set("PRODUCT_NAME", "Test Product")
	d.Set("PRODUCT_VERSION", "1.0.0")
	d.Set("FULL_NAME", "{{PRODUCT_NAME}} v{{PRODUCT_VERSION}}")

	err := d.ResolveAll()
	if err != nil {
		t.Fatalf("ResolveAll failed: %v", err)
	}

	expected := "Test Product v1.0.0"
	if d.Get("FULL_NAME") != expected {
		t.Errorf("expected %q, got %q", expected, d.Get("FULL_NAME"))
	}
}

func TestGetBool(t *testing.T) {
	d := New()

	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"True", "True", true},
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"Yes", "Yes", true},
		{"yes", "yes", true},
		{"On", "On", true},
		{"1", "1", true},
		{"False", "False", false},
		{"false", "false", false},
		{"No", "No", false},
		{"no", "no", false},
		{"Off", "Off", false},
		{"0", "0", false},
		{"empty", "", false},
		{"invalid", "invalid", false},
		// Mixed case (Codex review: must be case-insensitive)
		{"tRuE", "tRuE", true},
		{"yEs", "yEs", true},
		{"oN", "oN", true},
		{"FaLsE", "FaLsE", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d.Set("TEST_VAR", tt.value)
			result := d.GetBool("TEST_VAR")
			if result != tt.expected {
				t.Errorf("GetBool(%q) = %v, want %v", tt.value, result, tt.expected)
			}
		})
	}

	// Test missing variable
	if d.GetBool("NONEXISTENT") != false {
		t.Error("expected false for missing variable")
	}
}

func TestConvenienceMethods(t *testing.T) {
	d := New()
	d.Set("PRODUCT_NAME", "Test Product")
	d.Set("PRODUCT_VERSION", "1.0.0")
	d.Set("UPGRADE_CODE", "12345678-1234-1234-1234-123456789012")
	d.Set("MANUFACTURER", "Test Corp")
	d.Set("INSTALLDIR", "TestApp")
	d.Set("BUILD_TARGET", "test.msi")

	if d.ProductName() != "Test Product" {
		t.Errorf("expected 'Test Product', got %s", d.ProductName())
	}

	if d.ProductVersion() != "1.0.0" {
		t.Errorf("expected '1.0.0', got %s", d.ProductVersion())
	}

	if d.UpgradeCode() != "12345678-1234-1234-1234-123456789012" {
		t.Errorf("unexpected upgrade code")
	}

	if d.Manufacturer() != "Test Corp" {
		t.Errorf("expected 'Test Corp', got %s", d.Manufacturer())
	}

	if d.InstallDir() != "TestApp" {
		t.Errorf("expected 'TestApp', got %s", d.InstallDir())
	}

	if d.BuildTarget() != "test.msi" {
		t.Errorf("expected 'test.msi', got %s", d.BuildTarget())
	}
}

func TestContainsTemplate(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"{{VAR}}", true},
		{"Hello {{NAME}}", true},
		{"No template", false},
		{"{single brace}", false},
		{"", false},
		{"{", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := containsTemplate(tt.input)
			if result != tt.expected {
				t.Errorf("containsTemplate(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCheckDeprecated(t *testing.T) {
	// Test with no deprecated variables
	d := New()
	warnings := d.CheckDeprecated()
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for new dictionary, got %d", len(warnings))
	}

	// Test with INCLUDE_VCREDIST set
	d.Set("INCLUDE_VCREDIST", "True")
	warnings = d.CheckDeprecated()
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for INCLUDE_VCREDIST, got %d", len(warnings))
	}
	if len(warnings) > 0 && !contains(warnings[0], "INCLUDE_VCREDIST") {
		t.Errorf("expected warning about INCLUDE_VCREDIST, got: %s", warnings[0])
	}
	if len(warnings) > 0 && !contains(warnings[0], "<requires") {
		t.Errorf("expected migration hint to <requires>, got: %s", warnings[0])
	}

	// Test with multiple deprecated variables
	d.Set("INCLUDE_VC140", "True")
	warnings = d.CheckDeprecated()
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(warnings))
	}
	// Check that VC140 warning mentions backward compatibility
	foundVC140Warning := false
	for _, w := range warnings {
		if contains(w, "INCLUDE_VC140") && contains(w, "backward-compatible") {
			foundVC140Warning = true
			break
		}
	}
	if !foundVC140Warning {
		t.Error("expected VC140 warning to mention backward compatibility")
	}

	// Test with deprecated variable set to False (should not warn)
	d2 := New()
	d2.Set("INCLUDE_VCREDIST", "False")
	warnings = d2.CheckDeprecated()
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for deprecated variable set to False, got %d", len(warnings))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestResolveAllNestedIsDeterministic guards issue #7. Go randomises map order, and
// a single resolution pass gave BUILD_TARGET the still-unresolved text of
// PRODUCT_NAME whenever it was visited first — about one run in five, producing a
// file literally named "My Product - {{PRODUCT_VERSION}}.msi". Repeated because a
// single run passed most of the time even when broken.
func TestResolveAllNestedIsDeterministic(t *testing.T) {
	const want = "My Product - 1.2.3.msi"
	for i := 0; i < 500; i++ {
		d := Dictionary{
			"PRODUCT_VERSION": "1.2.3",
			"PRODUCT_NAME":    "My Product - {{PRODUCT_VERSION}}",
			"BUILD_TARGET":    "{{PRODUCT_NAME}}.msi",
		}
		if err := d.ResolveAll(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if d["BUILD_TARGET"] != want {
			t.Fatalf("run %d: BUILD_TARGET = %q, want %q", i, d["BUILD_TARGET"], want)
		}
	}
}

// TestResolveAllDeepChain: depth beyond the two levels issue #7 reported.
func TestResolveAllDeepChain(t *testing.T) {
	for i := 0; i < 200; i++ {
		d := Dictionary{
			"A": "{{B}}/a",
			"B": "{{C}}/b",
			"C": "{{D}}/c",
			"D": "root",
		}
		if err := d.ResolveAll(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if d["A"] != "root/c/b/a" {
			t.Fatalf("run %d: A = %q, want %q", i, d["A"], "root/c/b/a")
		}
	}
}

// TestResolveAllDoesNotEscape: the dictionary holds product names and file paths,
// not HTML. Escaping here turned "R&D" into "R&amp;D" in the output file name, and
// any fix that re-renders would have escalated it to "R&amp;amp;D".
func TestResolveAllDoesNotEscape(t *testing.T) {
	d := Dictionary{
		"MANUFACTURER": "R&D",
		"PRODUCT_NAME": "{{MANUFACTURER}} Tools",
		"BUILD_TARGET": "{{PRODUCT_NAME}}.msi",
	}
	if err := d.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	if d["BUILD_TARGET"] != "R&D Tools.msi" {
		t.Errorf("BUILD_TARGET = %q, want %q", d["BUILD_TARGET"], "R&D Tools.msi")
	}
}

// TestResolveDoesNotEscape: same for the single-string entry point, which callers
// use on <files source=> paths and env values.
func TestResolveDoesNotEscape(t *testing.T) {
	d := Dictionary{"COMPANY": "R&D"}
	got, err := d.Resolve("{{COMPANY}} <Support>")
	if err != nil {
		t.Fatal(err)
	}
	if got != "R&D <Support>" {
		t.Errorf("Resolve = %q, want %q", got, "R&D <Support>")
	}
}

// TestResolveAllReportsCycleByName: a cycle must name the variables involved, not
// merely fail or hang.
func TestResolveAllReportsCycleByName(t *testing.T) {
	d := Dictionary{
		"A": "{{B}}",
		"B": "{{A}}",
	}
	err := d.ResolveAll()
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	if !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "B") {
		t.Errorf("cycle error should name both variables, got: %v", err)
	}
}

// TestResolveAllSelfReferenceDoesNotGrow: "{{A}}{{A}}" doubles on every render, so
// a naive repeat-until-stable loop would allocate until it died. It must be
// reported as a cycle instead.
func TestResolveAllSelfReferenceDoesNotGrow(t *testing.T) {
	d := Dictionary{"A": "{{A}}{{A}}"}
	err := d.ResolveAll()
	if err == nil {
		t.Fatal("expected a cycle error for a self-reference")
	}
	if !strings.Contains(err.Error(), "A") {
		t.Errorf("error should name A, got: %v", err)
	}
	if len(d["A"]) > len("{{A}}{{A}}") {
		t.Errorf("value grew to %d bytes: %q", len(d["A"]), d["A"])
	}
}

// TestResolveAllUntakenBranchIsNotACycle: references inside a branch the engine
// never evaluates must not count as dependencies. A parse-tree scan would see B
// referencing A here and report a cycle that cannot happen at run time.
func TestResolveAllUntakenBranchIsNotACycle(t *testing.T) {
	d := Dictionary{
		"FLAG": "",
		"A":    "{{#if FLAG}}{{B}}{{else}}plain-a{{/if}}",
		"B":    "{{A}}-b",
	}
	if err := d.ResolveAll(); err != nil {
		t.Fatalf("untaken branch should not be a cycle: %v", err)
	}
	if d["A"] != "plain-a" {
		t.Errorf("A = %q, want %q", d["A"], "plain-a")
	}
	if d["B"] != "plain-a-b" {
		t.Errorf("B = %q, want %q", d["B"], "plain-a-b")
	}
}

// TestResolveAllLiteralMustacheIsNotReResolved: \{{X}} is Handlebars' escape for a
// literal mustache, so the resolved value legitimately CONTAINS "{{". Testing for
// "{{" to decide what still needs resolving would render it a second time and lose
// it; completion is tracked explicitly instead.
func TestResolveAllLiteralMustacheIsNotReResolved(t *testing.T) {
	d := Dictionary{
		"LITERAL": `\{{NOT_A_VAR}}`,
		"USER":    "{{LITERAL}} done",
	}
	if err := d.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	if d["LITERAL"] != "{{NOT_A_VAR}}" {
		t.Errorf("LITERAL = %q, want %q", d["LITERAL"], "{{NOT_A_VAR}}")
	}
	if d["USER"] != "{{NOT_A_VAR}} done" {
		t.Errorf("USER = %q, want %q", d["USER"], "{{NOT_A_VAR}} done")
	}
}

// TestResolveAllUnresolvedConditionIsNotSpeculated guards a defect found in review of
// issue #7. A sentinel renders as empty, so an unresolved {{#if FLAG}} takes the ELSE
// branch regardless of what FLAG will turn out to be. Harvesting dependencies from
// that speculative branch invents them — and here, where the else branch refers back
// to A, invented an outright false cycle "A -> B -> A". FLAG resolves to "yes", so the
// else branch is never taken and B is not a dependency of A at all.
func TestResolveAllUnresolvedConditionIsNotSpeculated(t *testing.T) {
	d := Dictionary{
		"A":       "{{#if FLAG}}plain-a{{else}}{{B}}{{/if}}",
		"B":       "{{A}}-b",
		"FLAG":    "{{ENABLED}}",
		"ENABLED": "yes",
	}
	if err := d.ResolveAll(); err != nil {
		t.Fatalf("speculative else branch must not create a dependency: %v", err)
	}
	if d["A"] != "plain-a" {
		t.Errorf("A = %q, want %q", d["A"], "plain-a")
	}
	if d["B"] != "plain-a-b" {
		t.Errorf("B = %q, want %q", d["B"], "plain-a-b")
	}
}

// TestResolveAllRealCycleThroughTakenBranch is the mirror image, and guards against
// over-correcting the above: the same shape, but FLAG resolves FALSY, so the else
// branch really is taken and A -> B -> A is a genuine cycle that must still be caught.
func TestResolveAllRealCycleThroughTakenBranch(t *testing.T) {
	d := Dictionary{
		"A":       "{{#if FLAG}}plain-a{{else}}{{B}}{{/if}}",
		"B":       "{{A}}-b",
		"FLAG":    "{{ENABLED}}",
		"ENABLED": "",
	}
	err := d.ResolveAll()
	if err == nil {
		t.Fatal("a cycle through the taken branch must still be reported")
	}
	if !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "B") {
		t.Errorf("cycle error should name both variables, got: %v", err)
	}
}
