package generator

import (
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
