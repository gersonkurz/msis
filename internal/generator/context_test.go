package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		target      string
		wantRootKey string
		wantSubPath string
	}{
		{"[INSTALLDIR]", "INSTALLDIR", ""},
		{"[INSTALLDIR]bin", "INSTALLDIR", "bin"},
		{"[INSTALLDIR]bin\\release", "INSTALLDIR", "bin\\release"},
		{"[APPDATADIR]config", "APPDATADIR", "config"},
		{"INSTALLDIR", "INSTALLDIR", ""},  // Bare root key = root with empty subpath
		{"APPDATADIR", "APPDATADIR", ""},  // Bare root key = root with empty subpath
		{"subdir", "INSTALLDIR", "subdir"}, // Non-root-key = subpath under INSTALLDIR
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rootKey, subPath := ParseTarget(tt.target)
			if rootKey != tt.wantRootKey {
				t.Errorf("rootKey = %q, want %q", rootKey, tt.wantRootKey)
			}
			if subPath != tt.wantSubPath {
				t.Errorf("subPath = %q, want %q", subPath, tt.wantSubPath)
			}
		})
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"path/with/slashes", "path_with_slashes"},
		{"path\\with\\backslashes", "path_with_backslashes"},
		{"spaces and symbols!@#", "spaces_and_symbols___"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizePath(tt.input)
			if got != tt.want {
				t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetOrCreateDirectory(t *testing.T) {
	setup := &ir.Setup{}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	// Create root
	root := ctx.GetOrCreateDirectory("INSTALLDIR", "", false)
	if root.CustomID != "INSTALLDIR" {
		t.Errorf("root.CustomID = %q, want %q", root.CustomID, "INSTALLDIR")
	}

	// Create nested path
	nested := ctx.GetOrCreateDirectory("INSTALLDIR", "bin\\release", false)
	if nested.Name != "release" {
		t.Errorf("nested.Name = %q, want %q", nested.Name, "release")
	}
	if nested.Parent == nil {
		t.Error("nested.Parent is nil")
	}
	if nested.Parent.Name != "bin" {
		t.Errorf("nested.Parent.Name = %q, want %q", nested.Parent.Name, "bin")
	}

	// Verify same path returns same directory
	nested2 := ctx.GetOrCreateDirectory("INSTALLDIR", "bin\\release", false)
	if nested2 != nested {
		t.Error("expected same directory for same path")
	}
}

func TestNextIDs(t *testing.T) {
	setup := &ir.Setup{}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	// Directory IDs
	id1 := ctx.NextDirectoryID()
	id2 := ctx.NextDirectoryID()
	if id1 == id2 {
		t.Error("directory IDs should be unique")
	}
	if id1 != "DIR_ID00000" {
		t.Errorf("first directory ID = %q, want DIR_ID00000", id1)
	}

	// Component IDs (path-based)
	cid1 := ctx.NextComponentID("test\\path")
	cid2 := ctx.NextComponentID("test\\path")
	if cid1 == cid2 {
		t.Error("component IDs should be unique even for same path")
	}

	// Environment IDs
	eid1 := ctx.NextEnvID()
	eid2 := ctx.NextEnvID()
	if eid1 == eid2 {
		t.Error("environment IDs should be unique")
	}
}

func TestProcessSetEnv(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main Feature",
				Enabled: true,
				Items: []ir.Item{
					ir.SetEnv{Name: "MY_VAR", Value: "my_value"},
					ir.SetEnv{Name: "PATH_VAR", Value: "[INSTALLDIR]bin"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check feature output contains the feature name
	if !strings.Contains(output.FeatureXML, "Main Feature") {
		t.Error("expected feature output to contain 'Main Feature'")
	}

	// Check directory output contains environment variable
	if !strings.Contains(output.DirectoryXML, "MY_VAR") {
		t.Error("expected directory output to contain 'MY_VAR'")
	}
	if !strings.Contains(output.DirectoryXML, "my_value") {
		t.Error("expected directory output to contain 'my_value'")
	}
	if !strings.Contains(output.DirectoryXML, "Environment") {
		t.Error("expected directory output to contain 'Environment' element")
	}
}

func TestProcessService(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Service Feature",
				Enabled: true,
				Items: []ir.Item{
					ir.Service{
						FileName:           "myservice.exe",
						ServiceName:        "MySvc",
						ServiceDisplayName: "My Service",
						Description:        "A test service",
						Start:              "auto",
					},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check service elements in output
	if !strings.Contains(output.DirectoryXML, "ServiceInstall") {
		t.Error("expected directory output to contain 'ServiceInstall'")
	}
	if !strings.Contains(output.DirectoryXML, "MySvc") {
		t.Error("expected directory output to contain service name 'MySvc'")
	}
	if !strings.Contains(output.DirectoryXML, "ServiceControl") {
		t.Error("expected directory output to contain 'ServiceControl'")
	}
}

func TestNestedFeatures(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Parent",
				Enabled: true,
				SubFeatures: []ir.Feature{
					{
						Name:    "Child1",
						Enabled: true,
						Items: []ir.Item{
							ir.SetEnv{Name: "CHILD1_VAR", Value: "value1"},
						},
					},
					{
						Name:    "Child2",
						Enabled: false,
						Allowed: true,
					},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check nested features in output
	if !strings.Contains(output.FeatureXML, "Parent") {
		t.Error("expected feature output to contain 'Parent'")
	}
	if !strings.Contains(output.FeatureXML, "Child1") {
		t.Error("expected feature output to contain 'Child1'")
	}
	if !strings.Contains(output.FeatureXML, "Child2") {
		t.Error("expected feature output to contain 'Child2'")
	}

	// Check feature level for disabled feature (msis-2.x uses 32767)
	if !strings.Contains(output.FeatureXML, "Level='32767'") {
		t.Error("expected disabled feature to have Level='32767'")
	}

	// Check feature IDs are unique generated IDs (FEATURE_XXXXX format)
	if !strings.Contains(output.FeatureXML, "FEATURE_") {
		t.Error("expected feature IDs to use FEATURE_XXXXX format")
	}
}

func TestFileEnumeration(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	testFile1 := filepath.Join(tmpDir, "file1.txt")
	testFile2 := filepath.Join(tmpDir, "file2.txt")
	subDir := filepath.Join(tmpDir, "subdir")

	if err := os.WriteFile(testFile1, []byte("test1"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(testFile2, []byte("test2"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	subFile := filepath.Join(subDir, "subfile.txt")
	if err := os.WriteFile(subFile, []byte("subtest"), 0644); err != nil {
		t.Fatalf("failed to create subfile: %v", err)
	}

	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Files Feature",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: tmpDir, Target: "[INSTALLDIR]dest"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check files in output
	if !strings.Contains(output.DirectoryXML, "file1.txt") {
		t.Error("expected directory output to contain 'file1.txt'")
	}
	if !strings.Contains(output.DirectoryXML, "file2.txt") {
		t.Error("expected directory output to contain 'file2.txt'")
	}
	if !strings.Contains(output.DirectoryXML, "subfile.txt") {
		t.Error("expected directory output to contain 'subfile.txt'")
	}
	if !strings.Contains(output.DirectoryXML, "subdir") {
		t.Error("expected directory output to contain 'subdir' directory")
	}
}

func TestExcludeFolders(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	keepFile := filepath.Join(tmpDir, "keep.txt")
	excludeDir := filepath.Join(tmpDir, "exclude_me")

	if err := os.WriteFile(keepFile, []byte("keep"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.Mkdir(excludeDir, 0755); err != nil {
		t.Fatalf("failed to create exclude dir: %v", err)
	}
	excludedFile := filepath.Join(excludeDir, "excluded.txt")
	if err := os.WriteFile(excludedFile, []byte("excluded"), 0644); err != nil {
		t.Fatalf("failed to create excluded file: %v", err)
	}

	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Files Feature",
				Enabled: true,
				Items: []ir.Item{
					ir.Exclude{Folder: excludeDir},
					ir.Files{Source: tmpDir, Target: "[INSTALLDIR]dest"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check excluded file is NOT in output
	if strings.Contains(output.DirectoryXML, "excluded.txt") {
		t.Error("expected directory output to NOT contain 'excluded.txt'")
	}
	// Check kept file is in output
	if !strings.Contains(output.DirectoryXML, "keep.txt") {
		t.Error("expected directory output to contain 'keep.txt'")
	}
}

func TestGenerateGUID(t *testing.T) {
	guid1 := GenerateGUID("path/to/file1")
	guid2 := GenerateGUID("path/to/file2")
	guid3 := GenerateGUID("path/to/file1") // Same as guid1

	// GUIDs for different paths should be different
	if guid1 == guid2 {
		t.Error("GUIDs for different paths should be different")
	}

	// GUIDs for same path should be same (deterministic)
	if guid1 != guid3 {
		t.Error("GUIDs for same path should be deterministic")
	}

	// GUID format check (8-4-4-4-12)
	parts := strings.Split(guid1, "-")
	if len(parts) != 5 {
		t.Errorf("GUID should have 5 parts, got %d", len(parts))
	}
	expectedLengths := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != expectedLengths[i] {
			t.Errorf("GUID part %d has length %d, expected %d", i, len(part), expectedLengths[i])
		}
	}
}

func TestRootDirectoryNameFromVariable(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Test",
				Enabled: true,
				Items: []ir.Item{
					ir.SetEnv{Name: "TEST_VAR", Value: "value"},
				},
			},
		},
	}
	vars := variables.New()
	vars["INSTALLDIR"] = "MyAppFolder"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check root directory has Name from variable
	if !strings.Contains(output.DirectoryXML, "Id='INSTALLDIR' Name='MyAppFolder'") {
		t.Errorf("expected root directory to have Name='MyAppFolder', got:\n%s", output.DirectoryXML)
	}
}

func TestRelativeExcludePath(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test structure
	keepFile := filepath.Join(tmpDir, "keep.txt")
	excludeDir := filepath.Join(tmpDir, "subdir", "exclude_me")

	if err := os.WriteFile(keepFile, []byte("keep"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.MkdirAll(excludeDir, 0755); err != nil {
		t.Fatalf("failed to create exclude dir: %v", err)
	}
	excludedFile := filepath.Join(excludeDir, "excluded.txt")
	if err := os.WriteFile(excludedFile, []byte("excluded"), 0644); err != nil {
		t.Fatalf("failed to create excluded file: %v", err)
	}

	// Use RELATIVE exclude path (matches msis-2.x behavior)
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Files Feature",
				Enabled: true,
				Items: []ir.Item{
					ir.Exclude{Folder: "subdir\\exclude_me"}, // Relative path
					ir.Files{Source: tmpDir, Target: "[INSTALLDIR]dest"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check excluded file is NOT in output (relative exclude should work)
	if strings.Contains(output.DirectoryXML, "excluded.txt") {
		t.Error("expected directory output to NOT contain 'excluded.txt' (relative exclude should work)")
	}
	// Check kept file is in output
	if !strings.Contains(output.DirectoryXML, "keep.txt") {
		t.Error("expected directory output to contain 'keep.txt'")
	}
}

func TestFeatureIDsAreUnique(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Same Name",
				Enabled: true,
			},
			{
				Name:    "Same Name", // Duplicate display name
				Enabled: true,
			},
			{
				Name:    "", // Empty name
				Enabled: true,
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check that we have unique FEATURE_XXXXX IDs
	if !strings.Contains(output.FeatureXML, "FEATURE_00000") {
		t.Error("expected first feature ID FEATURE_00000")
	}
	if !strings.Contains(output.FeatureXML, "FEATURE_00001") {
		t.Error("expected second feature ID FEATURE_00001")
	}
	if !strings.Contains(output.FeatureXML, "FEATURE_00002") {
		t.Error("expected third feature ID FEATURE_00002")
	}
}

func TestDuplicateFeatureNamesGetSeparateComponents(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create distinct files for each feature
	file1 := filepath.Join(tmpDir, "feature1_file.txt")
	file2 := filepath.Join(tmpDir, "feature2_file.txt")
	if err := os.WriteFile(file1, []byte("f1"), 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("f2"), 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}

	// Two features with SAME display name but DIFFERENT files
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Duplicate Name",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: file1, Target: "[INSTALLDIR]dir1"},
				},
			},
			{
				Name:    "Duplicate Name", // Same name!
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: file2, Target: "[INSTALLDIR]dir2"},
				},
			},
		},
	}
	vars := variables.New()
	// Disable file permissions for this test - we're testing feature component separation
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Parse the feature XML to verify each feature has its own components
	// First feature (FEATURE_00000) should have feature1_file.txt
	// Second feature (FEATURE_00001) should have feature2_file.txt

	// Find feature sections
	feature0Start := strings.Index(output.FeatureXML, "FEATURE_00000")
	feature1Start := strings.Index(output.FeatureXML, "FEATURE_00001")

	if feature0Start == -1 || feature1Start == -1 {
		t.Fatalf("expected both features in output:\n%s", output.FeatureXML)
	}

	// Get the section for feature 0 (from FEATURE_00000 to FEATURE_00001)
	feature0Section := output.FeatureXML[feature0Start:feature1Start]

	// Get the section for feature 1 (from FEATURE_00001 to end)
	feature1Section := output.FeatureXML[feature1Start:]

	// Feature 0 should have a ComponentRef (for file1)
	if !strings.Contains(feature0Section, "ComponentRef") {
		t.Error("FEATURE_00000 should have ComponentRef for its file")
	}

	// Feature 1 should have a ComponentRef (for file2)
	if !strings.Contains(feature1Section, "ComponentRef") {
		t.Error("FEATURE_00001 should have ComponentRef for its file")
	}

	// Verify the components are different by checking that we have 2 ComponentRefs total
	componentRefCount := strings.Count(output.FeatureXML, "ComponentRef")
	if componentRefCount != 2 {
		t.Errorf("expected 2 ComponentRefs (one per feature), got %d", componentRefCount)
	}
}

func TestDirectoryXMLSorted(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Test",
				Enabled: true,
				Items: []ir.Item{
					ir.SetEnv{Name: "VAR_Z", Value: "z"},
					ir.SetEnv{Name: "VAR_A", Value: "a"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Output should be deterministic regardless of input order
	// Run twice to verify
	ctx2 := NewContext(setup, vars, ".")
	output2, err := ctx2.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if output.DirectoryXML != output2.DirectoryXML {
		t.Error("directory XML output should be deterministic")
	}
	if output.FeatureXML != output2.FeatureXML {
		t.Error("feature XML output should be deterministic")
	}
}

func TestGenerateShortName(t *testing.T) {
	tests := []struct {
		fileName   string
		occurrence int
		want       string
	}{
		{"ng1_watchdog.processes.xml", 2, "NG1_WA_2.XML"},
		{"ng1_watchdog.processes.xml", 3, "NG1_WA_3.XML"},
		{"readme.txt", 2, "README_2.TXT"},
		{"LONGFILENAME.doc", 2, "LONGFI_2.DOC"},
		{"file", 2, "FILE_2"},                     // No extension
		{"a.b.c.d", 2, "ABC_2.D"},                 // Multiple dots - ext is last part
		{"file.toolongext", 2, "FILE_2.TOO"},     // Extension truncated to 3
		{"!!!special!!!.txt", 2, "SPECIA_2.TXT"}, // Special chars stripped, keeps SPECIA
		{"config_backup.xml", 2, "CONFIG_2.XML"}, // Underscore preserved
	}

	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			got := generateShortName(tt.fileName, tt.occurrence)
			if got != tt.want {
				t.Errorf("generateShortName(%q, %d) = %q, want %q", tt.fileName, tt.occurrence, got, tt.want)
			}
			// Verify 8.3 format: base max 8 chars, ext max 3 chars
			parts := strings.Split(got, ".")
			if len(parts[0]) > 8 {
				t.Errorf("base name %q exceeds 8 characters", parts[0])
			}
			if len(parts) > 1 && len(parts[1]) > 3 {
				t.Errorf("extension %q exceeds 3 characters", parts[1])
			}
		})
	}
}

func TestDuplicateTargetFilesGetShortName(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create two source directories with the same filename
	baseDir := filepath.Join(tmpDir, "base")
	overrideDir := filepath.Join(tmpDir, "override")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base dir: %v", err)
	}
	if err := os.MkdirAll(overrideDir, 0755); err != nil {
		t.Fatalf("failed to create override dir: %v", err)
	}

	// Same filename in both directories
	baseFile := filepath.Join(baseDir, "config.xml")
	overrideFile := filepath.Join(overrideDir, "config.xml")
	if err := os.WriteFile(baseFile, []byte("base"), 0644); err != nil {
		t.Fatalf("failed to create base file: %v", err)
	}
	if err := os.WriteFile(overrideFile, []byte("override"), 0644); err != nil {
		t.Fatalf("failed to create override file: %v", err)
	}

	// Two features targeting the SAME directory with files of the SAME name
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Base Feature",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: baseDir, Target: "[INSTALLDIR]config"},
				},
			},
			{
				Name:    "Override Feature",
				Enabled: false,
				Allowed: true,
				Items: []ir.Item{
					ir.Files{Source: overrideDir, Target: "[INSTALLDIR]config"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// First file should NOT have ShortName
	// Second file SHOULD have ShortName to avoid collision
	if strings.Contains(output.DirectoryXML, "ShortName='CONFIG_1") {
		t.Error("first occurrence should NOT have ShortName")
	}
	if !strings.Contains(output.DirectoryXML, "ShortName='CONFIG_2.XML'") {
		t.Errorf("expected second occurrence to have ShortName='CONFIG_2.XML', got:\n%s", output.DirectoryXML)
	}
}

func TestProcessShortcut(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Desktop Shortcuts",
				Enabled: true,
				Items: []ir.Item{
					ir.Shortcut{
						Name:        "MyApp",
						Target:      "DESKTOP",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "Launch MyApp",
					},
				},
			},
			{
				Name:    "Start Menu Shortcuts",
				Enabled: true,
				Items: []ir.Item{
					ir.Shortcut{
						Name:        "MyApp Start",
						Target:      "STARTMENU",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "Launch MyApp from Start Menu",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["PRODUCT_NAME"] = "TestProduct"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify desktop shortcuts
	if len(ctx.DesktopShortcuts) != 1 {
		t.Errorf("expected 1 desktop shortcut, got %d", len(ctx.DesktopShortcuts))
	}

	// Verify start menu shortcuts
	if len(ctx.StartMenuShortcuts) != 1 {
		t.Errorf("expected 1 start menu shortcut, got %d", len(ctx.StartMenuShortcuts))
	}

	// Verify desktop XML contains shortcut element
	if !strings.Contains(output.DesktopXML, "<Shortcut Id='SHORTCUT_ID") {
		t.Error("DesktopXML should contain <Shortcut> element")
	}
	if !strings.Contains(output.DesktopXML, "Name='MyApp'") {
		t.Error("DesktopXML should contain shortcut name")
	}
	if !strings.Contains(output.DesktopXML, "WorkingDirectory='INSTALLDIR'") {
		t.Error("DesktopXML should contain WorkingDirectory")
	}

	// Verify registry keypath for shortcut
	if !strings.Contains(output.DesktopXML, "<RegistryValue Root='HKCU'") {
		t.Error("DesktopXML should contain RegistryValue for keypath")
	}
	if !strings.Contains(output.DesktopXML, "KeyPath='yes'") {
		t.Error("DesktopXML should have KeyPath on RegistryValue")
	}

	// Verify start menu XML
	if !strings.Contains(output.StartMenuXML, "<Shortcut Id='SHORTCUT_ID") {
		t.Error("StartMenuXML should contain <Shortcut> element")
	}
}

func TestShortcutWithIcon(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Desktop Shortcuts",
				Enabled: true,
				Items: []ir.Item{
					ir.Shortcut{
						Name:        "MyApp",
						Target:      "DESKTOP",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "Launch MyApp",
						Icon:        "[INSTALLDIR]myapp.ico",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["PRODUCT_NAME"] = "TestProduct"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify icon element is present
	if !strings.Contains(output.DesktopXML, "<Icon Id='Icon_SHORTCUT_ID") {
		t.Error("DesktopXML should contain <Icon> element when icon is specified")
	}
	if !strings.Contains(output.DesktopXML, "SourceFile='[INSTALLDIR]myapp.ico'") {
		t.Error("DesktopXML should contain icon source file")
	}
}

func TestShortcutInvalidTarget(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Bad Shortcuts",
				Enabled: true,
				Items: []ir.Item{
					ir.Shortcut{
						Name:        "MyApp",
						Target:      "INVALID",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "This should fail",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["PRODUCT_NAME"] = "TestProduct"
	ctx := NewContext(setup, vars, ".")

	_, err := ctx.Generate()
	if err == nil {
		t.Error("expected error for invalid shortcut target")
	}
	if !strings.Contains(err.Error(), "invalid shortcut target") {
		t.Errorf("expected 'invalid shortcut target' error, got: %v", err)
	}
}

func TestShortcutDuplicateNames(t *testing.T) {
	// Same shortcut name for both Desktop and StartMenu should not collide
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Shortcuts",
				Enabled: true,
				Items: []ir.Item{
					ir.Shortcut{
						Name:        "MyApp",
						Target:      "DESKTOP",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "Desktop shortcut",
					},
					ir.Shortcut{
						Name:        "MyApp",
						Target:      "STARTMENU",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "Start menu shortcut",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["PRODUCT_NAME"] = "TestProduct"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Both shortcuts should have different component IDs
	if len(ctx.DesktopShortcuts) != 1 {
		t.Errorf("expected 1 desktop shortcut, got %d", len(ctx.DesktopShortcuts))
	}
	if len(ctx.StartMenuShortcuts) != 1 {
		t.Errorf("expected 1 start menu shortcut, got %d", len(ctx.StartMenuShortcuts))
	}

	// Registry value names should be different (use component ID, not shortcut name)
	desktopCompID := ctx.DesktopShortcuts[0].ID
	startMenuCompID := ctx.StartMenuShortcuts[0].ID

	if desktopCompID == startMenuCompID {
		t.Error("desktop and start menu shortcuts should have different component IDs")
	}

	// Verify registry values use component IDs
	if !strings.Contains(output.DesktopXML, fmt.Sprintf("Name='%s'", desktopCompID)) {
		t.Error("Desktop registry value should use component ID as name")
	}
	if !strings.Contains(output.StartMenuXML, fmt.Sprintf("Name='%s'", startMenuCompID)) {
		t.Error("StartMenu registry value should use component ID as name")
	}
}

func TestShortcutFeatureComponentRef(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Desktop Shortcuts",
				Enabled: true,
				Items: []ir.Item{
					ir.Shortcut{
						Name:        "MyApp",
						Target:      "DESKTOP",
						File:        "[INSTALLDIR]myapp.exe",
						Description: "Launch MyApp",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["PRODUCT_NAME"] = "TestProduct"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify feature XML contains component ref for shortcut
	if !strings.Contains(output.FeatureXML, "<ComponentRef Id='") {
		t.Error("FeatureXML should contain ComponentRef for shortcut")
	}

	// Verify the shortcut component ID is referenced
	if len(ctx.DesktopShortcuts) > 0 {
		compID := ctx.DesktopShortcuts[0].ID
		if !strings.Contains(output.FeatureXML, fmt.Sprintf("<ComponentRef Id='%s'/>", compID)) {
			t.Errorf("FeatureXML should reference shortcut component %s", compID)
		}
	}
}

func TestAddToPath(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
	}
	vars := variables.New()
	vars["ADD_TO_PATH"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify PATH environment component is added
	if !strings.Contains(output.DirectoryXML, "<Environment Id='ENV_ID") {
		t.Error("DirectoryXML should contain Environment element for PATH")
	}
	if !strings.Contains(output.DirectoryXML, "Name='PATH'") {
		t.Error("DirectoryXML should contain PATH environment name")
	}
	if !strings.Contains(output.DirectoryXML, "Value='[INSTALLDIR]'") {
		t.Error("DirectoryXML should set PATH value to [INSTALLDIR]")
	}
	if !strings.Contains(output.DirectoryXML, "Part='last'") {
		t.Error("DirectoryXML should append to PATH with Part='last'")
	}
	if !strings.Contains(output.DirectoryXML, "System='yes'") {
		t.Error("DirectoryXML should set system-level PATH")
	}
}

func TestAddToPathNotSet(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
	}
	vars := variables.New()
	// ADD_TO_PATH not set
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify no PATH environment component
	if strings.Contains(output.DirectoryXML, "Name='PATH'") {
		t.Error("DirectoryXML should not contain PATH environment when ADD_TO_PATH is not set")
	}
}

func TestFilePermissionsDefault(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file
	testDir := filepath.Join(tmpDir, "install")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: testDir, Target: "[INSTALLDIR]"},
				},
			},
		},
	}
	vars := variables.New()
	// Default: permissions enabled
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify CreateFolder with permissions is present
	if !strings.Contains(output.DirectoryXML, "<CreateFolder>") {
		t.Error("DirectoryXML should contain CreateFolder element")
	}
	if !strings.Contains(output.DirectoryXML, "<util:PermissionEx") {
		t.Error("DirectoryXML should contain util:PermissionEx element")
	}
	if !strings.Contains(output.DirectoryXML, "GenericAll='yes'") {
		t.Error("DirectoryXML should have GenericAll='yes' by default")
	}
}

func TestFilePermissionsDisabled(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testDir := filepath.Join(tmpDir, "install")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: testDir, Target: "[INSTALLDIR]"},
				},
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify no CreateFolder with permissions
	if strings.Contains(output.DirectoryXML, "<util:PermissionEx") {
		t.Error("DirectoryXML should not contain util:PermissionEx when disabled")
	}
}

func TestFilePermissionsFeatureRef(t *testing.T) {
	// Verify that permission components are referenced by features
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testDir := filepath.Join(tmpDir, "install")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: testDir, Target: "[INSTALLDIR]"},
				},
			},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify permission component exists in DirectoryXML
	if !strings.Contains(output.DirectoryXML, "<CreateFolder>") {
		t.Error("DirectoryXML should contain CreateFolder element")
	}

	// Extract permission component ID from DirectoryXML
	// Look for Component Id='...' before CreateFolder
	createFolderIdx := strings.Index(output.DirectoryXML, "<CreateFolder>")
	if createFolderIdx == -1 {
		t.Fatal("Could not find CreateFolder in DirectoryXML")
	}
	// Find the component ID before this CreateFolder
	beforeCreateFolder := output.DirectoryXML[:createFolderIdx]
	lastComponentIdx := strings.LastIndex(beforeCreateFolder, "<Component Id='")
	if lastComponentIdx == -1 {
		t.Fatal("Could not find Component before CreateFolder")
	}
	startIdx := lastComponentIdx + len("<Component Id='")
	endIdx := strings.Index(beforeCreateFolder[startIdx:], "'")
	permCompID := beforeCreateFolder[startIdx : startIdx+endIdx]

	// Verify this permission component is referenced in FeatureXML
	expectedRef := fmt.Sprintf("<ComponentRef Id='%s'/>", permCompID)
	if !strings.Contains(output.FeatureXML, expectedRef) {
		t.Errorf("FeatureXML should reference permission component %s, got:\n%s", permCompID, output.FeatureXML)
	}
}

func TestFilePermissionsRestricted(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "msis-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testDir := filepath.Join(tmpDir, "install")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: testDir, Target: "[INSTALLDIR]"},
				},
			},
		},
	}
	vars := variables.New()
	vars["RESTRICT_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, tmpDir)

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify restricted permissions
	if !strings.Contains(output.DirectoryXML, "<util:PermissionEx") {
		t.Error("DirectoryXML should contain util:PermissionEx element")
	}
	if !strings.Contains(output.DirectoryXML, "GenericRead='yes'") {
		t.Error("DirectoryXML should have GenericRead='yes' when restricted")
	}
	if !strings.Contains(output.DirectoryXML, "GenericExecute='yes'") {
		t.Error("DirectoryXML should have GenericExecute='yes' when restricted")
	}
	if strings.Contains(output.DirectoryXML, "GenericAll='yes'") {
		t.Error("DirectoryXML should not have GenericAll='yes' when restricted")
	}
}

func TestProcessExecute(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
		Items: []ir.Item{
			ir.Execute{
				Cmd:       "[INSTALLDIR]setup.exe /post-install",
				When:      "after-install",
				Directory: "INSTALLDIR",
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify custom action is generated
	if !strings.Contains(output.CustomActionsXML, "<CustomAction Id='CUSTOMACTION_00000'") {
		t.Error("CustomActionsXML should contain CustomAction element")
	}
	if !strings.Contains(output.CustomActionsXML, "Directory='INSTALLDIR'") {
		t.Error("CustomActionsXML should contain Directory attribute")
	}
	if !strings.Contains(output.CustomActionsXML, "Execute='deferred'") {
		t.Error("CustomActionsXML should use deferred execution for after-install")
	}
	if !strings.Contains(output.CustomActionsXML, "Impersonate='no'") {
		t.Error("CustomActionsXML should have Impersonate='no' for deferred actions")
	}

	// Verify install execute sequence
	if !strings.Contains(output.InstallExecuteSequence, "<Custom Action='CUSTOMACTION_00000'") {
		t.Error("InstallExecuteSequence should contain Custom element")
	}
	if !strings.Contains(output.InstallExecuteSequence, "Before='InstallFinalize'") {
		t.Error("InstallExecuteSequence should schedule after-install before InstallFinalize")
	}
}

func TestExecuteBeforeInstall(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
		Items: []ir.Item{
			ir.Execute{
				Cmd:  "[INSTALLDIR]check.exe",
				When: "before-install",
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// before-install should use immediate execution
	if !strings.Contains(output.CustomActionsXML, "Execute='immediate'") {
		t.Error("before-install should use immediate execution")
	}
	// Should not have Impersonate for immediate
	if strings.Contains(output.CustomActionsXML, "Impersonate='no'") {
		t.Error("immediate actions should not have Impersonate attribute")
	}

	// Verify sequence timing
	if !strings.Contains(output.InstallExecuteSequence, "After='CostFinalize'") {
		t.Error("before-install should be After CostFinalize")
	}
}

func TestExecuteMultipleTimings(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
		Items: []ir.Item{
			ir.Execute{Cmd: "cmd1", When: "before-install"},
			ir.Execute{Cmd: "cmd2", When: "after-install"},
			ir.Execute{Cmd: "cmd3", When: "before-uninstall"},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should have 3 custom actions
	if len(ctx.CustomActions) != 3 {
		t.Errorf("expected 3 custom actions, got %d", len(ctx.CustomActions))
	}

	// Verify each timing has correct condition
	if !strings.Contains(output.InstallExecuteSequence, "After='CostFinalize'") {
		t.Error("before-install should have After='CostFinalize'")
	}
	if !strings.Contains(output.InstallExecuteSequence, "Before='InstallFinalize'") {
		t.Error("after-install should have Before='InstallFinalize'")
	}
	if !strings.Contains(output.InstallExecuteSequence, "After='InstallInitialize'") {
		t.Error("before-uninstall should have After='InstallInitialize'")
	}
	if !strings.Contains(output.InstallExecuteSequence, "(REMOVE=\"ALL\")") {
		t.Error("before-uninstall should have REMOVE=ALL condition")
	}
}

func TestExecuteInvalidWhen(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
		Items: []ir.Item{
			ir.Execute{
				Cmd:  "test.exe",
				When: "invalid-timing",
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	_, err := ctx.Generate()
	if err == nil {
		t.Error("expected error for invalid when value")
	}
	if !strings.Contains(err.Error(), "invalid execute when value") {
		t.Errorf("expected 'invalid execute when value' error, got: %v", err)
	}
}

func TestExecuteDefaultDirectory(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items:   []ir.Item{},
			},
		},
		Items: []ir.Item{
			ir.Execute{
				Cmd:  "test.exe",
				When: "after-install",
				// Directory not specified - should default to INSTALLDIR
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should default to INSTALLDIR
	if !strings.Contains(output.CustomActionsXML, "Directory='INSTALLDIR'") {
		t.Error("Execute without directory should default to INSTALLDIR")
	}
}

func TestAppDataDirFiles(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{
						Source: "[APPDATADIR]MyApp/config.json",
						Target: "[APPDATADIR]MyApp/config.json",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// APPDATADIR content should be in AppDataDirXML, not DirectoryXML
	if !strings.Contains(output.AppDataDirXML, "Directory Id='APPDATADIR'") {
		t.Error("AppDataDirXML should contain APPDATADIR directory")
	}
	if !strings.Contains(output.AppDataDirXML, "MyApp") {
		t.Error("AppDataDirXML should contain MyApp subdirectory")
	}

	// DirectoryXML should not contain APPDATADIR
	if strings.Contains(output.DirectoryXML, "APPDATADIR") {
		t.Error("DirectoryXML should not contain APPDATADIR content")
	}
}

func TestMixedDirectoryRoots(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{
						Source: "[INSTALLDIR]bin/app.exe",
						Target: "[INSTALLDIR]bin/app.exe",
					},
					ir.Files{
						Source: "[APPDATADIR]config/settings.json",
						Target: "[APPDATADIR]config/settings.json",
					},
				},
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// INSTALLDIR in DirectoryXML
	if !strings.Contains(output.DirectoryXML, "Directory Id='INSTALLDIR'") {
		t.Error("DirectoryXML should contain INSTALLDIR")
	}
	if !strings.Contains(output.DirectoryXML, "bin") {
		t.Error("DirectoryXML should contain bin subdirectory")
	}

	// APPDATADIR in AppDataDirXML
	if !strings.Contains(output.AppDataDirXML, "Directory Id='APPDATADIR'") {
		t.Error("AppDataDirXML should contain APPDATADIR")
	}
	if !strings.Contains(output.AppDataDirXML, "config") {
		t.Error("AppDataDirXML should contain config subdirectory")
	}
}

func TestAllDirectoryRoots(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{
				Name:    "Main",
				Enabled: true,
				Items: []ir.Item{
					ir.Files{Source: "[INSTALLDIR]app.exe", Target: "[INSTALLDIR]app.exe"},
					ir.Files{Source: "[APPDATADIR]data.json", Target: "[APPDATADIR]data.json"},
					ir.Files{Source: "[ROAMINGAPPDATADIR]roaming.json", Target: "[ROAMINGAPPDATADIR]roaming.json"},
					ir.Files{Source: "[LOCALAPPDATADIR]local.json", Target: "[LOCALAPPDATADIR]local.json"},
					ir.Files{Source: "[COMMONFILESDIR]shared.dll", Target: "[COMMONFILESDIR]shared.dll"},
					ir.Files{Source: "[WINDOWSDIR]win.ini", Target: "[WINDOWSDIR]win.ini"},
					ir.Files{Source: "[SYSTEMDIR]sys.dll", Target: "[SYSTEMDIR]sys.dll"},
				},
			},
		},
	}
	vars := variables.New()
	vars["DISABLE_FILE_PERMISSIONS"] = "True"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Each root should have its own output field
	if !strings.Contains(output.DirectoryXML, "INSTALLDIR") {
		t.Error("DirectoryXML should contain INSTALLDIR")
	}
	if !strings.Contains(output.AppDataDirXML, "APPDATADIR") {
		t.Error("AppDataDirXML should contain APPDATADIR")
	}
	if !strings.Contains(output.RoamingAppDataDirXML, "ROAMINGAPPDATADIR") {
		t.Error("RoamingAppDataDirXML should contain ROAMINGAPPDATADIR")
	}
	if !strings.Contains(output.LocalAppDataDirXML, "LOCALAPPDATADIR") {
		t.Error("LocalAppDataDirXML should contain LOCALAPPDATADIR")
	}
	if !strings.Contains(output.CommonFilesDirXML, "COMMONFILESDIR") {
		t.Error("CommonFilesDirXML should contain COMMONFILESDIR")
	}
	if !strings.Contains(output.WindowsDirXML, "WINDOWSDIR") {
		t.Error("WindowsDirXML should contain WINDOWSDIR")
	}
	if !strings.Contains(output.SystemDirXML, "SYSTEMDIR") {
		t.Error("SystemDirXML should contain SYSTEMDIR")
	}
}

func TestGenerateLaunchConditions(t *testing.T) {
	setup := &ir.Setup{
		Requires: []ir.Requirement{
			{Type: "vcredist", Version: "2022"},
			{Type: "netfx", Version: "4.8"},
		},
		Features: []ir.Feature{
			{Name: "Main", Enabled: true},
		},
	}
	vars := variables.New()
	vars["PLATFORM"] = "x64"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check registry search XML contains VC++ property
	if !strings.Contains(output.LaunchConditionSearchXML, "VCREDIST_X64_2022") {
		t.Error("LaunchConditionSearchXML should contain VCREDIST_X64_2022 property")
	}
	if !strings.Contains(output.LaunchConditionSearchXML, "RegistrySearch") {
		t.Error("LaunchConditionSearchXML should contain RegistrySearch")
	}

	// Check launch conditions XML
	if !strings.Contains(output.LaunchConditionsXML, "Launch Condition") {
		t.Error("LaunchConditionsXML should contain Launch Condition elements")
	}
	if !strings.Contains(output.LaunchConditionsXML, "Visual C++ 2022") {
		t.Error("LaunchConditionsXML should mention Visual C++ 2022 in error message")
	}
	if !strings.Contains(output.LaunchConditionsXML, ".NET Framework 4.8") {
		t.Error("LaunchConditionsXML should mention .NET Framework 4.8 in error message")
	}
}

func TestGenerateLaunchConditions_x86(t *testing.T) {
	setup := &ir.Setup{
		Requires: []ir.Requirement{
			{Type: "vcredist", Version: "2022"},
		},
		Features: []ir.Feature{
			{Name: "Main", Enabled: true},
		},
	}
	vars := variables.New()
	vars["PLATFORM"] = "x86"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should use x86 property name
	if !strings.Contains(output.LaunchConditionSearchXML, "VCREDIST_X86_2022") {
		t.Error("LaunchConditionSearchXML should contain VCREDIST_X86_2022 for x86 platform")
	}
	if strings.Contains(output.LaunchConditionSearchXML, "VCREDIST_X64") {
		t.Error("LaunchConditionSearchXML should NOT contain x64 for x86 platform")
	}
}

func TestGenerateLaunchConditions_arm64(t *testing.T) {
	setup := &ir.Setup{
		Requires: []ir.Requirement{
			{Type: "vcredist", Version: "2022"},
		},
		Features: []ir.Feature{
			{Name: "Main", Enabled: true},
		},
	}
	vars := variables.New()
	vars["PLATFORM"] = "arm64"
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should use arm64 property name and registry key
	if !strings.Contains(output.LaunchConditionSearchXML, "VCREDIST_ARM64_2022") {
		t.Error("LaunchConditionSearchXML should contain VCREDIST_ARM64_2022 for arm64 platform")
	}
	if !strings.Contains(output.LaunchConditionSearchXML, "Runtimes\\arm64") {
		t.Error("LaunchConditionSearchXML should reference arm64 registry key")
	}
}

func TestGenerateLaunchConditions_NoRequirements(t *testing.T) {
	setup := &ir.Setup{
		Features: []ir.Feature{
			{Name: "Main", Enabled: true},
		},
	}
	vars := variables.New()
	ctx := NewContext(setup, vars, ".")

	output, err := ctx.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Should be empty when no requirements
	if output.LaunchConditionSearchXML != "" {
		t.Errorf("LaunchConditionSearchXML should be empty, got: %s", output.LaunchConditionSearchXML)
	}
	if output.LaunchConditionsXML != "" {
		t.Errorf("LaunchConditionsXML should be empty, got: %s", output.LaunchConditionsXML)
	}
}
