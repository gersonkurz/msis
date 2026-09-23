package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The justfile's Windows recipes run under `powershell.exe -Command`, where a line ending in
// `finally { Pop-Location }` exits 0 even when the native command inside the try failed. That
// form made `just release` announce success after a failed package and let `release-all` carry
// on (#53), and made `just vet` pass with a failing nested module before that. This keeps it
// from coming back: no recipe line may use it. A comment explaining why is allowed.
func TestJustfileRecipesDoNotSwallowNativeFailures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "finally { Pop-Location }") {
			t.Errorf("justfile:%d uses `finally { Pop-Location }`, which exits 0 whatever the command inside "+
				"returned; use `Set-Location …; & .\\cmd …; exit $LASTEXITCODE` (each recipe line is its own shell)\n  %s",
				i+1, trimmed)
		}
	}
}
