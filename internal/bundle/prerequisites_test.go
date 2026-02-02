package bundle

import "testing"

func TestLookupPrerequisite(t *testing.T) {
	tests := []struct {
		prereqType string
		version    string
		wantNil    bool
		wantName   string
	}{
		{"vcredist", "2022", false, "Microsoft Visual C++ 2015-2022 Redistributable ({arch})"},
		{"vcredist", "2019", false, "Microsoft Visual C++ 2015-2019 Redistributable ({arch})"},
		{"netfx", "4.8", false, "Microsoft .NET Framework 4.8"},
		{"netfx", "4.7.2", false, "Microsoft .NET Framework 4.7.2"},
		{"unknown", "1.0", true, ""},
		{"vcredist", "2010", true, ""}, // Not defined
	}

	for _, tt := range tests {
		t.Run(tt.prereqType+"/"+tt.version, func(t *testing.T) {
			def := LookupPrerequisite(tt.prereqType, tt.version)
			if tt.wantNil {
				if def != nil {
					t.Errorf("expected nil, got %v", def)
				}
			} else {
				if def == nil {
					t.Fatal("expected non-nil")
				}
				if def.DisplayName != tt.wantName {
					t.Errorf("expected %q, got %q", tt.wantName, def.DisplayName)
				}
			}
		})
	}
}

func TestExpandArch(t *testing.T) {
	tests := []struct {
		input   string
		is64bit bool
		want    string
	}{
		{"vc_redist.{arch}.exe", true, "vc_redist.x64.exe"},
		{"vc_redist.{arch}.exe", false, "vc_redist.x86.exe"},
		{"no-placeholder.exe", true, "no-placeholder.exe"},
		{"{arch}_{arch}.dll", true, "x64_x64.dll"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExpandArch(tt.input, tt.is64bit)
			if got != tt.want {
				t.Errorf("ExpandArch(%q, %v) = %q, want %q", tt.input, tt.is64bit, got, tt.want)
			}
		})
	}
}

func TestPrerequisiteDefFields(t *testing.T) {
	// Verify all prerequisites have required fields
	for prereqType, versions := range Prerequisites {
		for version, def := range versions {
			t.Run(prereqType+"/"+version, func(t *testing.T) {
				if def.DisplayName == "" {
					t.Error("DisplayName is empty")
				}
				if def.Source == "" {
					t.Error("Source is empty")
				}
				if def.DetectCondition == "" {
					t.Error("DetectCondition is empty")
				}
				if def.InstallArgs == "" {
					t.Error("InstallArgs is empty")
				}
			})
		}
	}
}

func TestNetFxVersionOrdering(t *testing.T) {
	// Verify .NET Framework release numbers are in ascending order
	versions := []struct {
		version string
		release int
	}{
		{"4.6.2", 394802},
		{"4.7", 460798},
		{"4.7.1", 461308},
		{"4.7.2", 461808},
		{"4.8", 528040},
		{"4.8.1", 533320},
	}

	prev := 0
	for _, v := range versions {
		def := LookupPrerequisite("netfx", v.version)
		if def == nil {
			t.Fatalf("netfx %s not found", v.version)
		}
		if v.release <= prev {
			t.Errorf("netfx %s release %d should be > %d", v.version, v.release, prev)
		}
		prev = v.release
	}
}
