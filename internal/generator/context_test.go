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
