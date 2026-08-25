package genutil

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// AssertGolden compares got against the golden file byte-exactly, so template
// drift a fragment check cannot see becomes a visible diff. update rewrites
// the golden instead of comparing (wire it to a -update test flag).
func AssertGolden(t *testing.T, goldenPath string, got []byte, update bool) {
	t.Helper()
	if update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update after intentional template changes): %v", goldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from generated output; run with -update if the template change is intentional", goldenPath)
	}
}
